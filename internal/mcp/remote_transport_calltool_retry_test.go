package mcp

import (
	"bufio"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	gmcpclient "github.com/hollis-labs/go-mcp/client"
	"github.com/hollis-labs/nanite/internal/pathsafe"
)

// TestIsProvablyUnsent pins the exact classifier CallTool's retry gate
// relies on: only the official SDK's "client is closing" phrasing (see
// remote_transport.go's doc comment on isProvablyUnsent for why that
// specific phrase is safe to retry unconditionally) should match, not any
// other recoverable-looking connection error. Widening this match is what
// would reintroduce the double-execution risk CallTool's general no-retry
// policy exists to avoid.
func TestIsProvablyUnsent(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"exact SDK sentinel text", errors.New("client is closing"), true},
		{"wrapped with a read EOF, the common case", errors.New("client is closing: EOF"), true},
		{"wrapped through the full call chain", errors.New(`tools/call noop: go-mcp/client: call tool noop "Agent Mux": client is closing: EOF`), true},
		{"a plain EOF from an in-flight call, not this connection's shutdown path", errors.New("EOF"), false},
		{"go-mcp's own recoverable substring, a different case entirely", errors.New("connection lost"), false},
		{"connection closed, a different case entirely", errors.New("connection closed"), false},
		{"an ordinary context deadline", context.DeadlineExceeded, false},
		{"a tool-level argument error", errors.New("invalid arguments: missing required field \"path\""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isProvablyUnsent(tt.err); got != tt.want {
				t.Errorf("isProvablyUnsent(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// stdioFixtureTransport registers a stdio server backed by this test binary
// re-exec'd as a real MCP server (stdio_fixture_test.go), on a fresh
// single-server pool, and returns the remoteTransport wired to it. Mirrors
// remote_transport_sse_test.go's sseTransport helper, for the stdio kind.
func stdioFixtureTransport(t *testing.T, extraEnv map[string]string) *remoteTransport {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	env := map[string]string{runAsFixtureServerEnv: "1"}
	for k, v := range extraEnv {
		env[k] = v
	}

	pool := gmcpclient.NewPool(gmcpclient.WithIdentity("nanite-test", "0.0.0"))
	t.Cleanup(func() { _ = pool.Close() })
	if err := pool.Register("test-stdio", gmcpclient.ServerConfig{
		Transport: gmcpclient.TransportStdio,
		Command:   exe,
		Env:       env,
	}); err != nil {
		t.Fatalf("pool.Register: %v", err)
	}
	return newRemoteTransport(pool, "test-stdio", gmcpclient.TransportStdio)
}

func readPIDLog(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 -- path is confined beneath t.TempDir by stdioFixtureTransport's caller.
	if err != nil {
		t.Fatalf("open pid log: %v", err)
	}
	defer f.Close()
	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// TestRemoteTransportCallTool_RetriesProvablyUnsentError is the Done-means
// test for the Agent Mux incident (CW-20260918, "connection closed: client
// is closing: EOF" against a stdio server whose subprocess died between two
// calls): once the fixture subprocess has exited after answering its first
// call, the next CallTool must still succeed -- the retry invalidates the
// dead connection and the Pool's lazy dial-on-first-use respawns a fresh
// subprocess. The PID log proves a real respawn happened rather than a
// fluke (e.g. the original process just not having exited yet).
//
// Polls instead of sleeping a fixed guess before the second call: how long
// the fixture's background exit takes to land (and how long the read loop
// then takes to notice the EOF) both vary with host load -- sharply so
// under -race, which also instruments the re-exec'd fixture process itself.
// Every poll issues a real CallTool that must succeed regardless of whether
// the fixture has exited yet (each call is a real, if redundant, exercise
// of the transport); the loop stops the instant a second distinct pid
// appears, so a slow environment costs the test wall-clock time, not
// correctness, and the final assertion only ever sees the two pids that
// matter -- one before the exit, one after the fix's retry recovered it.
func TestRemoteTransportCallTool_RetriesProvablyUnsentError(t *testing.T) {
	pidLog, err := pathsafe.ResolveUnder(t.TempDir(), "pids.log")
	if err != nil {
		t.Fatalf("pathsafe.ResolveUnder: %v", err)
	}
	transport := stdioFixtureTransport(t, map[string]string{
		runAsFixtureServerExitAfterCallEnv: "1",
		runAsFixtureServerPIDLogEnv:        pidLog,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const (
		maxAttempts = 60
		pollDelay   = 200 * time.Millisecond
	)

	var pids []string
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		res, err := transport.CallTool(ctx, "noop", nil)
		if err != nil {
			t.Fatalf("CallTool attempt %d did not recover: %v", attempt, err)
		}
		if res.IsError {
			t.Fatalf("CallTool attempt %d returned IsError: %+v", attempt, res)
		}

		pids = readPIDLog(t, pidLog)
		if len(pids) >= 2 {
			break
		}
		time.Sleep(pollDelay)
	}

	if len(pids) < 2 {
		t.Fatalf("pid log = %v after %d attempts, want a second startup (the retry's respawn) to have appeared", pids, maxAttempts)
	}
	if len(pids) > 2 {
		// Every respawned process also carries exitAfterCall=1, so it dies
		// ~150ms after its own first call too -- if the poll interval ever
		// raced a third exit, that's still real respawn behavior, just more
		// of it than this test needs to prove the point. Assert on the
		// first two rather than treating a third as a failure.
		t.Logf("pid log = %v -- more than 2 startups (a slow poll interval outraced more than one respawn); checking the first two", pids)
	}
	if pids[0] == pids[1] {
		t.Fatalf("pid log = %v, want the first two entries to be DISTINCT pids -- the same pid twice means no real respawn happened", pids)
	}
}

// TestRemoteTransportCallTool_DialFailureIsNotMisclassifiedAsProvablyUnsent
// guards the other direction: a stdio server that can never be dialed at
// all (bad command) fails with an ordinary exec/dial error, not the SDK's
// "client is closing" phrasing -- isProvablyUnsent must not fire on it, so
// CallTool must not (incorrectly) attempt a retry-after-invalidate cycle
// for a failure class the classifier was never meant to cover.
//
// This can't directly count Pool-level call attempts (remoteTransport.pool
// is a concrete *gmcpclient.Pool, not an interface a fake can stand in
// for), so it asserts on the one thing that is directly observable: the
// returned error's shape. TestIsProvablyUnsent above pins the classifier's
// own behavior on this same error family directly.
func TestRemoteTransportCallTool_DialFailureIsNotMisclassifiedAsProvablyUnsent(t *testing.T) {
	pool := gmcpclient.NewPool(gmcpclient.WithIdentity("nanite-test", "0.0.0"))
	t.Cleanup(func() { _ = pool.Close() })
	if err := pool.Register("test-stdio", gmcpclient.ServerConfig{
		Transport: gmcpclient.TransportStdio,
		Command:   "/nonexistent/nanite-test-fixture-binary-that-does-not-exist",
	}); err != nil {
		t.Fatalf("pool.Register: %v", err)
	}
	transport := newRemoteTransport(pool, "test-stdio", gmcpclient.TransportStdio)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := transport.CallTool(ctx, "noop", nil)
	if err == nil {
		t.Fatal("CallTool against an unresolvable command succeeded, want a dial error")
	}
	if isProvablyUnsent(err) {
		t.Fatalf("CallTool's dial-failure error was classified as provably-unsent: %v", err)
	}
	if strings.Contains(err.Error(), "client is closing") {
		t.Fatalf("dial-failure error unexpectedly contains \"client is closing\": %v", err)
	}
}
