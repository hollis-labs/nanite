package service

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/selftools"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

type pythonDispatcherToolService struct {
	execute func(context.Context, string, string, map[string]any) (*ToolResult, error)
}

func (s *pythonDispatcherToolService) Execute(ctx context.Context, agentID, toolName string, input map[string]any) (*ToolResult, error) {
	return s.execute(ctx, agentID, toolName, input)
}

func (*pythonDispatcherToolService) SelectForAgent(context.Context, string, string, string, string, int) (*ToolSelection, error) {
	return nil, nil
}

func (*pythonDispatcherToolService) HandleRequestTools(context.Context, string, map[string]any) ([]llmtypes.ToolDefinition, string, error) {
	return nil, "", nil
}

func (*pythonDispatcherToolService) ListSummaries() []toolclient.ToolSummary { return nil }

func (*pythonDispatcherToolService) GetToolMeta(context.Context, string) (ToolMetaInfo, bool) {
	return ToolMetaInfo{}, false
}

func (*pythonDispatcherToolService) GetToolSchema(string) map[string]any { return nil }

func TestPythonToolDispatcher(t *testing.T) {
	t.Run("success uses caller identity and wraps output", func(t *testing.T) {
		args := map[string]any{"query": "nanite"}
		tools := &pythonDispatcherToolService{
			execute: func(_ context.Context, agentID, toolName string, gotArgs map[string]any) (*ToolResult, error) {
				if agentID != "agent-profile-1" {
					t.Errorf("agentID = %q, want agent-profile-1", agentID)
				}
				if toolName != "search_docs" {
					t.Errorf("toolName = %q, want search_docs", toolName)
				}
				if !reflect.DeepEqual(gotArgs, args) {
					t.Errorf("args = %#v, want %#v", gotArgs, args)
				}
				return &ToolResult{Output: "broker result"}, nil
			},
		}
		dispatcher := NewPythonToolDispatcher(tools)
		ctx := mcp.WithCallerProfile(context.Background(), "agent-profile-1")

		got, err := dispatcher.Dispatch(ctx, "session-1", "search_docs", args)
		if err != nil {
			t.Fatalf("Dispatch: %v", err)
		}
		want := map[string]any{"output": "broker result"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("result = %#v, want %#v", got, want)
		}
	})

	t.Run("tool result error becomes dispatch error", func(t *testing.T) {
		tools := &pythonDispatcherToolService{
			execute: func(context.Context, string, string, map[string]any) (*ToolResult, error) {
				return &ToolResult{Output: "tool rejected input", IsError: true}, nil
			},
		}

		got, err := NewPythonToolDispatcher(tools).Dispatch(context.Background(), "session-1", "write_file", nil)
		if got != nil {
			t.Fatalf("result = %#v, want nil", got)
		}
		if err == nil || err.Error() != "tool rejected input" {
			t.Fatalf("error = %v, want tool rejected input", err)
		}
	})

	t.Run("tool service error is preserved", func(t *testing.T) {
		wantErr := errors.New("transport failed")
		tools := &pythonDispatcherToolService{
			execute: func(context.Context, string, string, map[string]any) (*ToolResult, error) {
				return nil, wantErr
			},
		}

		got, err := NewPythonToolDispatcher(tools).Dispatch(context.Background(), "session-1", "search_docs", nil)
		if got != nil {
			t.Fatalf("result = %#v, want nil", got)
		}
		if !errors.Is(err, wantErr) {
			t.Fatalf("error = %v, want %v", err, wantErr)
		}
	})
}

func TestPythonRunProductionCollaborators(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found on PATH")
	}

	t.Run("real permission engine denies before dispatch", func(t *testing.T) {
		dispatchCalls := 0
		tools := &pythonDispatcherToolService{
			execute: func(context.Context, string, string, map[string]any) (*ToolResult, error) {
				dispatchCalls++
				return &ToolResult{Output: "must not execute"}, nil
			},
		}
		transport := &selftools.SelfToolsTransport{
			PythonPermChecker: permission.NewEngine(permission.ModeDefault, &permission.RuleSet{
				Rules: []permission.Rule{{Tool: "blocked_tool", Behavior: permission.DecisionDeny}},
			}),
			PythonDispatcher: NewPythonToolDispatcher(tools),
		}

		ctx := mcp.WithCallerProfile(mcp.WithSessionID(context.Background(), "session-denied"), "agent-denied")
		result := runPythonThroughTransport(ctx, t, transport, `
try:
    tool_call("blocked_tool", {})
    result = {"unexpected": True}
except RuntimeError as exc:
    result = {"message": str(exc)}
`)

		if dispatchCalls != 0 {
			t.Fatalf("ToolService.Execute calls = %d, want 0", dispatchCalls)
		}
		if len(result.ToolCalls) != 1 || result.ToolCalls[0].Status != "denied" {
			t.Fatalf("tool calls = %#v, want one denied call", result.ToolCalls)
		}
		resultMap, ok := result.Result.(map[string]any)
		if !ok {
			t.Fatalf("result = %T %#v, want object", result.Result, result.Result)
		}
		message, _ := resultMap["message"].(string)
		if !strings.Contains(message, `permission denied for tool "blocked_tool": denied by rule`) {
			t.Fatalf("denial message = %q, want rule-backed permission denial", message)
		}
	})

	t.Run("real dispatcher returns broker output", func(t *testing.T) {
		dispatchCalls := 0
		tools := &pythonDispatcherToolService{
			execute: func(_ context.Context, agentID, toolName string, args map[string]any) (*ToolResult, error) {
				dispatchCalls++
				if agentID != "agent-success" {
					t.Errorf("agentID = %q, want agent-success", agentID)
				}
				if toolName != "search_docs" {
					t.Errorf("toolName = %q, want search_docs", toolName)
				}
				if args["query"] != "nanite" {
					t.Errorf("query = %#v, want nanite", args["query"])
				}
				return &ToolResult{Output: "real broker output"}, nil
			},
		}
		transport := &selftools.SelfToolsTransport{
			PythonPermChecker: permission.NewEngine(permission.ModeDefault, nil),
			PythonDispatcher:  NewPythonToolDispatcher(tools),
		}

		ctx := mcp.WithCallerProfile(mcp.WithSessionID(context.Background(), "session-success"), "agent-success")
		result := runPythonThroughTransport(ctx, t, transport, `
response = tool_call("search_docs", {"query": "nanite"})
result = response["output"]
`)

		if dispatchCalls != 1 {
			t.Fatalf("ToolService.Execute calls = %d, want 1", dispatchCalls)
		}
		if result.Result != "real broker output" {
			t.Fatalf("result = %#v, want real broker output", result.Result)
		}
		if len(result.ToolCalls) != 1 || result.ToolCalls[0].Status != "ok" {
			t.Fatalf("tool calls = %#v, want one successful call", result.ToolCalls)
		}
	})
}

func runPythonThroughTransport(ctx context.Context, t *testing.T, transport *selftools.SelfToolsTransport, code string) selftools.PythonRunResult {
	t.Helper()
	callResult, err := transport.CallTool(ctx, "python_run", map[string]any{"code": code})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if callResult.IsError {
		t.Fatalf("python_run tool error: %s", callResult.Content[0].Text)
	}

	var result selftools.PythonRunResult
	if err := json.Unmarshal([]byte(callResult.Content[0].Text), &result); err != nil {
		t.Fatalf("unmarshal python_run result: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("sandbox error: %s", result.Error)
	}
	return result
}

var _ ToolService = (*pythonDispatcherToolService)(nil)
