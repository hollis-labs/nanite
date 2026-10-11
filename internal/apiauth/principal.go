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
