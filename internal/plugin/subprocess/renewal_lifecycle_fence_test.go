package subprocess

import (
	"context"
	"errors"
	"testing"
	"time"

	pluginhost "github.com/hollis-labs/libs/plugin-mcp/plugin-host"
	sdksub "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
)

// pendingCallerContext holds AfterFunc callbacks pending to reproduce the
// cancellation propagation race without depending on scheduler timing.
type pendingCallerContext struct {
	context.Context
	done chan struct{}
}

func (c *pendingCallerContext) Done() <-chan struct{} { return c.done }
func (c *pendingCallerContext) Err() error {
	select {
	case <-c.done:
		return context.Canceled
	default:
		return nil
	}
}
func (c *pendingCallerContext) AfterFunc(func()) func() bool { return func() bool { return true } }

func TestRenewalChecksCanceledCallerBeforePendingCallback(t *testing.T) {
	entered := make(chan struct{})
	unblock := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-unblock:
		default:
			close(unblock)
		}
	})
	manager, lease, next := renewalPeer(t, time.Hour, func(request sdksub.GrantsRenewParams) (any, *RPCError) {
		close(entered)
		<-unblock
		return renewalAck(request), nil
	})
	caller := &pendingCallerContext{Context: context.Background(), done: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- manager.RenewGrants(caller, next) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("renewal did not reach peer")
	}
	close(caller.done)
	close(unblock)
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("acknowledged renewal ignored caller cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled renewal did not terminate")
	}
	if manager.State() != StateStopped || manager.CurrentGrants() != nil {
		t.Fatal("uncertain acknowledged owner was not fenced")
	}
	if _, err := lease.Context(); !errors.Is(err, ErrGrantLeaseEnded) {
		t.Fatal("fenced lease survived")
	}
}

func TestExpiredLiveLeaseRemainsAuthorizationFailure(t *testing.T) {
	owner, grants := leaseFixture(20 * time.Millisecond)
	lease, err := NewGrantLease(context.Background(), grants, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Revoke()
	manager := NewManager(ManagerConfig{})
	manager.incarnation, manager.grantLease, manager.connection, manager.state = owner, lease, &pluginhost.Conn{}, StateRunning
	// The real grant deadline expires with no process-exit callback; the
	// manager and connection remain live while authorization has ended.
	original, err := lease.Context()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-original.Done():
	case <-time.After(time.Second):
		t.Fatal("grant lease did not expire")
	}
	if _, _, _, err := manager.acquireDispatch(context.Background()); !errors.Is(err, ErrGrantLeaseEnded) || errors.Is(err, ErrSubprocessGone) {
		t.Fatalf("authorization expiry became process death: %v", err)
	}
}
