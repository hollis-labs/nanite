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
//
// CW-20260812-0001 investigation added two more pins after finding a
// live production bug: user_settings.default_provider was never
// consulted at all, so a session with no explicit provider fell straight
// to ProviderFallbackChain — and a stale `["pty"]` entry left over from
// before this app's API-first default won over a correctly-configured
// default_provider="anthropic", silently routing ordinary API sessions
// into a CLI boot attempt that crashed downstream with `bootdir for
// provider ""`.
//
//  6. default_provider="anthropic", ProviderFallbackChain=["pty"] →
//     ("anthropic", <non-nil>) — default_provider must win over a stale
//     fallback-chain CLI alias, not the other way around.
//  7. default_provider="pty" (itself CLI-shaped) → ("pty", nil) — the
//     new step gets the same CLI short-circuit treatment as every other
//     step in the chain.

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
	"github.com/hollis-labs/nanite/internal/storetest"
)

func TestResolveProvider_CLISessionProvider_ShortCircuits(t *testing.T) {
	s := &chatServiceImpl{
		providers: newRegistryWithAnthropicStub(),
		store:     mustNewStoreForResolveTest(t),
	}

	name, prov := s.resolveProvider("sess-1", "pty", "", "claude-cli", "api")
	if name != "pty" {
		t.Fatalf("resolveProvider returned name=%q, want %q (c195 regression — must not fall through to anthropic stub)", name, "pty")
	}
	if prov != nil {
		t.Fatalf("resolveProvider returned non-nil prov for CLI alias; must be nil so classifyNilProvider routes to driveBootSession")
	}
}

func TestResolveProvider_StoredProviderID_UsesRuntimeProviderType(t *testing.T) {
	st := mustNewStoreForResolveTest(t)
	if _, err := st.DB.Exec(
		`INSERT INTO providers (id, name, provider_type, is_enabled, settings) VALUES (?, ?, ?, ?, ?)`,
		"tether-001", "Tether", "tether", true, "{}",
	); err != nil {
		t.Fatalf("insert provider row: %v", err)
	}

	reg := newRegistryWithAnthropicStub()
	reg.Register("tether", stubLLMProvider{})

	s := &chatServiceImpl{
		providers: reg,
		store:     st,
	}

	name, prov := s.resolveProvider("sess-1", "tether-001", "", "auto", "api")
	if name != "tether" {
		t.Fatalf("resolveProvider returned name=%q, want %q (stored provider ids must map to runtime provider_type before fallback)", name, "tether")
	}
	if prov == nil {
		t.Fatal("resolveProvider returned nil prov for mapped tether provider; want registered runtime provider")
	}
}

func TestResolveProvider_CLIAgentProvider_ShortCircuits(t *testing.T) {
	s := &chatServiceImpl{
		providers: newRegistryWithAnthropicStub(),
		store:     mustNewStoreForResolveTest(t),
	}

	name, prov := s.resolveProvider("sess-1", "", "pty-claude", "claude-cli", "api")
	if name != "pty-claude" {
		t.Fatalf("resolveProvider returned name=%q, want %q (must not fall through to anthropic stub)", name, "pty-claude")
	}
	if prov != nil {
		t.Fatalf("resolveProvider returned non-nil prov for CLI agent default; must be nil")
	}
}

func TestResolveProvider_FallbackChainCLI_ShortCircuits(t *testing.T) {
	st := mustNewStoreForResolveTest(t)
	us, err := st.GetUserSettings(context.Background())
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	us.ProviderFallbackChain = []string{"pty"}
	if err := st.UpdateUserSettings(context.Background(), us); err != nil {
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
	name, prov := s.resolveProvider("sess-1", "", "", "claude-sonnet-4-20250514", "api")
	if name != "pty" {
		t.Fatalf("resolveProvider returned name=%q, want %q (fallback-chain CLI alias must short-circuit before InferProvider→anthropic stub)", name, "pty")
	}
	if prov != nil {
		t.Fatalf("resolveProvider returned non-nil prov for CLI fallback entry; must be nil")
	}
}

// TestResolveProvider_DefaultProviderWinsOverStaleFallbackChain pins the
// live production bug found during the CW-20260812-0001 investigation:
// user_settings.default_provider="anthropic" alongside a stale
// user_settings.provider_fallback_chain=["pty"] must resolve to the
// registered "anthropic" provider, not short-circuit on the fallback
// chain's CLI alias. Reproduces the exact settings shape found on the
// live instance (default_provider correctly configured, fallback_chain
// left over from before this app's API-first default).
func TestResolveProvider_DefaultProviderWinsOverStaleFallbackChain(t *testing.T) {
	st := mustNewStoreForResolveTest(t)
	us, err := st.GetUserSettings(context.Background())
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	us.DefaultProvider = "anthropic"
	us.ProviderFallbackChain = []string{"pty"}
	if err := st.UpdateUserSettings(context.Background(), us); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}

	s := &chatServiceImpl{
		providers: newRegistryWithAnthropicStub(),
		store:     st,
	}

	name, prov := s.resolveProvider("sess-1", "", "", "claude-sonnet-4-5-20250929", "api")
	if name != "anthropic" {
		t.Fatalf("resolveProvider returned name=%q, want %q (default_provider must win over stale fallback-chain CLI alias)", name, "anthropic")
	}
	if prov == nil {
		t.Fatalf("resolveProvider returned nil prov for a registered default_provider — must be non-nil")
	}
}

