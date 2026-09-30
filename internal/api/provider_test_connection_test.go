package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/nanite/internal/secrets"
	"github.com/zalando/go-keyring"
)

// isolateProviderKeys points the keychain at an in-memory mock and clears the
// provider env vars, so a test never reads or writes the operator's real
// keys and never reaches a real provider.
func isolateProviderKeys(t *testing.T) {
	t.Helper()
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)
	for _, env := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_BASE_URL", "OPENAI_BASE_URL"} {
		t.Setenv(env, "")
	}
}

// fakeAnthropicModels serves GET /v1/models with the status in *status, and
// records the key it was called with.
func fakeAnthropicModels(t *testing.T, status *atomic.Int32, gotKey *atomic.Value) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey.Store(r.Header.Get("X-Api-Key"))
		w.Header().Set("Content-Type", "application/json")
		code := int(status.Load())
		w.WriteHeader(code)
		if code == http.StatusOK {
			_, _ = w.Write([]byte(`{"data":[],"has_more":false,"first_id":null,"last_id":null}`))
			return
		}
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
}

func postProviderTest(t *testing.T, mux *http.ServeMux, id string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/providers/"+id+"/test", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON (status %d): %v — %s", w.Code, err, w.Body.String())
	}
	return w.Code, body
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestProviderTestConnection_RejectedThenAccepted(t *testing.T) {
	isolateProviderKeys(t)
	var status atomic.Int32
	var gotKey atomic.Value
	fakeAnthropicModels(t, &status, &gotKey)
	_, mux := newTestAPIWithSeededProviders(t)

	if err := secrets.Set(secrets.ProviderKeyName("anthropic-001"), "sk-bogus"); err != nil {
		t.Fatalf("secrets.Set: %v", err)
	}
	status.Store(http.StatusUnauthorized)
	code, body := postProviderTest(t, mux, "anthropic-001")
	if code != http.StatusOK || body["ok"] != false || body["status"] != "rejected" {
		t.Fatalf("bogus key: %d %v, want 200 ok=false status=rejected", code, body)
	}
	if gotKey.Load() != "sk-bogus" {
		t.Fatalf("provider called with key %v, want the keychain key", gotKey.Load())
	}

	if err := secrets.Set(secrets.ProviderKeyName("anthropic-001"), "sk-valid"); err != nil {
		t.Fatalf("secrets.Set: %v", err)
	}
	status.Store(http.StatusOK)
	code, body = postProviderTest(t, mux, "anthropic-001")
	if code != http.StatusOK || body["ok"] != true || body["status"] != "accepted" {
		t.Fatalf("valid key: %d %v, want 200 ok=true status=accepted", code, body)
	}
	if gotKey.Load() != "sk-valid" {
		t.Fatalf("provider called with key %v, want the newly saved key", gotKey.Load())
	}

	// Golden keys: path is omitted for an API provider row.
	if got, want := mapKeys(body), []string{"message", "ok", "status"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("response keys = %v, want %v", got, want)
	}
}

func TestProviderTestConnection_NoKey(t *testing.T) {
	isolateProviderKeys(t)
	var status atomic.Int32
	var gotKey atomic.Value
	fakeAnthropicModels(t, &status, &gotKey)
	_, mux := newTestAPIWithSeededProviders(t)

	code, body := postProviderTest(t, mux, "anthropic-001")
	if code != http.StatusOK || body["ok"] != false || body["status"] != "no_key" {
		t.Fatalf("no key: %d %v, want 200 ok=false status=no_key", code, body)
	}
	if gotKey.Load() != nil {
		t.Fatal("a check with no key reached the provider")
	}
}

func TestProviderTestConnection_UnknownProviderIs404JSON(t *testing.T) {
	isolateProviderKeys(t)
	_, mux := newTestAPIWithSeededProviders(t)

	code, body := postProviderTest(t, mux, "no-such-provider")
	if code != http.StatusNotFound || !reflect.DeepEqual(body, map[string]any{"error": "provider not found"}) {
		t.Fatalf("unknown id: %d %v, want 404 {error: provider not found}", code, body)
	}
}

// detect-cli moved its logic into ProviderConfigService; its wire shape is
// unchanged.
func TestDetectCLI_WireShapeUnchanged(t *testing.T) {
	_, mux := newTestAPIWithSeededProviders(t)
	req := httptest.NewRequest(http.MethodGet, "/api/providers/detect-cli", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	wantTypes := []string{"pty", "pty-codex", "pty-opencode"}
	if len(got) != len(wantTypes) {
		t.Fatalf("got %d entries, want %d: %v", len(got), len(wantTypes), got)
	}
	for i, entry := range got {
		if keys := mapKeys(entry); !reflect.DeepEqual(keys, []string{"detected", "env_var", "name", "path", "provider_type"}) {
			t.Fatalf("entry %d keys = %v", i, keys)
		}
		if entry["provider_type"] != wantTypes[i] {
			t.Fatalf("entry %d provider_type = %v, want %s", i, entry["provider_type"], wantTypes[i])
		}
	}
}
