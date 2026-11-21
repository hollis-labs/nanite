package provider

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/libs/security/secrets/secretstest"
)

// The built-in providers read ANTHROPIC_API_KEY / OPENAI_API_KEY only when
// NANITE_PROVIDER_KEY_ENV=1. Without the opt-in, a key in the environment is
// never used: resolution fails and no request leaves the process.
func TestBuiltinProviderEnvKeyNeedsOptIn(t *testing.T) {
	const key = "fixture-env-key-must-not-be-used"
	for _, provider := range []string{"anthropic", "openai"} {
		envName := map[string]string{"anthropic": "ANTHROPIC_API_KEY", "openai": "OPENAI_API_KEY"}[provider]
		for _, optIn := range []string{"", "0", "true", "1"} {
			t.Run(provider+"/opt-in="+optIn, func(t *testing.T) {
				secretstest.Install(t)
				t.Setenv("NANITE_PROVIDER_KEY_ENV", optIn)
				t.Setenv(envName, key)
				var requests, carried atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					for _, values := range r.Header {
						for _, v := range values {
							if strings.Contains(v, key) {
								carried.Add(1)
							}
						}
					}
					w.WriteHeader(http.StatusBadRequest)
				}))
				defer server.Close()
				p, err := New(Config{Provider: provider, Gateway: Gateway{BaseURL: server.URL}})
				if err != nil {
					t.Fatal(err)
				}
				_, err = p.Complete(t.Context(), llmtypes.ChatRequest{Model: "fixture-model", Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hi"}}})
				if optIn == "1" {
					// Control: with the opt-in the key is used, proving the
					// request path is reached.
					if carried.Load() == 0 {
						t.Fatalf("opted-in key not sent: requests=%d err=%v", requests.Load(), err)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), "provider key") {
					t.Fatalf("expected key resolution failure, got %v", err)
				}
				if strings.Contains(err.Error(), key) {
					t.Fatal("error leaks the environment key")
				}
				if requests.Load() != 0 {
					t.Fatalf("%d request(s) sent without a resolved key (%d carried the env key)", requests.Load(), carried.Load())
				}
			})
		}
	}
}
