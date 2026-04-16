package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/nanite/internal/safego"
)

// maxStdioResponseBytes caps the size of a single JSON-RPC response line read
// from an MCP subprocess. Without this cap, bufio.Reader.ReadBytes would grow
// unbounded and a malicious or misbehaving server could OOM the host with one
// reply (audit 2026-04-10-mcp-client-transport finding 02). Per-server
// overrides are deferred to S4b; 10 MiB matches the audit recommendation.
const maxStdioResponseBytes = 10 * 1024 * 1024

// StdioTransport implements MCP over a subprocess stdin/stdout.
type StdioTransport struct {
	command      string
	args         []string
	env          []string // "KEY=VALUE" pairs declared on MCPServerConfig.Env
	envAllowlist []string // host env var names the subprocess may inherit

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader

	nextID           atomic.Int64
	mu               sync.Mutex // serializes requests
	started          bool
	maxResponseBytes int // 0 means use the package-default maxStdioResponseBytes
}

// NewStdioTransport creates a new stdio-based MCP transport.
//
// env is the per-server user-declared "KEY=VALUE" list from
// MCPServerConfig.Env — trusted and appended as-is to the subprocess env.
// envAllowlist is the list of host env var names (e.g. "PATH", "HOME") the
// subprocess is permitted to inherit from the nanite process (S4b D6).
// Default nil/empty allowlist means the subprocess starts with only the
// explicit env entries, nothing inherited — closes finding 10.
func NewStdioTransport(command string, args []string, env []string, envAllowlist []string) *StdioTransport {
	return &StdioTransport{
		command:      command,
		args:         args,
		env:          env,
		envAllowlist: envAllowlist,
	}
}

// SetMaxResponseBytes overrides the default 10 MiB per-line cap with a
// tier-derived ceiling. Values ≤ 0 are ignored so accidental zeroing can't
// disable the cap. Manager calls this after AddServer based on the registered
// server's TrustTier (S4b D2).
func (t *StdioTransport) SetMaxResponseBytes(n int) {
	if n > 0 {
		t.mu.Lock()
		t.maxResponseBytes = n
		t.mu.Unlock()
	}
}

// effectiveMaxResponseBytes returns the active cap — the tier override when
// set, the package default otherwise. Caller must hold t.mu.
func (t *StdioTransport) effectiveMaxResponseBytesLocked() int {
	if t.maxResponseBytes > 0 {
		return t.maxResponseBytes
	}
	return maxStdioResponseBytes
}

// start launches the subprocess if not already running.
func (t *StdioTransport) start() error {
	if t.started {
		return nil
	}

	t.cmd = exec.Command(t.command, t.args...)

	// S4b D6 / finding 10: the subprocess env is built deterministically
	// from the allowlist + the per-server Env declared on MCPServerConfig,
	// NOT inherited from nanite's environment by default. Host vars only
	// enter when their name is explicitly allowlisted. This closes the
	// credential-leak vector (AWS_*, GITHUB_TOKEN, etc.) identified by the
	// audit.
	childEnv, err := t.buildSubprocessEnv()
	if err != nil {
		return err
	}
	t.cmd.Env = childEnv

	t.stdin, err = t.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	stdoutPipe, err := t.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	t.stdout = bufio.NewReader(stdoutPipe)

	// Discard stderr to avoid blocking
	t.cmd.Stderr = nil

	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", t.command, err)
	}

	t.started = true
	return nil
}

