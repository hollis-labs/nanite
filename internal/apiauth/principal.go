package apiauth

import "context"

// Principal is the authenticated caller attached to a request context.
type Principal struct {
	CredentialID   string
	Kind           Kind
	Label          string
	Scopes         []Scope
	BoundSessionID string // agent credentials only
	Cookie         bool   // authenticated by a UI session cookie
	UISessionID    string
	CSRF           string
}

// Has reports whether the principal carries a scope. An operator carries all.
func (p Principal) Has(s Scope) bool {
	if p.Kind == KindOperator {
		return true
	}
	for _, have := range p.Scopes {
		if have == s {
			return true
		}
	}
	return false
}

type principalKey struct{}

// WithPrincipal attaches a principal to a context.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// FromContext returns the request's authenticated principal, if any.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// Actor is a stable audit label for the principal: never a secret.
func (p Principal) Actor() string {
	if p.Label != "" {
		return string(p.Kind) + ":" + p.Label
	}
	return string(p.Kind) + ":" + p.CredentialID
}

type auditKey struct{}

// AuditFunc records one audit row on behalf of the request it came from.
type AuditFunc func(method, route, outcome string)

// WithAudit attaches the request's audit writer to a context, so a handler
// that fans out into other routes in-process (the admin MCP endpoint) can
// record which one it ran; the middleware only sees the outer route.
func WithAudit(ctx context.Context, f AuditFunc) context.Context {
	return context.WithValue(ctx, auditKey{}, f)
}

// AuditFromContext returns the request's audit writer, or a no-op.
func AuditFromContext(ctx context.Context) AuditFunc {
	if f, ok := ctx.Value(auditKey{}).(AuditFunc); ok {
		return f
	}
	return func(string, string, string) {}
}
