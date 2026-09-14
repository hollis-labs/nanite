package subprocess

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/nanite/internal/safego"
)

// ErrSubprocessGone reports that a subprocess plugin's pipe closed — it exited,
// crashed, or was killed — while a call was outstanding or before one was sent.
var ErrSubprocessGone = errors.New("subprocess: plugin is gone")

// Transport handles JSON-RPC communication with a subprocess plugin over
// a reader/writer pair (typically stdin/stdout pipes). The transport is
// designed to be swappable — stdio now, unix socket later.
//
// Every call gets a monotonic id, registers a response channel under it, and a
// single background reader goroutine routes each response frame to the waiter
// that asked for it. If a call's context expires or times out, the waiter deregisters
// its channel and returns without closing the underlying reader pipe. The background
// reader remains healthy and subsequent calls continue to function normally (CW-20260914-0006).
type Transport struct {
	w      io.Writer
	r      *bufio.Reader
	rClose io.Closer // underlying reader, closed by Close() on host shutdown

	nextID atomic.Int64

	mu       sync.Mutex
	pending  map[int64]chan *RPCResponse
	closedBy error

	writeMu sync.Mutex
	done    chan struct{}
}

// NewTransport creates a transport over the given reader/writer pair and starts
// the background reader loop. If r implements io.Closer, it will be closed by
// Close() to unblock any pending reads on host shutdown.
func NewTransport(r io.Reader, w io.Writer) *Transport {
	t := &Transport{
		w:       w,
		pending: make(map[int64]chan *RPCResponse),
		done:    make(chan struct{}),
	}
	if rc, ok := r.(io.Closer); ok {
		t.rClose = rc
	}
	if r != nil {
		t.r = bufio.NewReaderSize(r, 64*1024)
		safego.Go(context.Background(), "plugin.subprocess.transport.read", func() {
			t.readLoop()
		})
	} else {
		close(t.done)
	}
	return t
}

// Close closes the underlying reader and marks the transport as closed.
func (t *Transport) Close() error {
	var err error
	if t.rClose != nil {
		err = t.rClose.Close()
	}
	t.closeWith(err)
	return err
}

// Call sends a JSON-RPC request and waits for a response with matching ID.
//
// ctx bounds this call and nothing else. Canceling or timing out deregisters
// the waiter and returns; it does NOT close the connection or cancel the child's
// process, leaving the transport intact for subsequent calls.
func (t *Transport) Call(ctx context.Context, method string, params any) (*RPCResponse, error) {
	if t.r == nil {
		return nil, fmt.Errorf("call %s: transport has no reader", method)
	}

	id := t.nextID.Add(1)
	reply := make(chan *RPCResponse, 1)

	t.mu.Lock()
	if t.closedBy != nil {
		err := t.closedBy
		t.mu.Unlock()
		return nil, fmt.Errorf("call %s: %w", method, err)
	}
	t.pending[id] = reply
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
	}()

	req := RPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	if err := t.write(req); err != nil {
		return nil, fmt.Errorf("write %s: %w", method, err)
	}

	// Apply a default 30-second timeout if caller context has no deadline.
	callCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	select {
	case resp := <-reply:
		if resp == nil {
			return nil, fmt.Errorf("read %s response: %w", method, ErrSubprocessGone)
		}
		if resp.Error != nil {
			return resp, resp.Error
		}
		return resp, nil
	case <-callCtx.Done():
		return nil, fmt.Errorf("read %s response: %w", method, callCtx.Err())
	case <-t.done:
		t.mu.Lock()
		err := t.closedBy
		t.mu.Unlock()
		if err == nil {
			err = ErrSubprocessGone
		}
		return nil, fmt.Errorf("read %s response: %w", method, err)
	}
}

// Notify sends a JSON-RPC notification (no response expected).
func (t *Transport) Notify(method string, params any) error {
	t.mu.Lock()
	if t.closedBy != nil {
		err := t.closedBy
		t.mu.Unlock()
		return fmt.Errorf("notify %s: %w", method, err)
	}
	t.mu.Unlock()

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
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_, err = t.w.Write(data)
	return err
}

// readLoop runs as a single background reader until EOF or reader error.
func (t *Transport) readLoop() {
	var readErr error
	for {
		line, err := t.r.ReadBytes('\n')
		if len(line) > 0 {
			t.deliver(line)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				readErr = err
			}
			break
		}
	}
	t.closeWith(readErr)
}

// deliver routes one response frame to whoever is waiting for its ID.
func (t *Transport) deliver(line []byte) {
	var resp RPCResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return
	}
	t.mu.Lock()
	reply, waiting := t.pending[resp.ID]
	t.mu.Unlock()
	if !waiting {
		return
	}
	select {
	case reply <- &resp:
	default:
	}
}

// closeWith marks the transport closed and fails pending/future calls.
func (t *Transport) closeWith(cause error) {
	t.mu.Lock()
	if t.closedBy == nil {
		if cause != nil {
			t.closedBy = fmt.Errorf("%w: %w", ErrSubprocessGone, cause)
		} else {
			t.closedBy = ErrSubprocessGone
		}
		close(t.done)
	}
	t.pending = make(map[int64]chan *RPCResponse)
	t.mu.Unlock()
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
