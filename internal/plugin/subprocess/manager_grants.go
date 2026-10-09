package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability"
	sdksub "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
)

// RenewGrants serializes one update for an exact live connection. The host
// retains its old lease until the child acknowledges and current policy is
// revalidated. A possibly applied update is never retried or rolled back.
func (m *Manager) RenewGrants(ctx context.Context, proposed capability.GrantSet) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	next := proposed.Clone()
	m.mu.Lock()
	lease, conn, owner := m.grantLease, m.connection, m.incarnation
	if m.state != StateRunning || lease == nil || conn == nil {
		m.mu.Unlock()
		return ErrGrantLeaseEnded
	}
	if !m.renewalSupported {
		m.mu.Unlock()
		return ErrGrantRenewalUnavailable
	}
	if m.renewing != nil {
		m.mu.Unlock()
		return fmt.Errorf("grant renewal already in progress")
	}
	if m.renewalSequence >= capability.MaxSafeInteger {
		m.mu.Unlock()
		lease.Revoke()
		return fmt.Errorf("grant renewal sequence exhausted")
	}
	current := lease.Grants()
	if err := current.ValidateRenewal(next, owner, time.Now()); err != nil {
		m.mu.Unlock()
		return err
	}
	m.renewing = lease
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		if m.renewing == lease {
			m.renewing = nil
		}
		m.mu.Unlock()
	}()
	fence := func(err error) error {
		// Revocation is synchronous for active permits. Stop is bound to this
		// exact attempt, so a replacement incarnation cannot be stopped here.
		lease.Revoke()
		_ = m.stopLease(context.Background(), lease)
		return redactPluginError(err, m.secretValues())
	}
	if m.cfg.RevalidateGrants != nil {
		if err := m.cfg.RevalidateGrants(ctx, current); err != nil {
			return fence(err)
		}
	}
	permit, release, err := lease.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	call, cancel := context.WithTimeout(permit, MaxCallDuration)
	defer cancel()
	deadline, _ := call.Deadline()
	budget := time.Until(deadline).Milliseconds()
	if budget <= 0 || budget > math.MaxUint32 {
		return context.DeadlineExceeded
	}
	m.mu.Lock()
	if m.state != StateRunning || m.grantLease != lease || m.connection != conn || m.incarnation != owner {
		m.mu.Unlock()
		return ErrGrantLeaseEnded
	}
	m.renewalSequence++
	sequence := m.renewalSequence
	m.mu.Unlock()
	// The closed SDK context carries no caller-asserted binding or identity.
	params := sdksub.GrantsRenewParams{RenewalVersion: sdksub.GrantsRenewalVersion,
		Sequence: sequence, Incarnation: owner, Grants: next,
		Context: sdksub.ForwardContext{TimeoutMS: uint32(budget)}}
	raw, err := conn.Call(call, sdksub.MethodGrantsRenew, params)
	if err != nil {
		return fence(err)
	}
	var ack sdksub.GrantsRenewResult
	if decodeErr := json.Unmarshal(raw, &ack); decodeErr != nil {
		return fence(decodeErr)
	}
	if ack.Sequence != sequence || ack.Incarnation != owner || ack.RenewalVersion != sdksub.GrantsRenewalVersion {
		return fence(fmt.Errorf("grant renewal acknowledgment mismatch"))
	}
	// Caller cancellation propagates into permit through AfterFunc, whose
	// callback may still be pending when an acknowledgment arrives.
	if callerErr := ctx.Err(); callerErr != nil {
		return fence(callerErr)
	}
	if callErr := call.Err(); callErr != nil {
		return fence(callErr)
	}
	if m.cfg.RevalidateGrants != nil {
		if policyErr := m.cfg.RevalidateGrants(call, current); policyErr != nil {
			return fence(policyErr)
		}
	}
	m.mu.Lock()
	if m.state != StateRunning || m.grantLease != lease || m.connection != conn || m.incarnation != owner {
		m.mu.Unlock()
		return fence(ErrGrantLeaseEnded)
	}
	if callerErr := ctx.Err(); callerErr != nil {
		m.mu.Unlock()
		return fence(callerErr)
	}
	// ValidateRenewal runs again inside Renew while the old lease is still live;
	// current state and the host replacement commit share this manager lock.
	err = lease.Renew(renewalCommitContext{Context: call, caller: ctx}, next)
	m.mu.Unlock()
	if err != nil {
		return fence(err)
	}
	return nil
}

// The lease performs its final check under its own lock. Check both request
// contexts there; caller cancellation may precede its AfterFunc propagation.
type renewalCommitContext struct {
	context.Context
	caller context.Context
}

func (c renewalCommitContext) Err() error {
	if err := c.caller.Err(); err != nil {
		return err
	}
	return c.Context.Err()
}

// RevokeIncarnation fences only the owner observed by the host's failed policy
// check. A concurrent replacement cannot inherit this revocation.
func (m *Manager) RevokeIncarnation(owner capability.RuntimeIdentity) {
	m.mu.Lock()
	lease := m.grantLease
	if m.incarnation != owner {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	if lease != nil {
		lease.Revoke()
		_ = m.stopLease(context.Background(), lease)
	}
}