// TestResolveProvider_DefaultProviderCLI_ShortCircuits confirms the new
// default_provider step gets the same CLI short-circuit treatment as
// every other step in the chain: if an operator deliberately sets
// default_provider to a CLI alias, resolveProvider must return (name,
// nil) so classifyNilProvider can route to driveBootSession, not fall
// through to a registered HTTP provider.
func TestResolveProvider_DefaultProviderCLI_ShortCircuits(t *testing.T) {
	st := mustNewStoreForResolveTest(t)
	us, err := st.GetUserSettings(context.Background())
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	us.DefaultProvider = "pty"
	if err := st.UpdateUserSettings(context.Background(), us); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}

	s := &chatServiceImpl{
		providers: newRegistryWithAnthropicStub(),
		store:     st,
	}

	name, prov := s.resolveProvider("sess-1", "", "", "claude-sonnet-4-5-20250929", "api")
	if name != "pty" {
		t.Fatalf("resolveProvider returned name=%q, want %q (CLI-shaped default_provider must short-circuit before anthropic stub)", name, "pty")
	}
	if prov != nil {
		t.Fatalf("resolveProvider returned non-nil prov for CLI default_provider; must be nil")
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

	name, prov := s.resolveProvider("sess-1", "", "", "claude-cli", "api")
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
	us, err := st.GetUserSettings(context.Background())
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	us.ProviderFallbackChain = []string{"openai"} // non-CLI, will miss
	if err := st.UpdateUserSettings(context.Background(), us); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}

	s := &chatServiceImpl{
		providers: newRegistryWithAnthropicStub(),
		store:     st,
	}

	// The fallback chain entry "openai" must miss the registry (only
	// "anthropic" is registered), emit a Warn, and let resolution
	// continue to InferProvider→anthropic.
	name, _ := s.resolveProvider("sess-1", "", "", "claude-sonnet-4-20250514", "api")
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

// TestResolveProvider_NothingConfigured_NoSilentDefault pins the removal
// of resolveProvider's old unconditional "if all else fails, try
// anthropic" catch-all (CW-20260812-0001, per an explicit product
// decision: an unconfigured installation must error, not silently
// default). With no session/agent/user_settings provider set anywhere,
// and no provider registered at all (not even anthropic), resolveProvider
// must return a nil Provider so the caller surfaces a configuration
// error — it must NOT probe the registry for a hardcoded "anthropic" as
// a last resort independent of what InferProvider actually derived.
func TestResolveProvider_NothingConfigured_NoSilentDefault(t *testing.T) {
	s := &chatServiceImpl{
		providers: provider.NewRegistry(), // nothing registered, not even anthropic
		store:     mustNewStoreForResolveTest(t),
	}

	name, prov := s.resolveProvider("sess-1", "", "", "claude-sonnet-4-5-20250929", "api")
	if prov != nil {
		t.Fatalf("resolveProvider returned non-nil prov with an empty registry; must be nil so the caller errors")
	}
	if name != "anthropic" {
		// InferProvider's own model-name routing floor is a separate,
		// legitimate concern (see internal/chat/engine.go InferProvider
		// doc) — resolveProvider still surfaces whatever InferProvider
		// derived so callers can log/report it, it just must not turn an
		// unregistered "anthropic" into a usable Provider.
		t.Fatalf("resolveProvider returned name=%q, want %q (InferProvider's own routing floor)", name, "anthropic")
	}
}

