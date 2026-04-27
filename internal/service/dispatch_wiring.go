package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// SubagentSpawner is the narrow surface dispatchSpawner needs from
// internal/subagent.Service. *subagent.Service satisfies it; tests can
// swap a fake.
type SubagentSpawner interface {
	Spawn(ctx context.Context, req subagent.SpawnRequest) (string, error)
	Status(ctx context.Context, runID string) (*subagent.Run, error)
}

// MessageReader is the narrow surface dispatchSpawner uses to recover
// the assistant text from a completed sync subagent run. *store.Store
// satisfies it.
type MessageReader interface {
	ListMessages(sessionID string, limit int) ([]store.Message, error)
}

// dispatchSpawner adapts the subagent.Service Spawn flow to the
// dispatch.Spawner interface. ExecuteTask hands a SpawnRequest to this
// adapter; the adapter translates to subagent.SpawnRequest, blocks on
// the run's terminal status (sync semantics), and returns the captured
// SpawnResult.
//
// The subagent package writes the worker's emitted envelope JSON to the
// Run row (ResultJSON) and posts the prose summary to the parent
// session's messaging channel. dispatchSpawner reads ResultJSON
// directly and recovers the prose by scanning the child session's
// assistant messages — same pattern as self_tools_transport's
// syncSubagentSummary helper.
type dispatchSpawner struct {
	svc      SubagentSpawner
	messages MessageReader
}

// NewDispatchSpawner wraps a subagent.Service in the dispatch.Spawner
// shape. messages is optional: when nil, prose recovery is skipped and
// the Summary field on SpawnResult is left empty (DefaultEnvelopeWrapper
// will then synthesize an envelope from the empty summary, which is
// degraded but not broken).
func NewDispatchSpawner(svc SubagentSpawner, messages MessageReader) dispatch.Spawner {
	return &dispatchSpawner{svc: svc, messages: messages}
}

// Spawn implements dispatch.Spawner. The subagent service's ModeSync
// already blocks until the runner returns and persists the terminal
// status, so this adapter submits the request, reads the final row,
// and recovers the assistant text from the child session.
func (d *dispatchSpawner) Spawn(ctx context.Context, req dispatch.SpawnRequest) (*dispatch.SpawnResult, error) {
	if d == nil || d.svc == nil {
		return nil, fmt.Errorf("dispatch: subagent service not configured")
	}

	// Force sync mode at the dispatch boundary — ExecuteTask must
	// capture the result. The dispatch primitive itself already coerces
	// async to sync; this is defense-in-depth.
	mode := req.Mode
	if mode == "" || mode == subagent.ModeAsync || mode == subagent.ModeAPI {
		mode = subagent.ModeSync
	}

	runID, err := d.svc.Spawn(ctx, subagent.SpawnRequest{
		ParentSessionID: req.ParentSessionID,
		ParentAgentID:   req.ParentAgentID,
		Role:            req.Role,
		Prompt:          req.Prompt,
		Mode:            mode,
		Provider:        req.Provider,
		TimeoutSeconds:  req.TimeoutSeconds,
	})
	if err != nil {
		return nil, fmt.Errorf("dispatch spawn: %w", err)
	}

	run, err := d.svc.Status(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("dispatch read run: %w", err)
	}
	if run == nil {
		return nil, fmt.Errorf("dispatch: subagent run %q not found", runID)
	}

	switch run.Status {
	case subagent.StatusCompleted:
		return &dispatch.SpawnResult{
			Summary:      d.recoverSummary(run),
			EnvelopeJSON: run.ResultJSON,
		}, nil
	case subagent.StatusFailed:
		return nil, fmt.Errorf("dispatch: subagent failed: %s", run.Error)
	case subagent.StatusCancelled:
		return nil, fmt.Errorf("dispatch: subagent cancelled")
	case subagent.StatusRejected:
		return nil, fmt.Errorf("dispatch: subagent rejected: %s", run.RejectionReason)
	default:
		// Defensive: should not reach here under sync mode, but if it
		// does (approval-gated or wedged race) surface the state
		// instead of hanging.
		return nil, fmt.Errorf("dispatch: subagent run in non-terminal state %q", run.Status)
	}
}

// recoverSummary scans the child session's assistant messages to find
// the most recent text response. Mirrors self_tools_transport's
// syncSubagentSummary pattern. Returns empty string when the messages
// reader is nil or no assistant text is found.
func (d *dispatchSpawner) recoverSummary(run *subagent.Run) string {
	if d.messages == nil || run == nil || run.ChildSessionID == "" {
		return ""
	}
	msgs, err := d.messages.ListMessages(run.ChildSessionID, 20)
	if err != nil {
		return ""
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "assistant" || msgs[i].Content == "" {
			continue
		}
		if text := extractAssistantText(msgs[i].Content); text != "" {
			return text
		}
		return msgs[i].Content
	}
	return ""
}

// extractAssistantText pulls the text field from a stored assistant
// message JSON payload. Mirrors the helper in self_tools_transport but
// stays local so dispatch_wiring does not import internal/mcp.
func extractAssistantText(content string) string {
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return ""
	}
	return payload.Text
}
