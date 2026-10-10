// Package mcpbridge contains candidate host/proxy machinery. It registers no
// public endpoint and supplies no trusted-host enrollment or bootstrap policy.
package mcpbridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	credentialhost "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability/host"
)

var (
	ErrUnauthenticated      = errors.New("a current scoped MCP proxy credential is required")
	ErrAuthorityUnavailable = errors.New("trusted host binding verification is unavailable")
	ErrCapacity             = errors.New("MCP proxy capacity exhausted")
)

// VerifiedCaller comes only from the application's trusted verification port.
// Request fields, loopback presence and profile names never populate it.
type VerifiedCaller struct{ ActorID, SessionID string }

// VerifiedBinding contains proof resolved under current reviewed host policy.
// Claims must identify an MCPProxyClient and contain actual reviewed grants;
// this package never derives claims or activates an owner from presented proof.
type VerifiedBinding struct {
	Caller VerifiedCaller
	Claims credentialhost.CredentialClaims
}

type BindingVerifier interface {
	VerifyBinding(context.Context, []byte) (VerifiedBinding, error)
}

type authorityKey struct{}

// RevalidateAuthority supplies the execution owner with copied reviewed
// credential scopes after checking the live lease and trusted host binding.
// A backend must serialize this check with its actual mutation/receipt guard;
// a returned snapshot alone does not protect a later commit.
func RevalidateAuthority(ctx context.Context) (credentialhost.CredentialLease, error) {
	if err := ctx.Err(); err != nil {
		return credentialhost.CredentialLease{}, err
	}
	check, ok := ctx.Value(authorityKey{}).(func() (credentialhost.CredentialLease, error))
	if !ok {
		return credentialhost.CredentialLease{}, ErrAuthorityUnavailable
	}
	return check()
}

func sameClaims(a, b credentialhost.CredentialClaims) bool {
	left, e1 := json.Marshal(a)
	right, e2 := json.Marshal(b)
	return e1 == nil && e2 == nil && bytes.Equal(left, right)
}

// CredentialLimits must be selected explicitly by the integrating host.
type CredentialLimits struct {
	Capacity        int
	MaxTTL          time.Duration
	MaxBindingBytes int
}

type credential struct {
	verified VerifiedBinding
	binding  []byte
	lease    credentialhost.CredentialLease
}

// Credentials is a bounded proof/caller sidecar to the released SDK store.
// The SDK owns token hashing, expiry, active owner fencing and revocation.
type Credentials struct {
	mu       sync.Mutex
	verifier BindingVerifier
	store    *credentialhost.CredentialStore
	limits   CredentialLimits
	records  map[[32]byte]*credential
	closed   bool
}

func NewCredentials(verifier BindingVerifier, store *credentialhost.CredentialStore, limits CredentialLimits) (*Credentials, error) {
	if limits.Capacity <= 0 || limits.MaxTTL <= 0 || limits.MaxBindingBytes <= 0 {
		return nil, errors.New("explicit positive MCP proxy credential limits are required")
	}
	return &Credentials{verifier: verifier, store: store, limits: limits, records: make(map[[32]byte]*credential)}, nil
}

// IssuedCredential reveals a proxy bearer once. Formatting and logging redact it.
type IssuedCredential struct {
	LeaseID   string
	ExpiresAt time.Time
	reveal    func() string
}

func (c IssuedCredential) Reveal() (string, error) {
	if c.reveal != nil {
		if secret := c.reveal(); secret != "" {
			return secret, nil
		}
	}
	return "", ErrUnauthenticated
}
func (IssuedCredential) String() string               { return "[credential redacted]" }
func (IssuedCredential) GoString() string             { return "[credential redacted]" }
func (IssuedCredential) Format(s fmt.State, _ rune)   { _, _ = fmt.Fprint(s, "[credential redacted]") }
func (IssuedCredential) MarshalJSON() ([]byte, error) { return []byte(`"[credential redacted]"`), nil }
func (IssuedCredential) MarshalText() ([]byte, error) { return []byte("[credential redacted]"), nil }
func (IssuedCredential) LogValue() slog.Value         { return slog.StringValue("[credential redacted]") }

