package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability"

	pluginhost "github.com/hollis-labs/libs/plugin-mcp/plugin-host"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-host/pluginhosttest"
)

func TestMain(m *testing.M) {
	pluginhosttest.MaybeRunFixture()
	os.Exit(m.Run())
}

// Exercise the host adapter, including Nanite's environment policy and RPC path.
func TestHostConformance(t *testing.T) { pluginhosttest.Run(t, naniteHarness{}) }

type naniteHarness struct{}

func (naniteHarness) Start(ctx context.Context, c pluginhosttest.Case) (pluginhosttest.Instance, error) {
	mgr := NewManager(ManagerConfig{ID: "fixture", Command: c.Command, Args: c.Args, Env: c.Env, Secrets: c.Secrets, StartupTimeout: 10 * time.Second, ShutdownTimeout: 2 * time.Second})
	transport, err := mgr.Start(ctx, InitParams{PluginDir: c.DataDir, DataDir: c.DataDir, CacheDir: c.CacheDir, HostInfo: HostInfo{Version: "fixture", Protocol: ProtocolVersion}})
	if err != nil {
		return nil, err
	}
	return &naniteInstance{manager: mgr, transport: transport, process: mgr.process()}, nil
}

type naniteInstance struct {
	manager   *Manager
	transport *Transport
	process   *pluginhost.Process
}

func (n *naniteInstance) Call(ctx context.Context, method string, params, result any) error {
	response, err := n.transport.Call(ctx, method, params)
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(response.Result, result)
}
func (n *naniteInstance) Stop(ctx context.Context) error { return n.manager.stop(ctx) }
func (n *naniteInstance) Exited() <-chan struct{}        { return n.process.Exited() }
func (n *naniteInstance) Pid() int                       { return n.process.Pid() }
func (n *naniteInstance) Diagnostics() string            { return n.process.Diagnostics() }
func (n *naniteInstance) IsGone(err error) bool          { return errors.Is(err, ErrSubprocessGone) }
func (n *naniteInstance) RPCCode(err error) (int, bool) {
	var rpc *RPCError
	if errors.As(err, &rpc) {
		return rpc.Code, true
	}
	return 0, false
}

func TestManagerTransportFollowsSupervisedRestart(t *testing.T) {
	command, env := pluginhosttest.FixtureCommand(pluginhosttest.BehaviourEcho, t.TempDir())
	mgr := NewManager(ManagerConfig{ID: "fixture", Command: command, Env: env, MaxRestarts: 1, InitialBackoff: 10 * time.Millisecond, StartupTimeout: 5 * time.Second, ShutdownTimeout: time.Second})
	transport, startErr := mgr.Start(context.Background(), fixtureInit(t))
	if startErr != nil {
		t.Fatal(startErr)
	}
	t.Cleanup(func() { _ = mgr.Stop() })
	old := mgr.process()
	oldOwner := mgr.Incarnation()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = transport.Call(ctx, MethodMCPCallTool, MCPCallRequest{ToolName: "exit"})
	for {
		current := mgr.process()
		if current != nil && current.Pid() != old.Pid() {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("supervised restart did not finish")
		case <-time.After(10 * time.Millisecond):
		}
	}
	result, callErr := CallResult[MCPCallResult](transport, ctx, MethodMCPCallTool, MCPCallRequest{ToolName: "echo", Arguments: map[string]any{"message": "after-restart"}})
	if callErr != nil {
		t.Fatal(callErr)
	}
	var echo map[string]string
	if decodeErr := json.Unmarshal(result.Content, &echo); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	newOwner := mgr.Incarnation()
	if newOwner.HostInstance != oldOwner.HostInstance || newOwner.OwnerID != oldOwner.OwnerID || newOwner.OwnerGeneration <= oldOwner.OwnerGeneration {
		t.Fatalf("restart reused or fabricated incarnation: old=%+v new=%+v", oldOwner, newOwner)
	}
	if echo["echo"] != "after-restart" || mgr.Restarts() != 1 {
		t.Fatalf("new child reply = %v, restarts=%d", echo, mgr.Restarts())
	}
}

func TestManagerPreservesFreshHostIssuedGrants(t *testing.T) {
	command, env := pluginhosttest.FixtureCommand(pluginhosttest.BehaviourEcho, t.TempDir())
	mgr := NewManager(ManagerConfig{ID: "fixture", Command: command, Env: env})
	init := fixtureInit(t)
	init.Config = map[string]string{"enabled": "false", "empty": ""}
	var issued capability.GrantSet
	mgr.cfg.IssueGrants = func(_ context.Context, owner capability.RuntimeIdentity) (capability.GrantSet, error) {
		issued = capability.GrantSet{{GrantID: "fixture-grant", Name: "readonly.query", SchemaVersion: 1, Scope: json.RawMessage(`{}`), HostInstance: owner.HostInstance, OwnerID: owner.OwnerID, OwnerGeneration: owner.OwnerGeneration, Audience: "fixture", IssuedAt: time.Now().UTC().Format(time.RFC3339Nano), ExpiresAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano), PolicyRevision: "fixture-policy"}}
		return issued, nil
	}
	transport, startErr := mgr.Start(context.Background(), init)
	if startErr != nil {
		t.Fatal(startErr)
	}
	t.Cleanup(func() { _ = mgr.Stop() })
	result, callErr := CallResult[MCPCallResult](transport, context.Background(), MethodMCPCallTool, MCPCallRequest{ToolName: "init"})
	if callErr != nil {
		t.Fatal(callErr)
	}
	var got InitParams
	if decodeErr := json.Unmarshal(result.Content, &got); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if !reflect.DeepEqual(init.Config, got.Config) || !reflect.DeepEqual(issued, got.Grants) {
		t.Fatalf("host init changed: %+v", got)
	}
}

