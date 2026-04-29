package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hollis-labs/nanite/internal/reminders"
	"github.com/hollis-labs/nanite/internal/store"
)

// callSetReminder handles nanite_set_reminder. Validates the trigger JSON,
// persists the reminder, and registers the creation turn with the session's
// reminder engine (when wired). Returns a confirmation with the new reminder ID.
//
// J11 (CW-20260426-0009).
// D1 (CW-20260428-0014): supports scope and project_id parameters.
func (st *SelfToolsTransport) callSetReminder(ctx context.Context, args map[string]any) (*ToolResult, error) {
	text := strArg(args, "text", "")
	if text == "" {
		return errorResult("text is required"), nil
	}

	triggerRaw, _ := args["trigger"]
	var triggerJSON string
	switch v := triggerRaw.(type) {
	case string:
		triggerJSON = v
	case map[string]any:
		b, err := json.Marshal(v)
		if err != nil {
			return errorResult("trigger: failed to marshal: " + err.Error()), nil
		}
		triggerJSON = string(b)
	default:
		return errorResult("trigger is required (object with type 'time' or 'turn_count')"), nil
	}

	// Validate trigger shape before persisting.
	if _, err := reminders.ParseTrigger(triggerJSON); err != nil {
		return errorResult("trigger: " + err.Error()), nil
	}

	sessionID := SessionIDFromContext(ctx)
	if sessionID == "" {
		return errorResult("set_reminder: no session in context"), nil
	}

	// D1 scope handling. Default to session scope. Project scope auto-fills
	// project_id from the current session when not supplied.
	scope := strArg(args, "scope", store.ReminderScopeSession)
	switch scope {
	case store.ReminderScopeTurn, store.ReminderScopeSession, store.ReminderScopeProject:
	default:
		return errorResult(fmt.Sprintf("scope must be one of turn, session, project (got %q)", scope)), nil
	}
	projectID := strArg(args, "project_id", "")
	if scope == store.ReminderScopeProject {
		if projectID == "" {
			projectID = st.resolveProjectIDFromSession(sessionID)
		}
		if projectID == "" {
			return errorResult("set_reminder: scope=project requires project_id (current session has no project)"), nil
		}
	}

	id := fmt.Sprintf("rem-%d", time.Now().UnixNano())
	r := store.Reminder{
		ID:          id,
		SessionID:   sessionID,
		Scope:       scope,
		ProjectID:   projectID,
		Text:        text,
		TriggerJSON: triggerJSON,
	}
	if err := st.Store.CreateReminder(r); err != nil {
		return errorResult(fmt.Sprintf("set_reminder: %v", err)), nil
	}

	// Register with the in-process reminder engine when wired.
	if st.ReminderEngine != nil {
		st.ReminderEngine.RegisterTurnCount(id, st.currentTurnCount(sessionID))
	}

	return textResult(fmt.Sprintf(`{"reminder_id":%q,"scope":%q,"status":"set","trigger":%s}`, id, scope, triggerJSON)), nil
}

// callPin handles nanite_pin. Persists pinned content to the DB for session
// and project scopes. Turn-scoped pins are acknowledged but not stored
// (they live in-memory in the engine and are cleared after the turn).
//
// J11 (CW-20260426-0009).
// D1 (CW-20260428-0014): the legacy `cross_session` scope is replaced by
// `project`, which scopes the pin to a specific project (cross-session
// continuity within a project).
func (st *SelfToolsTransport) callPin(ctx context.Context, args map[string]any) (*ToolResult, error) {
	content := strArg(args, "content", "")
	if content == "" {
		return errorResult("content is required"), nil
	}
	scope := strArg(args, "scope", store.PinScopeSession)
	switch scope {
	case store.PinScopeTurn, store.PinScopeSession, store.PinScopeProject:
	default:
		return errorResult(fmt.Sprintf("scope must be one of: turn, session, project (got %q)", scope)), nil
	}

	sessionID := SessionIDFromContext(ctx)
	if sessionID == "" {
		return errorResult("pin: no session in context"), nil
	}

	agentID := strArg(args, "agent_id", "")
	if agentID == "" {
		// Fall back to caller profile from context (set by H1 trust middleware).
		_, agentID = CallerProfileFromContext(ctx)
	}

	// Resolve project_id when scope=project. Auto-fill from the current
	// session's project when not supplied.
	projectID := strArg(args, "project_id", "")
	if scope == store.PinScopeProject {
		if projectID == "" {
			projectID = st.resolveProjectIDFromSession(sessionID)
		}
		if projectID == "" {
			return errorResult("pin: scope=project requires project_id (current session has no project)"), nil
		}
	}

	id := fmt.Sprintf("pin-%d", time.Now().UnixNano())

	// Turn-scoped pins are ephemeral — not persisted to DB.
	if scope == store.PinScopeTurn {
		return textResult(fmt.Sprintf(`{"pin_id":%q,"scope":"turn","status":"pinned","note":"turn-scoped pin is ephemeral and will be cleared after this turn"}`, id)), nil
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

	if err := st.Store.CreatePinnedContent(p); err != nil {
		return errorResult(fmt.Sprintf("pin: %v", err)), nil
	}

	return textResult(fmt.Sprintf(`{"pin_id":%q,"scope":%q,"status":"pinned"}`, id, scope)), nil
}

// callUnpin handles nanite_unpin. Deletes a pinned_content row by ID.
//
// J11 (CW-20260426-0009).
func (st *SelfToolsTransport) callUnpin(_ context.Context, args map[string]any) (*ToolResult, error) {
	id := strArg(args, "pin_id", "")
	if id == "" {
		return errorResult("pin_id is required"), nil
	}
	if err := st.Store.DeletePinnedContent(id); err != nil {
		return errorResult(fmt.Sprintf("unpin: %v", err)), nil
	}
	return textResult(fmt.Sprintf(`{"pin_id":%q,"status":"unpinned"}`, id)), nil
}

// currentTurnCount returns the current turn counter for a session from the
// reminder engine's perspective. Falls back to 0 when the engine doesn't track
// the session yet. This is a best-effort registration — the engine will fall
// back gracefully when the counter is missing.
func (st *SelfToolsTransport) currentTurnCount(_ string) int {
	// The turn counter is maintained by the service layer (chat_loop_state).
	// The MCP transport does not have direct access to it; the engine accepts
	// 0 as "unknown creation turn" and will use session start as fallback.
	// A future integration point is to pass the turn counter through the ctx.
	return 0
}
