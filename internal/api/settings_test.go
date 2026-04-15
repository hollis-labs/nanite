package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newSettingsTestAPI wraps newTestAPI and ensures the singleton user_settings
// row exists — the container itself handles a missing row, but the settings
// handlers expect it to be present.
func newSettingsTestAPI(t *testing.T) (*API, *http.ServeMux) {
	t.Helper()
	a, mux := newTestAPI(t)
	if _, err := a.Services.Store.DB.Exec(`INSERT OR IGNORE INTO user_settings (id) VALUES (1)`); err != nil {
		t.Fatalf("seed user_settings: %v", err)
	}
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
	_, mux := newSettingsTestAPI(t)

	body := bytes.NewBufferString(`{"embedding_provider":"ollama","embedding_model":"nomic-embed-text","embedding_mode":"explicit"}`)
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
	if got["embedding_provider"] != "ollama" {
		t.Errorf("embedding_provider: got %v", got["embedding_provider"])
	}
	if got["embedding_mode"] != "explicit" {
		t.Errorf("embedding_mode: got %v", got["embedding_mode"])
	}
	// Status should be one of: active, unreachable (Ollama probe outcome).
	status, _ := got["embedding_status"].(string)
	if status != "active" && status != "unreachable" {
		t.Errorf("embedding_status: got %q, want active or unreachable", status)
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
	if len(providers) != 5 {
		t.Errorf("expected 5 providers, got %d", len(providers))
	}
}
