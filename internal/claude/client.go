package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	anthropicURL     = "https://api.anthropic.com/v1/messages"
	anthropicVersion = "2023-06-01"
	defaultOllamaURL = "http://localhost:11434"

	// Default model pair for each provider. The Fast model handles structured,
	// high-volume calls (per-chapter scene scan, continuity check, outline
	// chunk expansion). The Robust model handles foundational/creative work
	// (macro analysis, chapter writing, fixes).
	DefaultAnthropicFast   = "claude-haiku-4-5"
	DefaultAnthropicRobust = "claude-sonnet-4-6"
	DefaultOllamaFast      = "qwen2.5:14b"
	DefaultOllamaRobust    = "gemma2:27b"
)

// Pair couples a fast model (cheap, structured-JSON tasks) with a robust model
// (creative or quality-critical tasks). Callers pick which one to use based on
// the nature of each call.
type Pair struct {
	Fast   *Client
	Robust *Client
}

// Provider returns the provider string of the underlying clients (assumes both
// share a provider, which is always the case as set up by NewPair*).
func (p *Pair) Provider() string {
	if p == nil || p.Robust == nil {
		return ""
	}
	return p.Robust.Provider()
}

// NewAnthropicPair creates a Fast/Robust pair against Anthropic. Empty model
// names fall back to the package defaults.
func NewAnthropicPair(apiKey, fastModel, robustModel string) *Pair {
	if fastModel == "" {
		fastModel = DefaultAnthropicFast
	}
	if robustModel == "" {
		robustModel = DefaultAnthropicRobust
	}
	return &Pair{
		Fast:   NewAnthropicWithModel(apiKey, fastModel),
		Robust: NewAnthropicWithModel(apiKey, robustModel),
	}
}

// NewOllamaPair creates a Fast/Robust pair against a local Ollama server.
// Empty model names fall back to the package defaults.
func NewOllamaPair(fastModel, robustModel, baseURL string, numCtx int) *Pair {
	if fastModel == "" {
		fastModel = DefaultOllamaFast
	}
	if robustModel == "" {
		robustModel = DefaultOllamaRobust
	}
	return &Pair{
		Fast:   NewOllama(fastModel, baseURL, numCtx),
		Robust: NewOllama(robustModel, baseURL, numCtx),
	}
}

// Options is per-call configuration.
type Options struct {
	// MaxTokens caps the response. Defaults to 8192 if zero.
	MaxTokens int
	// JSONMode asks the provider to return strict JSON. On Ollama this enables
	// the OpenAI-compatible response_format=json_object hint; on Anthropic it
	// reinforces the system prompt with a "respond with valid JSON only" line.
	JSONMode bool
	// Schema is an optional JSON schema for structured output (Ollama ≥0.4.0
	// via response_format=json_schema). When set, overrides JSONMode for Ollama
	// and constrains the model to output exactly the specified fields.
	// Ignored by Anthropic (which uses different structured-output mechanisms).
	Schema json.RawMessage
	// Stream forces streaming on Ollama. Default is true for any call >2048
	// max_tokens since long generations otherwise time out before headers.
	Stream *bool
}

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

// New creates an Anthropic client using the default robust model.
// Kept for backward compatibility. New code should use NewAnthropicPair.
func New(apiKey string) *Client {
	return NewAnthropicWithModel(apiKey, DefaultAnthropicRobust)
}

// NewAnthropicWithModel creates an Anthropic client against a specific model.
func NewAnthropicWithModel(apiKey, model string) *Client {
	return &Client{
		provider:  "anthropic",
		model:     model,
		apiKey:    apiKey,
		baseURL:   anthropicURL,
		batchSize: 5, // smaller batches → less truncation risk in JSON output
		http:      &http.Client{Timeout: 0}, // controlled by ctx
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
		batchSize: 2, // local models are slower; smaller batches keep output tight
		http:      &http.Client{Timeout: 0}, // controlled by ctx
	}
}

// BatchSize returns how many chapters should be sent per analysis call.
func (c *Client) BatchSize() int { return c.batchSize }

// Provider returns "anthropic" or "ollama".
func (c *Client) Provider() string { return c.provider }

// Model returns the model name in use.
func (c *Client) Model() string { return c.model }

// Complete is a convenience wrapper for a default-options call.
func (c *Client) Complete(ctx context.Context, maxTokens int, messages []Message) (string, error) {
	return c.CompleteEx(ctx, "", messages, Options{MaxTokens: maxTokens})
}

// CompleteWithSystem keeps the legacy 4-arg signature working.
func (c *Client) CompleteWithSystem(ctx context.Context, system string, maxTokens int, messages []Message) (string, error) {
	return c.CompleteEx(ctx, system, messages, Options{MaxTokens: maxTokens})
}

// CompleteJSON requests strict JSON output (provider-specific hint).
func (c *Client) CompleteJSON(ctx context.Context, system string, maxTokens int, messages []Message) (string, error) {
	return c.CompleteEx(ctx, system, messages, Options{MaxTokens: maxTokens, JSONMode: true})
}

