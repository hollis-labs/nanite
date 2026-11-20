package providers_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/libs/security/secrets"
	"github.com/hollis-labs/libs/security/secrets/secretstest"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/agent/authored"
	"github.com/hollis-labs/nanite/agent/providers"
)

// seen is what the server saw on the last request.
type seen struct{ path, auth, apiKey, team string }

// server answers as an OpenAI-compatible chat server, or as the Anthropic
// Messages API under /v1/messages, and records the last request.
func server(t *testing.T) (*httptest.Server, func() seen) {
	t.Helper()
	var mu sync.Mutex
	var last seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		last = seen{r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Api-Key"), r.Header.Get("X-Team")}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/messages") {
			_, _ = io.WriteString(w, `{"id":"m","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"answer"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv, func() seen { mu.Lock(); defer mu.Unlock(); return last }
}

// fixtureValue is a test credential built at run time, so the source holds
// nothing a secret scanner reads as a key.
func fixtureValue(kind string) string { return "fixture-" + kind + "-7f3a91c2" }

var request = llmtypes.ChatRequest{Model: "gpt-5.6", Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hi"}}}

// scrubs reports whether text is scrubbed of key by a redactor on scope.
func scrubs(t *testing.T, scope *redact.Scope, key string) bool {
	t.Helper()
	r, err := redact.New(redact.WithScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	return !strings.Contains(r.Text("the key is "+key), key)
}

// TestNewThroughAGateway: a built-in provider behind a gateway sends the key
// its KeyRef resolves to and the gateway's headers, and puts the key in the
// caller's redaction scope.
func TestNewThroughAGateway(t *testing.T) {
	secretstest.Install(t)
	srv, last := server(t)
	gatewayKey := fixtureValue("gateway-key")
	t.Setenv("WORKBENCH_TEST_KEY", gatewayKey)
	scope := redact.NewScope()
	p, err := providers.New(providers.Config{Name: providers.OpenAI,
		Gateway: agent.Gateway{KeyRef: "env://WORKBENCH_TEST_KEY", BaseURL: srv.URL + "/v1", Headers: map[string]string{"X-Team": "nanite"}},
		Options: providers.Options{Secrets: secrets.New(secrets.WithEnv()), Redactor: scope}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := p.Complete(t.Context(), request); err != nil || got != "answer" {
		t.Fatalf("Complete = %q, %v", got, err)
	}
	if s := last(); s.path != "/v1/chat/completions" || s.auth != "Bearer "+gatewayKey || s.team != "nanite" {
		t.Fatalf("server saw %+v", s)
	}
	if !scrubs(t, scope, gatewayKey) {
		t.Fatal("the resolved key is not in the caller's redaction scope")
	}
}

// TestAnthropicBearer: auth bearer sends the key as a bearer token on the
// Anthropic wire, not as x-api-key.
func TestAnthropicBearer(t *testing.T) {
	secretstest.Install(t)
	srv, last := server(t)
	bearerKey := fixtureValue("bearer-key")
	t.Setenv("WORKBENCH_TEST_KEY", bearerKey)
	p, err := providers.New(providers.Config{Name: providers.Anthropic,
		Gateway: agent.Gateway{KeyRef: "env://WORKBENCH_TEST_KEY", BaseURL: srv.URL, Auth: "bearer"},
		Options: providers.Options{Secrets: secrets.New(secrets.WithEnv())}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := p.Complete(t.Context(), llmtypes.ChatRequest{Model: "claude-sonnet-5-5", MaxTokens: 16, Messages: request.Messages}); err != nil || got != "answer" {
		t.Fatalf("Complete = %q, %v", got, err)
	}
	if s := last(); s.path != "/v1/messages" || s.auth != "Bearer "+bearerKey || s.apiKey != "" {
		t.Fatalf("server saw %+v", s)
	}
}

func TestNewRefusesAnUnknownProvider(t *testing.T) {
	if _, err := providers.New(providers.Config{Name: "grpc"}); err == nil || !strings.Contains(err.Error(), "unsupported provider") {
		t.Fatalf("unknown provider: %v", err)
	}
	if _, err := providers.New(providers.Config{Name: providers.OpenAI, Gateway: agent.Gateway{Headers: map[string]string{"X-Team": "n"}}}); err == nil {
		t.Fatal("headers without a gateway URL were accepted")
	}
}

// TestNewNamed: a named provider is validated, reaches its endpoint with
// its key, and puts the key in the caller's redaction scope.
func TestNewNamed(t *testing.T) {
	secretstest.Install(t)
	srv, last := server(t)
	withEnv := secrets.New(secrets.WithEnv())
	if _, err := providers.NewNamed(authored.ProviderSpec{Name: "anthropic", Wire: authored.WireAnthropic, BaseURL: srv.URL}, providers.Options{}); err == nil {
		t.Fatal("a named provider redefined a built-in")
	}
	namedKey := fixtureValue("named-key")
	t.Setenv("WORKBENCH_TEST_KEY", namedKey)
	scope := redact.NewScope()
	p, err := providers.NewNamed(authored.ProviderSpec{Name: "local", Wire: authored.WireOpenAIChat, BaseURL: srv.URL + "/v1", KeyRef: "env://WORKBENCH_TEST_KEY"},
		providers.Options{Secrets: withEnv, Redactor: scope})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := p.Complete(t.Context(), request); err != nil || got != "answer" {
		t.Fatalf("Complete = %q, %v", got, err)
	}
	if s := last(); s.auth != "Bearer "+namedKey {
		t.Fatalf("server saw %+v", s)
	}
	if !scrubs(t, scope, namedKey) {
		t.Fatal("the resolved key is not in the caller's redaction scope")
	}
}
