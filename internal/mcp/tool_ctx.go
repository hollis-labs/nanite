package mcp

import (
	"context"
)

// sessionCtxKey is the key used to attach the current chat session ID to
// the context threaded through tool execution. MCP self-tool handlers
// read this when an arg like `scope_id` would otherwise be empty.
// CW-20260418 (c7 scope_id bug fix).
//
// The helpers live in the mcp package (not service) because service already
// imports mcp — putting the key in mcp avoids a circular import while still
// letting the service layer stamp the ctx via mcp.WithSessionID before
// handing off to tool execution.
type sessionCtxKey struct{}

// WithSessionID returns a new context carrying the given session ID.
// A zero session ID returns ctx unchanged so callers can pass through
// unconditionally.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionCtxKey{}, sessionID)
}

// SessionIDFromContext returns the session ID attached via WithSessionID,
// or "" if none. Used by MCP self-tool handlers (todo/plan create + list) to
// auto-fill scope_id when scope="session" and the arg is empty, fixing
// records that otherwise land in the DB with empty scope_id.
func SessionIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(sessionCtxKey{}).(string)
	return v
}

// callerProfileCtxKey carries the agent_profile_id of the session whose tool
// execution is in flight. Used by:
//
//   - callExecuteTask → dispatch.SpawnRequest fields (H1 subagent trust gate)
//
// Stamped by the service layer in executeToolBatch alongside WithSessionID so
// both ctx-stamping conventions stay co-located at the same call site.
// H1 CW-20260421-0014.
//
// Phase 0 item 20 (retire workspaces,
// TASKS/phase-0/20-retire-workspaces-and-instance-mechanism.md): this used
// to carry a (workspace_id, agent_profile_id) pair. workspace_id is gone —
// it only ever fed the now-retired workspace_role_trust override lookup and
// the cross-workspace chat-search gate (both removed).
type callerProfileCtxKey struct{}

// WithCallerProfile returns a new context carrying the agent profile ID of
// the calling session. An empty value returns ctx unchanged.
func WithCallerProfile(ctx context.Context, agentProfileID string) context.Context {
	if agentProfileID == "" {
		return ctx
	}
	return context.WithValue(ctx, callerProfileCtxKey{}, agentProfileID)
}

// CallerProfileFromContext extracts the agentProfileID stamped by
// WithCallerProfile. Returns "" when none was set.
func CallerProfileFromContext(ctx context.Context) (agentProfileID string) {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(callerProfileCtxKey{}).(string)
	return v
}

// turnToolUseIDsCtxKey carries the set of tool_use_id strings observed during
// the current chat-generate loop (the "turn", from the agent's perspective).
// Stamped by service.executeToolBatch before each tool dispatch so handlers
// can validate agent-supplied tool_use_id references against actual calls.
//
// Primary consumer: self_tools_transport.callShowCard, which validates that
// every entry in the `sources` array of a report-card / document-viewer cites
// a real tool_use_id from this turn rather than a fabricated string. A nil
// or absent value MUST disable the check (test paths and subagent paths run
// with bare contexts and should not trip the gate).
//
// CW-20260429-0024.
type turnToolUseIDsCtxKey struct{}

// turnToolNamesCtxKey carries the set of tool names called during the current
// turn. Sibling to turnToolUseIDsCtxKey. Originally introduced for the
// describe-required gate (CW-20260429-0025); the gate was removed in Phase A
// of the architectural rebalancing (see docs/architecture/agent-context-architecture.md),
// but the per-turn name set is preserved as plumbing for any future per-turn
// observability or trust check that wants to read which tools the turn actually
// invoked. Independent of tool_use_ids because turn-tool-name checks care
// about the verb, not the call ID.
type turnToolNamesCtxKey struct{}

// WithTurnToolUseIDs returns a new context carrying the given tool_use_id
// set as the "turn so far" for grounding checks. Empty / nil input returns
// ctx unchanged so callers can pass through unconditionally; downstream
// readers treat nil as "not stamped — skip the check".
//
// The set is stored as []string rather than map[string]struct{} because
// it is small (single-digit IDs in practice), cheap to defensively copy,
// preserves insertion order for actionable error messages, and lets
// callers range over it without an extra allocation.
func WithTurnToolUseIDs(ctx context.Context, ids []string) context.Context {
	if len(ids) == 0 {
		return ctx
	}
	// Defensive copy — callers (executeToolBatch) hand us a slice they may
	// continue to mutate as the loop accumulates more refs.
	cp := make([]string, len(ids))
	copy(cp, ids)
	return context.WithValue(ctx, turnToolUseIDsCtxKey{}, cp)
}

// TurnToolUseIDsFromContext returns the tool_use_id set stamped by
// WithTurnToolUseIDs, or nil if none was stamped. Callers MUST treat nil as
// "no enforcement" (see WithTurnToolUseIDs doc comment).
func TurnToolUseIDsFromContext(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(turnToolUseIDsCtxKey{}).([]string)
	return v
}

// WithTurnToolNames returns a new context carrying the set of tool names
// called this turn. Sibling to WithTurnToolUseIDs; same nil-input semantics.
// Originally introduced for the describe-required gate (CW-20260429-0025);
// retained as observability plumbing after the gate was removed in Phase A
// of the architectural rebalancing.
func WithTurnToolNames(ctx context.Context, names []string) context.Context {
	if len(names) == 0 {
		return ctx
	}
	cp := make([]string, len(names))
	copy(cp, names)
	return context.WithValue(ctx, turnToolNamesCtxKey{}, cp)
}

// TurnToolNamesFromContext returns the tool-name set stamped by
// WithTurnToolNames, or nil if none was stamped.
func TurnToolNamesFromContext(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(turnToolNamesCtxKey{}).([]string)
	return v
}
