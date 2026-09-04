package service

import (
	"context"
	"errors"

	"github.com/hollis-labs/nanite/internal/mcp"
)

// PythonToolDispatcher adapts ToolService to the dispatcher used by
// python_run's sandboxed tool_call helper. It deliberately reuses the same
// ToolService execution path as ordinary chat-turn tool calls.
type PythonToolDispatcher struct {
	tools ToolService
}

// NewPythonToolDispatcher builds the stateless ToolService adapter used by a
// Python sandbox. The calling agent identity comes from the tool-execution
// context rather than from sandbox-controlled input.
func NewPythonToolDispatcher(tools ToolService) *PythonToolDispatcher {
	return &PythonToolDispatcher{tools: tools}
}

func (d *PythonToolDispatcher) Dispatch(ctx context.Context, _ string, toolName string, args map[string]any) (any, error) {
	agentID := mcp.CallerProfileFromContext(ctx)
	result, err := d.tools.Execute(ctx, agentID, toolName, args)
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, errors.New(result.Output)
	}
	return map[string]any{"output": result.Output}, nil
}
