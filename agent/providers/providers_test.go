package providers_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/libs/security/secrets"
	"github.com/hollis-labs/libs/security/secrets/secretstest"
	"github.com/hollis-labs/nanite/agent/authored"
	"github.com/hollis-labs/nanite/agent/providers"
)

// server is a minimal OpenAI-compatible chat server that records the path
// and Authorization header of the last request.
func server(t *testing.T) (*httptest.Server, func() (string, string)) {
	t.Helper()
	var mu sync.Mutex
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv, func() (string, string) { mu.Lock(); defer mu.Unlock(); return path, auth }
}

var withEnv = secrets.New(secrets.WithEnv())

var request = llmtypes.ChatRequest{Model: "gpt-5.6", Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hi"}}}

// TestNewResolvesTheKeyRef: a built-in provider behind a gateway sends the
// key its KeyRef resolves to.
func TestNewResolvesTheKeyRef(t *testing.T) {
	secretstest.Install(t)
	srv, seen := server(t)
	t.Setenv("WORKBENCH_TEST_KEY", "fixture-key")
	p, err := providers.New(providers.Config{Name: providers.OpenAI, KeyRef: "env://WORKBENCH_TEST_KEY", BaseURL: srv.URL + "/v1", Options: providers.Options{Secrets: withEnv}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := p.Complete(t.Context(), request); err != nil || got != "answer" {
		t.Fatalf("Complete = %q, %v", got, err)
	}
	if path, auth := seen(); path != "/v1/chat/completions" || auth != "Bearer fixture-key" {
		t.Fatalf("path %q auth %q", path, auth)
	}
}

func TestNewRefusesAnUnknownProvider(t *testing.T) {
	if _, err := providers.New(providers.Config{Name: "grpc"}); err == nil || !strings.Contains(err.Error(), "unsupported provider") {
		t.Fatalf("unknown provider: %v", err)
	}
}

// TestNewNamed: a named provider is validated and reaches its endpoint.
func TestNewNamed(t *testing.T) {
	secretstest.Install(t)
	srv, seen := server(t)
	if _, err := providers.NewNamed(authored.ProviderSpec{Name: "anthropic", Wire: authored.WireAnthropic, BaseURL: srv.URL}, providers.Options{}); err == nil {
		t.Fatal("a named provider redefined a built-in")
	}
	t.Setenv("WORKBENCH_TEST_KEY", "named-key")
	p, err := providers.NewNamed(authored.ProviderSpec{Name: "local", Wire: authored.WireOpenAIChat, BaseURL: srv.URL + "/v1", KeyRef: "env://WORKBENCH_TEST_KEY"}, providers.Options{Secrets: withEnv})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := p.Complete(t.Context(), request); err != nil || got != "answer" {
		t.Fatalf("Complete = %q, %v", got, err)
	}
	if _, auth := seen(); auth != "Bearer named-key" {
		t.Fatalf("auth %q", auth)
	}
}
