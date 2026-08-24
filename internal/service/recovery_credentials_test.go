package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/provider"
	nllmanthropic "github.com/hollis-labs/nanite/internal/llm/anthropic"
	nllmopenai "github.com/hollis-labs/nanite/internal/llm/openai"
	"github.com/hollis-labs/nanite/internal/store"
)

// fakeProfileResolver lets tests dictate exactly what GetOrDefault returns
// without spinning up the store. Returning (nil, nil) exercises the
// "resolved nil with no error" defensive branch; returning a non-nil err
// exercises the resolution-failure branch.
type fakeProfileResolver struct {
	profiles map[string]*store.AgentProfile
	err      error
}

func (f *fakeProfileResolver) GetOrDefault(name string) (*store.AgentProfile, error) {
	if f.err != nil {
		return nil, f.err
	}
	if p, ok := f.profiles[name]; ok {
		return p, nil
	}
	// Mirror the production default the real resolver returns so
	// "unknown name → default profile" can be exercised without panic.
	if def, ok := f.profiles["default"]; ok {
		return def, nil
	}
	return nil, nil
}

// newAdapterForTest builds an adapter with stubbed keychain seams so
// tests don't touch the real OS keychain.
func newAdapterForTest(profiles map[string]*store.AgentProfile, reg *provider.Registry, keys map[string]string) *recoveryCredentialsAdapter {
	return &recoveryCredentialsAdapter{
		agents:    &fakeProfileResolver{profiles: profiles},
		providers: reg,
		keyringLookup: func(k string) string {
			return keys[k]
		},
		providerKeyName: func(provID string) string {
			return "provider-api-key:" + provID
		},
	}
}

// TestRefresh_AnthropicSuccess: happy path — agent profile uses
// "anthropic", keychain has a key, registered *anthropic.Client receives
// it via SetAPIKey, no error.
func TestRefresh_AnthropicSuccess(t *testing.T) {
	profiles := map[string]*store.AgentProfile{
		"chat": {ID: "chat-1", Name: "chat", Slug: "chat", DefaultProvider: "anthropic"},
	}
	reg := provider.NewRegistry()
	client := nllmanthropic.New()
	reg.Register("anthropic", client)

	a := newAdapterForTest(profiles, reg, map[string]string{
		"provider-api-key:anthropic-001": "sk-ant-test-key",
	})

	if err := a.Refresh(context.Background(), "chat"); err != nil {
		t.Fatalf("Refresh: unexpected error: %v", err)
	}
	// SetAPIKey is not directly observable on Client (apiKey is private)
	// but its observable side-effect is that StreamChat no longer
	// returns "ANTHROPIC_API_KEY not set". We exercise that by
	// constructing a request with an empty model — the early return for
	// missing key would fire if SetAPIKey hadn't taken effect.
	// (Skipped: full StreamChat path needs an HTTP transport stub. The
	// type-assertion path is what we cover here; the SetAPIKey call
	// itself is exercised in internal/llm/anthropic/client_test.go.)
}

// TestRefresh_OpenAISuccess: parity with anthropic for the openai
// provider — different concrete type, different keychain provID.
func TestRefresh_OpenAISuccess(t *testing.T) {
	profiles := map[string]*store.AgentProfile{
		"summarize": {ID: "sum-1", Name: "summarize", Slug: "summarize", DefaultProvider: "openai"},
	}
	reg := provider.NewRegistry()
	client := nllmopenai.New("", nil)
	reg.Register("openai", client)

	a := newAdapterForTest(profiles, reg, map[string]string{
		"provider-api-key:openai-001": "sk-openai-test-key",
	})

	if err := a.Refresh(context.Background(), "summarize"); err != nil {
		t.Fatalf("Refresh: unexpected error: %v", err)
	}
}

