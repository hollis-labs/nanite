package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

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
		t.started = false
		return nil, fmt.Errorf("write to stdin: %w", err)
	}

	// Read response line with timeout.
	type readResult struct {
		line []byte
		err  error
	}
	readCh := make(chan readResult, 1)
	go func() {
		line, err := t.stdout.ReadBytes('\n')
		readCh <- readResult{line, err}
	}()

	timeout := 30 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}

	var line []byte
	select {
	case res := <-readCh:
		if res.err != nil {
			t.started = false
			return nil, fmt.Errorf("read from stdout: %w", res.err)
		}
		line = res.line
	case <-time.After(timeout):
		t.started = false
		return nil, fmt.Errorf("timeout waiting for response from %s after %s", t.command, timeout)
	case <-ctx.Done():
		t.started = false
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

// Close stops the subprocess.
func (t *StdioTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.started {
		return nil
	}

	t.stdin.Close()
	err := t.cmd.Process.Kill()
	t.cmd.Wait()
	t.started = false
	log.Printf("mcp: stopped stdio transport for %s", t.command)
	return err
}
