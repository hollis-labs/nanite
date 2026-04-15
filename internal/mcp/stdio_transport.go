package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
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
	command string
	args    []string
	env     []string // "KEY=VALUE" pairs

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader

	nextID  atomic.Int64
	mu      sync.Mutex // serializes requests
	started bool
}

// NewStdioTransport creates a new stdio-based MCP transport.
func NewStdioTransport(command string, args []string, env []string) *StdioTransport {
	return &StdioTransport{
		command: command,
		args:    args,
		env:     env,
	}
}

// start launches the subprocess if not already running.
func (t *StdioTransport) start() error {
	if t.started {
		return nil
	}

	t.cmd = exec.Command(t.command, t.args...)
	if len(t.env) > 0 {
		t.cmd.Env = append(t.cmd.Environ(), t.env...)
	}

	var err error
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
	safego.Go(ctx, "mcp.stdio.transport.read", func() {
		line, err := readLineBounded(t.stdout, maxStdioResponseBytes)
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