// buildSubprocessEnv computes the env slice to hand to exec.Cmd based on
// the configured allowlist + user-declared Env. A bare command like
// "my-mcp-server" needs PATH to resolve via LookPath, so require that PATH
// be satisfied by at least one of:
//
//   - the allowlist (host PATH inherited),
//   - an explicit "PATH=..." entry in the per-server Env, or
//   - the command itself being path-qualified (contains a '/'),
//
// whichever is true. When none are, fail loudly at start-time rather than
// surface as an opaque "exec: file not found" later. Audit finding 10.
func (t *StdioTransport) buildSubprocessEnv() ([]string, error) {
	if !t.pathSatisfied() {
		return nil, fmt.Errorf(
			"mcp stdio: %q is a bare command but no PATH source (allowlist=%v, env has PATH=? no)",
			t.command, t.envAllowlist,
		)
	}

	env := make([]string, 0, len(t.envAllowlist)+len(t.env))
	for _, key := range t.envAllowlist {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	// User-declared per-server env is already trusted; append last so it
	// overrides any inherited value of the same key.
	env = append(env, t.env...)
	return env, nil
}

// pathSatisfied reports whether the subprocess will have a PATH to resolve
// t.command against — either via the allowlist, an explicit "PATH=..." in
// t.env, or because t.command itself is path-qualified (so exec skips
// LookPath entirely).
func (t *StdioTransport) pathSatisfied() bool {
	if strings.ContainsRune(t.command, '/') {
		return true
	}
	for _, k := range t.envAllowlist {
		if k == "PATH" {
			return true
		}
	}
	for _, kv := range t.env {
		if strings.HasPrefix(kv, "PATH=") {
			return true
		}
	}
	return false
}

// call sends a JSON-RPC request and reads the response.
func (t *StdioTransport) call(ctx context.Context, method string, params any) (*JSONRPCResponse, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.start(); err != nil {
		return nil, err
	}

	id := t.nextID.Add(1)
	rpcReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	payload, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Write request + newline
	payload = append(payload, '\n')
	if _, err := t.stdin.Write(payload); err != nil {
		t.killAndReapLocked()
		return nil, fmt.Errorf("write to stdin: %w", err)
	}

	// Read response line with timeout.
	type readResult struct {
		line []byte
		err  error
	}
	readCh := make(chan readResult, 1)
	maxBytes := t.effectiveMaxResponseBytesLocked()
	safego.Go(ctx, "mcp.stdio.transport.read", func() {
		line, err := readLineBounded(t.stdout, maxBytes)
		readCh <- readResult{line, err}
	})

	timeout := 30 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}

	var line []byte
	select {
	case res := <-readCh:
		if res.err != nil {
			t.killAndReapLocked()
			return nil, fmt.Errorf("read from stdout: %w", res.err)
		}
		line = res.line
	case <-time.After(timeout):
		t.killAndReapLocked()
		return nil, fmt.Errorf("timeout waiting for response from %s after %s", t.command, timeout)
	case <-ctx.Done():
		t.killAndReapLocked()
		return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
	}

	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(line, &rpcResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("JSON-RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return &rpcResp, nil
}

// ListTools sends a tools/list request and returns the available tools.
func (t *StdioTransport) ListTools(ctx context.Context) ([]Tool, error) {
	resp, err := t.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}

	var result struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse tools/list result: %w", err)
	}

	return result.Tools, nil
}

// CallTool sends a tools/call request and returns the result.
func (t *StdioTransport) CallTool(ctx context.Context, name string, arguments map[string]any) (*ToolResult, error) {
	params := map[string]any{
		"name":      name,
		"arguments": arguments,
	}

	resp, err := t.call(ctx, "tools/call", params)
	if err != nil {
		return nil, fmt.Errorf("tools/call %s: %w", name, err)
	}

	var result ToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse tools/call result: %w", err)
	}

	return &result, nil
}

// readLineBounded reads bytes until a newline or until max bytes have been
// buffered (excluding the newline). Exceeding max returns an error without
// consuming the rest of the line — the caller must tear the transport down,
// otherwise the next call will read mid-line garbage.
//
// Uses ReadSlice chunked through bufio's internal buffer rather than byte-
// at-a-time ReadByte to avoid a per-byte method-call loop on legitimately
// large (near-cap) responses.
func readLineBounded(r *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		switch err {
		case nil:
			chunk = chunk[:len(chunk)-1]
			if len(buf)+len(chunk) > max {
				return nil, fmt.Errorf("mcp response exceeded %d bytes", max)
			}
			return append(buf, chunk...), nil
		case bufio.ErrBufferFull:
			if len(buf)+len(chunk) > max {
				return nil, fmt.Errorf("mcp response exceeded %d bytes", max)
			}
			buf = append(buf, chunk...)
			continue
		default:
			return nil, err
		}
	}
}

// killAndReapLocked kills the current subprocess, closes its pipes, waits
// for exit, and marks the transport as not started. Caller must hold t.mu.
// Idempotent.
//
// Closes audit 2026-04-10-mcp-client-transport finding 01. Before this
// helper, timeout / ctx.Done / read-error / write-error branches only set
// t.started = false and leaked the subprocess, its reader goroutine, and a
// stdin/stdout FD pair. The next start() then overwrote t.cmd in place,
// making the previous process unreachable. This reaps it properly; Wait()
// closes the stdout pipe so the orphaned reader goroutine unblocks via EOF.
func (t *StdioTransport) killAndReapLocked() {
	if !t.started {
		return
	}
	if t.stdin != nil {
		_ = t.stdin.Close()
	}
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
		_ = t.cmd.Wait()
	}
	t.started = false
}

// Close stops the subprocess.
func (t *StdioTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.started {
		return nil
	}

	t.killAndReapLocked()
	slog.Info("mcp: stopped stdio transport", "command", t.command)
	return nil
}
