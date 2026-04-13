package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	sdksub "github.com/hollis-labs/plugin-sdk/subprocess"
)

// newPipedTransport returns a real subprocess.Transport connected to a
// scripted in-process responder over io.Pipe pairs. The responder reads each
// JSON-RPC request line and synthesizes a response via the handler, which
// receives the decoded method and params and returns a result value (or an
// error to emit an RPC error).
//
// plugin-sdk v0.1.1 does not ship a subprocesstest harness, so we build the
// minimum needed to exercise PluginMCPTransport against a real Transport +
// real JSON-RPC wire format. The responder goroutine exits on pipe close.
func newPipedTransport(t *testing.T, handler func(method string, params json.RawMessage) (any, *subprocess.RPCError)) *subprocess.Transport {
	t.Helper()

	// Host -> plugin: host writes to hostW, plugin reads from pluginR.
	pluginR, hostW := io.Pipe()
	// Plugin -> host: plugin writes to pluginW, host reads from hostR.
	hostR, pluginW := io.Pipe()

	transport := subprocess.NewTransport(hostR, hostW)

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer pluginW.Close()
		br := bufio.NewReader(pluginR)
		for {
			line, err := br.ReadBytes('\n')
			if err != nil {
				return
			}
			var req subprocess.RPCRequest
			if err := json.Unmarshal(line, &req); err != nil {
				continue
			}
			// req.Params is any on the wire, re-marshal to pass as RawMessage
			// to the handler for ergonomic decoding.
			rawParams, _ := json.Marshal(req.Params)
			result, rpcErr := handler(req.Method, rawParams)
			resp := subprocess.RPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
			}
			if rpcErr != nil {
				resp.Error = rpcErr
			} else {
				data, _ := json.Marshal(result)
				resp.Result = data
			}
			out, _ := json.Marshal(resp)
			out = append(out, '\n')
			if _, err := pluginW.Write(out); err != nil {
				return
			}
		}
	}()

	t.Cleanup(func() {
		hostW.Close()
		pluginR.Close()
		// Wait briefly for responder to exit so goroutines don't leak.
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
		}
	})

	return transport
}

func TestPluginMCPTransport_ListAndCall(t *testing.T) {
	wantTool := Tool{
		Name:        "echo",
		Description: "echo the input",
		InputSchema: map[string]any{"type": "object"},
	}

	transport := newPipedTransport(t, func(method string, params json.RawMessage) (any, *subprocess.RPCError) {
		switch method {
		case sdksub.MethodListTools:
			var p pluginListToolsParams
			_ = json.Unmarshal(params, &p)
			if p.Server != "echo-srv" {
				return nil, &subprocess.RPCError{Code: subprocess.ErrCodeInvalidParams, Message: "wrong server: " + p.Server}
			}
			return pluginListToolsResult{Tools: []Tool{wantTool}}, nil
		case sdksub.MethodCallTool:
			var p pluginCallToolParams
			_ = json.Unmarshal(params, &p)
			if p.Server != "echo-srv" || p.Tool != "echo" {
				return nil, &subprocess.RPCError{Code: subprocess.ErrCodeInvalidParams, Message: "unknown tool"}
			}
			text, _ := p.Arguments["text"].(string)
			return ToolResult{
				Content: []ToolContent{{Type: "text", Text: "echoed: " + text}},
			}, nil
		default:
			return nil, &subprocess.RPCError{Code: subprocess.ErrCodeMethodNotFound, Message: method}
		}
	})

	mgr := NewManager()
	if err := mgr.AddPluginServer("echo-srv", transport); err != nil {
		t.Fatalf("AddPluginServer: %v", err)
	}

	// Duplicate registration rejected.
	if err := mgr.AddPluginServer("echo-srv", transport); err == nil {
		t.Fatal("expected duplicate registration to fail")
	}
	// Empty name rejected.
	if err := mgr.AddPluginServer("", transport); err == nil {
		t.Fatal("expected empty name to fail")
	}
	// Nil transport rejected.
	if err := mgr.AddPluginServer("nil-srv", nil); err == nil {
		t.Fatal("expected nil transport to fail")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	tools, err := mgr.DiscoverServerTools(ctx, "echo-srv")
	if err != nil {
		t.Fatalf("DiscoverServerTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != wantTool.Name || tools[0].Description != wantTool.Description {
		t.Fatalf("unexpected tools: %+v", tools)
	}

	// Drive ExecuteTool end-to-end through the manager's prefixed-name API.
	if err := mgr.DiscoverTools(ctx); err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	out, err := mgr.ExecuteTool(ctx, "mcp__echo-srv__echo", map[string]any{"text": "hi"})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if out != "echoed: hi" {
		t.Fatalf("unexpected result: %q", out)
	}
}

func TestPluginMCPTransport_NilTransport(t *testing.T) {
	p := NewPluginMCPTransport(nil, "x")
	if _, err := p.ListTools(context.Background()); err == nil {
		t.Fatal("expected ListTools on nil transport to fail")
	}
	if _, err := p.CallTool(context.Background(), "t", nil); err == nil {
		t.Fatal("expected CallTool on nil transport to fail")
	}
}

// stubTransport is a no-op MCPTransport used to exercise Manager.AddServer
// validation without spinning up a real transport.
type stubTransport struct{}

func (stubTransport) ListTools(_ context.Context) ([]Tool, error) { return nil, nil }
func (stubTransport) CallTool(_ context.Context, _ string, _ map[string]any) (*ToolResult, error) {
	return &ToolResult{}, nil
}

func TestManager_AddServer_Validation(t *testing.T) {
	t.Run("rejects empty name", func(t *testing.T) {
		mgr := NewManager()
		if err := mgr.AddServer("", stubTransport{}); err == nil {
			t.Fatal("expected empty name to fail")
		}
	})

	t.Run("rejects nil transport", func(t *testing.T) {
		mgr := NewManager()
		if err := mgr.AddServer("srv", nil); err == nil {
			t.Fatal("expected nil transport to fail")
		}
	})

	t.Run("rejects duplicate registration", func(t *testing.T) {
		mgr := NewManager()
		if err := mgr.AddServer("srv", stubTransport{}); err != nil {
			t.Fatalf("first AddServer: %v", err)
		}
		if err := mgr.AddServer("srv", stubTransport{}); err == nil {
			t.Fatal("expected duplicate registration to fail")
		}
	})
}