func TestSubprocessLoadRetainsSkipsAndOneHandshake(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(base, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(base, "cache"))
	command, env := pluginhosttest.FixtureCommand(pluginhosttest.BehaviourLoadSkips, base)
	instance := NewSubprocessPlugin(base, "fixture", nil, ManagerConfig{ID: "fixture", Command: command, Env: env})
	if loadErr := instance.Load(newFakeHost()); loadErr != nil {
		t.Fatal(loadErr)
	}
	t.Cleanup(func() { _ = instance.Unload() })
	if skipped := instance.SkippedRegistrations(); len(skipped) != 1 || skipped[0].ID != "example" {
		t.Fatalf("skipped = %+v", skipped)
	}
	result, callErr := CallResult[MCPCallResult](instance.Transport(), context.Background(), MethodMCPCallTool, MCPCallRequest{ToolName: "trace"})
	if callErr != nil {
		t.Fatal(callErr)
	}
	var trace []string
	if decodeErr := json.Unmarshal(result.Content, &trace); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if !reflect.DeepEqual(trace, []string{MethodInit, MethodLoad}) {
		t.Fatalf("handshake = %v", trace)
	}
}

func TestManagerRejectsIdentityDifferentFromManifest(t *testing.T) {
	command, env := pluginhosttest.FixtureCommand(pluginhosttest.BehaviourEcho, t.TempDir())
	mgr := NewManager(ManagerConfig{ID: "declared-plugin", Command: command, Env: env})
	if _, startErr := mgr.Start(context.Background(), fixtureInit(t)); startErr == nil {
		_ = mgr.Stop()
		t.Fatal("mismatched child identity was accepted")
	}
	if process := mgr.process(); process != nil {
		t.Fatal("mismatched child remains supervised")
	}
}

