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

	pluginhost "github.com/hollis-labs/plugin-host"
	"github.com/hollis-labs/plugin-host/pluginhosttest"
)

func TestMain(m *testing.M) {
	pluginhosttest.MaybeRunFixture()
	os.Exit(m.Run())
}

// Exercise the host adapter, including Nanite's environment policy and RPC path.
func TestHostConformance(t *testing.T) { pluginhosttest.Run(t, naniteHarness{}) }

type naniteHarness struct{}

func (naniteHarness) Start(ctx context.Context, c pluginhosttest.Case) (pluginhosttest.Instance, error) {
	mgr := NewManager(ManagerConfig{Command: c.Command, Args: c.Args, Env: c.Env, Secrets: c.Secrets, StartupTimeout: 10 * time.Second, ShutdownTimeout: 2 * time.Second})
	transport, err := mgr.Start(ctx, InitParams{DataDir: c.DataDir, CacheDir: c.CacheDir})
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
	mgr := NewManager(ManagerConfig{Command: command, Env: env, ID: "fixture", MaxRestarts: 1, InitialBackoff: 10 * time.Millisecond, StartupTimeout: 5 * time.Second, ShutdownTimeout: time.Second})
	transport, startErr := mgr.Start(context.Background(), InitParams{})
	if startErr != nil {
		t.Fatal(startErr)
	}
	t.Cleanup(func() { _ = mgr.Stop() })
	old := mgr.process()
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
	if echo["echo"] != "after-restart" || mgr.Restarts() != 1 {
		t.Fatalf("new child reply = %v, restarts=%d", echo, mgr.Restarts())
	}
}

func TestManagerPreservesHostInitGrants(t *testing.T) {
	command, env := pluginhosttest.FixtureCommand(pluginhosttest.BehaviourEcho, t.TempDir())
	mgr := NewManager(ManagerConfig{Command: command, Env: env})
	init := InitParams{Config: map[string]string{"enabled": "false", "empty": ""}, Granted: []string{"readonly.query"}}
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
	if !reflect.DeepEqual(init.Config, got.Config) || !reflect.DeepEqual(init.Granted, got.Granted) {
		t.Fatalf("host init changed: %+v", got)
	}
}

func TestSubprocessLoadRetainsSkipsAndOneHandshake(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(base, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(base, "cache"))
	command, env := pluginhosttest.FixtureCommand(pluginhosttest.BehaviourLoadSkips, base)
	instance := NewSubprocessPlugin(base, "fixture", nil, ManagerConfig{Command: command, Env: env})
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
	mgr := NewManager(ManagerConfig{Command: command, Env: env, ID: "declared-plugin"})
	if _, startErr := mgr.Start(context.Background(), InitParams{}); startErr == nil {
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
	manager := NewManager(ManagerConfig{Command: command, Env: env, StartupTimeout: 5 * time.Second, ShutdownTimeout: time.Second, OnUnload: func() { once.Do(func() { close(released) }) }})
	transport, err := manager.Start(context.Background(), InitParams{})
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
