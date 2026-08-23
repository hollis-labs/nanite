package sandbox

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// skipIfNoOSSandbox skips a test on Linux when bwrap is unavailable and the
// AD-01 degraded-mode opt-in is not set (TASKS/audit-remediation/
// ARCHITECT-DECISIONS.md). Every test below that calls AgentExec (which
// unconditionally requires OS-level isolation, per AD-01's fail-closed
// default) needs this guard now that a missing bwrap is a hard error
// instead of a silent Tier-1-only fallback — these tests are about
// AgentExec's other guarantees (denylist, env filtering, CWD, timeout),
// not about proving isolation succeeded, so skipping here (rather than
// asserting the fail-closed error) is correct: os_linux_test.go's own
// TestApplyOSSandbox_FailsClosedWithoutBwrap /
// TestApplyOSSandbox_DegradesWithOptIn already cover that behavior
// directly. darwin is never affected (seatbelt always present); a Linux
// host WITH bwrap installed is never affected either.
func skipIfNoOSSandbox(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		return
	}
	if allowUnsandboxedExec() {
		return
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed and NANITE_ALLOW_UNSANDBOXED_AGENT_EXEC not set — AgentExec now fails closed (AD-01); see os_linux_test.go for the dedicated fail-closed/degraded regression tests")
	}
}

