package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	naniteotel "github.com/hollis-labs/nanite/internal/otel"
	"github.com/hollis-labs/nanite/internal/slogx"
)

type countingCloser struct {
	calls atomic.Int32
}

func (c *countingCloser) Close() error {
	c.calls.Add(1)
	return nil
}

func TestCmdServeStartupFailureReturns(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	// SQLite cannot open a directory as a database file. The directory itself
	// is resolvable by go-apppaths, so this reaches a real store.New failure
	// after the logging and OTel cleanup defers have been registered, without
	// requiring a composition-root dependency-injection seam.
	dbPath := t.TempDir()

	logCleanup := &countingCloser{}
	var otelCleanupCalls atomic.Int32

	err := cmdServeWithInitializers(
		[]string{"--db", dbPath, "--dev"},
		func(slogx.Config) (*slog.Logger, io.Closer, error) {
			return slog.Default(), logCleanup, nil
		},
		func(context.Context, naniteotel.Config) (func(context.Context) error, error) {
			return func(context.Context) error {
				otelCleanupCalls.Add(1)
				return nil
			}, nil
		},
	)
	if err == nil {
		t.Fatal("cmdServe returned nil for an unopenable database path")
	}
	if !strings.Contains(err.Error(), "failed to open store") {
		t.Fatalf("cmdServe error = %q, want failed-to-open-store context", err)
	}
	if got := logCleanup.calls.Load(); got != 1 {
		t.Errorf("logging cleanup calls = %d, want 1", got)
	}
	if got := otelCleanupCalls.Load(); got != 1 {
		t.Errorf("OTel cleanup calls = %d, want 1", got)
	}
}

// Phase 4c.6 (CW-20260508-0002): TestRegisterLegacyPTYAliasPrefersRegisteredClaudeProvider
// removed — the registerLegacyPTYAlias function it covered was deleted along
// with provider.PTYBridge / provider.SubprocessBridge registry registrations.
// CLI agents now spawn through internal/runtime/agent.Boot.

// TestInitProviders_ClaudeAdapterIsStreamingStdioShape pins the
// c200 + c202 regression chain.
//
//	c200 (CW-20260515-0003): the bare NewClaudeAdapter() emitted
//	  print-mode argv with an EMPTY `-p ""` positional. claude bailed
//	  on arg validation in ~750ms, restart_exhausted. Fix swapped to
//	  NewClaudeAdapterPTY (interactive TUI argv).
//	c202 (CW-20260515-0004 — this fix): NewClaudeAdapterPTY launched
//	  claude as an interactive TUI inside the allocated PTY. claude
//	  ran fine but its ANSI/screen-redraw output had no parser in
//	  go-providers (pty_claude_events.go expects stream-json). Sessions
//	  stayed state=running forever with zero assistant deltas.
//	  Fix swaps to NewClaudeAdapterStreamingStdio: long-lived
//	  `-p --input-format stream-json --output-format stream-json
//	  --verbose` process that reads NDJSON `{"type":"user",...}` from
//	  stdin and emits stream-json events on stdout — the exact shape
//	  ParseLineEvents was built for.
//
// This test pins the StreamingStdio contract: BuildArgs MUST emit
// every flag in the required set (-p, --input-format, stream-json,
// --output-format, stream-json, --verbose). Reading the adapter
// through the CLIAdapter interface keeps the test free of the
// concrete *ClaudeAdapter type.
func TestInitProviders_ClaudeAdapterIsStreamingStdioShape(t *testing.T) {
	t.Run("non-dev", func(t *testing.T) {
		_, cliAdapters, _ := initProviders(false)
		claude := findAdapter(t, cliAdapters, "claude")

		args := claude.BuildArgs("", "", "")
		assertStreamingStdioFlags(t, args)
		// Non-dev must NOT skip permissions.
		if slices.Contains(args, "--dangerously-skip-permissions") {
			t.Errorf("non-dev claude adapter emitted --dangerously-skip-permissions; args=%v", args)
		}
	})

	t.Run("dev", func(t *testing.T) {
		_, cliAdapters, _ := initProviders(true)
		claude := findAdapter(t, cliAdapters, "claude")

		args := claude.BuildArgs("", "", "")
		assertStreamingStdioFlags(t, args)
		// Dev mode uses NewClaudeAdapterDevStreamingStdio which sets
		// SkipPermissions=true in the adapter itself. The flag must
		// appear exactly once (a regression that re-introduced the
		// legacy skipPermsAdapter wrapper alongside the Dev*
		// constructor would double-append it).
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
// returned. Fails the test if absent — c200/c202 were both claude-
// adapter routing bugs, so the slice not containing one would itself
// be a regression worth catching here.
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

// assertStreamingStdioFlags pins the four-flag contract of
// NewClaudeAdapterStreamingStdio's BuildArgs output. The flag pairs
// matter as a SET (order is decided by go-providers and could shift in
// a future minor version) — what we pin is presence + the flag/value
// pairing for the two "kv" flags. A regression that flipped back to
// NewClaudeAdapterPTY would emit bare-claude argv missing every flag
// in this set; a regression to NewClaudeAdapter would have `-p ""`
// (empty positional) and no --input-format. Both fail loudly here.
func assertStreamingStdioFlags(t *testing.T, args []string) {
	t.Helper()
	for _, required := range []string{"-p", "--input-format", "--output-format", "--verbose"} {
		if !slices.Contains(args, required) {
			t.Errorf("claude StreamingStdio adapter missing required flag %q; args=%v", required, args)
		}
	}
	// Both kv flags must carry "stream-json" as the immediate next
	// arg. flagValue returns "" when the flag isn't present, which
	// the presence check above already flagged.
	if v := flagValue(args, "--input-format"); v != "stream-json" {
		t.Errorf("--input-format value = %q, want stream-json; args=%v", v, args)
	}
	if v := flagValue(args, "--output-format"); v != "stream-json" {
		t.Errorf("--output-format value = %q, want stream-json; args=%v", v, args)
	}
}

// flagValue returns the arg immediately following flag in args, or ""
// if flag is absent or terminal. Adequate for the StreamingStdio
// contract (--input-format and --output-format both take exactly one
// value); not a general-purpose flag parser.
func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
