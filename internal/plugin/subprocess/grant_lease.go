package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability"
)

var ErrGrantLeaseEnded = errors.New("plugin grant lease expired or revoked")
var ErrGrantRenewalUnavailable = errors.New("plugin does not support acknowledged grant renewal")

// GrantLease is host execution state, not authority obtained from Init metadata.
// Renewal requires a fresh owner-policy decision and preserves the exact grant
// identities, incarnation, scopes and revision. Ended leases cannot be revived.
type GrantLease struct {
	mu         sync.Mutex
	grants     capability.GrantSet
	owner      capability.RuntimeIdentity
	ctx        context.Context
	parent     context.Context
	cancel     context.CancelFunc
	timer      *time.Timer
	expires    time.Time
	ended      bool
	onEnd      func()
	stopParent func() bool
}

func NewGrantLease(parent context.Context, grants capability.GrantSet, owner capability.RuntimeIdentity, onEnd func()) (*GrantLease, error) {
	if err := grants.ValidateForRuntime(owner); err != nil {
		return nil, err
	}
	lease := &GrantLease{onEnd: onEnd, parent: parent, owner: owner}
	if err := lease.replace(parent, grants); err != nil {
		return nil, err
	}
	lease.mu.Lock()
	lease.stopParent = context.AfterFunc(parent, lease.Revoke)
	lease.mu.Unlock()
	return lease, nil
}

func copyGrants(grants capability.GrantSet) capability.GrantSet {
	copied := append(capability.GrantSet{}, grants...)
	for i := range copied {
		copied[i].Scope = append(json.RawMessage(nil), copied[i].Scope...)
	}
	return copied
}

func (l *GrantLease) replace(parent context.Context, grants capability.GrantSet) error {
	var expires time.Time
	for _, grant := range grants {
		end, err := time.Parse(time.RFC3339Nano, grant.ExpiresAt)
		if err != nil || !end.After(time.Now()) {
			return ErrGrantLeaseEnded
		}
		if expires.IsZero() || end.Before(expires) {
			expires = end
		}
	}
	if err := parent.Err(); err != nil {
		return err
	}
	if expires.IsZero() {
		l.ctx, l.cancel = context.WithCancel(parent)
	} else {
		// Forward RPC budgets must expose the lease deadline, not just rely
		// on a local timer canceling an otherwise longer call.
		l.ctx, l.cancel = context.WithDeadline(parent, expires)
	}
	l.grants = copyGrants(grants)
	l.expires = expires
	if !expires.IsZero() {
		l.timer = time.AfterFunc(time.Until(expires), l.expire)
	}
	return nil
}

func (l *GrantLease) Revoke() {
	if l == nil {
		return
	}
	l.mu.Lock()
	if l.ended {
		l.mu.Unlock()
		return
	}
	l.ended = true
	if l.stopParent != nil {
		l.stopParent()
	}
	if l.timer != nil {
		l.timer.Stop()
	}
	l.cancel()
	callback := l.onEnd
	l.mu.Unlock()
	if callback != nil {
		callback()
	}
}

// Acquire ties each dispatch, including in-flight work, to the current lease.
func (l *GrantLease) Acquire(ctx context.Context) (context.Context, func(), error) {
	if l == nil {
		return nil, nil, ErrGrantLeaseEnded
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ended || l.ctx.Err() != nil || (!l.expires.IsZero() && !time.Now().Before(l.expires)) {
		return nil, nil, ErrGrantLeaseEnded
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	base := grantPermitContext{Context: l.ctx, values: context.WithoutCancel(ctx)}
	var permit context.Context
	var cancel context.CancelFunc
	if deadline, ok := ctx.Deadline(); ok {
		permit, cancel = context.WithDeadline(base, deadline)
	} else {
		permit, cancel = context.WithCancel(base)
	}
	stop := context.AfterFunc(ctx, cancel)
	return permit, func() { stop(); cancel() }, nil
}

// Renew does not trust plugin-requested scope or lifetime. The host revalidates
// the same policy before providing next. It cancels old permits conservatively.
func (l *GrantLease) Renew(parent context.Context, next capability.GrantSet) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ended || l.ctx.Err() != nil || (!l.expires.IsZero() && !time.Now().Before(l.expires)) {
		return ErrGrantLeaseEnded
	}
	if err := l.grants.ValidateRenewal(next, l.owner, time.Now()); err != nil {
		return err
	}
	if err := parent.Err(); err != nil {
		return err
	}
	if l.timer != nil {
		l.timer.Stop()
	}
	l.cancel()
	return l.replace(l.parent, next)
}

func (l *GrantLease) Grants() capability.GrantSet {
	l.mu.Lock()
	defer l.mu.Unlock()
	return copyGrants(l.grants)
}

func (l *GrantLease) Context() (context.Context, error) {
	if l == nil {
		return nil, ErrGrantLeaseEnded
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ended || l.ctx.Err() != nil || (!l.expires.IsZero() && !time.Now().Before(l.expires)) {
		return nil, ErrGrantLeaseEnded
	}
	return l.ctx, nil
}

type grantPermitContext struct {
	context.Context
	values context.Context
}

func (c grantPermitContext) Value(key any) any {
	// WithoutCancel preserves caller values but withholds its internal
	// cancellation owner, so Go links this permit to the lease context.
	if value := c.values.Value(key); value != nil {
		return value
	}
	return c.Context.Value(key)
}
func (l *GrantLease) expire() {
	l.mu.Lock()
	if l.ended || l.expires.IsZero() || time.Now().Before(l.expires) {
		l.mu.Unlock()
		return
	}
	l.ended = true
	if l.stopParent != nil {
		l.stopParent()
	}
	l.cancel()
	callback := l.onEnd
	l.mu.Unlock()
	if callback != nil {
		callback()
	}
}
