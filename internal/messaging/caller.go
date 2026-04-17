// Package messaging — caller.go
//
// CallerIdentity formalizes the (session_id, agent_id) tuple already
// threaded through Inbox / Thread / Ack / Resolve. It is carried on
// the request context from the HTTP boundary (and can be carried by
// MCP/CLI in the same shape when those gain header-equivalent plumbing)
// so service-layer authz checks see a caller identity that is NOT
// derived from the same query/body fields that name the target.
//
// G-6.3 (Phase 3 S7 follow-up): the pre-existing comment on
// Service.UnreadCount noted "HTTP handler does not have caller
// identity plumbed yet." This file is that plumbing. The HTTP
// middleware sets the ctx value from X-Nanite-Caller-Session +
// X-Nanite-Caller-Agent headers; handlers read it with CallerFromCtx
// and pass it to the service as the caller-args the service already
// validates.
//
// MVP scope: nanite is a single-user local app. "Auth" is optional
// HTTP Basic Auth; this plumbing does NOT authenticate the caller
// against a password / token. It contractually carries caller
// identity so service-layer authz checks become effective over HTTP
// when headers are set, and falls back to the legacy trust-the-body
// behavior when they are not.
package messaging

import "context"

// CallerIdentity is the (session_id, agent_id) tuple that identifies
// the caller of a messaging operation. Empty strings mean "not set" —
// callers that want strict identity enforcement must populate both
// fields.
type CallerIdentity struct {
	SessionID string
	AgentID   string
}

// IsZero reports whether the identity is unset (both fields empty).
// Distinct from partial identity (only one field set), which IsComplete
// rejects. Preserved as "both empty" for any caller that cares about the
// strict all-empty state.
func (c CallerIdentity) IsZero() bool {
	return c.SessionID == "" && c.AgentID == ""
}

// IsComplete reports whether BOTH fields are populated. The HTTP header
// contract (X-Nanite-Caller-Session + X-Nanite-Caller-Agent) is
// all-or-nothing, and service-layer authz checks compare on (session,
// agent) pairs — a partial identity would flip authz semantics
// (fall-open vs enforce) or trigger 400s in resolveCaller. Handlers and
// WithCaller use this predicate to decide whether an identity is
// trustworthy enough to stamp on ctx.
func (c CallerIdentity) IsComplete() bool {
	return c.SessionID != "" && c.AgentID != ""
}

// callerCtxKey is the private context key used to carry
// CallerIdentity through middleware and handlers. Intentionally
// unexported so that only messaging and the HTTP boundary can
// read/write it.
type callerCtxKey struct{}

// WithCaller returns a new context carrying the given CallerIdentity.
// Only a COMPLETE identity (both SessionID and AgentID populated) is
// stamped; zero or partial identities return the original ctx unchanged
// and fall through to any parent ctx's identity. This matches the
// HTTP X-Nanite-Caller-Session/Agent header contract, which is
// all-or-nothing — a partial identity would otherwise flip service-layer
// authz checks from fall-open to enforce (or cause resolveCaller to 400)
// without a legitimate caller behind it.
func WithCaller(ctx context.Context, id CallerIdentity) context.Context {
	if !id.IsComplete() {
		return ctx
	}
	return context.WithValue(ctx, callerCtxKey{}, id)
}

// CallerFromCtx extracts the CallerIdentity carried on ctx. The
// second return is false when no identity was plumbed — callers can
// distinguish "identity known and incomplete" (never happens because
// WithCaller rejects zero/partial values) from "identity not set, fall
// back to legacy behavior."
func CallerFromCtx(ctx context.Context) (CallerIdentity, bool) {
	if ctx == nil {
		return CallerIdentity{}, false
	}
	v, ok := ctx.Value(callerCtxKey{}).(CallerIdentity)
	if !ok {
		return CallerIdentity{}, false
	}
	return v, true
}
