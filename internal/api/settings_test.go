package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
)

// newSettingsTestAPI wraps newTestAPI, seeds the singleton user_settings row,
// and injects an embedder-select deps that never hits the network. That keeps
// settings-endpoint tests deterministic and fast.
func newSettingsTestAPI(t *testing.T) (*API, *http.ServeMux) {
	t.Helper()
	a, mux := newTestAPI(t)
	if _, err := a.Services.Store.DB.Exec(`INSERT OR IGNORE INTO user_settings (id) VALUES (1)`); err != nil {
		t.Fatalf("seed user_settings: %v", err)
	}
	a.SetEmbedderSelectDeps(service.EmbedderSelectDeps{
		LookupSecret: func(string) string { return "" },
		Getenv:       func(string) string { return "" },
	})
	return a, mux
}

func TestSettings_GetIncludesEmbeddingStatus(t *testing.T) {
	_, mux := newSettingsTestAPI(t)

	req := httptest.NewRequest("GET", "/api/settings", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	status, ok := got["embedding_status"].(string)
	if !ok {
		t.Fatalf("embedding_status missing from response: %v", got)
	}
	// Fresh install = disabled.
	if status != "disabled" {
		t.Errorf("expected disabled status on fresh install, got %q", status)
	}
}

func TestSettings_PutValidatesProvider(t *testing.T) {
	_, mux := newSettingsTestAPI(t)

	body := bytes.NewBufferString(`{"embedding_provider":"anthropic"}`)
	req := httptest.NewRequest("PUT", "/api/settings", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid provider, got %d", w.Code)
	}
}

func TestSettings_PutValidatesMode(t *testing.T) {
	_, mux := newSettingsTestAPI(t)

	body := bytes.NewBufferString(`{"embedding_mode":"auto"}`)
	req := httptest.NewRequest("PUT", "/api/settings", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid mode, got %d", w.Code)
	}
}

func TestSettings_PutRoundTripsEmbeddingFields(t *testing.T) {
	a, mux := newSettingsTestAPI(t)

	// Inject an OpenAI key so the active embedder selects without a real network call.
	a.SetEmbedderSelectDeps(service.EmbedderSelectDeps{
		LookupSecret: func(id string) string {
			if id == "openai-001" {
				return "sk-test"
			}
			return ""
		},
		Getenv: func(string) string { return "" },
	})

	body := bytes.NewBufferString(`{"embedding_provider":"openai","embedding_model":"text-embedding-3-large","embedding_mode":"explicit"}`)
	req := httptest.NewRequest("PUT", "/api/settings", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["embedding_provider"] != "openai" {
		t.Errorf("embedding_provider: got %v", got["embedding_provider"])
	}
	if got["embedding_mode"] != "explicit" {
		t.Errorf("embedding_mode: got %v", got["embedding_mode"])
	}
	if got["embedding_status"] != "active" {
		t.Errorf("embedding_status: got %v, want active", got["embedding_status"])
	}
}

func TestSettings_EmbeddingProvidersEndpoint(t *testing.T) {
	_, mux := newSettingsTestAPI(t)

	req := httptest.NewRequest("GET", "/api/settings/embedding/providers", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	providers, ok := got["providers"].([]any)
	if !ok {
		t.Fatalf("providers missing or wrong type: %v", got)
	}
	// Step 6.5 reduced supported providers to OpenAI only.
	if len(providers) != 1 {
		t.Errorf("expected 1 provider, got %d", len(providers))
	}
}