func TestManagerReleasesConnectionOnPermanentFailure(t *testing.T) {
	command, env := pluginhosttest.FixtureCommand(pluginhosttest.BehaviourEcho, t.TempDir())
	released := make(chan struct{})
	var once sync.Once
	manager := NewManager(ManagerConfig{ID: "fixture", Command: command, Env: env, StartupTimeout: 5 * time.Second, ShutdownTimeout: time.Second, OnUnload: func() { once.Do(func() { close(released) }) }})
	transport, err := manager.Start(context.Background(), fixtureInit(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Stop() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = transport.Call(ctx, MethodMCPCallTool, MCPCallRequest{ToolName: "exit"})
	select {
	case <-released:
	case <-ctx.Done():
		t.Fatal("permanent supervisor failure retained connection")
	}
}

func fixtureInit(t *testing.T) InitParams {
	t.Helper()
	root := t.TempDir()
	return InitParams{PluginDir: root, DataDir: root, CacheDir: root, HostInfo: HostInfo{Version: "fixture", Protocol: ProtocolVersion}}
}

func TestManagerRecreationHasFreshGenerationAndStoppedOwnerIsNotLive(t *testing.T) {
	command, env := pluginhosttest.FixtureCommand(pluginhosttest.BehaviourEcho, t.TempDir())
	cfg := ManagerConfig{ID: "fixture", Command: command, Env: env}
	first := NewManager(cfg)
	if _, err := first.Start(context.Background(), fixtureInit(t)); err != nil {
		t.Fatal(err)
	}
	owner := first.Incarnation()
	if err := first.Stop(); err != nil {
		t.Fatal(err)
	}
	if actual := first.Incarnation(); actual.OwnerGeneration != 0 {
		t.Fatalf("stopped owner is live: %+v", actual)
	}
	second := NewManager(cfg)
	if _, err := second.Start(context.Background(), fixtureInit(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Stop() })
	next := second.Incarnation()
	if next.HostInstance != owner.HostInstance || next.OwnerID != owner.OwnerID || next.OwnerGeneration <= owner.OwnerGeneration {
		t.Fatalf("controller recreation reused identity: old=%+v new=%+v", owner, next)
	}
}

func TestManagerRefusesMissingOrFailedIssuerBeforeSpawning(t *testing.T) {
	for _, scenario := range []string{"missing", "failed"} {
		t.Run(scenario, func(t *testing.T) {
			spawns := 0
			cfg := ManagerConfig{ID: "fixture", Command: "unused", Granted: []string{"readonly.query"}, BeforeSpawn: func(context.Context) error { spawns++; return nil }}
			refusal := errors.New("owner policy refused issuance")
			if scenario == "failed" {
				cfg.IssueGrants = func(context.Context, capability.RuntimeIdentity) (capability.GrantSet, error) { return nil, refusal }
			}
			mgr := NewManager(cfg)
			_, err := mgr.Start(context.Background(), fixtureInit(t))
			if err == nil || spawns != 0 || mgr.process() != nil || mgr.Incarnation().OwnerGeneration != 0 {
				t.Fatalf("refused policy produced effects: err=%v spawns=%d owner=%+v", err, spawns, mgr.Incarnation())
			}
			if scenario == "failed" && !errors.Is(err, refusal) {
				t.Fatalf("issuance cause lost: %v", err)
			}
		})
	}
}

func TestManagerOldLeaseCannotStopReplacement(t *testing.T) {
	owner, grants := leaseFixture(time.Hour)
	old, err := NewGrantLease(context.Background(), grants, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Revoke()
	current, err := NewGrantLease(context.Background(), grants, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Revoke()
	unloaded := false
	mgr := &Manager{state: StateRunning, grantLease: current, cfg: ManagerConfig{OnUnload: func() { unloaded = true }}}
	if err := mgr.stopLease(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	if unloaded || mgr.State() != StateRunning {
		t.Fatal("stale expiry affected replacement")
	}
	if _, err := current.Context(); err != nil {
		t.Fatal("replacement lease revoked")
	}
}

func TestManagerDispatchRefusesReplacementDuringPolicyCheck(t *testing.T) {
	owner, grants := leaseFixture(time.Hour)
	old, err := NewGrantLease(context.Background(), grants, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Revoke()
	next, err := NewGrantLease(context.Background(), grants, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Revoke()
	// An unused connection is sufficient: rejection must happen before RPC.
	conn := new(pluginhost.Conn)
	mgr := &Manager{state: StateRunning, grantLease: old, connection: conn}
	mgr.cfg.RevalidateGrants = func(context.Context, capability.GrantSet) error {
		mgr.mu.Lock()
		mgr.grantLease = next
		mgr.connection = new(pluginhost.Conn)
		mgr.mu.Unlock()
		return nil
	}
	if _, _, _, err := mgr.acquireDispatch(context.Background()); !errors.Is(err, ErrGrantLeaseEnded) {
		t.Fatalf("replacement dispatch: %v", err)
	}
}

func TestManagerExpiryStopsChildWithoutRestart(t *testing.T) {
	command, env := pluginhosttest.FixtureCommand(pluginhosttest.BehaviourEcho, t.TempDir())
	mgr := NewManager(ManagerConfig{ID: "fixture", Command: command, Env: env, MaxRestarts: 2, StartupTimeout: 5 * time.Second, ShutdownTimeout: time.Second})
	mgr.cfg.IssueGrants = func(_ context.Context, owner capability.RuntimeIdentity) (capability.GrantSet, error) {
		_, grants := leaseFixture(time.Second)
		grants[0].HostInstance, grants[0].OwnerID, grants[0].OwnerGeneration = owner.HostInstance, owner.OwnerID, owner.OwnerGeneration
		return grants, nil
	}
	transport, err := mgr.Start(context.Background(), fixtureInit(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Stop() })
	child := mgr.process()
	select {
	case <-child.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("expired child retained process privilege")
	}
	if _, err := transport.Call(context.Background(), MethodMCPCallTool, MCPCallRequest{ToolName: "echo"}); !errors.Is(err, ErrGrantLeaseEnded) {
		t.Fatalf("post-expiry call: %v", err)
	}
	if mgr.Restarts() != 0 {
		t.Fatal("expired privilege resurrected by restart")
	}
}

func TestManagerHostLifetimeIsIndependentOfStartupRequest(t *testing.T) {
	command, env := pluginhosttest.FixtureCommand(pluginhosttest.BehaviourEcho, t.TempDir())
	host, stopHost := context.WithCancel(context.Background())
	defer stopHost()
	request, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	manager := NewManager(ManagerConfig{ID: "fixture", Command: command, Env: env, LifecycleContext: host, StartupTimeout: 5 * time.Second, ShutdownTimeout: time.Second})
	transport, err := manager.Start(request, InitParams{PluginDir: t.TempDir(), DataDir: t.TempDir(), CacheDir: t.TempDir(), HostInfo: HostInfo{Version: "fixture", Protocol: ProtocolVersion}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Stop() })
	exited := manager.process().Exited()
	cancelRequest()
	if _, err := transport.Call(context.Background(), "plugin/health", nil); err != nil {
		t.Fatal("startup request ended accepted child", err)
	}
	stopHost()
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("host lifetime did not stop child")
	}
	if _, err := transport.Call(context.Background(), "plugin/health", nil); err == nil {
		t.Fatal("host-ended child still dispatchable")
	}
}
