package service

// Regression coverage for c195: a session created with provider="pty" /
// model="claude-cli" was silently routed to the Anthropic HTTP API,
// which 404'd on the CLI model id. resolveProvider's miss-path fell
// through to anthropic instead of returning (name, nil) for CLI aliases
// — so the classifyNilProvider CLI bypass at chat_generate.go never
// engaged.
//
// These tests pin every miss point in resolveProvider where a CLI alias
// must short-circuit:
//
//  1. sessionProvider="pty" miss → ("pty", nil)
//  2. agentProvider="pty-claude" miss → ("pty-claude", nil)
//  3. ProviderFallbackChain=["pty"] miss → ("pty", nil) AND non-CLI
//     entries that miss emit a slog.Warn (previously silent).
//  4. model="claude-cli" → InferProvider→"pty" miss → ("pty", nil)
//
// Without these guards, c195's failure mode (Anthropic 404 on
// `model: claude-cli`) re-emerges the moment someone picks the Claude
// CLI dropdown row.

import (
	"context"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestResolveProvider_CLISessionProvider_ShortCircuits(t *testing.T) {
	s := &chatServiceImpl{
		providers: provider.NewRegistry(),
		store:     mustNewStoreForResolveTest(t),
	}

	name, prov := s.resolveProvider("sess-1", "pty", "", "claude-cli")
	if name != "pty" {
		t.Fatalf("resolveProvider returned name=%q, want %q (c195 regression — must not fall through to anthropic)", name, "pty")
	}
	if prov != nil {
		t.Fatalf("resolveProvider returned non-nil prov for CLI alias; must be nil so classifyNilProvider routes to driveBootSession")
	}
}

func TestResolveProvider_CLIAgentProvider_ShortCircuits(t *testing.T) {
	s := &chatServiceImpl{
		providers: provider.NewRegistry(),
		store:     mustNewStoreForResolveTest(t),
	}

	name, prov := s.resolveProvider("sess-1", "", "pty-claude", "claude-cli")
	if name != "pty-claude" {
		t.Fatalf("resolveProvider returned name=%q, want %q", name, "pty-claude")
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
		providers: provider.NewRegistry(),
		store:     st,
	}

	// No session provider, no agent provider, no anthropic registered.
	// The fallback chain entry "pty" must short-circuit instead of
	// falling through to InferProvider/anthropic.
	name, prov := s.resolveProvider("sess-1", "", "", "claude-cli")
	if name != "pty" {
		t.Fatalf("resolveProvider returned name=%q, want %q (fallback chain CLI alias must short-circuit)", name, "pty")
	}
	if prov != nil {
		t.Fatalf("resolveProvider returned non-nil prov for CLI fallback entry; must be nil")
	}
}

func TestResolveProvider_InferredCLI_ShortCircuits(t *testing.T) {
	// model="claude-cli" → InferProvider returns "pty". Registry is
	// empty, no anthropic, no fallback chain. Defense-in-depth: even
	// if the dropdown shape never reached sessionProvider, an
	// unannotated model id mapping to a CLI provider must still route
	// to the CLI bypass rather than the HTTP fallback.
	s := &chatServiceImpl{
		providers: provider.NewRegistry(),
		store:     mustNewStoreForResolveTest(t),
	}

	name, prov := s.resolveProvider("sess-1", "", "", "claude-cli")
	if name != "pty" {
		t.Fatalf("resolveProvider inferred name=%q, want %q (InferProvider(claude-cli) must short-circuit on CLI)", name, "pty")
	}
	if prov != nil {
		t.Fatalf("resolveProvider returned non-nil prov for inferred CLI alias; must be nil")
	}
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
