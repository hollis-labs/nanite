package service

// Regression coverage for c195: a session created with provider="pty" /
// model="claude-cli" was silently routed to the Anthropic HTTP API,
// which 404'd on the CLI model id. resolveProvider's miss-path fell
// through to anthropic instead of returning (name, nil) for CLI aliases
// — so the classifyNilProvider CLI bypass at chat_generate.go never
// engaged.
//
// These tests pin every miss point in resolveProvider where a CLI alias
// must short-circuit. The trick: a registered "anthropic" provider must
// be present in the registry for each test, because the pre-fix bug
// only manifests when the final `providers.Get("anthropic")` succeeds
// and the chat loop ends up with a non-nil HTTP Provider in hand. With
// an empty registry, every miss path falls through to `return inferred,
// nil` at the bottom of resolveProvider — which happens to satisfy the
// post-fix contract by accident and would mask a regression.
//
// Pinned:
//
//  1. sessionProvider="pty" → ("pty", nil)               [CLI session]
//  2. agentProvider="pty-claude" → ("pty-claude", nil)   [CLI agent default]
//  3. ProviderFallbackChain=["pty"] → ("pty", nil)       [CLI in fallback]
//     — uses model="claude-sonnet-..." so InferProvider returns
//       "anthropic"; without the fallback-chain CLI guard the test
//       would fall through to the registered anthropic stub.
//  4. model="claude-cli" → ("pty", nil)                  [CLI inferred]
//  5. ProviderFallbackChain=["openai"] (non-CLI, unregistered) emits
//     a slog.Warn — previously a silent miss that masked correctly
//     configured chains being ignored.
//
// Without these guards, c195's failure mode (Anthropic 404 on
// `model: claude-cli`) re-emerges the moment someone picks the Claude
// CLI dropdown row, OR sets up a `["pty"]` fallback chain.

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestResolveProvider_CLISessionProvider_ShortCircuits(t *testing.T) {
	s := &chatServiceImpl{
		providers: newRegistryWithAnthropicStub(),
		store:     mustNewStoreForResolveTest(t),
	}

	name, prov := s.resolveProvider("sess-1", "pty", "", "claude-cli")
	if name != "pty" {
		t.Fatalf("resolveProvider returned name=%q, want %q (c195 regression — must not fall through to anthropic stub)", name, "pty")
	}
	if prov != nil {
		t.Fatalf("resolveProvider returned non-nil prov for CLI alias; must be nil so classifyNilProvider routes to driveBootSession")
	}
}

func TestResolveProvider_CLIAgentProvider_ShortCircuits(t *testing.T) {
	s := &chatServiceImpl{
		providers: newRegistryWithAnthropicStub(),
		store:     mustNewStoreForResolveTest(t),
	}

	name, prov := s.resolveProvider("sess-1", "", "pty-claude", "claude-cli")
	if name != "pty-claude" {
		t.Fatalf("resolveProvider returned name=%q, want %q (must not fall through to anthropic stub)", name, "pty-claude")
	}
	if prov != nil {
		t.Fatalf("resolveProvider returned non-nil prov for CLI agent default; must be nil")
	}
}

func TestResolveProvider_FallbackChainCLI_ShortCircuits(t *testing.T) {
	st := mustNewStoreForResolveTest(t)
	us, err := st.GetUserSettings()
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	us.ProviderFallbackChain = []string{"pty"}
	if err := st.UpdateUserSettings(us); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}

	s := &chatServiceImpl{
		providers: newRegistryWithAnthropicStub(),
		store:     st,
	}

	// Use a non-CLI model so InferProvider returns "anthropic" — that
	// way the fallback-chain CLI guard is the ONLY thing standing
	// between us and the registered anthropic stub. With model=
	// "claude-cli" InferProvider would return "pty" and the
	// inferred-CLI guard would short-circuit too, masking a broken
	// fallback-chain layer.
	name, prov := s.resolveProvider("sess-1", "", "", "claude-sonnet-4-20250514")
	if name != "pty" {
		t.Fatalf("resolveProvider returned name=%q, want %q (fallback-chain CLI alias must short-circuit before InferProvider→anthropic stub)", name, "pty")
	}
	if prov != nil {
		t.Fatalf("resolveProvider returned non-nil prov for CLI fallback entry; must be nil")
	}
}

