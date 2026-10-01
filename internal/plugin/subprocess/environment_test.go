package subprocess

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
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
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "environment.json")
	mgr := NewManager(ManagerConfig{
		Command:         executable,
		Args:            []string{"-test.run=^TestPluginEnvironmentHelper$"},
		Env:             []string{"NANITE_ENV_HELPER=1", "NANITE_ENV_OUTPUT=" + output, "NANITE_ENV_APPROVED=declared-value"},
		ShutdownTimeout: time.Second,
	})
	if _, startErr := mgr.Start(context.Background()); startErr != nil {
		t.Fatal(startErr)
	}
	t.Cleanup(func() {
		if stopErr := mgr.Stop(); stopErr != nil {
			t.Error(stopErr)
		}
	})
	mgr.mu.Lock()
	wait := mgr.waitCh
	mgr.mu.Unlock()
	select {
	case <-wait:
	case <-time.After(5 * time.Second):
		t.Fatal("environment helper did not exit")
	}
	data, err := os.ReadFile(output) // #nosec G304 -- output is a fixed filename inside t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if decodeErr := json.Unmarshal(data, &got); decodeErr != nil {
		t.Fatal(decodeErr)
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

func TestPluginEnvironmentHelper(t *testing.T) {
	if os.Getenv("NANITE_ENV_HELPER") != "1" {
		return
	}
	values := map[string]string{}
	for _, key := range []string{"PATH", "HOME", "NANITE_ENV_PRIVATE_TEST", "SSH_AUTH_SOCK", "DOCKER_HOST", "NANITE_ENV_APPROVED"} {
		values[key] = os.Getenv(key)
	}
	data, err := json.Marshal(values)
	if err != nil {
		os.Exit(1)
	}
	if err := os.WriteFile(os.Getenv("NANITE_ENV_OUTPUT"), data, 0o600); err != nil { // #nosec G703 -- this test-only helper receives a parent-owned temporary file path.
		os.Exit(1)
	}
	os.Exit(0)
}
