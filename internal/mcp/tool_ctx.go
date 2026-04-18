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