// TestRefresh_UnknownProfileFallsThroughToDefault: GetOrDefault returns
// the default profile (DefaultProvider="claude"); the adapter sees the
// CLI-managed kind and errors cleanly. No panic on "unknown name".
func TestRefresh_UnknownProfileFallsThroughToDefault(t *testing.T) {
	profiles := map[string]*store.AgentProfile{
		"default": {ID: "default-1", Name: "default", Slug: "default", DefaultProvider: "claude"},
	}
	a := newAdapterForTest(profiles, provider.NewRegistry(), nil)

	err := a.Refresh(context.Background(), "this-profile-does-not-exist")
	if err == nil {
		t.Fatal("Refresh: expected error for CLI-managed default fallback, got nil")
	}
	if !strings.Contains(err.Error(), "manages its own auth") {
		t.Errorf("Refresh: expected CLI-managed error, got: %v", err)
	}
}

// TestRefresh_ResolverError: GetOrDefault returns an error; Refresh
// wraps it with context (no panic).
func TestRefresh_ResolverError(t *testing.T) {
	a := &recoveryCredentialsAdapter{
		agents:    &fakeProfileResolver{err: errors.New("db unavailable")},
		providers: provider.NewRegistry(),
		keyringLookup: func(string) string {
			return "x"
		},
		providerKeyName: func(string) string {
			return "x"
		},
	}

	err := a.Refresh(context.Background(), "any")
	if err == nil {
		t.Fatal("Refresh: expected error from failed resolver, got nil")
	}
	if !strings.Contains(err.Error(), "resolve agent profile") {
		t.Errorf("Refresh: expected resolver wrap, got: %v", err)
	}
	if !strings.Contains(err.Error(), "db unavailable") {
		t.Errorf("Refresh: expected wrapped underlying error, got: %v", err)
	}
}

// TestRefresh_NilProfile: defensive — resolver returns (nil, nil)
// without error. Adapter must not panic and must surface a clean error.
func TestRefresh_NilProfile(t *testing.T) {
	a := newAdapterForTest(nil, provider.NewRegistry(), nil) // empty profile map → nil profile, nil err

	err := a.Refresh(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("Refresh: expected error for nil profile, got nil")
	}
	if !strings.Contains(err.Error(), "nil profile") {
		t.Errorf("Refresh: expected nil-profile message, got: %v", err)
	}
}

// TestRefresh_EmptyDefaultProvider: profile resolved but its
// DefaultProvider is empty. Surface a clean error rather than blank
// keychain lookup.
func TestRefresh_EmptyDefaultProvider(t *testing.T) {
	profiles := map[string]*store.AgentProfile{
		"orphan": {ID: "orphan-1", Name: "orphan", Slug: "orphan", DefaultProvider: ""},
	}
	a := newAdapterForTest(profiles, provider.NewRegistry(), nil)

	err := a.Refresh(context.Background(), "orphan")
	if err == nil {
		t.Fatal("Refresh: expected error for empty DefaultProvider, got nil")
	}
	if !strings.Contains(err.Error(), "no DefaultProvider") {
		t.Errorf("Refresh: expected empty-default-provider message, got: %v", err)
	}
}

// TestRefresh_UnknownProvider: profile maps to a provider name the
// adapter doesn't recognize. Returns clean error so the broker
// escalates.
func TestRefresh_UnknownProvider(t *testing.T) {
	profiles := map[string]*store.AgentProfile{
		"weird": {ID: "weird-1", Name: "weird", Slug: "weird", DefaultProvider: "mystery-llm"},
	}
	a := newAdapterForTest(profiles, provider.NewRegistry(), nil)

	err := a.Refresh(context.Background(), "weird")
	if err == nil {
		t.Fatal("Refresh: expected error for unknown provider, got nil")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("Refresh: expected unknown-provider message, got: %v", err)
	}
}

// TestRefresh_CLIManaged_ReturnsError: CLI providers (claude/codex/
// opencode) own their own auth. The broker must escalate to Permanent
// rather than retry a session that will hit the same auth wall, so
// Refresh returns a non-nil error.
func TestRefresh_CLIManaged_ReturnsError(t *testing.T) {
	cases := []string{"claude", "codex", "opencode", "pty-claude", "pty-codex", "pty-opencode"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			profiles := map[string]*store.AgentProfile{
				"agent": {ID: "a-1", Name: "agent", Slug: "agent", DefaultProvider: name},
			}
			a := newAdapterForTest(profiles, provider.NewRegistry(), nil)

			err := a.Refresh(context.Background(), "agent")
			if err == nil {
				t.Fatalf("Refresh: expected error for CLI-managed provider %q, got nil", name)
			}
			if !strings.Contains(err.Error(), "manages its own auth") {
				t.Errorf("Refresh: expected CLI-managed error for %q, got: %v", name, err)
			}
		})
	}
}

