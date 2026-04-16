package mcp

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// Regression tests for audit 2026-04-10-mcp-client-transport finding 01
// (stdio transport leaks subprocess/goroutine/FDs on timeout or cancel).
//
// The fix installs killAndReapLocked() in every error branch of call().
// These tests drive a real subprocess (`sleep`) that will never respond
// and assert the transport reaps it rather than letting it orphan.

func startSleepTransport(t *testing.T) *StdioTransport {
	t.Helper()
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skipf("sleep not on PATH: %v", err)
	}
	// `sleep 300` consumes no stdin and never writes stdout — exactly the
	// shape of an MCP server that hangs.
	// Allowlist PATH so buildSubprocessEnv doesn't refuse to start.
	return NewStdioTransport("sleep", []string{"300"}, nil, []string{"PATH"})
}

func TestStdioTransport_ReapsOnTimeout(t *testing.T) {
	tr := startSleepTransport(t)
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := tr.call(ctx, "tools/list", nil)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.started {
		t.Fatal("transport still marked started after timeout — kill/reap did not run")
	}
	if tr.cmd == nil || tr.cmd.ProcessState == nil {
		t.Fatal("subprocess was not waited on — ProcessState nil; would leak")
	}
}

func TestStdioTransport_ReapsOnContextCancel(t *testing.T) {
	tr := startSleepTransport(t)
	defer tr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel shortly after call() starts waiting.
	time.AfterFunc(75*time.Millisecond, cancel)

	_, err := tr.call(ctx, "tools/list", nil)
	if err == nil {
		t.Fatal("expected cancel error, got nil")
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.started {
		t.Fatal("transport still marked started after cancel")
	}
	if tr.cmd == nil || tr.cmd.ProcessState == nil {
		t.Fatal("subprocess was not waited on — ProcessState nil; would leak")
	}
}

func TestStdioTransport_KillAndReapLockedIdempotent(t *testing.T) {
	tr := startSleepTransport(t)
	defer tr.Close()

	tr.mu.Lock()
	if err := tr.start(); err != nil {
		tr.mu.Unlock()
		t.Fatalf("start: %v", err)
	}
	tr.killAndReapLocked()
	// Second call must not panic or block — idempotency guard.
	tr.killAndReapLocked()
	if tr.started {
		t.Fatal("started flag not cleared")
	}
	tr.mu.Unlock()
}
