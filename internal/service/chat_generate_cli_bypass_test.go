package service

// CW-20260514-0045 regression coverage for the CLI dropdown route fix.
//
// The dropdown emits CLI provider names ("pty-claude", "pty-codex",
// "pty-opencode", legacy "pty"). The bridges that used to register
// llmcontracts.Provider entries for these were removed in Phase 4c.6
// (CW-20260508-0002), so chat-service's resolveProvider returns
// (name, nil) — the chat-generate code USED to error out before the
// CLI-bypass branch at the per-iteration provider call site could route
// the turn to driveBootSession.
//
// These tests pin:
//
//  1. stripRegistryPrefix (the agent_deps.go normalization) covers all
//     four dropdown shapes + the legacy bare "pty" alias.
//  2. classifyNilProvider routes:
//     - non-CLI → fatal (legacy behavior preserved)
//     - CLI + registered adapter → CLI route (drives the driveBootSession bypass)
//     - CLI + no adapter → CLINoAdapter (pointed error instead of generic)
//     - CLI + no runtime wiring → fatal (degrade gracefully)
//  3. The legacy non-CLI nil-provider path STILL fatals — no regression
//     where non-CLI dropdown misconfig silently becomes a CLI bypass.

import (
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
)

// TestStripRegistryPrefix_AliasTable exercises the agent_deps.go alias
// table directly. CW-20260514-0045 extended stripRegistryPrefix to
// delegate to chat.NormalizeCLIProvider (including the legacy bare "pty"
// → "claude" mapping); this regression pins the four dropdown-shape
// aliases AND the legacy bare alias.
func TestStripRegistryPrefix_AliasTable(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		// CW-20260514-0045 acceptance: legacy bare "pty" normalizes
		// to "claude". Before the fix, this returned "pty" verbatim
		// and the adapter index miss caused agent.Boot to fail.
		{"pty", "claude"},
		// Dropdown-shape prefixed aliases → bare adapter names.
		{"pty-claude", "claude"},
		{"pty-codex", "codex"},
		{"pty-opencode", "opencode"},
		// Generic subprocess prefix strip (pre-existing behavior).
		{"sub-claude", "claude"},
		{"sub-codex", "codex"},
		// Bare adapter names pass through unchanged.
		{"claude", "claude"},
		{"codex", "codex"},
		{"opencode", "opencode"},
		// Non-CLI providers untouched.
		{"anthropic", "anthropic"},
		{"openai", "openai"},
		// Empty input.
		{"", ""},
	}
	for _, tt := range tests {
		if got := stripRegistryPrefix(tt.in); got != tt.want {
			t.Errorf("stripRegistryPrefix(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestClassifyNilProvider_CLIWithAdapter is the load-bearing case for
// CW-20260514-0045: the dropdown selected a CLI provider, the registry
// has no llmcontracts.Provider for it, but the agent runtime has a CLI
// adapter registered. classifyNilProvider must return nilProviderRouteCLI
// so chat_generate.go falls through to driveBootSession instead of
// erroring out.
func TestClassifyNilProvider_CLIWithAdapter(t *testing.T) {
	// Stub adapter index resolves "claude" → a non-nil adapter, which
	// is what the agent_deps.go closure does after stripRegistryPrefix
	// normalizes the dropdown name.
	stubAdapter := &stubCLIAdapter{name: "claude"}
	deps := &runtimeagent.Dependencies{
		ProviderAdapter: func(name string) provider.CLIAdapter {
			// Mirror agent_deps.go's normalize-then-lookup behavior.
			bare := stripRegistryPrefix(name)
			if bare == "claude" {
				return stubAdapter
			}
			return nil
		},
	}
	s := &chatServiceImpl{agentDeps: deps}

	cliInputs := []string{"pty", "pty-claude"}
	for _, name := range cliInputs {
		if got := s.classifyNilProvider(name); got != nilProviderRouteCLI {
			t.Errorf("classifyNilProvider(%q) = %v, want nilProviderRouteCLI", name, got)
		}
	}
}

// TestClassifyNilProvider_CLINoAdapter verifies the pointed-error
// branch: a dropdown CLI name without a registered runtime adapter
// returns CLINoAdapter, not fatal. This separates "operator forgot to
// wire CLIAdapters" from "operator typed a bad provider name".
func TestClassifyNilProvider_CLINoAdapter(t *testing.T) {
	// ProviderAdapter returns nil for every name — simulates an empty
	// adapter index.
	deps := &runtimeagent.Dependencies{
		ProviderAdapter: func(string) provider.CLIAdapter { return nil },
	}
	s := &chatServiceImpl{agentDeps: deps}

	for _, name := range []string{"pty-claude", "pty-codex", "pty-opencode"} {
		if got := s.classifyNilProvider(name); got != nilProviderRouteCLINoAdapter {
			t.Errorf("classifyNilProvider(%q) = %v, want nilProviderRouteCLINoAdapter", name, got)
		}
	}
}

// TestClassifyNilProvider_CLIWithoutDeps degrades gracefully to fatal
// when the runtime composition root isn't wired. Without agentDeps the
// chat service has nowhere to route the CLI turn, so surfacing the
// legacy "Provider not available" error is the correct conservative
// behavior. Tested separately so a future re-wiring of the runtime
// can't accidentally turn this into a CLI bypass.
func TestClassifyNilProvider_CLIWithoutDeps(t *testing.T) {
	s := &chatServiceImpl{} // agentDeps == nil
	if got := s.classifyNilProvider("pty-claude"); got != nilProviderRouteFatal {
		t.Errorf("classifyNilProvider(pty-claude) with nil agentDeps = %v, want nilProviderRouteFatal", got)
	}

	// agentDeps non-nil but ProviderAdapter nil — same degradation.
	s2 := &chatServiceImpl{agentDeps: &runtimeagent.Dependencies{ProviderAdapter: nil}}
	if got := s2.classifyNilProvider("pty-claude"); got != nilProviderRouteFatal {
		t.Errorf("classifyNilProvider(pty-claude) with nil ProviderAdapter = %v, want nilProviderRouteFatal", got)
	}
}

// TestClassifyNilProvider_NonCLIStaysFatal pins the no-regression
// guarantee: dropdown names that AREN'T CLI providers (anthropic,
// openai, etc.) must still produce the legacy fatal error when the
// registry has no provider registered. Otherwise a typo'd provider
// name would silently route through driveBootSession.
func TestClassifyNilProvider_NonCLIStaysFatal(t *testing.T) {
	// Even with a fully-wired adapter index that would return non-nil
	// for any name, non-CLI names must NOT route to CLI.
	deps := &runtimeagent.Dependencies{
		ProviderAdapter: func(string) provider.CLIAdapter {
			return &stubCLIAdapter{name: "wat"}
		},
	}
	s := &chatServiceImpl{agentDeps: deps}

	nonCLI := []string{"anthropic", "openai", "gemini-api", "mistral", "openrouter", "ollama", "typo-provider"}
	for _, name := range nonCLI {
		if got := s.classifyNilProvider(name); got != nilProviderRouteFatal {
			t.Errorf("classifyNilProvider(%q) = %v, want nilProviderRouteFatal", name, got)
		}
	}
}

// stubCLIAdapter is a minimal provider.CLIAdapter for tests that only
// need to assert "an adapter exists for this name". None of the
// classifyNilProvider tests invoke BuildArgs / ParseLine — the adapter
// identity is the only fact the bypass decision consumes.
type stubCLIAdapter struct {
	name string
}

func (s *stubCLIAdapter) Name() string { return s.name }
func (s *stubCLIAdapter) BuildArgs(prompt, systemPrompt, cliSessionID string) []string {
	return nil
}
func (s *stubCLIAdapter) ParseLine(line []byte) ([]llmtypes.StreamEvent, error) { return nil, nil }
func (s *stubCLIAdapter) Detect() (string, bool)                                { return "", false }

// Compile-time assertion that the stub satisfies the interface — if a
// future go-providers release adds a method this test catches the gap
// before runtime.
var _ provider.CLIAdapter = (*stubCLIAdapter)(nil)
