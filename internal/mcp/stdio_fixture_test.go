package mcp

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	gmcpserver "github.com/hollis-labs/go-mcp/server"
)

// runAsFixtureServerEnv, when set in this test binary's own environment,
// makes TestMain (main_test.go) run a real MCP server over stdio instead
// of any test. See TestMain's doc comment for why: the official SDK's
// client defaults to the 2026-07-28 stateless server/discover handshake
// (SEP-2575), not the old 2024-11-05 initialize/initialized dance — a
// hand-rolled shell-script stub that only answered the latter (this
// package's prior fixture, matching what the pre-migration hand-rolled
// StdioTransport itself spoke) hangs forever against the real client,
// which this migration's own test run caught. A real go-mcp/server
// instance, spoken to over a real stdio pipe, can't drift from what the
// real client actually sends the way a hand simulation can.
const runAsFixtureServerEnv = "_NANITE_MCP_TEST_RUN_AS_FIXTURE_SERVER"

// runAsFixtureServerExitAfterCallEnv, when also set alongside
// runAsFixtureServerEnv, makes the fixture process exit shortly after
// answering its first "noop" call, simulating a stdio server's subprocess
// dying between two calls a caller makes against it (e.g. during an idle
// period -- see remote_transport_calltool_retry_test.go). The delay lets
// the response reach the pipe before the process disappears from under it.
const runAsFixtureServerExitAfterCallEnv = "_NANITE_MCP_TEST_FIXTURE_EXIT_AFTER_CALL"

// runAsFixtureServerPIDLogEnv, when set to a file path, makes the fixture
// append its own PID to that file (one line) on startup. A test that
// restarts this fixture (directly, or via CallTool retrying after
// invalidating a dead connection) can read the file afterward and confirm
// two distinct PIDs served the two calls -- real evidence of a respawn,
// not just an absence of error.
const runAsFixtureServerPIDLogEnv = "_NANITE_MCP_TEST_FIXTURE_PID_LOG"

// runFixtureServer serves one no-op tool over stdio — enough for a real
// ListTools/CallTool round trip; nothing in this package's tests calls it
// directly (TestMain does, on re-exec).
func runFixtureServer() {
	if logPath := os.Getenv(runAsFixtureServerPIDLogEnv); logPath != "" {
		// #nosec G304 G703 -- logPath is an env var this same test binary set on
		// its own subprocess (stdioFixtureTransport, remote_transport_calltool_retry_test.go),
		// confined beneath that test's t.TempDir; never externally supplied.
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
		}
	}

	exitAfterCall := os.Getenv(runAsFixtureServerExitAfterCallEnv) != ""

	srv := gmcpserver.NewServer("nanite-mcp-test-fixture", "0.0.0")
	srv.RegisterTool(gmcpserver.Tool{
		Name:           "noop",
		Description:    "test fixture tool; always returns ok",
		InputSchema:    gmcpserver.EmptyObjectSchema(),
		ReadOnlyHint:   true,
		IdempotentHint: true,
		Handler: func(context.Context, map[string]any) (any, error) {
			if exitAfterCall {
				// Exit off the request goroutine, after a short delay, so
				// the response has time to flush to the pipe before the
				// process (and its stdout) disappears.
				go func() {
					time.Sleep(150 * time.Millisecond)
					os.Exit(0)
				}()
			}
			return "ok", nil
		},
	})
	if err := srv.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
