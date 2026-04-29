package mcp

import "context"

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

// callerProfileCtxKey carries the (workspace_id, agent_profile_id) pair of the
// session whose tool execution is in flight. Used by:
//
//   - MuxTransportAdapter.CallTool → muxproxy.WithCallerCtx (H1 mux trust gate)
//   - callExecuteTask → dispatch.SpawnRequest fields (H1 subagent trust gate)
//
// Stamped by the service layer in executeToolBatch alongside WithSessionID so
// all three ctx-stamping conventions stay co-located at the same call site.
// H1 CW-20260421-0014.
type callerProfileCtxKey struct{}

// callerProfile is the value type for callerProfileCtxKey.
type callerProfile struct {
	WorkspaceID    string
	AgentProfileID string
}

// WithCallerProfile returns a new context carrying the workspace and agent
// profile IDs of the calling session. Both values must be non-empty; partial
// identity returns ctx unchanged (same all-or-nothing contract as
// messaging.WithCaller).
func WithCallerProfile(ctx context.Context, workspaceID, agentProfileID string) context.Context {
	if workspaceID == "" || agentProfileID == "" {
		return ctx
	}
	return context.WithValue(ctx, callerProfileCtxKey{}, callerProfile{workspaceID, agentProfileID})
}

// CallerProfileFromContext extracts the (workspaceID, agentProfileID) stamped
// by WithCallerProfile. Returns ("", "") when none was set.
func CallerProfileFromContext(ctx context.Context) (workspaceID, agentProfileID string) {
	if ctx == nil {
		return "", ""
	}
	v, _ := ctx.Value(callerProfileCtxKey{}).(callerProfile)
	return v.WorkspaceID, v.AgentProfileID
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
// The set is stored as []string (small, immutable, cheap) rather than
// map[string]struct{} so the value is comparable for context.WithValue and
// callers can range over it for actionable error messages.
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