// Issue is an in-process host port. There is no public credential mint handler.
func (c *Credentials) Issue(ctx context.Context, binding []byte, ttl time.Duration) (IssuedCredential, error) {
	if c == nil || c.verifier == nil || c.store == nil {
		return IssuedCredential{}, ErrAuthorityUnavailable
	}
	if ttl <= 0 || ttl > c.limits.MaxTTL || len(binding) == 0 || len(binding) > c.limits.MaxBindingBytes {
		return IssuedCredential{}, ErrUnauthenticated
	}
	proof := append([]byte(nil), binding...)
	verified, err := c.verifier.VerifyBinding(ctx, proof)
	if err != nil || verified.Caller.ActorID == "" || verified.Caller.SessionID == "" || verified.Claims.Subject.Kind != credentialhost.MCPProxyClient {
		return IssuedCredential{}, ErrUnauthenticated
	}
	if err := ctx.Err(); err != nil {
		return IssuedCredential{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return IssuedCredential{}, ErrAuthorityUnavailable
	}
	for key, record := range c.records {
		if record.lease.Context.Err() != nil {
			delete(c.records, key)
		}
	}
	if len(c.records) >= c.limits.Capacity {
		return IssuedCredential{}, ErrCapacity
	}
	issued, err := c.store.Issue(verified.Claims, ttl)
	if err != nil {
		return IssuedCredential{}, ErrUnauthenticated
	}
	secret, err := issued.Reveal()
	if err != nil {
		c.store.RevokeLease(issued.LeaseID)
		return IssuedCredential{}, ErrUnauthenticated
	}
	lease, err := c.store.Verify(secret, verified.Claims.Subject, verified.Claims.Owner, verified.Claims.Audience)
	if err != nil {
		c.store.RevokeLease(issued.LeaseID)
		return IssuedCredential{}, ErrUnauthenticated
	}
	// Store SDK's copied claims, never mutable maps belonging to the verifier.
	verified.Claims = lease.Claims
	c.records[sha256.Sum256([]byte(secret))] = &credential{verified: verified, binding: proof, lease: lease}
	var revealMu sync.Mutex
	return IssuedCredential{LeaseID: issued.LeaseID, ExpiresAt: issued.ExpiresAt, reveal: func() string { revealMu.Lock(); defer revealMu.Unlock(); result := secret; secret = ""; return result }}, nil
}

// Acquire verifies both the SDK lease and current host proof on every request.
// Request cancellation is joined with credential expiry/revocation.
func (c *Credentials) Acquire(ctx context.Context, token string) (VerifiedCaller, context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return VerifiedCaller{}, nil, nil, err
	}
	if c == nil || c.verifier == nil || c.store == nil {
		return VerifiedCaller{}, nil, nil, ErrAuthorityUnavailable
	}
	if len(token) != 43 {
		return VerifiedCaller{}, nil, nil, ErrUnauthenticated
	}
	digest := sha256.Sum256([]byte(token))
	c.mu.Lock()
	record := c.records[digest]
	c.mu.Unlock()
	if record == nil {
		return VerifiedCaller{}, nil, nil, ErrUnauthenticated
	}
	claims := record.verified.Claims
	lease, err := c.store.Verify(token, claims.Subject, claims.Owner, claims.Audience)
	if err != nil {
		c.Revoke(token)
		return VerifiedCaller{}, nil, nil, ErrUnauthenticated
	}
	verified, err := c.verifier.VerifyBinding(ctx, append([]byte(nil), record.binding...))
	// VerifyBinding owns current reviewed policy. Compare authority snapshots in
	// addition to caller identity so a changed proof cannot reuse an older lease.
	if err != nil || verified.Caller != record.verified.Caller || !sameClaims(verified.Claims, lease.Claims) {
		c.Revoke(token)
		return VerifiedCaller{}, nil, nil, ErrUnauthenticated
	}
	c.mu.Lock()
	current := !c.closed && c.records[digest] == record
	c.mu.Unlock()
	if !current {
		return VerifiedCaller{}, nil, nil, ErrUnauthenticated
	}
	// Reverify after the trusted host callback: revocation may have won while it ran.
	lease, err = c.store.Verify(token, claims.Subject, claims.Owner, claims.Audience)
	if err != nil {
		c.Revoke(token)
		return VerifiedCaller{}, nil, nil, ErrUnauthenticated
	}
	permit, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(lease.Context, cancel)
	if lease.Context.Err() != nil {
		stop()
		cancel()
		return VerifiedCaller{}, nil, nil, ErrUnauthenticated
	}
	permit = context.WithValue(permit, authorityKey{}, func() (credentialhost.CredentialLease, error) {
		_, current, done, err := c.Acquire(ctx, token)
		if err != nil {
			return credentialhost.CredentialLease{}, err
		}
		defer done()
		if err := current.Err(); err != nil {
			return credentialhost.CredentialLease{}, err
		}
		snapshot, err := c.store.Verify(token, claims.Subject, claims.Owner, claims.Audience)
		if err != nil {
			return credentialhost.CredentialLease{}, ErrUnauthenticated
		}
		return snapshot, nil
	})
	return verified.Caller, permit, func() { stop(); cancel() }, nil
}

func (c *Credentials) Revoke(token string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.records, sha256.Sum256([]byte(token)))
	c.mu.Unlock()
	if c.store != nil {
		c.store.Revoke(token)
	}
}
func (c *Credentials) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for key, record := range c.records {
		if c.store != nil {
			c.store.RevokeLease(record.lease.LeaseID)
		}
		delete(c.records, key)
	}
}
