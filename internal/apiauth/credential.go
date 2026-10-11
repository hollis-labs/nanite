// Package apiauth is Nanite's HTTP API authentication: scoped, revocable,
// hash-only bearer credentials in their own database, UI session cookies,
// one-time login links, and the middleware that enforces them on every route
// (CW-20261011-0036). Nanite has no unauthenticated mode except an explicit,
// loud, loopback-only development flag.
package apiauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Kind says who holds a credential.
type Kind string

const (
	// KindOperator is the human owner: every scope, no expiry until rotated.
	KindOperator Kind = "operator"
	// KindClient is an embedded client such as the Tangent backend: explicit
	// scopes (never admin), finite lifetime.
	KindClient Kind = "client"
	// KindAgent is minted by the host for one agent session: the tools scope
	// only, bound to that session, short sliding lifetime.
	KindAgent Kind = "agent"
)

// Scope is one class of permission. Route classification lives in routes.go.
type Scope string

const (
	ScopeRead     Scope = "read"
	ScopeOperate  Scope = "operate"
	ScopeTools    Scope = "tools"
	ScopeMaintain Scope = "maintain"
	ScopeAdmin    Scope = "admin"
)

var allScopes = []Scope{ScopeRead, ScopeOperate, ScopeTools, ScopeMaintain, ScopeAdmin}

// AllScopes returns every scope, the set an operator holds.
func AllScopes() []Scope { return append([]Scope(nil), allScopes...) }

// ParseScope validates one scope name.
func ParseScope(name string) (Scope, error) {
	for _, s := range allScopes {
		if string(s) == name {
			return s, nil
		}
	}
	return "", fmt.Errorf("unknown scope %q (want one of read, operate, tools, maintain, admin)", name)
}

// ParseScopes validates and de-duplicates a scope list.
func ParseScopes(names []string) ([]Scope, error) {
	seen := map[Scope]bool{}
	var out []Scope
	for _, n := range names {
		s, err := ParseScope(strings.TrimSpace(n))
		if err != nil {
			return nil, err
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out, nil
}

// Credential is a stored credential. The secret is never stored or returned
// after creation.
type Credential struct {
	ID             string
	Kind           Kind
	Label          string
	Scopes         []Scope
	BoundSessionID string
	CreatedAt      time.Time
	ExpiresAt      time.Time // zero means none
	LastUsedAt     time.Time
	RevokedAt      time.Time
	CreatedBy      string
}

// Has reports whether the credential carries a scope. An operator carries all.
func (c Credential) Has(s Scope) bool {
	if c.Kind == KindOperator {
		return true
	}
	for _, have := range c.Scopes {
		if have == s {
			return true
		}
	}
	return false
}

// Active reports whether the credential is usable at now.
func (c Credential) Active(now time.Time) bool {
	if !c.RevokedAt.IsZero() {
		return false
	}
	return c.ExpiresAt.IsZero() || now.Before(c.ExpiresAt)
}

const (
	secretPrefix = "nan_"
	secretBytes  = 32
)

var errMalformedSecret = errors.New("malformed credential")

// newSecret returns a fresh credential secret of the form nan_<kind>_<43
// base64url characters of 256 random bits>, and its stored hash.
func newSecret(kind Kind) (secret, hash string, err error) {
	raw := make([]byte, secretBytes)
	if _, err = rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate credential: %w", err)
	}
	secret = secretPrefix + string(kind) + "_" + base64.RawURLEncoding.EncodeToString(raw)
	return secret, hashSecret(secret), nil
}

// hashSecret is the only form of a secret that is ever stored.
func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// kindOfSecret validates the shape of a presented secret before any lookup.
func kindOfSecret(secret string) (Kind, error) {
	rest, ok := strings.CutPrefix(secret, secretPrefix)
	if !ok {
		return "", errMalformedSecret
	}
	kindName, body, ok := strings.Cut(rest, "_")
	if !ok || len(body) != base64.RawURLEncoding.EncodedLen(secretBytes) {
		return "", errMalformedSecret
	}
	switch k := Kind(kindName); k {
	case KindOperator, KindClient, KindAgent:
		return k, nil
	}
	return "", errMalformedSecret
}

// opaqueToken is a random token for cookies and one-time links.
func opaqueToken() (token, hash string, err error) {
	raw := make([]byte, secretBytes)
	if _, err = rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, hashSecret(token), nil
}

// timeLayout is the one stored timestamp format: UTC with nine fractional
// digits always present, so text comparison is chronological (see the
// message-order fix, CW-20261011-0041).
const timeLayout = "2006-01-02T15:04:05.000000000Z"

func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeLayout)
}

func parseTS(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