// TestResolveProvider_CLIRuntimeKind_BareDefaultProvider_NeverRoutesHTTP is
// the literal reproduction of the real, confirmed bug this task
// (TASKS/phase-3/01-collapse-resolveprovider-into-cascade.md) fixes —
// found and empirically confirmed twice independently during Phase 2's
// close-out (Orchestrator's live dogfeed validation against the running
// nanite-api-service, and the fresh Phase 2 Reviewer's own throwaway
// probe test against the real resolveProvider method — both 2026-08-19;
// see TASKS/ESCALATIONS.md's matching 2026-08-19 entries).
//
// Pre-fix: resolveProvider had zero awareness of agent.RuntimeKind. A
// runtime_kind='cli' agent configured the "obvious" way post-Phase-2 — a
// bare default_provider ("claude", not the legacy "pty-claude"/"sub-
// claude" alias shape) — never reached chat_generate.go's
// classifyNilProvider (where runtime_kind is otherwise consulted) at
// all: resolveProvider's old chain fell through the agent-provider step
// (a bare "claude" name isn't itself registered as an HTTP provider, and
// isn't CLI-shaped either) straight to user_settings.default_provider,
// which resolved to a real, registered HTTP provider and returned it —
// silently routing the turn through the HTTP/API path with no error, no
// warning distinguishing it from correct routing.
//
// This test reproduces the exact scenario the Reviewer used to confirm
// the bug: a runtime_kind='cli' agent with a bare, non-prefixed
// default_provider, alongside a registered HTTP provider AND a populated
// user_settings.default_provider that would otherwise resolve one.
// Post-fix, the turn must still route CLI (name=="claude", prov==nil),
// not HTTP.
func TestResolveProvider_CLIRuntimeKind_BareDefaultProvider_NeverRoutesHTTP(t *testing.T) {
	st := mustNewStoreForResolveTest(t)
	us, err := st.GetUserSettings(context.Background())
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	us.DefaultProvider = "anthropic"
	if err := st.UpdateUserSettings(context.Background(), us); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}

	s := &chatServiceImpl{
		providers: newRegistryWithAnthropicStub(), // a real, registered HTTP provider is present
		store:     st,
	}

	name, prov := s.resolveProvider("sess-1", "", "claude", "claude-sonnet-4-5-20250929", "cli")
	if name != "claude" {
		t.Fatalf("resolveProvider returned name=%q, want %q (runtime_kind='cli' must win over a bare default_provider even with user_settings.default_provider populated and a registered HTTP provider present)", name, "claude")
	}
	if prov != nil {
		t.Fatalf("resolveProvider returned non-nil prov for a runtime_kind='cli' agent; must be nil so classifyNilProvider routes the turn to driveBootSession, not the HTTP path (this is the exact silent-fail-open bug this task fixes)")
	}
}

// TestResolveProvider_CLIRuntimeKind_NoAgentProviderAtAll_StillCLI covers
// the companion case: runtime_kind='cli' with NO agent-level
// default_provider at all (agentProvider=="") and no explicit session
// provider either. Even with nothing CLI-shaped anywhere in the inputs,
// runtime_kind must still win over user_settings.default_provider/the
// fallback chain — the function must never fall through to the
// operator-level HTTP tiers once runtimeKind=="cli" is known.
func TestResolveProvider_CLIRuntimeKind_NoAgentProviderAtAll_StillCLI(t *testing.T) {
	st := mustNewStoreForResolveTest(t)
	us, err := st.GetUserSettings(context.Background())
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	us.DefaultProvider = "anthropic"
	if err := st.UpdateUserSettings(context.Background(), us); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}

	s := &chatServiceImpl{
		providers: newRegistryWithAnthropicStub(),
		store:     st,
	}

	name, prov := s.resolveProvider("sess-1", "", "", "claude-sonnet-4-5-20250929", "cli")
	if name != "" {
		t.Fatalf("resolveProvider returned name=%q, want \"\" (no agent/session provider name available to route with)", name)
	}
	if prov != nil {
		t.Fatalf("resolveProvider returned non-nil prov for a runtime_kind='cli' agent with no resolvable name; must stay nil, never fall through to user_settings.default_provider")
	}
}

// TestResolveProvider_APIRuntimeKind_BareDefaultProvider_StillResolvesHTTP
// is the no-regression companion: a runtime_kind='api' agent (the
// backfilled default for every pre-existing row — see this task's Work
// Log) with the exact same bare default_provider shape must still
// resolve to the registered HTTP provider exactly as before. Only
// runtimeKind=="cli" changes behavior.
func TestResolveProvider_APIRuntimeKind_BareDefaultProvider_StillResolvesHTTP(t *testing.T) {
	st := mustNewStoreForResolveTest(t)
	us, err := st.GetUserSettings(context.Background())
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	us.DefaultProvider = "anthropic"
	if err := st.UpdateUserSettings(context.Background(), us); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}

	s := &chatServiceImpl{
		providers: newRegistryWithAnthropicStub(),
		store:     st,
	}

	name, prov := s.resolveProvider("sess-1", "", "claude", "claude-sonnet-4-5-20250929", "api")
	if name != "anthropic" {
		t.Fatalf("resolveProvider returned name=%q, want %q (runtime_kind='api' must still fall through to user_settings.default_provider)", name, "anthropic")
	}
	if prov == nil {
		t.Fatalf("resolveProvider returned nil prov for a runtime_kind='api' agent that should resolve to the registered anthropic provider")
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
	s, err := storetest.New(t, context.Background(), t.TempDir()+"/resolve.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })
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
func (stubLLMProvider) Complete(context.Context, llmtypes.ChatRequest) (string, error) {
	return "", nil
}
func (stubLLMProvider) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{}
}

var _ llmcontracts.Provider = stubLLMProvider{}
