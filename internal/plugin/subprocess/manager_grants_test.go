package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	pluginhost "github.com/hollis-labs/libs/plugin-mcp/plugin-host"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-host/pluginhosttest"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability"
	sdksub "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
)

func renewalPeer(t *testing.T, ttl time.Duration, handler func(sdksub.GrantsRenewParams) (any, *RPCError)) (*Manager, *GrantLease, capability.GrantSet) {
	t.Helper()
	input, write := io.Pipe()
	read, output := io.Pipe()
	conn := pluginhost.NewConn(read, write)
	t.Cleanup(func() { _ = conn.Close(); _ = input.Close(); _ = output.Close() })
	go mockPlugin(input, output, map[string]func(json.RawMessage) (any, *RPCError){
		sdksub.MethodGrantsRenew: func(raw json.RawMessage) (any, *RPCError) {
			var request sdksub.GrantsRenewParams
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, &RPCError{Code: ErrCodeInvalidParams, Message: err.Error()}
			}
			return handler(request)
		},
	})
	owner, grants := leaseFixture(ttl)
	lease, err := NewGrantLease(context.Background(), grants, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager := &Manager{state: StateRunning, grantLease: lease, connection: conn, incarnation: owner, renewalSupported: true}
	t.Cleanup(func() { _ = manager.Stop() })
	next := grants.Clone()
	next[0].IssuedAt = time.Now().UTC().Format(time.RFC3339Nano)
	next[0].ExpiresAt = time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	return manager, lease, next
}
func renewalAck(request sdksub.GrantsRenewParams) sdksub.GrantsRenewResult {
	return sdksub.GrantsRenewResult{RenewalVersion: sdksub.GrantsRenewalVersion, Sequence: request.Sequence, Incarnation: request.Incarnation}
}

