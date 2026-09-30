package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

type verifiable interface {
	VerifyKey(ctx context.Context) error
}

func postAPIKey(t *testing.T, mux *http.ServeMux, id, key string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/providers/"+id+"/api-key", strings.NewReader(`{"api_key":"`+key+`"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w.Code, decodeObject(t, w.Body.Bytes())
}

// keyUsedBy calls VerifyKey on a registered adapter against the fake API and
// returns the key it sent.
func keyUsedBy(t *testing.T, p any, gotKey *atomic.Value) string {
	t.Helper()
	v, ok := p.(verifiable)
	if !ok {
		t.Fatalf("%T cannot verify a key", p)
	}
	gotKey.Store("")
	_ = v.VerifyKey(context.Background())
	return gotKey.Load().(string)
}

// CW-20260930-0101: saving a key makes it live without a restart. The
// registry gets a new adapter; one already held (a turn in flight) keeps the
// key it was built with.
func TestSetProviderAPIKey_HotSwapsTheRegisteredAdapter(t *testing.T) {
	isolateProviderKeys(t)
	var status atomic.Int32
	status.Store(http.StatusOK)
	var gotKey atomic.Value
	fakeAnthropicModels(t, &status, &gotKey)
	a, mux := newTestAPIWithSeededProviders(t)

	if a.Services.Providers.Has("anthropic") {
		t.Fatal("anthropic registered before any key was saved")
	}

	code, body := postAPIKey(t, mux, "anthropic-001", "sk-one")
	if code != http.StatusOK {
		t.Fatalf("save: %d %v", code, body)
	}
	want := map[string]any{"has_key": true, "key_source": "keychain", "provider_id": "anthropic-001"}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("save response = %v, want %v", body, want)
	}
	inFlight, ok := a.Services.Providers.Get("anthropic")
	if !ok {
		t.Fatal("anthropic not registered after saving a key")
	}

	if code, body := postAPIKey(t, mux, "anthropic-001", "sk-two"); code != http.StatusOK {
		t.Fatalf("second save: %d %v", code, body)
	}
	current, _ := a.Services.Providers.Get("anthropic")
	if current == inFlight {
		t.Fatal("the registry still holds the adapter built for the old key")
	}
	if got := keyUsedBy(t, inFlight, &gotKey); got != "sk-one" {
		t.Fatalf("the adapter a running turn holds sent %q, want the old key sk-one", got)
	}
	if got := keyUsedBy(t, current, &gotKey); got != "sk-two" {
		t.Fatalf("the registered adapter sent %q, want the new key sk-two", got)
	}
}

func TestSetProviderAPIKey_ClearFallsBackToEnvironment(t *testing.T) {
	isolateProviderKeys(t)
	var status atomic.Int32
	status.Store(http.StatusOK)
	var gotKey atomic.Value
	fakeAnthropicModels(t, &status, &gotKey)
	t.Setenv("ANTHROPIC_API_KEY", "sk-env")
	a, mux := newTestAPIWithSeededProviders(t)

	if code, body := postAPIKey(t, mux, "anthropic-001", "sk-keychain"); code != http.StatusOK {
		t.Fatalf("save: %d %v", code, body)
	}
	code, body := postAPIKey(t, mux, "anthropic-001", "")
	want := map[string]any{"has_key": false, "key_source": "environment", "provider_id": "anthropic-001"}
	if code != http.StatusOK || !reflect.DeepEqual(body, want) {
		t.Fatalf("clear: %d %v, want 200 %v", code, body, want)
	}
	p, ok := a.Services.Providers.Get("anthropic")
	if !ok {
		t.Fatal("clearing the keychain key unregistered a provider that has an env key")
	}
	if got := keyUsedBy(t, p, &gotKey); got != "sk-env" {
		t.Fatalf("after clear the adapter sent %q, want the env key", got)
	}
}

func TestSetProviderAPIKey_ClearWithNoKeyLeftUnregisters(t *testing.T) {
	isolateProviderKeys(t)
	a, mux := newTestAPIWithSeededProviders(t)

	if code, body := postAPIKey(t, mux, "anthropic-001", "sk-one"); code != http.StatusOK {
		t.Fatalf("save: %d %v", code, body)
	}
	code, body := postAPIKey(t, mux, "anthropic-001", "")
	want := map[string]any{"has_key": false, "key_source": "", "provider_id": "anthropic-001"}
	if code != http.StatusOK || !reflect.DeepEqual(body, want) {
		t.Fatalf("clear: %d %v, want 200 %v", code, body, want)
	}
	if a.Services.Providers.Has("anthropic") {
		t.Fatal("anthropic still registered with no key anywhere")
	}
}

func decodeObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("response is not a JSON object: %v — %s", err, raw)
	}
	return m
}