// TestRefresh_KeychainMiss: API provider but the keychain has no entry
// (user never configured / launchd context can't reach keychain). Clean
// error so the broker escalates.
func TestRefresh_KeychainMiss(t *testing.T) {
	profiles := map[string]*store.AgentProfile{
		"chat": {ID: "chat-1", Name: "chat", Slug: "chat", DefaultProvider: "anthropic"},
	}
	a := newAdapterForTest(profiles, provider.NewRegistry(), map[string]string{
		// no entry for provider-api-key:anthropic-001
	})

	err := a.Refresh(context.Background(), "chat")
	if err == nil {
		t.Fatal("Refresh: expected error for empty keychain, got nil")
	}
	if !strings.Contains(err.Error(), "keychain has no key") {
		t.Errorf("Refresh: expected keychain-miss message, got: %v", err)
	}
}

// TestRefresh_ProviderNotRegistered: keychain has a key, but the API
// provider was never registered (boot path skips Register when the key
// was missing at boot time). The user has set the key since but hasn't
// restarted nanite — clean error pointing at the restart.
func TestRefresh_ProviderNotRegistered(t *testing.T) {
	profiles := map[string]*store.AgentProfile{
		"chat": {ID: "chat-1", Name: "chat", Slug: "chat", DefaultProvider: "anthropic"},
	}
	a := newAdapterForTest(profiles, provider.NewRegistry(), map[string]string{
		"provider-api-key:anthropic-001": "sk-ant-fresh",
	})

	err := a.Refresh(context.Background(), "chat")
	if err == nil {
		t.Fatal("Refresh: expected error when provider absent from registry, got nil")
	}
	if !strings.Contains(err.Error(), "not registered") {
		t.Errorf("Refresh: expected not-registered message, got: %v", err)
	}
}

// TestRefresh_RegistryNotWired: API provider path with nil registry on
// the adapter (lightweight test setup). Surfaces the error rather than
// panicking on the nil deref.
func TestRefresh_RegistryNotWired(t *testing.T) {
	profiles := map[string]*store.AgentProfile{
		"chat": {ID: "chat-1", Name: "chat", Slug: "chat", DefaultProvider: "anthropic"},
	}
	a := &recoveryCredentialsAdapter{
		agents:          &fakeProfileResolver{profiles: profiles},
		providers:       nil,
		keyringLookup:   func(string) string { return "sk-x" },
		providerKeyName: func(string) string { return "x" },
	}

	err := a.Refresh(context.Background(), "chat")
	if err == nil {
		t.Fatal("Refresh: expected error for nil provider registry, got nil")
	}
	if !strings.Contains(err.Error(), "provider registry not wired") {
		t.Errorf("Refresh: expected registry-not-wired message, got: %v", err)
	}
}

