package mcp

import (
	"os"
	"slices"
	"strings"
	"testing"

	gmcpclient "github.com/hollis-labs/go-mcp/client"
)

// buildStdioEnv resolves and flattens a stdio server's env exactly the way
// AddStdioServer + naniteCommandEnv do at connect time, without needing a
// real go-mcp/client.Pool -- the resolve/flatten/PATH-check pipeline is the
// thing under test, not the dial itself.
func buildStdioEnv(t *testing.T, command string, env []string, envAllowlist []string) ([]string, error) {
	t.Helper()
	cfg := gmcpclient.ServerConfig{
		Command: command,
		Env:     resolveStdioEnv(env, envAllowlist),
	}
	return naniteCommandEnv(cfg)
}

// TestStdioEnv_FailsWithoutPath confirms the connect-time loud-fail behavior
// (S4b D6): if the allowlist omits PATH, naniteCommandEnv must return an
// error rather than silently launching with an empty PATH and producing an
// opaque `exec: "…": file not found` later.
func TestStdioEnv_FailsWithoutPath(t *testing.T) {
	if _, err := buildStdioEnv(t, "echo", nil, []string{"HOME", "USER"}); err == nil {
		t.Fatal("expected error when PATH not in allowlist, got nil")
	} else if !strings.Contains(err.Error(), "PATH") {
		t.Errorf("error should mention PATH: %v", err)
	}
}

// TestStdioEnv_FiltersHostEnv verifies that only the allowlisted keys are
// inherited from the host, and that MCPServerConfig.Env (trusted
// user-declared entries) passes through as-is regardless of the allowlist.
// Closes finding 10.
func TestStdioEnv_FiltersHostEnv(t *testing.T) {
	// Set two host env vars: one allowlisted, one not.
	t.Setenv("NANITE_TEST_ALLOWLISTED", "yes")
	t.Setenv("NANITE_TEST_SECRET", "SHOULD_NOT_APPEAR")

	env, err := buildStdioEnv(t, "/bin/echo",
		[]string{"NANITE_EXPLICIT=on"}, // trusted user-declared
		[]string{"PATH", "NANITE_TEST_ALLOWLISTED"},
	)
	if err != nil {
		t.Fatalf("buildStdioEnv: %v", err)
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

// TestStdioEnv_EmptyDefaultFailsClosed proves the zero-value allowlist
// (nil/empty) fails closed — no keys inherited, PATH missing → error.
// Users must explicitly opt into inheritance.
func TestStdioEnv_EmptyDefaultFailsClosed(t *testing.T) {
	if _, err := buildStdioEnv(t, "whatever", nil, nil); err == nil {
		t.Fatal("expected PATH-missing error with nil allowlist")
	}

	// Overriding with ["PATH"] only should succeed and yield exactly one
	// inherited entry (PATH) + no user-declared env.
	env, err := buildStdioEnv(t, "whatever", nil, []string{"PATH"})
	if err != nil {
		t.Fatalf("buildStdioEnv: %v", err)
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

// TestStdioEnv_PathQualifiedCommand covers the Copilot review finding: an
// absolute- or relative-path command does not need PATH from either the
// allowlist or the user-declared env, because exec.LookPath is bypassed
// when the command contains a path separator.
func TestStdioEnv_PathQualifiedCommand(t *testing.T) {
	for _, cmd := range []string{"/bin/echo", "./local-mcp", "../parent-mcp"} {
		env, err := buildStdioEnv(t, cmd, nil, nil)
		if err != nil {
			t.Errorf("path-qualified %q: unexpected error: %v", cmd, err)
			continue
		}
		if len(env) != 0 {
			t.Errorf("path-qualified %q: env should be empty, got %v", cmd, env)
		}
	}
}

// TestStdioEnv_ExplicitPathInEnv covers the Copilot finding's other branch:
// an operator who wants to pin a custom PATH for a specific server can do so
// via MCPServerConfig.Env, and naniteCommandEnv must accept that without
// also requiring PATH in the allowlist.
func TestStdioEnv_ExplicitPathInEnv(t *testing.T) {
	env, err := buildStdioEnv(t, "bare-server",
		[]string{"PATH=/usr/local/sbin:/usr/local/bin"},
		nil, // no allowlist at all
	)
	if err != nil {
		t.Fatalf("buildStdioEnv: %v", err)
	}
	if !hasKeyValue(env, "PATH", "/usr/local/sbin:/usr/local/bin") {
		t.Errorf("explicit PATH missing from env: %v", env)
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
