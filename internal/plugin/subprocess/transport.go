package subprocess

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// Transport handles JSON-RPC communication with a subprocess plugin over
// a reader/writer pair (typically stdin/stdout pipes). The transport is
// designed to be swappable — stdio now, unix socket later.
type Transport struct {
	w      io.Writer
	r      *bufio.Reader
	nextID atomic.Int64
	mu     sync.Mutex // serializes writes and read correlation
}

// NewTransport creates a transport over the given reader/writer pair.
func NewTransport(r io.Reader, w io.Writer) *Transport {
	return &Transport{
		w: w,
		r: bufio.NewReader(r),
	}
}

// Call sends a JSON-RPC request and waits for a response.
func (t *Transport) Call(ctx context.Context, method string, params any) (*RPCResponse, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	id := t.nextID.Add(1)
	req := RPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	if err := t.write(req); err != nil {
		return nil, fmt.Errorf("write %s: %w", method, err)
	}

	resp, err := t.read(ctx)
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", method, err)
	}

	if resp.Error != nil {
		return resp, resp.Error
	}
	return resp, nil
}

// Notify sends a JSON-RPC notification (no response expected).
func (t *Transport) Notify(method string, params any) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	req := RPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	return t.write(req)
}

// write marshals and sends a JSON-RPC message followed by a newline.
func (t *Transport) write(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')
	_, err = t.w.Write(data)
	return err
}

// read reads a single JSON-RPC response line with context-aware timeout.
func (t *Transport) read(ctx context.Context) (*RPCResponse, error) {
	type readResult struct {
		line []byte
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		line, err := t.r.ReadBytes('\n')
		ch <- readResult{line, err}
	}()

	// Default 30s timeout, respect context deadline if shorter.
	timeout := 30 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if d := time.Until(deadline); d < timeout {
			timeout = d
		}
	}

	select {
	case res := <-ch:
		if res.err != nil {
			return nil, fmt.Errorf("read: %w", res.err)
		}
		var resp RPCResponse
		if err := json.Unmarshal(res.line, &resp); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		return &resp, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout after %s", timeout)
	case <-ctx.Done():
		return nil, fmt.Errorf("context: %w", ctx.Err())
	}
}

// CallResult is a convenience that calls a method and unmarshals the result.
func CallResult[T any](t *Transport, ctx context.Context, method string, params any) (*T, error) {
	resp, err := t.Call(ctx, method, params)
	if err != nil {
		return nil, err
	}
	var result T
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("unmarshal %s result: %w", method, err)
	}
	return &result, nil
}
