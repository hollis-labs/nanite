package mcp

import (
	"bufio"
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	gmcpclient "github.com/hollis-labs/go-mcp/client"
	"github.com/hollis-labs/go-safefs/pathsafe"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestIsProvablyUnsent pins the exact classifier CallTool's retry gate
// relies on, now go-mcp's own gmcpclient.IsProvablyUnsent (RetryIfUnsent's
// gate; CW-20260930-0207): only the official SDK's "client is closing"
// phrasing (see remote_transport.go's CallTool doc for why that specific
// phrase is safe to retry) should match, not any other recoverable-looking
// connection error. Widening this match is what would reintroduce the
// double-execution risk CallTool's general no-retry policy exists to avoid.
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
			if got := gmcpclient.IsProvablyUnsent(tt.err); got != tt.want {
				t.Errorf("IsProvablyUnsent(%v) = %v, want %v", tt.err, got, tt.want)
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

	pool := gmcpclient.NewPool(gmcpclient.WithIdentity("nanite-test", "0.0.0"), gmcpclient.WithRetries(0))
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

// TestRemoteTransportCallTool_RetriesProvablyUnsentError pins recovery from
// the SDK's pre-write rejection after a stdio subprocess dies between calls.
// Receive the first reply before terminating the child, then wait for the
// SDK's read loop to observe shutdown. Calling during shutdown can instead
// produce an in-flight EOF, which is deliberately outside the retry contract.
// Neither the pool nor its cached session is invalidated by the test; the
// second transport call must invalidate and redial through RetryIfUnsent.
func TestRemoteTransportCallTool_RetriesProvablyUnsentError(t *testing.T) {
	pidLog, err := pathsafe.ResolveUnder(t.TempDir(), "pids.log")
	if err != nil {
		t.Fatalf("pathsafe.ResolveUnder: %v", err)
	}
	transport := stdioFixtureTransport(t, map[string]string{
		runAsFixtureServerPIDLogEnv: pidLog,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := transport.CallTool(ctx, "noop", nil)
	if err != nil || res.IsError {
		t.Fatalf("first CallTool = %+v, %v", res, err)
	}
	pids := readPIDLog(t, pidLog)
	if len(pids) != 1 {
		t.Fatalf("pid log = %v, want one initial startup", pids)
	}
	pid, err := strconv.Atoi(pids[0])
	if err != nil {
		t.Fatalf("parse fixture pid: %v", err)
	}
	client, err := transport.pool.Get(transport.name)
	if err != nil {
		t.Fatal(err)
	}
	session := client.SDKSession()
	if session == nil {
		t.Fatal("first call did not leave a cached SDK session")
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("find fixture process: %v", err)
	}
	defer process.Release()
	if killErr := process.Kill(); killErr != nil {
		t.Fatalf("terminate fixture after its reply: %v", killErr)
	}
	closed := make(chan error, 1)
	go func() { closed <- session.Wait() }()
	select {
	case waitErr := <-closed:
		t.Logf("fixture shutdown observed by SDK: %v", waitErr)
	case <-ctx.Done():
		t.Fatal("SDK did not observe fixture shutdown before deadline")
	}

	// Use the same cached SDK session, without any retry or invalidation, to
	// prove the next request is rejected before writing rather than in flight.
	_, err = session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "noop"})
	if !gmcpclient.IsProvablyUnsent(err) {
		t.Fatalf("closed-session call = %v, want provably-unsent rejection", err)
	}
	t.Logf("cached-session rejection: %v", err)
	res, err = transport.CallTool(ctx, "noop", nil)
	if err != nil || res.IsError {
		t.Fatalf("second CallTool did not recover: result=%+v err=%v", res, err)
	}
	pids = readPIDLog(t, pidLog)
	if len(pids) != 2 || pids[0] == pids[1] {
		t.Fatalf("pid log = %v, want exactly two DISTINCT startups after retry", pids)
	}
}

// TestRemoteTransportCallTool_DialFailureIsNotMisclassifiedAsProvablyUnsent
// guards the other direction: a stdio server that can never be dialed at
// all (bad command) fails with an ordinary exec/dial error, not the SDK's
// "client is closing" phrasing -- IsProvablyUnsent must not fire on it, so
// CallTool must not (incorrectly) attempt a retry-after-invalidate cycle
// for a failure class the classifier was never meant to cover.
//
// This can't directly count Pool-level call attempts (remoteTransport.pool
// is a concrete *gmcpclient.Pool, not an interface a fake can stand in
// for), so it asserts on the one thing that is directly observable: the
// returned error's shape. TestIsProvablyUnsent above pins the classifier's
// own behavior on this same error family directly.
func TestRemoteTransportCallTool_DialFailureIsNotMisclassifiedAsProvablyUnsent(t *testing.T) {
	pool := gmcpclient.NewPool(gmcpclient.WithIdentity("nanite-test", "0.0.0"), gmcpclient.WithRetries(0))
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
	if gmcpclient.IsProvablyUnsent(err) {
		t.Fatalf("CallTool's dial-failure error was classified as provably-unsent: %v", err)
	}
	if strings.Contains(err.Error(), "client is closing") {
		t.Fatalf("dial-failure error unexpectedly contains \"client is closing\": %v", err)
	}
}
