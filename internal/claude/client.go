package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	anthropicURL     = "https://api.anthropic.com/v1/messages"
	anthropicVersion = "2023-06-01"
	defaultOllamaURL = "http://localhost:11434"
)

// Client can talk to either the Anthropic API or a local Ollama instance.
type Client struct {
	provider  string // "anthropic" | "ollama"
	model     string
	apiKey    string // Anthropic only
	baseURL   string
	numCtx    int // Ollama context window
	batchSize int // chapters per analysis batch
	http      *http.Client
}

// New creates an Anthropic client using claude-sonnet-4-6.
func New(apiKey string) *Client {
	return &Client{
		provider:  "anthropic",
		model:     "claude-sonnet-4-6",
		apiKey:    apiKey,
		baseURL:   anthropicURL,
		batchSize: 10,
		http:      &http.Client{Timeout: 180 * time.Second},
	}
}

// NewOllama creates a client that talks to a local Ollama server.
// baseURL defaults to http://localhost:11434 if empty.
// numCtx is the context window; 0 defaults to 16384.
func NewOllama(model, baseURL string, numCtx int) *Client {
	if baseURL == "" {
		baseURL = defaultOllamaURL
	}
	if numCtx == 0 {
		numCtx = 16384
	}
	return &Client{
		provider:  "ollama",
		model:     model,
		baseURL:   baseURL,
		numCtx:    numCtx,
		batchSize: 3, // smaller batches to fit the local model's context
		http:      &http.Client{Timeout: 600 * time.Second},
	}
}

// BatchSize returns how many chapters should be sent per analysis call.
func (c *Client) BatchSize() int { return c.batchSize }

// Provider returns "anthropic" or "ollama".
func (c *Client) Provider() string { return c.provider }

// Complete sends messages and returns the assistant's text response.
func (c *Client) Complete(ctx context.Context, maxTokens int, messages []Message) (string, error) {
	return c.CompleteWithSystem(ctx, "", maxTokens, messages)
}

// CompleteWithSystem sends messages with an optional system prompt.
func (c *Client) CompleteWithSystem(ctx context.Context, system string, maxTokens int, messages []Message) (string, error) {
	if c.provider == "ollama" {
		if system != "" {
			sysMsg := Message{Role: "system", Content: []ContentBlock{{Type: "text", Text: system}}}
			messages = append([]Message{sysMsg}, messages...)
		}
		return c.completeOllama(ctx, maxTokens, messages)
	}
	return c.completeAnthropic(ctx, system, maxTokens, messages)
}

// --- Anthropic ---

type anthropicRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Messages  []Message `json:"messages"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *Client) completeAnthropic(ctx context.Context, system string, maxTokens int, messages []Message) (string, error) {
	body, err := json.Marshal(anthropicRequest{
		Model:     c.model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  messages,
	})
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")
	req.Header.Set("content-type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	var result anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("API error (%s): %s", result.Error.Type, result.Error.Message)
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("empty response content")
	}
	return result.Content[0].Text, nil
}

// --- Ollama (OpenAI-compatible endpoint) ---

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaRequest struct {
	Model     string          `json:"model"`
	Messages  []ollamaMessage `json:"messages"`
	Stream    bool            `json:"stream"`
	MaxTokens int             `json:"max_tokens,omitempty"`
	Options   struct {
		NumCtx int `json:"num_ctx"`
	} `json:"options"`
}

type ollamaResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *Client) completeOllama(ctx context.Context, maxTokens int, messages []Message) (string, error) {
	// Convert multi-block messages to plain strings (Ollama uses simple content strings)
	oMsgs := make([]ollamaMessage, 0, len(messages))
	for _, msg := range messages {
		var sb strings.Builder
		for i, block := range msg.Content {
			if i > 0 && block.Text != "" {
				sb.WriteString("\n\n")
			}
			sb.WriteString(block.Text)
		}
		oMsgs = append(oMsgs, ollamaMessage{Role: msg.Role, Content: sb.String()})
	}

	var reqBody ollamaRequest
	reqBody.Model = c.model
	reqBody.Messages = oMsgs
	reqBody.Stream = false
	reqBody.MaxTokens = maxTokens
	reqBody.Options.NumCtx = c.numCtx

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshaling Ollama request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("Ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	var result ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding Ollama response: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("Ollama error: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("empty Ollama response")
	}
	return result.Choices[0].Message.Content, nil
}

// --- Content block helpers ---

// CacheControl marks a block for Anthropic prompt caching (ignored by Ollama).
type CacheControl struct {
	Type string `json:"type"`
}

// ContentBlock is a piece of content within a message.
type ContentBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// Message is a single conversation turn.
type Message struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

// TextBlock creates a plain text content block.
func TextBlock(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

// CachedTextBlock creates a text block with Anthropic prompt caching enabled.
// When using Ollama the cache hint is silently ignored.
func CachedTextBlock(text string) ContentBlock {
	return ContentBlock{
		Type:         "text",
		Text:         text,
		CacheControl: &CacheControl{Type: "ephemeral"},
	}
}

// UserMessage builds a user-role message from one or more content blocks.
func UserMessage(blocks ...ContentBlock) Message {
	return Message{Role: "user", Content: blocks}
}