func TestAgentExec_BasicCommand(t *testing.T) {
	skipIfNoOSSandbox(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	result, err := AgentExec(AgentExecOpts{
		SessionID: "test-basic",
		Command:   "/bin/echo",
		Args:      []string{"hello"},
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("AgentExec() error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0; stderr: %s", result.ExitCode, result.Stderr)
	}
	if got := strings.TrimSpace(result.Stdout); got != "hello" {
		t.Errorf("stdout = %q, want %q", got, "hello")
	}
	if result.TimedOut {
		t.Error("unexpected timeout")
	}
}

func TestAgentExec_DenylistBlocked(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	_, err := AgentExec(AgentExecOpts{
		SessionID: "test-deny",
		Command:   "rm",
		Args:      []string{"-rf", "/"},
	})
	if err == nil {
		t.Fatal("expected error for denied command, got nil")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("error = %q, want it to contain 'denied'", err.Error())
	}
}

func TestAgentExec_Timeout(t *testing.T) {
	skipIfNoOSSandbox(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	result, err := AgentExec(AgentExecOpts{
		SessionID: "test-timeout",
		Command:   "/bin/sleep",
		Args:      []string{"10"},
		Timeout:   500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("AgentExec() error: %v", err)
	}
	if !result.TimedOut {
		t.Error("expected TimedOut = true")
	}
	if result.ExitCode != 124 {
		t.Errorf("exit code = %d, want 124", result.ExitCode)
	}
}

func TestAgentExec_EnvFiltering(t *testing.T) {
	skipIfNoOSSandbox(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("ANTHROPIC_API_KEY", "sk-secret-12345")

	result, err := AgentExec(AgentExecOpts{
		SessionID: "test-env",
		Command:   "/usr/bin/env",
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("AgentExec() error: %v", err)
	}
	if strings.Contains(result.Stdout, "sk-secret-12345") {
		t.Error("ANTHROPIC_API_KEY leaked to child process")
	}
	if strings.Contains(result.Stdout, "ANTHROPIC_API_KEY") {
		t.Error("ANTHROPIC_API_KEY env var name visible in child process")
	}
}

func TestAgentExec_CWDRestricted(t *testing.T) {
	skipIfNoOSSandbox(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	result, err := AgentExec(AgentExecOpts{
		SessionID: "test-cwd",
		Command:   "/bin/pwd",
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("AgentExec() error: %v", err)
	}

	sandboxDir, _ := Dir("test-cwd")
	got := strings.TrimSpace(result.Stdout)

	// On macOS, /var and /tmp may resolve through /private.
	if got != sandboxDir && !strings.HasSuffix(got, "/test-cwd") {
		t.Errorf("CWD = %q, want sandbox dir %q", got, sandboxDir)
	}
}

func TestAgentExec_ExtraEnvFiltered(t *testing.T) {
	skipIfNoOSSandbox(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	result, err := AgentExec(AgentExecOpts{
		SessionID: "test-extra-env",
		Command:   "/usr/bin/env",
		Timeout:   5 * time.Second,
		Env: map[string]string{
			"MY_VAR":            "safe-value",
			"MY_SECRET_KEY":     "substring-secret-value",
			"DATABASE_TOKEN":    "substring-token-value",
			"DATABASE_URL":      "database-url-secret-value",
			"REDIS_URL":         "redis-url-secret-value",
			"SLACK_WEBHOOK_URL": "slack-webhook-secret-value",
			"GH_PAT":            "github-pat-secret-value",
		},
	})
	if err != nil {
		t.Fatalf("AgentExec() error: %v", err)
	}
	if !strings.Contains(result.Stdout, "MY_VAR=safe-value") {
		t.Error("expected MY_VAR to be present")
	}
	if strings.Contains(result.Stdout, "substring-secret-value") {
		t.Error("MY_SECRET_KEY leaked to child process")
	}
	if strings.Contains(result.Stdout, "substring-token-value") {
		t.Error("DATABASE_TOKEN leaked to child process")
	}
	for _, secretValue := range []string{
		"database-url-secret-value",
		"redis-url-secret-value",
		"slack-webhook-secret-value",
		"github-pat-secret-value",
	} {
		if strings.Contains(result.Stdout, secretValue) {
			t.Errorf("AgentExec output contains blocked credential value %q", secretValue)
		}
	}
}

func TestUserExec_BasicCommand(t *testing.T) {
	result, err := UserExec(UserExecOpts{
		Command: "/bin/echo",
		Args:    []string{"hello from user"},
		Dir:     os.TempDir(),
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("UserExec() error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}
	if got := strings.TrimSpace(result.Stdout); got != "hello from user" {
		t.Errorf("stdout = %q, want %q", got, "hello from user")
	}
}

func TestUserExec_DenylistBlocked(t *testing.T) {
	_, err := UserExec(UserExecOpts{
		Command: "rm",
		Args:    []string{"-rf", "/"},
		Dir:     os.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for denied command, got nil")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("error = %q, want it to contain 'denied'", err.Error())
	}
}

func TestUserExec_UsesRealDir(t *testing.T) {
	dir := t.TempDir()

	result, err := UserExec(UserExecOpts{
		Command: "/bin/pwd",
		Dir:     dir,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("UserExec() error: %v", err)
	}

	got := strings.TrimSpace(result.Stdout)
	// On macOS, temp dirs may resolve through /private.
	if got != dir && !strings.HasPrefix(got, "/private"+dir) {
		t.Errorf("CWD = %q, want %q (or /private%s)", got, dir, dir)
	}
}

func TestCheckDenylist(t *testing.T) {
	// Blocked commands.
	blocked := []string{
		"rm -rf /",
		"rm -rf /*",
		"sudo rm -rf /",
		"mkfs.ext4 /dev/sda1",
		"dd if=/dev/zero of=/dev/sda",
		"shutdown -h now",
		":(){ :|:& };:",
	}
	for _, cmd := range blocked {
		if b, _ := CheckDenylist(cmd); !b {
			t.Errorf("CheckDenylist(%q) = false, want true (blocked)", cmd)
		}
	}

	// Allowed commands.
	allowed := []string{
		"ls -la",
		"git status",
		"echo hello",
		"rm -rf ./build",
		"cat /etc/hosts",
		"go build ./...",
	}
	for _, cmd := range allowed {
		if b, reason := CheckDenylist(cmd); b {
			t.Errorf("CheckDenylist(%q) = true (%s), want false (allowed)", cmd, reason)
		}
	}
}

func TestIsSecretKey(t *testing.T) {
	secrets := []string{
		"ANTHROPIC_API_KEY",
		"AWS_SECRET_ACCESS_KEY",
		"GITHUB_TOKEN",
		"DATABASE_PASSWORD",
		"MY_CREDENTIAL",
		"BASIC_AUTH_HEADER",
	}
	for _, k := range secrets {
		if !isSecretKey(k) {
			t.Errorf("isSecretKey(%q) = false, want true", k)
		}
	}

	safe := []string{
		"HOME",
		"USER",
		"PATH",
		"LANG",
		"TERM",
		"GOPATH",
		"NODE_ENV",
	}
	for _, k := range safe {
		if isSecretKey(k) {
			t.Errorf("isSecretKey(%q) = true, want false", k)
		}
	}
}

func TestBuildAgentEnv_FiltersKnownCredentialNames(t *testing.T) {
	credentialKeys := []string{
		"DATABASE_URL",
		"REDIS_URL",
		"SLACK_WEBHOOK_URL",
		"GH_PAT",
		"AMQP_URL",
		"ELASTICSEARCH_URL",
		"MONGODB_URI",
		"DISCORD_WEBHOOK_URL",
		"SUPPORT_DATABASE_URL",
	}
	extra := map[string]string{"SERVICE_URL": "https://service.example.test"}
	for _, key := range credentialKeys {
		extra[key] = "must-not-reach-agent"
	}

	got := envMap(buildAgentEnv(extra))
	if got["SERVICE_URL"] != "https://service.example.test" {
		t.Errorf("SERVICE_URL = %q, want safe overlay value", got["SERVICE_URL"])
	}
	for _, key := range credentialKeys {
		if value, ok := got[key]; ok {
			t.Errorf("buildAgentEnv retained blocked credential %s=%q", key, value)
		}
	}
}

func TestFilterSecrets_UserExecSemanticsUnchanged(t *testing.T) {
	input := []string{
		"DATABASE_URL=postgres://user:password@example.test/db",
		"REDIS_URL=redis://:password@example.test/0",
		"SLACK_WEBHOOK_URL=https://hooks.slack.example.test/credential",
		"GH_PAT=github-personal-access-token",
		"GITHUB_TOKEN=must-still-be-filtered",
	}

	got := envMap(filterSecrets(input))
	for _, key := range []string{"DATABASE_URL", "REDIS_URL", "SLACK_WEBHOOK_URL", "GH_PAT"} {
		if _, ok := got[key]; !ok {
			t.Errorf("filterSecrets removed %s; UserExec must retain its existing substring-only behavior", key)
		}
	}
	if _, ok := got["GITHUB_TOKEN"]; ok {
		t.Error("filterSecrets retained GITHUB_TOKEN; existing UserExec secret filtering regressed")
	}
}

func TestIsAgentSecretKey(t *testing.T) {
	tests := map[string]bool{
		"database_url":         true,
		"REDIS_URL":            true,
		"Slack_Webhook_Url":    true,
		"GH_PAT":               true,
		"AMQP_URL":             true,
		"ELASTICSEARCH_URL":    true,
		"MONGODB_URI":          true,
		"DISCORD_WEBHOOK_URL":  true,
		"SUPPORT_DATABASE_URL": true,
		"MY_API_KEY":           true,
		"SERVICE_URL":          false,
		"DATABASE_HOST":        false,
	}
	for key, want := range tests {
		if got := isAgentSecretKey(key); got != want {
			t.Errorf("isAgentSecretKey(%q) = %t, want %t", key, got, want)
		}
	}
}

func envMap(environ []string) map[string]string {
	result := make(map[string]string, len(environ))
	for _, entry := range environ {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			result[key] = value
		}
	}
	return result
}

// TestAgentExec_HonorsWorkingDir (CW-20260504-0003) — when WorkingDir is
// non-empty, AgentExec runs the command with cmd.Dir = WorkingDir
// instead of the sandbox scoping dir. Verified by /bin/pwd: caller-set
// dir wins over sandboxDir. The OS sandbox profile is widened to permit
// reads under that dir (already true on darwin; bind-mounted on linux).
func TestAgentExec_HonorsWorkingDir(t *testing.T) {
	skipIfNoOSSandbox(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	workDir := t.TempDir()
	result, err := AgentExec(AgentExecOpts{
		SessionID:  "test-workingdir",
		Command:    "/bin/pwd",
		Timeout:    5 * time.Second,
		WorkingDir: workDir,
	})
	if err != nil {
		t.Fatalf("AgentExec: %v", err)
	}
	got := strings.TrimSpace(result.Stdout)
	// macOS may resolve /var → /private/var; accept either.
	if got != workDir && !strings.HasSuffix(got, strings.TrimPrefix(workDir, "/private")) {
		t.Errorf("CWD = %q, want WorkingDir %q", got, workDir)
	}
}

// TestAgentExec_EmptyWorkingDir_FallsBackToSandboxDir verifies the
// back-compat path: when WorkingDir is empty (legacy callers), cmd.Dir
// is the sandbox scoping dir as before. Same shape as
// TestAgentExec_CWDRestricted but with the field explicitly empty so
// the regression is locked against future field additions.
func TestAgentExec_EmptyWorkingDir_FallsBackToSandboxDir(t *testing.T) {
	skipIfNoOSSandbox(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	result, err := AgentExec(AgentExecOpts{
		SessionID:  "test-empty-workdir",
		Command:    "/bin/pwd",
		Timeout:    5 * time.Second,
		WorkingDir: "", // explicit empty
	})
	if err != nil {
		t.Fatalf("AgentExec: %v", err)
	}
	sandboxDir, _ := Dir("test-empty-workdir")
	got := strings.TrimSpace(result.Stdout)
	if got != sandboxDir && !strings.HasSuffix(got, "/test-empty-workdir") {
		t.Errorf("CWD = %q, want sandbox dir %q (legacy fallback)", got, sandboxDir)
	}
}

// TestAgentExec_WorkingDir_AllowsReads (CW-20260504-0003) — when
// WorkingDir is set, the OS sandbox profile is widened so the command
// can read files under that dir. We write a fixture file in workDir
// then cat it; success proves the read-allow path works.
func TestAgentExec_WorkingDir_AllowsReads(t *testing.T) {
	skipIfNoOSSandbox(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	workDir := t.TempDir()
	fixturePath := workDir + "/hello.txt"
	if err := os.WriteFile(fixturePath, []byte("hi from workdir\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	result, err := AgentExec(AgentExecOpts{
		SessionID:  "test-workdir-read",
		Command:    "/bin/cat",
		Args:       []string{fixturePath},
		Timeout:    5 * time.Second,
		WorkingDir: workDir,
	})
	if err != nil {
		t.Fatalf("AgentExec: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0; stderr: %s", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "hi from workdir") {
		t.Errorf("stdout = %q, want it to contain the fixture content", result.Stdout)
	}
}
