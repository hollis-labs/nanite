package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/libs/security/secrets/secretstest"
)

// compatServer is a minimal OpenAI-compatible chat server that records the
// path and Authorization header of each request.
func compatServer(t *testing.T) (*httptest.Server, func() (string, string, string)) {
	t.Helper()
	var mu sync.Mutex
	var path, auth, team string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		path, auth, team = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Team")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"local answer"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv, func() (string, string, string) { mu.Lock(); defer mu.Unlock(); return path, auth, team }
}

func TestNamedOpenAICompatibleProvider(t *testing.T) {
	srv, seen := compatServer(t)
	p, err := NewNamed(Spec{Name: "local", Wire: WireOpenAIChat, BaseURL: srv.URL + "/v1", Auth: AuthNone, Headers: map[string]string{"X-Team": "nanite"}}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	// A reasoning model with a budget would use the Responses API on the
	// openai wire; openai-chat pins Chat Completions.
	ctx := llmcontracts.WithReasoningConfig(context.Background(), llmcontracts.ReasoningConfig{Enabled: true, BudgetTokens: 8000})
	got, err := p.Complete(ctx, llmtypes.ChatRequest{Model: "gpt-5.6", Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil || got != "local answer" {
		t.Fatalf("Complete = %q, %v", got, err)
	}
	path, auth, team := seen()
	if path != "/v1/chat/completions" || auth != "Bearer "+keylessPlaceholder || team != "nanite" {
		t.Fatalf("path %q auth %q team %q", path, auth, team)
	}
}

func TestNamedProviderKeyFromEnvNeedsOptIn(t *testing.T) {
	secretstest.Install(t)
	srv, seen := compatServer(t)
	spec := Spec{Name: "router", Wire: WireOpenAIChat, BaseURL: srv.URL + "/v1", KeyEnv: "ROUTER_TEST_KEY"}
	t.Setenv("ROUTER_TEST_KEY", "fixture-router-key")
	request := llmtypes.ChatRequest{Model: "m", Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hi"}}}

	p, err := NewNamed(spec, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Complete(context.Background(), request); err == nil {
		t.Fatal("key_env was read without NANITE_PROVIDER_KEY_ENV=1")
	}

	t.Setenv("NANITE_PROVIDER_KEY_ENV", "1")
	p, err = NewNamed(spec, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Complete(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, auth, _ := seen(); auth != "Bearer fixture-router-key" {
		t.Fatalf("auth %q", auth)
	}
}

func TestNamedAnthropicWireUsesBearerAuth(t *testing.T) {
	var auth, apiKey, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, apiKey, path = r.Header.Get("Authorization"), r.Header.Get("X-Api-Key"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"m","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"text","text":"via gateway"}]}`)
	}))
	t.Cleanup(srv.Close)
	secretstest.Install(t)
	t.Setenv("NANITE_PROVIDER_KEY_ENV", "1")
	t.Setenv("GATEWAY_TEST_KEY", "fixture-gateway-key")
	p, err := NewNamed(Spec{Name: "station", Wire: WireAnthropic, BaseURL: srv.URL, KeyEnv: "GATEWAY_TEST_KEY", Auth: AuthBearer}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Complete(context.Background(), llmtypes.ChatRequest{Model: "claude-sonnet-5-5", Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil || got != "via gateway" {
		t.Fatalf("Complete = %q, %v", got, err)
	}
	if path != "/v1/messages" || auth != "Bearer fixture-gateway-key" || apiKey != "" {
		t.Fatalf("path %q auth %q x-api-key %q", path, auth, apiKey)
	}
}

func TestNamedProvidersIgnoreAmbientCredentials(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "ambient-anthropic-key")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "ambient-anthropic-token")
	t.Setenv("OPENAI_ORG_ID", "ambient-org")
	t.Setenv("OPENAI_PROJECT_ID", "ambient-project")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "X-Ambient: ambient-header")
	var mu sync.Mutex
	var seen []http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Clone())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/messages") {
			_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"m","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"text","text":"ok"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)
	request := llmtypes.ChatRequest{Model: "m", Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hi"}}}
	for _, spec := range []Spec{
		{Name: "a-none", Wire: WireAnthropic, BaseURL: srv.URL, Auth: AuthNone},
		{Name: "o-none", Wire: WireOpenAIChat, BaseURL: srv.URL + "/v1", Auth: AuthNone},
	} {
		p, err := NewNamed(spec, Config{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Complete(context.Background(), request); err != nil {
			t.Fatalf("%s: %v", spec.Name, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("requests %d", len(seen))
	}
	for _, h := range seen {
		for name, values := range h {
			for _, v := range values {
				if strings.Contains(v, "ambient") {
					t.Errorf("ambient value reached the endpoint in %s: %q", name, v)
				}
			}
		}
	}
}
