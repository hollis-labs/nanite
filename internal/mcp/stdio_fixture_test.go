package mcp

import (
	"context"
	"log"

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

// runFixtureServer serves one no-op tool over stdio — enough for a real
// ListTools round trip; nothing in this package's tests calls it.
func runFixtureServer() {
	srv := gmcpserver.NewServer("nanite-mcp-test-fixture", "0.0.0")
	srv.RegisterTool(gmcpserver.Tool{
		Name:           "noop",
		Description:    "test fixture tool; always returns ok",
		InputSchema:    gmcpserver.EmptyObjectSchema(),
		ReadOnlyHint:   true,
		IdempotentHint: true,
		Handler: func(context.Context, map[string]any) (any, error) {
			return "ok", nil
		},
	})
	if err := srv.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
