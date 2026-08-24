package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"
)

// mockPlugin reads JSON-RPC requests from r and writes responses to w.
// It handles the core protocol methods for testing.
func mockPlugin(r io.Reader, w io.Writer, handlers map[string]func(json.RawMessage) (any, *RPCError)) {
	transport := NewTransport(r, w)
	for {
		line, err := transport.r.ReadBytes('\n')
		if err != nil {
			return
		}
		var req RPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if req.ID == 0 {
			continue // notification, no response needed
		}

		handler, ok := handlers[req.Method]
		var resp RPCResponse
		resp.JSONRPC = "2.0"
		resp.ID = req.ID

		if !ok {
			resp.Error = &RPCError{Code: ErrCodeMethodNotFound, Message: "method not found: " + req.Method}
		} else {
			var rawParams json.RawMessage
			if req.Params != nil {
				rawParams, _ = json.Marshal(req.Params)
			}
			result, rpcErr := handler(rawParams)
			if rpcErr != nil {
				resp.Error = rpcErr
			} else {
				resp.Result, _ = json.Marshal(result)
			}
		}

		data, _ := json.Marshal(resp)
		data = append(data, '\n')
		w.Write(data)
	}
}

func TestTransport_Call(t *testing.T) {
	// Create two pipe pairs: host writes to pluginIn, reads from pluginOut.
	pluginInR, pluginInW := io.Pipe()
	pluginOutR, pluginOutW := io.Pipe()

	handlers := map[string]func(json.RawMessage) (any, *RPCError){
		"plugin/health": func(_ json.RawMessage) (any, *RPCError) {
			return &HealthResult{OK: true, Message: "all good"}, nil
		},
		"test/echo": func(params json.RawMessage) (any, *RPCError) {
			return json.RawMessage(params), nil
		},
	}

	go mockPlugin(pluginInR, pluginOutW, handlers)

	transport := NewTransport(pluginOutR, pluginInW)
	ctx := context.Background()

	// Test health call.
	result, err := CallResult[HealthResult](transport, ctx, "plugin/health", nil)
	if err != nil {
		t.Fatalf("health call failed: %v", err)
	}
	if !result.OK {
		t.Errorf("expected OK=true, got false")
	}
	if result.Message != "all good" {
		t.Errorf("expected message 'all good', got %q", result.Message)
	}

	// Test echo call.
	echoParams := map[string]string{"hello": "world"}
	resp, err := transport.Call(ctx, "test/echo", echoParams)
	if err != nil {
		t.Fatalf("echo call failed: %v", err)
	}
	var echoed map[string]string
	if err := json.Unmarshal(resp.Result, &echoed); err != nil {
		t.Fatalf("unmarshal echo: %v", err)
	}
	if echoed["hello"] != "world" {
		t.Errorf("expected hello=world, got %v", echoed)
	}

	pluginInW.Close()
	pluginOutW.Close()
}

func TestTransport_CallError(t *testing.T) {
	pluginInR, pluginInW := io.Pipe()
	pluginOutR, pluginOutW := io.Pipe()

	handlers := map[string]func(json.RawMessage) (any, *RPCError){
		"test/fail": func(_ json.RawMessage) (any, *RPCError) {
			return nil, &RPCError{Code: ErrCodeNotFound, Message: "thing not found"}
		},
	}

	go mockPlugin(pluginInR, pluginOutW, handlers)

	transport := NewTransport(pluginOutR, pluginInW)
	ctx := context.Background()

	_, err := transport.Call(ctx, "test/fail", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected *RPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != ErrCodeNotFound {
		t.Errorf("expected code %d, got %d", ErrCodeNotFound, rpcErr.Code)
	}

	pluginInW.Close()
	pluginOutW.Close()
}

func TestTransport_CallTimeout(t *testing.T) {
	// Plugin reads the request (so write doesn't block) but never sends a response.
	pluginInR, pluginInW := io.Pipe()
	pluginOutR, pluginOutW := io.Pipe()

	// Drain requests from the host so the write side doesn't block.
	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := pluginInR.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	transport := NewTransport(pluginOutR, pluginInW)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := transport.Call(ctx, "plugin/health", nil)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	// Close pipes so goroutines can exit.
	pluginInW.Close()
	pluginOutW.Close()
}

func TestTransport_Notify(t *testing.T) {
	pluginInR, pluginInW := io.Pipe()

	// Read in a goroutine so the pipe write doesn't block.
	readCh := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 4096)
		n, _ := pluginInR.Read(buf)
		readCh <- buf[:n]
	}()

	transport := NewTransport(nil, pluginInW)

	// Notification should not block (no response expected).
	err := transport.Notify("event/handle", map[string]string{"type": "test"})
	if err != nil {
		t.Fatalf("notify failed: %v", err)
	}

	// Verify the notification was written.
	data := <-readCh
	var req RPCRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal notification: %v", err)
	}
	if req.ID != 0 {
		t.Errorf("notification should have ID=0, got %d", req.ID)
	}
	if req.Method != "event/handle" {
		t.Errorf("expected method event/handle, got %q", req.Method)
	}

	pluginInW.Close()
}
