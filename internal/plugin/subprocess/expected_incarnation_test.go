package subprocess

import (
	"context"
	"errors"
	"testing"
	"time"

	pluginhost "github.com/hollis-labs/libs/plugin-mcp/plugin-host"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-host/pluginhosttest"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability"
)

func TestExpectedIncarnationRefusesForeignTuple(t *testing.T) {
	command, env := pluginhosttest.FixtureCommand(pluginhosttest.BehaviourEcho, t.TempDir())
	manager := NewManager(ManagerConfig{ID: "fixture", Command: command, Env: env, StartupTimeout: 5 * time.Second, ShutdownTimeout: time.Second})
	transport, err := manager.Start(context.Background(), fixtureInit(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Stop() })
	owner := manager.Incarnation()
	bound := WithExpectedIncarnation(context.Background(), owner)
	if _, err := transport.Call(bound, MethodMCPCallTool, MCPCallRequest{ToolName: "echo"}); err != nil {
		t.Fatalf("current binding refused: %v", err)
	}
	for _, foreign := range []func(){
		func() { owner.HostInstance = "different-host" },
		func() { owner.OwnerID = "different-owner" },
		func() { owner.OwnerGeneration++ },
	} {
		owner = manager.Incarnation()
		foreign()
		// The fixture exits if this call reaches its connection. A stale
		// binding must refuse before any request is published.
		_, err := transport.Call(WithExpectedIncarnation(context.Background(), owner), MethodMCPCallTool, MCPCallRequest{ToolName: "exit"})
		if !errors.Is(err, ErrStaleBinding) {
			t.Fatalf("foreign binding reached replacement: %v", err)
		}
	}
	if _, err := transport.Call(bound, MethodMCPCallTool, MCPCallRequest{ToolName: "echo"}); err != nil {
		t.Fatalf("denied stale calls affected the current child: %v", err)
	}
}

func TestExpectedIncarnationCannotRetargetDuringRevalidation(t *testing.T) {
	owner, grants := leaseFixture(time.Hour)
	lease, err := NewGrantLease(context.Background(), grants, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Revoke()
	conn := &pluginhost.Conn{}
	manager := NewManager(ManagerConfig{})
	manager.incarnation, manager.grantLease, manager.connection, manager.state = owner, lease, conn, StateRunning
	manager.cfg.RevalidateGrants = func(context.Context, capability.GrantSet) error {
		manager.mu.Lock()
		manager.incarnation.OwnerGeneration++
		manager.connection = &pluginhost.Conn{}
		manager.mu.Unlock()
		return nil
	}
	if _, _, _, err := manager.acquireDispatch(WithExpectedIncarnation(context.Background(), owner)); !errors.Is(err, ErrStaleBinding) {
		t.Fatalf("changed connection received an old permit: %v", err)
	}
}

func TestDispatchValidationRefusesBeforeConnectionBytes(t *testing.T) {
	command, env := pluginhosttest.FixtureCommand(pluginhosttest.BehaviourEcho, t.TempDir())
	manager := NewManager(ManagerConfig{ID: "fixture", Command: command, Env: env, StartupTimeout: 5 * time.Second, ShutdownTimeout: time.Second})
	transport, err := manager.Start(context.Background(), fixtureInit(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Stop() })
	denied := errors.New("trusted owner withdrew caller authority")
	var permit context.Context
	bound := WithExpectedIncarnation(context.Background(), manager.Incarnation())
	bound = WithDispatchValidation(bound, func(ctx context.Context) error {
		// Reentrant state read demonstrates no manager lock spans the callback.
		if manager.State() != StateRunning {
			t.Fatal("no current owner")
		}
		permit = ctx
		return denied
	})
	if _, err := transport.Call(bound, MethodMCPCallTool, MCPCallRequest{ToolName: "exit"}); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	if permit == nil || !errors.Is(permit.Err(), context.Canceled) {
		t.Fatal("refused dispatch retained permit")
	}
	if _, err := transport.Call(context.Background(), MethodMCPCallTool, MCPCallRequest{ToolName: "echo"}); err != nil {
		t.Fatal("refused request reached child", err)
	}
}

func TestDispatchValidationRechecksOwnerAfterCallback(t *testing.T) {
	owner, grants := leaseFixture(time.Hour)
	lease, err := NewGrantLease(context.Background(), grants, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Revoke()
	manager := NewManager(ManagerConfig{})
	manager.incarnation, manager.grantLease, manager.connection, manager.state = owner, lease, &pluginhost.Conn{}, StateRunning
	var permit context.Context
	ctx := WithExpectedIncarnation(context.Background(), owner)
	ctx = WithDispatchValidation(ctx, func(call context.Context) error {
		permit = call
		manager.mu.Lock()
		manager.incarnation.OwnerGeneration++
		manager.connection = &pluginhost.Conn{}
		manager.mu.Unlock()
		return nil
	})
	if _, _, _, err := manager.acquireDispatch(ctx); !errors.Is(err, ErrStaleBinding) {
		t.Fatal("callback retargeted dispatch", err)
	}
	if permit == nil || !errors.Is(permit.Err(), context.Canceled) {
		t.Fatal("stale dispatch retained permit")
	}
}