// CompleteEx is the full-featured entry point.
func (c *Client) CompleteEx(ctx context.Context, system string, messages []Message, opts Options) (string, error) {
	if opts.MaxTokens == 0 {
		opts.MaxTokens = 8192
	}
	if c.provider == "ollama" {
		stream := true // default to streaming on Ollama (avoids "awaiting headers" timeouts)
		if opts.Stream != nil {
			stream = *opts.Stream
		}
		if system != "" {
			sysMsg := Message{Role: "system", Content: []ContentBlock{{Type: "text", Text: system}}}
			messages = append([]Message{sysMsg}, messages...)
		}
		return c.completeOllama(ctx, opts.MaxTokens, opts.JSONMode, opts.Schema, stream, messages)
	}
	return c.completeAnthropic(ctx, system, opts.MaxTokens, opts.JSONMode, messages)
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

func (c *Client) completeAnthropic(ctx context.Context, system string, maxTokens int, jsonMode bool, messages []Message) (string, error) {
	if jsonMode && system != "" {
		system += "\n\nRespond with strictly valid JSON only — no markdown, no preamble, no commentary."
	} else if jsonMode {
		system = "Respond with strictly valid JSON only — no markdown, no preamble, no commentary."
	}

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

type ollamaJSONSchema struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict bool            `json:"strict"`
}

type ollamaResponseFormat struct {
	Type       string            `json:"type"`
	JSONSchema *ollamaJSONSchema `json:"json_schema,omitempty"`
}

type ollamaRequest struct {
	Model          string                `json:"model"`
	Messages       []ollamaMessage       `json:"messages"`
	Stream         bool                  `json:"stream"`
	MaxTokens      int                   `json:"max_tokens,omitempty"`
	ResponseFormat *ollamaResponseFormat `json:"response_format,omitempty"`
	Options        struct {
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

type ollamaStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *Client) completeOllama(ctx context.Context, maxTokens int, jsonMode bool, schema json.RawMessage, stream bool, messages []Message) (string, error) {
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
	reqBody.Stream = stream
	reqBody.MaxTokens = maxTokens
	reqBody.Options.NumCtx = c.numCtx
	if len(schema) > 0 {
		reqBody.ResponseFormat = &ollamaResponseFormat{
			Type: "json_schema",
			JSONSchema: &ollamaJSONSchema{
				Name:   "response",
				Schema: schema,
				Strict: true,
			},
		}
	} else if jsonMode {
		reqBody.ResponseFormat = &ollamaResponseFormat{Type: "json_object"}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshaling Ollama request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	if stream {
		req.Header.Set("accept", "text/event-stream")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("Ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Ollama HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	if stream {
		return readOllamaStream(ctx, resp.Body)
	}

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

// readOllamaStream consumes the SSE stream and concatenates the deltas.
// Honours ctx cancellation between chunks.
func readOllamaStream(ctx context.Context, body io.Reader) (string, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)

	var sb strings.Builder
	idle := time.Now()

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return sb.String(), ctx.Err()
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			// SSE separator — keep going but check idle window
			if time.Since(idle) > 30*time.Minute {
				return sb.String(), fmt.Errorf("Ollama stream idle for >30 min")
			}
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}

		var chunk ollamaStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			// Tolerate a malformed chunk — keep going. Local servers occasionally
			// emit a non-JSON keep-alive comment.
			continue
		}
		if chunk.Error != nil {
			return sb.String(), fmt.Errorf("Ollama stream error: %s", chunk.Error.Message)
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				sb.WriteString(ch.Delta.Content)
				idle = time.Now()
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return sb.String(), fmt.Errorf("Ollama stream read: %w", err)
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("Ollama stream produced no content")
	}
	return sb.String(), nil
}

// --- Model availability check ---

// CheckModels queries Ollama's /api/tags endpoint and returns the model names
// that are configured in the pair but not yet pulled locally. Returns nil for
// Anthropic pairs (availability is not checkable without a live API call).
func CheckModels(pair *Pair) []string {
	if pair == nil || pair.Robust == nil || pair.Robust.provider != "ollama" {
		return nil
	}

	available, err := listOllamaModels(pair.Robust.baseURL, pair.Robust.http)
	if err != nil {
		// If we can't reach Ollama at all, report both models as unavailable.
		return []string{pair.Fast.model, pair.Robust.model}
	}

	pulled := make(map[string]bool, len(available))
	for _, m := range available {
		pulled[m] = true
		// Ollama sometimes appends ":latest" even when the user omits it.
		// Treat "foo:latest" as equivalent to "foo".
		if tag := strings.TrimSuffix(m, ":latest"); tag != m {
			pulled[tag] = true
		}
	}

	var missing []string
	for _, model := range []string{pair.Fast.model, pair.Robust.model} {
		if !pulled[model] {
			missing = append(missing, model)
		}
	}
	return missing
}

type ollamaTagsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

func listOllamaModels(baseURL string, httpClient *http.Client) ([]string, error) {
	resp, err := httpClient.Get(baseURL + "/api/tags")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	names := make([]string, len(result.Models))
	for i, m := range result.Models {
		names[i] = m.Name
	}
	return names, nil
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
