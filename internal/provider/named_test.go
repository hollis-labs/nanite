package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/libs/security/secrets/secretstest"
	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	"github.com/hollis-labs/nanite/internal/warden"
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

// wardenFake mints tok-1, tok-2, ... from rotating parents.
func wardenFake(t *testing.T) (*warden.Source, *httptest.Server) {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		_ = r.ParseForm()
		if r.Form.Get("refresh_token") != "parent-"+strconv.Itoa(n) || r.Form.Get("audience") != "llm-gateway" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		n++
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":"tok-%d","expires_in":900,"refresh_token":"parent-%d"}`, n, n)
	}))
	t.Cleanup(srv.Close)
	file := filepath.Join(t.TempDir(), "agent.cred")
	if err := tokenfile.Write(file, "parent-0"); err != nil {
		t.Fatal(err)
	}
	src, err := warden.New(warden.Config{URL: srv.URL, CredentialFile: file, Principal: "agent:test"})
	if err != nil {
		t.Fatal(err)
	}
	return src, srv
}

func TestNamedProviderWithWardenAuthSendsAFreshToken(t *testing.T) {
	src, _ := wardenFake(t)
	request := llmtypes.ChatRequest{Model: "m", Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hi"}}}
	for _, wire := range []string{WireOpenAIChat, WireAnthropic} {
		t.Run(wire, func(t *testing.T) {
			var mu sync.Mutex
			var auths, apiKeys []string
			reject := true
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				auths, apiKeys = append(auths, r.Header.Get("Authorization")), append(apiKeys, r.Header.Get("X-Api-Key"))
				first := reject
				reject = false
				mu.Unlock()
				if first {
					// The gateway refuses the first token (revoked, say).
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/messages") {
					_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"m","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"text","text":"ok"}]}`)
					return
				}
				_, _ = io.WriteString(w, `{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
			}))
			t.Cleanup(srv.Close)
			base := srv.URL
			if wire != WireAnthropic {
				base += "/v1"
			}
			p, err := NewNamed(Spec{Name: "gateway", Wire: wire, BaseURL: base, Auth: AuthWarden, Audience: "llm-gateway", Scopes: []string{"llm-gateway.chat"}}, Config{Warden: src})
			if err != nil {
				t.Fatal(err)
			}
			// The first token is refused; the request is retried once with a
			// fresh one and succeeds.
			if _, err := p.Complete(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(auths) != 2 || auths[0] == auths[1] || !strings.HasPrefix(auths[1], "Bearer tok-") {
				t.Fatalf("Authorization headers %q: want a retry with a new token after the 401", auths)
			}
			for _, k := range apiKeys {
				if k != "" {
					t.Fatalf("x-api-key %q sent with Warden auth", k)
				}
			}
		})
	}
}

func TestWardenAuthValidation(t *testing.T) {
	base := Spec{Name: "gateway", Wire: WireOpenAIChat, BaseURL: "https://llm.example.test/v1"}
	for name, tc := range map[string]struct {
		mod func(*Spec)
		ok  bool
	}{
		"ok":               {func(s *Spec) { s.Auth, s.Audience, s.Scopes = AuthWarden, "llm-gateway", []string{"llm-gateway.chat"} }, true},
		"no audience":      {func(s *Spec) { s.Auth = AuthWarden }, false},
		"key with warden":  {func(s *Spec) { s.Auth, s.Audience, s.KeyRef = AuthWarden, "llm-gateway", "env://X" }, false},
		"audience no auth": {func(s *Spec) { s.Audience = "llm-gateway" }, false},
		"bad scope":        {func(s *Spec) { s.Auth, s.Audience, s.Scopes = AuthWarden, "llm-gateway", []string{"a b"} }, false},
	} {
		s := base
		tc.mod(&s)
		if err := s.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: Validate() = %v", name, err)
		}
	}
	if _, err := NewNamed(Spec{Name: "gateway", Wire: WireOpenAIChat, BaseURL: "https://llm.example.test/v1", Auth: AuthWarden, Audience: "llm-gateway"}, Config{}); err == nil {
		t.Fatal("auth warden without a Warden source was accepted")
	}
}