func TestManagerRenewalCommitsOnlyAfterExactAcknowledgment(t *testing.T) {
	entered := make(chan sdksub.GrantsRenewParams, 2)
	continueAck := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-continueAck:
		default:
			close(continueAck)
		}
	})
	manager, lease, next := renewalPeer(t, time.Hour, func(request sdksub.GrantsRenewParams) (any, *RPCError) {
		entered <- request
		<-continueAck
		return renewalAck(request), nil
	})
	var checked atomic.Int32
	manager.cfg.RevalidateGrants = func(context.Context, capability.GrantSet) error { checked.Add(1); return nil }
	old := manager.CurrentGrants()
	active, release, err := lease.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- manager.RenewGrants(ctx, next) }()
	select {
	case request := <-entered:
		if request.Sequence != 1 || request.Incarnation != manager.Incarnation() || request.Context.BindingID != nil || request.Context.TimeoutMS == 0 || request.Context.TimeoutMS > 5000 || !reflect.DeepEqual(request.Grants, next) {
			t.Fatal("renewal request changed", request)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !reflect.DeepEqual(manager.CurrentGrants(), old) || active.Err() != nil || checked.Load() != 1 {
		t.Fatal("host committed before acknowledgment")
	}
	if competingErr := manager.RenewGrants(ctx, next); competingErr == nil {
		t.Fatal("competing renewal admitted")
	}
	close(continueAck)
	select {
	case renewErr := <-result:
		if renewErr != nil {
			t.Fatal(renewErr)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !reflect.DeepEqual(manager.CurrentGrants(), next) || active.Err() == nil || checked.Load() != 2 {
		t.Fatal("acknowledged replacement lost")
	}
	second := next.Clone()
	second[0].ExpiresAt = time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339Nano)
	if renewErr := manager.RenewGrants(ctx, second); renewErr != nil {
		t.Fatal(renewErr)
	}
	if request := <-entered; request.Sequence != 2 {
		t.Fatal("sequence reused", request.Sequence)
	}
	cancel()
	if _, liveErr := lease.Context(); liveErr != nil {
		t.Fatal("request lifetime terminated renewed owner", liveErr)
	}
}

func TestManagerRenewalFencesUncertainOrInvalidOutcome(t *testing.T) {
	for _, mode := range []string{"wrong-sequence", "wrong-owner", "unknown-field", "rpc-error", "canceled", "expired", "revoked", "policy-changed"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{}, 1)
			unblock := make(chan struct{})
			t.Cleanup(func() {
				select {
				case <-unblock:
				default:
					close(unblock)
				}
			})
			ttl := time.Hour
			if mode == "expired" {
				ttl = 250 * time.Millisecond
			}
			manager, lease, next := renewalPeer(t, ttl, func(request sdksub.GrantsRenewParams) (any, *RPCError) {
				entered <- struct{}{}
				<-unblock
				ack := renewalAck(request)
				switch mode {
				case "wrong-sequence":
					ack.Sequence++
				case "wrong-owner":
					ack.Incarnation.OwnerGeneration++
				case "unknown-field":
					return map[string]any{"renewal_version": 1, "sequence": ack.Sequence, "incarnation": ack.Incarnation, "extra": true}, nil
				case "rpc-error":
					return nil, &RPCError{Code: ErrCodeInternal, Message: "fixture refusal"}
				}
				return ack, nil
			})
			var checks atomic.Int32
			manager.cfg.RevalidateGrants = func(context.Context, capability.GrantSet) error {
				if checks.Add(1) == 2 && mode == "policy-changed" {
					return errors.New("accepted policy changed")
				}
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- manager.RenewGrants(ctx, next) }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			switch mode {
			case "canceled":
				cancel()
			case "revoked":
				lease.Revoke()
			case "expired":
				original, _ := lease.Context()
				if original != nil {
					select {
					case <-original.Done():
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
				}
			}
			close(unblock)
			select {
			case renewErr := <-result:
				if renewErr == nil {
					t.Fatal("uncertain or refused update succeeded")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("renewal did not terminate")
			}
			if manager.State() != StateStopped || manager.CurrentGrants() != nil {
				t.Fatal("owner was not fenced", manager.State())
			}
			if _, leaseErr := lease.Context(); !errors.Is(leaseErr, ErrGrantLeaseEnded) {
				t.Fatal("fenced authority survived", leaseErr)
			}
		})
	}
}

func TestManagerRenewalRefusesUnsentExpansionAndCanceledRequest(t *testing.T) {
	var sent atomic.Int32
	manager, lease, next := renewalPeer(t, time.Hour, func(request sdksub.GrantsRenewParams) (any, *RPCError) { sent.Add(1); return renewalAck(request), nil })
	expanded := next.Clone()
	expanded[0].Scope = json.RawMessage(`{"operations":["query"],"allowlists":{"sessions":["one","two"]}}`)
	if err := manager.RenewGrants(context.Background(), expanded); err == nil {
		t.Fatal("expanded authority admitted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.RenewGrants(canceled, next); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if sent.Load() != 0 || manager.State() != StateRunning {
		t.Fatal("unsent refusal dispatched or fenced")
	}
	if _, err := lease.Context(); err != nil {
		t.Fatal(err)
	}
}

func TestManagerRenewalUncertaintyCannotStopReplacementOwner(t *testing.T) {
	entered := make(chan struct{})
	unblock := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-unblock:
		default:
			close(unblock)
		}
	})
	manager, old, next := renewalPeer(t, time.Hour, func(request sdksub.GrantsRenewParams) (any, *RPCError) {
		close(entered)
		<-unblock
		return renewalAck(request), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- manager.RenewGrants(ctx, next) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	owner, newGrants := leaseFixture(time.Hour)
	owner.OwnerGeneration++
	newGrants[0].OwnerGeneration = owner.OwnerGeneration
	replacement, err := NewGrantLease(context.Background(), newGrants, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.grantLease = replacement
	manager.incarnation = owner
	manager.mu.Unlock()
	close(unblock)
	select {
	case renewErr := <-result:
		if renewErr == nil {
			t.Fatal("old attempt committed")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if manager.State() != StateRunning || manager.Incarnation() != owner {
		t.Fatal("replacement stopped")
	}
	if _, liveErr := replacement.Context(); liveErr != nil {
		t.Fatal(liveErr)
	}
	if _, oldErr := old.Context(); !errors.Is(oldErr, ErrGrantLeaseEnded) {
		t.Fatal("old authority survived", oldErr)
	}
}

func TestManagerRenewalRequiresActualInitAcknowledgment(t *testing.T) {
	command, env := pluginhosttest.FixtureCommand(pluginhosttest.BehaviourEcho, t.TempDir())
	manager := NewManager(ManagerConfig{ID: "fixture", Command: command, Env: env})
	manager.cfg.IssueGrants = func(_ context.Context, owner capability.RuntimeIdentity) (capability.GrantSet, error) {
		_, grants := leaseFixture(time.Hour)
		grants[0].HostInstance, grants[0].OwnerID, grants[0].OwnerGeneration = owner.HostInstance, owner.OwnerID, owner.OwnerGeneration
		return grants, nil
	}
	transport, err := manager.Start(context.Background(), fixtureInit(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Stop() })
	response, callErr := CallResult[MCPCallResult](transport, context.Background(), MethodMCPCallTool, MCPCallRequest{ToolName: "init"})
	if callErr != nil {
		t.Fatal(callErr)
	}
	var delivered InitParams
	if decodeErr := json.Unmarshal(response.Content, &delivered); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if delivered.GrantsRenewalVersion == nil || *delivered.GrantsRenewalVersion != sdksub.GrantsRenewalVersion {
		t.Fatal("host did not offer renewal")
	}
	next := manager.CurrentGrants()
	next[0].ExpiresAt = time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	if renewErr := manager.RenewGrants(context.Background(), next); !errors.Is(renewErr, ErrGrantRenewalUnavailable) {
		t.Fatal("unacknowledged profile admitted", renewErr)
	}
	if manager.State() != StateRunning {
		t.Fatal("unsent unsupported renewal stopped owner")
	}
}

func TestManagerRevocationCannotFenceReplacementIncarnation(t *testing.T) {
	manager, oldLease, _ := renewalPeer(t, time.Hour, func(request sdksub.GrantsRenewParams) (any, *RPCError) { return renewalAck(request), nil })
	old := manager.Incarnation()
	replacement := old
	replacement.OwnerGeneration++
	grants := oldLease.Grants()
	for i := range grants {
		grants[i].OwnerGeneration = replacement.OwnerGeneration
	}
	newLease, err := NewGrantLease(context.Background(), grants, replacement, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer newLease.Revoke()
	manager.mu.Lock()
	manager.incarnation = replacement
	manager.grantLease = newLease
	manager.mu.Unlock()
	defer oldLease.Revoke()
	manager.RevokeIncarnation(old)
	if _, err := newLease.Context(); err != nil {
		t.Fatal("stale host policy failure revoked replacement", err)
	}
	manager.RevokeIncarnation(replacement)
	if _, err := newLease.Context(); !errors.Is(err, ErrGrantLeaseEnded) {
		t.Fatal("actual incarnation was not fenced", err)
	}
}
