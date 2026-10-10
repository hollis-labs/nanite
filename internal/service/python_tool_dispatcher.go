package service

import (
	"context"
	"errors"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/selftools"
)

// PythonExecutionOwner is an internal host port, not actor metadata supplied by
// Python. VerifyPythonRun must verify the current caller's exact session and
// existing authority. ExecutePythonTool must revalidate pinned tool ownership,
// policy and schema, and own hooks, effect commit and redacted durable child
// records. Ask needs a genuine continuation owner; this adapter never approves it.
// No production implementation is supplied here.
type PythonExecutionOwner interface {
	VerifyPythonRun(context.Context, string) error
	ExecutePythonTool(context.Context, string, string, map[string]any) (*ToolResult, error)
}

// PythonToolDispatcher delegates only to an actual host execution owner. Raw
// ToolService execution and its manager fallback do not establish that owner.
type PythonToolDispatcher struct {
	owner PythonExecutionOwner
}

type pythonOwnerUnavailable struct{ cause error }

func (e pythonOwnerUnavailable) Error() string {
	return selftools.ErrPythonExecutionUnavailable.Error()
}
func (e pythonOwnerUnavailable) Unwrap() error { return e.cause }
func (e pythonOwnerUnavailable) Is(target error) bool {
	return target == selftools.ErrPythonExecutionUnavailable
}

// NewPythonToolDispatcher preserves the existing construction API. A ToolService
// without the host-owned execution port yields explicit unavailability before a
// process or nested tool starts. Caller identity must come from trusted context.
func NewPythonToolDispatcher(tools ToolService) *PythonToolDispatcher {
	owner, _ := tools.(PythonExecutionOwner)
	return &PythonToolDispatcher{owner: owner}
}

func (d *PythonToolDispatcher) AdmitPythonRun(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d == nil || d.owner == nil || sessionID == "" || mcp.SessionIDFromContext(ctx) != sessionID {
		return selftools.ErrPythonExecutionUnavailable
	}
	if err := d.owner.VerifyPythonRun(ctx, sessionID); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return pythonOwnerUnavailable{cause: err}
	}
	return ctx.Err()
}

func (d *PythonToolDispatcher) Dispatch(ctx context.Context, sessionID string, toolName string, args map[string]any) (any, error) {
	if err := d.AdmitPythonRun(ctx, sessionID); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, err := d.owner.ExecutePythonTool(ctx, sessionID, toolName, args)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, selftools.ErrPythonExecutionUnavailable
	}
	if result.IsError {
		return nil, errors.New(result.Output)
	}
	return map[string]any{"output": result.Output}, nil
}
