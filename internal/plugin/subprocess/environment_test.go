package subprocess

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/plugin-host/pluginhosttest"
)

func TestPluginEnvironmentAllowlist(t *testing.T) {
	parent := []string{"PATH=/bin", "HOME=/home/test", "TMPDIR=/tmp", "TMP=/tmp", "TEMP=/tmp", "USER=test", "LOGNAME=test", "LANG=C", "LC_ALL=C", "XDG_RUNTIME_DIR=/run/user/test", "ANTHROPIC_API_KEY=parent-secret", "SSH_AUTH_SOCK=/tmp/agent", "DOCKER_HOST=unix:///tmp/docker.sock", "HTTP_PROXY=http://proxy", "LD_PRELOAD=library", "PYTHONPATH=/injected", "GO_WANT_PLUGIN_HELPER=1", "NANITE_OTHER=private", "malformed"}
	want := append(append([]string{}, parent[:10]...), "APPROVED=value")
	got := pluginEnvironment(parent, []string{"APPROVED=value"})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment = %v, want %v", got, want)
	}
	if pluginEnvironment(nil, nil) == nil {
		t.Fatal("nil environment would re-enable implicit host inheritance")
	}
}

func TestManagerLaunchFiltersActualChildEnvironment(t *testing.T) {
	t.Setenv("NANITE_ENV_PRIVATE_TEST", "parent-only-value")
	t.Setenv("SSH_AUTH_SOCK", "/tmp/test-agent-socket")
	t.Setenv("DOCKER_HOST", "unix:///tmp/test-daemon")
	command, env := pluginhosttest.FixtureCommand(pluginhosttest.BehaviourEcho, t.TempDir(), "NANITE_ENV_APPROVED=declared-value")
	mgr := NewManager(ManagerConfig{Command: command, Env: env, ShutdownTimeout: time.Second})
	transport, startErr := mgr.Start(context.Background(), InitParams{})
	if startErr != nil {
		t.Fatal(startErr)
	}
	t.Cleanup(func() {
		if stopErr := mgr.Stop(); stopErr != nil {
			t.Error(stopErr)
		}
	})
	result, callErr := CallResult[MCPCallResult](transport, context.Background(), MethodMCPCallTool, MCPCallRequest{ToolName: "env"})
	if callErr != nil {
		t.Fatal(callErr)
	}
	var entries []string
	if decodeErr := json.Unmarshal(result.Content, &entries); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	got := map[string]string{}
	for _, entry := range entries {
		key, value, _ := strings.Cut(entry, "=")
		got[key] = value
	}
	for _, key := range []string{"NANITE_ENV_PRIVATE_TEST", "SSH_AUTH_SOCK", "DOCKER_HOST"} {
		if got[key] != "" {
			t.Errorf("ambient credential entry %s reached child", key)
		}
	}
	for _, key := range []string{"PATH", "HOME"} {
		if got[key] != os.Getenv(key) {
			t.Errorf("base environment entry %s changed", key)
		}
	}
	if got["NANITE_ENV_APPROVED"] != "declared-value" {
		t.Error("explicit host-approved environment entry did not reach child")
	}
}
