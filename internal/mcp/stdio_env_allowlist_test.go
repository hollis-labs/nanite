package mcp

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// TestStdioTransport_EnvAllowlist_FailsWithoutPath confirms the start-time
// loud-fail behavior (S4b D6): if the allowlist omits PATH, buildSubprocessEnv
// must return an error rather than silently launching with an empty PATH and
// producing an opaque `exec: "…": file not found` later.
func TestStdioTransport_EnvAllowlist_FailsWithoutPath(t *testing.T) {
	tr := NewStdioTransport("echo", nil, nil, []string{"HOME", "USER"})
	if _, err := tr.buildSubprocessEnv(); err == nil {
		t.Fatal("expected error when PATH not in allowlist, got nil")
	} else if !strings.Contains(err.Error(), "PATH") {
		t.Errorf("error should mention PATH: %v", err)
	}
}

// TestStdioTransport_EnvAllowlist_FiltersHostEnv verifies that only the
// allowlisted keys are inherited from the host, and that MCPServerConfig.Env
// (trusted user-declared entries) passes through as-is regardless of the
// allowlist. Closes finding 10.
func TestStdioTransport_EnvAllowlist_FiltersHostEnv(t *testing.T) {
	// Set two host env vars: one allowlisted, one not.
	t.Setenv("NANITE_TEST_ALLOWLISTED", "yes")
	t.Setenv("NANITE_TEST_SECRET", "SHOULD_NOT_APPEAR")

	tr := NewStdioTransport(
		"/bin/echo",
		nil,
		[]string{"NANITE_EXPLICIT=on"}, // trusted user-declared
		[]string{"PATH", "NANITE_TEST_ALLOWLISTED"},
	)

	env, err := tr.buildSubprocessEnv()
	if err != nil {
		t.Fatalf("buildSubprocessEnv: %v", err)
	}

	// Expected contents:
	//   * PATH (from host)
	//   * NANITE_TEST_ALLOWLISTED=yes (from host, because allowlisted)
	//   * NANITE_EXPLICIT=on (from per-server user-declared env)
	// NOT expected:
	//   * NANITE_TEST_SECRET (host, not allowlisted → must not leak)
	//   * any other host var (AWS_*, GITHUB_TOKEN, etc. audit scenario)
	if hasKey(env, "NANITE_TEST_SECRET") {
		t.Error("non-allowlisted host var leaked into subprocess env")
	}
	if !hasKeyValue(env, "NANITE_TEST_ALLOWLISTED", "yes") {
		t.Error("allowlisted host var missing from subprocess env")
	}
	if !hasKeyValue(env, "NANITE_EXPLICIT", "on") {
		t.Error("user-declared env entry missing from subprocess env")
	}
	// PATH is inherited from the test process — just assert it's present.
	if !hasKey(env, "PATH") {
		t.Error("PATH not inherited despite being allowlisted")
	}
}

// TestStdioTransport_EnvAllowlist_EmptyDefaultOverridable proves the zero-value
// allowlist (nil/empty) fails closed — no keys inherited, PATH missing →
// start-time error. Users must explicitly opt into inheritance.
func TestStdioTransport_EnvAllowlist_EmptyDefaultOverridable(t *testing.T) {
	tr := NewStdioTransport("whatever", nil, nil, nil)
	if _, err := tr.buildSubprocessEnv(); err == nil {
		t.Fatal("expected PATH-missing error with nil allowlist")
	}

	// Overriding with ["PATH"] only should succeed and yield exactly one
	// inherited entry (PATH) + no user-declared env.
	tr2 := NewStdioTransport("whatever", nil, nil, []string{"PATH"})
	env, err := tr2.buildSubprocessEnv()
	if err != nil {
		t.Fatalf("buildSubprocessEnv: %v", err)
	}
	pathVal, ok := os.LookupEnv("PATH")
	if !ok {
		t.Skip("test host has no PATH; skipping")
	}
	if !slices.Contains(env, "PATH="+pathVal) {
		t.Errorf("PATH not present in env: %v", env)
	}
	if len(env) != 1 {
		t.Errorf("expected exactly 1 env entry (PATH), got %d: %v", len(env), env)
	}
}

func hasKey(env []string, key string) bool {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}

func hasKeyValue(env []string, key, value string) bool {
	return slices.Contains(env, key+"="+value)
}
