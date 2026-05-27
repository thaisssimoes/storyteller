package claude_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"inconsistencyfixer/internal/claude"
)

// ─── Pair construction ────────────────────────────────────────────────────────

func TestNewOllamaPair_CustomModels(t *testing.T) {
	pair := claude.NewOllamaPair("qwen2.5:7b", "gemma2:9b", "", 0)
	if got := pair.Fast.Model(); got != "qwen2.5:7b" {
		t.Errorf("fast model = %q, want qwen2.5:7b", got)
	}
	if got := pair.Robust.Model(); got != "gemma2:9b" {
		t.Errorf("robust model = %q, want gemma2:9b", got)
	}
}

func TestNewOllamaPair_Defaults(t *testing.T) {
	pair := claude.NewOllamaPair("", "", "", 0)
	if got := pair.Fast.Model(); got != claude.DefaultOllamaFast {
		t.Errorf("fast default = %q, want %q", got, claude.DefaultOllamaFast)
	}
	if got := pair.Robust.Model(); got != claude.DefaultOllamaRobust {
		t.Errorf("robust default = %q, want %q", got, claude.DefaultOllamaRobust)
	}
}

func TestNewAnthropicPair_Defaults(t *testing.T) {
	pair := claude.NewAnthropicPair("dummy-key", "", "")
	if got := pair.Fast.Model(); got != claude.DefaultAnthropicFast {
		t.Errorf("fast default = %q, want %q", got, claude.DefaultAnthropicFast)
	}
	if got := pair.Robust.Model(); got != claude.DefaultAnthropicRobust {
		t.Errorf("robust default = %q, want %q", got, claude.DefaultAnthropicRobust)
	}
}

func TestPair_Provider(t *testing.T) {
	ollama := claude.NewOllamaPair("a", "b", "", 0)
	if ollama.Provider() != "ollama" {
		t.Errorf("provider = %q, want ollama", ollama.Provider())
	}
	anthropic := claude.NewAnthropicPair("key", "", "")
	if anthropic.Provider() != "anthropic" {
		t.Errorf("provider = %q, want anthropic", anthropic.Provider())
	}
}

func TestOllamaClient_BatchSize(t *testing.T) {
	pair := claude.NewOllamaPair("a", "b", "", 0)
	// Local models use smaller batches to keep output tight
	if pair.Fast.BatchSize() != 2 {
		t.Errorf("ollama batch size = %d, want 2", pair.Fast.BatchSize())
	}
}

// ─── CheckModels ──────────────────────────────────────────────────────────────

// TestCheckModels_AllPresent uses a fake Ollama server that reports both
// models as available.
func TestCheckModels_AllPresent(t *testing.T) {
	srv := fakeOllamaTagsServer(t, []string{"qwen2.5:7b", "gemma2:9b"})
	defer srv.Close()

	pair := claude.NewOllamaPair("qwen2.5:7b", "gemma2:9b", srv.URL, 0)
	missing := claude.CheckModels(pair)
	if len(missing) != 0 {
		t.Errorf("expected no missing models, got %v", missing)
	}
}

// TestCheckModels_FastMissing verifies that an un-pulled fast model is reported.
func TestCheckModels_FastMissing(t *testing.T) {
	srv := fakeOllamaTagsServer(t, []string{"gemma2:9b"}) // fast model absent
	defer srv.Close()

	pair := claude.NewOllamaPair("qwen2.5:7b", "gemma2:9b", srv.URL, 0)
	missing := claude.CheckModels(pair)
	if len(missing) != 1 || missing[0] != "qwen2.5:7b" {
		t.Errorf("expected [qwen2.5:7b] missing, got %v", missing)
	}
}

// TestCheckModels_BothMissing verifies that both un-pulled models are reported.
func TestCheckModels_BothMissing(t *testing.T) {
	srv := fakeOllamaTagsServer(t, []string{}) // nothing pulled
	defer srv.Close()

	pair := claude.NewOllamaPair("qwen2.5:7b", "gemma2:9b", srv.URL, 0)
	missing := claude.CheckModels(pair)
	if len(missing) != 2 {
		t.Errorf("expected 2 missing models, got %v", missing)
	}
}

// TestCheckModels_Anthropic skips model validation for Anthropic (no local server).
func TestCheckModels_Anthropic(t *testing.T) {
	pair := claude.NewAnthropicPair("dummy", "", "")
	missing := claude.CheckModels(pair)
	if len(missing) != 0 {
		t.Errorf("Anthropic check should return no missing, got %v", missing)
	}
}

// ─── Integration test ─────────────────────────────────────────────────────────

// TestOllamaIntegration_ModelsAvailable queries the real Ollama instance and
// verifies that the models in OLLAMA_FAST_MODEL / OLLAMA_ROBUST_MODEL are
// actually pulled. Run with OLLAMA_INTEGRATION=1 to enable.
func TestOllamaIntegration_ModelsAvailable(t *testing.T) {
	if os.Getenv("OLLAMA_INTEGRATION") != "1" {
		t.Skip("set OLLAMA_INTEGRATION=1 to run against the real Ollama instance")
	}

	fastModel := os.Getenv("OLLAMA_FAST_MODEL")
	robustModel := os.Getenv("OLLAMA_ROBUST_MODEL")
	baseURL := os.Getenv("OLLAMA_URL")

	pair := claude.NewOllamaPair(fastModel, robustModel, baseURL, 0)
	missing := claude.CheckModels(pair)
	if len(missing) != 0 {
		t.Errorf("the following models are not pulled in Ollama: %v\n"+
			"run: ollama pull %v", missing, missing[0])
	}
}

// ─── Helper ───────────────────────────────────────────────────────────────────

// fakeOllamaTagsServer returns an httptest.Server that responds to GET /api/tags
// with the given model names.
func fakeOllamaTagsServer(t *testing.T, models []string) *httptest.Server {
	t.Helper()
	type modelEntry struct {
		Name string `json:"name"`
	}
	type tagsResponse struct {
		Models []modelEntry `json:"models"`
	}
	entries := make([]modelEntry, len(models))
	for i, m := range models {
		entries[i] = modelEntry{Name: m}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tagsResponse{Models: entries})
	}))
}
