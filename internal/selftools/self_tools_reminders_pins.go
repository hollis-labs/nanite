package selftools

import (
	"context"
	"fmt"
	"time"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
)

// callPin handles context_pin. Persists pinned content to the DB for session
// and project scopes. Turn-scoped pins are acknowledged but not stored
// (they live in-memory in the engine and are cleared after the turn).
//
// J11 (CW-20260426-0009).
// D1 (CW-20260428-0014): the legacy `cross_session` scope is replaced by
// `project`, which scopes the pin to a specific project (cross-session
// continuity within a project).
func (st *SelfToolsTransport) callPin(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	content := strArg(args, "content", "")
	if content == "" {
		return mcp.ErrorResult("content is required"), nil
	}
	scope := strArg(args, "scope", store.PinScopeSession)
	switch scope {
	case store.PinScopeTurn, store.PinScopeSession, store.PinScopeProject:
	default:
		return mcp.ErrorResult(fmt.Sprintf("scope must be one of: turn, session, project (got %q)", scope)), nil
	}

	sessionID := mcp.SessionIDFromContext(ctx)
	if sessionID == "" {
		return mcp.ErrorResult("pin: no session in context"), nil
	}

	agentID := strArg(args, "agent_id", "")
	if agentID == "" {
		// Fall back to caller profile from context (set by H1 trust middleware).
		agentID = mcp.CallerProfileFromContext(ctx)
	}

	// Resolve project_id when scope=project. Auto-fill from the current
	// session's project when not supplied.
	projectID := strArg(args, "project_id", "")
	if scope == store.PinScopeProject {
		if projectID == "" {
			projectID = resolveSelfToolProjectID(st.Reads.Sessions, sessionID)
		}
		if projectID == "" {
			return mcp.ErrorResult("pin: scope=project requires project_id (current session has no project)"), nil
		}
	}

	id := fmt.Sprintf("pin-%d", time.Now().UnixNano())

	// Turn-scoped pins are ephemeral — not persisted to DB.
	if scope == store.PinScopeTurn {
		return mcp.TextResult(fmt.Sprintf(`{"pin_id":%q,"scope":"turn","status":"pinned","note":"turn-scoped pin is ephemeral and will be cleared after this turn"}`, id)), nil
	}

	p := store.PinnedContent{
		ID:        id,
		Scope:     scope,
		ProjectID: projectID,
		Content:   content,
		AgentID:   agentID,
	}
	// Always retain the originating session for provenance, even when the
	// pin is project-scoped — UI displays it under "by <session>".
	p.SessionID = &sessionID

	if err := st.Writes.Pins.Create(ctx, p); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("pin: %v", err)), nil
	}

	return mcp.TextResult(fmt.Sprintf(`{"pin_id":%q,"scope":%q,"status":"pinned"}`, id, scope)), nil
}

// callUnpin handles context_unpin. Deletes a pinned_content row by ID.
//
// J11 (CW-20260426-0009).
func (st *SelfToolsTransport) callUnpin(_ context.Context, args map[string]any) (*mcp.ToolResult, error) {
	id := strArg(args, "pin_id", "")
	if id == "" {
		return mcp.ErrorResult("pin_id is required"), nil
	}
	if err := st.Writes.Pins.Delete(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, id); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("unpin: %v", err)), nil
	}
	return mcp.TextResult(fmt.Sprintf(`{"pin_id":%q,"status":"unpinned"}`, id)), nil
}

func resolveSelfToolProjectID(reader SessionReader, sessionID string) string {
	if reader == nil || sessionID == "" {
		return ""
	}
	sess, err := reader.Get(context.TODO(), sessionID)
	if err != nil || sess == nil {
		return ""
	}
	return sess.ProjectID
}