// TestRefresh_RespectsCanceledContext: pre-canceled ctx short-circuits
// the flow without a panic and without touching the resolver/keychain.
func TestRefresh_RespectsCanceledContext(t *testing.T) {
	resolverCalls := 0
	keyringCalls := 0
	a := &recoveryCredentialsAdapter{
		agents: &fakeProfileResolver{
			profiles: map[string]*store.AgentProfile{
				"chat": {DefaultProvider: "anthropic"},
			},
		},
		providers: provider.NewRegistry(),
		keyringLookup: func(string) string {
			keyringCalls++
			return "sk-x"
		},
		providerKeyName: func(s string) string { return "k:" + s },
	}
	// Wrap the resolver to count calls — fakeProfileResolver doesn't
	// expose that but the public Refresh path is the surface we care
	// about.
	a.agents = &countingResolver{
		inner:    a.agents.(*fakeProfileResolver),
		callsPtr: &resolverCalls,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := a.Refresh(ctx, "chat")
	if err == nil {
		t.Fatal("Refresh: expected error for canceled ctx, got nil")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("Refresh: expected ctx.Canceled wrap, got: %v", err)
	}
	if resolverCalls != 0 {
		t.Errorf("Refresh: resolver called %d times after canceled ctx, want 0", resolverCalls)
	}
	if keyringCalls != 0 {
		t.Errorf("Refresh: keychain consulted %d times after canceled ctx, want 0", keyringCalls)
	}
}

// TestRefresh_RespectsTimeoutContext: deadline-exceeded ctx behaves the
// same as canceled — short-circuit before any work.
func TestRefresh_RespectsTimeoutContext(t *testing.T) {
	a := &recoveryCredentialsAdapter{
		agents: &fakeProfileResolver{
			profiles: map[string]*store.AgentProfile{
				"chat": {DefaultProvider: "anthropic"},
			},
		},
		providers:       provider.NewRegistry(),
		keyringLookup:   func(string) string { return "sk-x" },
		providerKeyName: func(s string) string { return "k:" + s },
	}

	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancel()

	err := a.Refresh(ctx, "chat")
	if err == nil {
		t.Fatal("Refresh: expected error for past-deadline ctx, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("Refresh: expected deadline-exceeded wrap, got: %v", err)
	}
}

// TestRefresh_NilReceiver: defensive — calling Refresh on a nil pointer
// receiver should error cleanly, not panic. Documented as legitimate
// (callers in tests can pass nil where the broker expects non-nil and
// surface this via Remediate's nil-check).
func TestRefresh_NilReceiver(t *testing.T) {
	var a *recoveryCredentialsAdapter
	err := a.Refresh(context.Background(), "anything")
	if err == nil {
		t.Fatal("Refresh: expected error on nil receiver, got nil")
	}
}

// TestRefresh_NilAgentsResolver: adapter constructed without a profile
// resolver; surface clean error.
func TestRefresh_NilAgentsResolver(t *testing.T) {
	a := &recoveryCredentialsAdapter{
		agents:          nil,
		providers:       provider.NewRegistry(),
		keyringLookup:   func(string) string { return "" },
		providerKeyName: func(string) string { return "" },
	}
	err := a.Refresh(context.Background(), "x")
	if err == nil {
		t.Fatal("Refresh: expected error for nil agents resolver, got nil")
	}
	if !strings.Contains(err.Error(), "agents resolver not wired") {
		t.Errorf("Refresh: expected agents-not-wired message, got: %v", err)
	}
}

// countingResolver wraps fakeProfileResolver to expose a call counter
// (used to assert we don't reach the resolver after a canceled ctx).
type countingResolver struct {
	inner    *fakeProfileResolver
	callsPtr *int
}

func (c *countingResolver) GetOrDefault(name string) (*store.AgentProfile, error) {
	*c.callsPtr++
	return c.inner.GetOrDefault(name)
}

// TestClassifyProvider exercises the bare mapping table so future
// edits to the catalog (e.g. adding "azure-openai") are forced through
// a deliberate test update.
func TestClassifyProvider(t *testing.T) {
	cases := []struct {
		name       string
		wantKind   providerKind
		wantProvID string
	}{
		{"anthropic", providerKindAPIAnthropic, "anthropic-001"},
		{"openai", providerKindAPIOpenAI, "openai-001"},
		{"claude", providerKindCLIManaged, ""},
		{"codex", providerKindCLIManaged, ""},
		{"opencode", providerKindCLIManaged, ""},
		{"pty-claude", providerKindCLIManaged, ""},
		{"pty-codex", providerKindCLIManaged, ""},
		{"pty-opencode", providerKindCLIManaged, ""},
		{"", providerKindUnknown, ""},
		{"gemini", providerKindUnknown, ""},
		{"mystery", providerKindUnknown, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotKind, gotProvID := classifyProvider(tc.name)
			if gotKind != tc.wantKind {
				t.Errorf("classifyProvider(%q) kind = %v, want %v", tc.name, gotKind, tc.wantKind)
			}
			if gotProvID != tc.wantProvID {
				t.Errorf("classifyProvider(%q) provID = %q, want %q", tc.name, gotProvID, tc.wantProvID)
			}
		})
	}
}
