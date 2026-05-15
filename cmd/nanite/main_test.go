package main

import (
	"slices"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
)

// Phase 4c.6 (CW-20260508-0002): TestRegisterLegacyPTYAliasPrefersRegisteredClaudeProvider
// removed — the registerLegacyPTYAlias function it covered was deleted along
// with provider.PTYBridge / provider.SubprocessBridge registry registrations.
// CLI agents now spawn through internal/runtime/agent.Boot.

// TestInitProviders_ClaudeAdapterIsPTYShape pins the c200 regression
// (CW-20260515-0003). The runtime layer's factory.shouldUsePTY returns
// true for ("claude", ModeLongLived) → agentsessions spawns claude
// under a PTY expecting an interactive TUI. Pre-fix, initProviders
// registered the bare NewClaudeAdapter() whose BuildArgs falls through
// to the print-mode default (-p, --print, --output-format stream-json,
// --verbose). The two-sided mismatch made claude exit 1 in ~750ms with
// "Error: Input must be provided either through stdin or as a prompt
// argument when using --print", three times, restart_exhausted.
//
// The fix swaps in NewClaudeAdapterPTY which sets PTY=true so BuildArgs
// emits interactive-shape args. This test pins that contract: the
// adapter returned from initProviders for claude MUST NOT emit any of
// the print-mode flags. Reading the adapter through the CLIAdapter
// interface keeps the test free of the concrete *ClaudeAdapter type so
// a future go-providers refactor that splits the constructor doesn't
// require touching this test.
func TestInitProviders_ClaudeAdapterIsPTYShape(t *testing.T) {
	t.Run("non-dev", func(t *testing.T) {
		_, cliAdapters := initProviders(false)
		claude := findAdapter(t, cliAdapters, "claude")

		args := claude.BuildArgs("", "", "")
		assertNoPrintModeFlags(t, args)
		// Non-dev must NOT skip permissions.
		if slices.Contains(args, "--dangerously-skip-permissions") {
			t.Errorf("non-dev claude adapter emitted --dangerously-skip-permissions; args=%v", args)
		}
	})

	t.Run("dev", func(t *testing.T) {
		_, cliAdapters := initProviders(true)
		claude := findAdapter(t, cliAdapters, "claude")

		args := claude.BuildArgs("", "", "")
		assertNoPrintModeFlags(t, args)
		// Dev mode replaces the legacy skipPermsAdapter wrapper with
		// NewClaudeAdapterDevPTY, which sets SkipPermissions=true in
		// the adapter itself. The flag must still appear exactly once
		// (the wrapper-stacking trap that would have appended it
		// twice is gone with the wrapper).
		count := 0
		for _, a := range args {
			if a == "--dangerously-skip-permissions" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("dev claude adapter emitted --dangerously-skip-permissions %d times, want 1; args=%v", count, args)
		}
	})
}

// findAdapter locates a CLIAdapter by Name() in the slice initProviders
// returned. Fails the test if absent — c200 was specifically a claude-
// adapter routing bug, so the slice not containing one would itself be
// a regression worth catching here.
func findAdapter(t *testing.T, adapters []provider.CLIAdapter, name string) provider.CLIAdapter {
	t.Helper()
	for _, a := range adapters {
		if a.Name() == name {
			return a
		}
	}
	t.Fatalf("no %q adapter in initProviders return; got %d adapter(s)", name, len(adapters))
	return nil
}

// assertNoPrintModeFlags fails if the args slice contains any of the
// print-mode flags that broke c200. The set is the inverse of the PTY
// branch in go-providers/provider/pty_claude.go's BuildArgs — those
// flags must NOT appear when the runtime expects an interactive
// long-lived spawn.
func assertNoPrintModeFlags(t *testing.T, args []string) {
	t.Helper()
	for _, banned := range []string{"-p", "--print", "--output-format", "--input-format", "--verbose"} {
		if slices.Contains(args, banned) {
			t.Errorf("claude PTY adapter emitted forbidden print-mode flag %q; args=%v (c200 regression — these flags belong to bare/print mode, not the interactive PTY spawn)", banned, args)
		}
	}
}