func TestResolveProvider_InferredCLI_ShortCircuits(t *testing.T) {
	// model="claude-cli" → InferProvider returns "pty". Registry has
	// an "anthropic" provider but no "pty". Defense-in-depth: even if
	// the dropdown shape never reached sessionProvider, an unannotated
	// model id mapping to a CLI provider must still route to the CLI
	// bypass rather than the HTTP anthropic stub.
	s := &chatServiceImpl{
		providers: newRegistryWithAnthropicStub(),
		store:     mustNewStoreForResolveTest(t),
	}

	name, prov := s.resolveProvider("sess-1", "", "", "claude-cli")
	if name != "pty" {
		t.Fatalf("resolveProvider inferred name=%q, want %q (InferProvider(claude-cli) must short-circuit on CLI before anthropic stub)", name, "pty")
	}
	if prov != nil {
		t.Fatalf("resolveProvider returned non-nil prov for inferred CLI alias; must be nil")
	}
}

// TestResolveProvider_FallbackChainNonCLIMiss_Warns pins the audibility
// fix: before the slog.Warn was added, an entry like ["openai"] in the
// fallback chain that wasn't registered (no API key configured) was
// silently skipped — masking operator misconfiguration. The c195 debug
// surfaced this when a correctly-set ["pty"] chain was ignored without
// any signal.
func TestResolveProvider_FallbackChainNonCLIMiss_Warns(t *testing.T) {
	logs := captureSlogOutput(t)

	st := mustNewStoreForResolveTest(t)
	us, err := st.GetUserSettings()
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	us.ProviderFallbackChain = []string{"openai"} // non-CLI, will miss
	if err := st.UpdateUserSettings(us); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}

	s := &chatServiceImpl{
		providers: newRegistryWithAnthropicStub(),
		store:     st,
	}

	// The fallback chain entry "openai" must miss the registry (only
	// "anthropic" is registered), emit a Warn, and let resolution
	// continue to InferProvider→anthropic.
	name, _ := s.resolveProvider("sess-1", "", "", "claude-sonnet-4-20250514")
	if name != "anthropic" {
		t.Fatalf("resolveProvider returned name=%q, want %q (non-CLI miss must skip but not short-circuit)", name, "anthropic")
	}

	out := logs.String()
	if !strings.Contains(out, "fallback-chain provider not registered") {
		t.Fatalf("expected fallback-chain miss Warn in logs, got: %s", out)
	}
	if !strings.Contains(out, "openai") {
		t.Fatalf("expected the missing provider name in the Warn, got: %s", out)
	}
}

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

// newRegistryWithAnthropicStub builds a registry that has ONLY an
// "anthropic" provider registered. This is the pre-c195-fix trap shape:
// any miss path in resolveProvider that falls through to the final
// `providers.Get("anthropic")` will return a non-nil Provider, which is
// exactly what produced the 404 on `model: claude-cli`. Tests use this
// registry to prove that the CLI guards short-circuit BEFORE that
// terminal fallthrough.
func newRegistryWithAnthropicStub() *provider.Registry {
	reg := provider.NewRegistry()
	reg.Register("anthropic", stubLLMProvider{})
	return reg
}

func mustNewStoreForResolveTest(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(context.Background(), t.TempDir()+"/resolve.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.DB.Exec(`INSERT OR IGNORE INTO user_settings (id) VALUES (1)`); err != nil {
		t.Fatalf("seed user_settings: %v", err)
	}
	return s
}

func captureSlogOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// stubLLMProvider is the minimum llmcontracts.Provider needed to occupy
// a registry slot. resolveProvider only checks identity (registry hit
// vs miss) and never invokes any method, so the bodies stay no-op.
type stubLLMProvider struct{}

func (stubLLMProvider) StreamChat(context.Context, llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	return nil, nil
}
func (stubLLMProvider) Complete(context.Context, llmtypes.ChatRequest) (string, error) { return "", nil }
func (stubLLMProvider) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{}
}

var _ llmcontracts.Provider = stubLLMProvider{}
