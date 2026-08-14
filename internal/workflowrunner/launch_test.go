package workflowrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
)

// skipIfNoPython3 skips the test if python3 is not on PATH — mirrors
// internal/mcp/self_tools_python_test.go's guard so this package's tests
// degrade the same way in an environment without a Python interpreter.
func skipIfNoPython3(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found on PATH — skipping workflowrunner launch tests")
	}
}

func baseConfig(t *testing.T, scriptPath string) Config {
	t.Helper()
	return Config{
		PythonPath:       "python3",
		ScriptPath:       scriptPath,
		NaniteBinaryPath: "/usr/bin/true", // never actually invoked by these tests
		DBPath:           filepath.Join(t.TempDir(), "test.db"),
		SessionID:        "sess-test",
		APIBaseURL:       "http://127.0.0.1:9999",
	}
}

// writeScript writes a python script fixture to a temp dir and returns
// its path.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "runner.py")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

func TestLaunch_PlantsMCPJSONAndInputFile(t *testing.T) {
	skipIfNoPython3(t)

	script := writeScript(t, `
import json, sys, os
input_path = sys.argv[1]
with open(input_path) as f:
    payload = json.load(f)
mcp_path = os.path.join(os.path.dirname(input_path), ".mcp.json")
with open(mcp_path) as f:
    mcp_cfg = json.load(f)
print(json.dumps({"input": payload, "mcp": mcp_cfg}))
`)

	cfg := baseConfig(t, script)
	cfg.KeepWorkDir = true

	result, err := Launch(context.Background(), cfg, agentworkflow.WorkflowInput{
		Params: map[string]any{"greeting": "hello"},
	})
	if err != nil {
		t.Fatalf("Launch returned err: %v (stderr=%s)", err, result.Stderr)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%s)", result.ExitCode, result.Stderr)
	}

	var out struct {
		Input map[string]any `json:"input"`
		MCP   struct {
			MCPServers map[string]struct {
				Command string         `json:"command"`
				Args    []string       `json:"args"`
				Env     map[string]any `json:"env"`
			} `json:"mcpServers"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &out); err != nil {
		t.Fatalf("stdout not valid JSON (%q): %v", result.Stdout, err)
	}

	params, _ := out.Input["params"].(map[string]any)
	if params == nil || params["greeting"] != "hello" {
		t.Fatalf("workflow input not planted correctly: %+v", out.Input)
	}

	nanite, ok := out.MCP.MCPServers["nanite"]
	if !ok {
		t.Fatalf(".mcp.json missing 'nanite' server entry: %+v", out.MCP)
	}
	if nanite.Command != cfg.NaniteBinaryPath {
		t.Fatalf("command mismatch: got %q want %q", nanite.Command, cfg.NaniteBinaryPath)
	}
	wantArgs := []string{"mcp", "--db", cfg.DBPath, "--session", cfg.SessionID}
	if len(nanite.Args) != len(wantArgs) {
		t.Fatalf("args mismatch: got %v want %v", nanite.Args, wantArgs)
	}
	for i, a := range wantArgs {
		if nanite.Args[i] != a {
			t.Fatalf("args mismatch at %d: got %v want %v", i, nanite.Args, wantArgs)
		}
	}
	if nanite.Env["NANITE_API_URL"] != cfg.APIBaseURL {
		t.Fatalf("NANITE_API_URL not planted: %+v", nanite.Env)
	}
	if got, want := nanite.Env[ToolAllowlistEnvVar], strings.Join(CallbackToolNames, ","); got != want {
		t.Fatalf("%s = %v, want %q", ToolAllowlistEnvVar, got, want)
	}

	// KeepWorkDir=true — the planted files should still be on disk.
	if _, err := os.Stat(filepath.Join(result.WorkDir, ".mcp.json")); err != nil {
		t.Fatalf("expected .mcp.json to remain on disk: %v", err)
	}
}

func TestLaunch_RunsInWorkDirAndCleansUpByDefault(t *testing.T) {
	skipIfNoPython3(t)

	script := writeScript(t, `
import os
print(os.getcwd())
`)

	cfg := baseConfig(t, script)
	// KeepWorkDir left false (default) — an auto-created WorkDir must be
	// removed after the run.

	result, err := Launch(context.Background(), cfg, agentworkflow.WorkflowInput{})
	if err != nil {
		t.Fatalf("Launch returned err: %v", err)
	}
	// Compare basenames rather than full paths — on macOS $TMPDIR is a
	// symlink (/var/folders/... -> /private/var/folders/...) that the
	// child process's getcwd() resolves but our own os.MkdirTemp result
	// does not; the auto-created dir is also already removed by the time
	// Launch returns, so we can't resolve it after the fact either.
	// os.MkdirTemp's unique suffix on the "nanite-workflowrunner-*"
	// pattern makes the basename an unambiguous identifier.
	cwd := strings.TrimSpace(result.Stdout)
	if filepath.Base(cwd) != filepath.Base(result.WorkDir) {
		t.Fatalf("script did not run in the planted work dir: cwd=%q workDir=%q", cwd, result.WorkDir)
	}

	if _, err := os.Stat(result.WorkDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected auto-created work dir to be removed after the run, stat err=%v", err)
	}
}

func TestLaunch_NonZeroExitIsNotAGoError(t *testing.T) {
	skipIfNoPython3(t)

	script := writeScript(t, `
import sys
sys.stderr.write("boom\n")
sys.exit(7)
`)

	cfg := baseConfig(t, script)
	result, err := Launch(context.Background(), cfg, agentworkflow.WorkflowInput{})
	if err != nil {
		t.Fatalf("Launch returned err for a plain non-zero exit: %v", err)
	}
	if result.ExitCode != 7 {
		t.Fatalf("expected ExitCode=7, got %d", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "boom") {
		t.Fatalf("expected stderr to be captured, got %q", result.Stderr)
	}
}

func TestLaunch_TimeoutIsReportedAndErrored(t *testing.T) {
	skipIfNoPython3(t)

	script := writeScript(t, `
import time
time.sleep(5)
`)

	cfg := baseConfig(t, script)
	cfg.Timeout = 100 * time.Millisecond

	result, err := Launch(context.Background(), cfg, agentworkflow.WorkflowInput{})
	if err == nil {
		t.Fatal("expected an error on timeout")
	}
	if !result.TimedOut {
		t.Fatalf("expected Result.TimedOut=true, got %+v", result)
	}
}

// mustExitError runs a shell command that exits non-zero and returns the
// resulting *exec.ExitError, for constructing realistic runErr values in
// table tests without depending on a specific interpreter.
func mustExitError(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	if err == nil {
		t.Fatalf("expected a non-zero exit, got nil error")
	}
	return err
}

// TestClassifyRunResult covers Launch's post-run decision logic in
// isolation — in particular the caller-cancellation race Copilot flagged
// in review: a subprocess that already exited cleanly (runErr == nil)
// must report success even if the caller's ambient parentCtx happens to
// be cancelled (for reasons unrelated to this subprocess) at the same
// moment, rather than surfacing a spurious launch-level error over an
// otherwise-valid Result.
func TestClassifyRunResult(t *testing.T) {
	cases := []struct {
		name         string
		runErr       error
		runCtxErr    error
		parentCtxErr error
		wantTimedOut bool
		wantErr      bool
	}{
		{
			name:    "clean exit, no cancellation anywhere",
			runErr:  nil,
			wantErr: false,
		},
		{
			// The exact race from review: the subprocess already
			// finished successfully, but the caller's own ambient
			// context happens to be cancelled at the same instant for
			// unrelated reasons. Must NOT be reported as a failure.
			name:         "clean exit despite a concurrently-cancelled parent ctx",
			runErr:       nil,
			parentCtxErr: context.Canceled,
			wantErr:      false,
		},
		{
			name:      "clean exit despite an expired run ctx deadline",
			runErr:    nil,
			runCtxErr: context.DeadlineExceeded,
			wantErr:   false,
		},
		{
			name:         "run ctx deadline exceeded with a real run error",
			runErr:       mustExitError(t, 1),
			runCtxErr:    context.DeadlineExceeded,
			wantTimedOut: true,
			wantErr:      true,
		},
		{
			name:         "parent ctx cancelled with a real run error",
			runErr:       mustExitError(t, 1),
			parentCtxErr: context.Canceled,
			wantErr:      true,
		},
		{
			name:    "plain non-zero exit, no cancellation",
			runErr:  mustExitError(t, 7),
			wantErr: false,
		},
		{
			name:    "spawn failure (not an *exec.ExitError)",
			runErr:  errors.New("fork/exec: no such file or directory"),
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			timedOut, err := classifyRunResult(tc.runErr, tc.runCtxErr, tc.parentCtxErr, "script.py", time.Second)
			if timedOut != tc.wantTimedOut {
				t.Errorf("timedOut = %v, want %v", timedOut, tc.wantTimedOut)
			}
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

// TestRenderMCPJSON_PlantsToolAllowlist verifies the .mcp.json this package
// hands a workflow-runner subprocess always carries
// NANITE_MCP_TOOL_ALLOWLIST restricted to exactly the three workflow
// callback tools — regardless of whether APIBaseURL is set — so `nanite
// mcp` (cmd/nanite/main.go's cmdMCPServe) has the signal it needs to scope
// the self-tool catalog down (CW-20260814-0006). The registration-time
// enforcement itself lives in internal/mcpserver; this test only proves
// the launch config side plants the right value.
func TestRenderMCPJSON_PlantsToolAllowlist(t *testing.T) {
	cfg := Config{
		NaniteBinaryPath: "/usr/bin/true",
		DBPath:           filepath.Join(t.TempDir(), "test.db"),
		SessionID:        "sess-test",
		// APIBaseURL deliberately left empty — the allowlist must be
		// planted unconditionally, not only alongside NANITE_API_URL.
	}

	raw, err := renderMCPJSON(cfg)
	if err != nil {
		t.Fatalf("renderMCPJSON: %v", err)
	}

	var out struct {
		MCPServers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("mcp.json not valid JSON: %v", err)
	}

	nanite, ok := out.MCPServers[DefaultMCPServerID]
	if !ok {
		t.Fatalf(".mcp.json missing %q server entry: %+v", DefaultMCPServerID, out.MCPServers)
	}

	got := nanite.Env[ToolAllowlistEnvVar]
	want := strings.Join(CallbackToolNames, ",")
	if got != want {
		t.Fatalf("%s = %q, want %q", ToolAllowlistEnvVar, got, want)
	}
	for _, name := range []string{"workflow_execute_llm_step", "workflow_execute_tool_step", "workflow_verify_step"} {
		if !strings.Contains(got, name) {
			t.Errorf("allowlist %q missing expected tool %q", got, name)
		}
	}
}

func TestLaunch_MissingRequiredConfigFieldsFailFast(t *testing.T) {
	_, err := Launch(context.Background(), Config{}, agentworkflow.WorkflowInput{})
	if err == nil {
		t.Fatal("expected an error for an empty Config")
	}
}

func TestLaunch_OutputTruncation(t *testing.T) {
	skipIfNoPython3(t)

	script := writeScript(t, `
print("x" * 100)
`)

	cfg := baseConfig(t, script)
	cfg.MaxOutputBytes = 10

	result, err := Launch(context.Background(), cfg, agentworkflow.WorkflowInput{})
	if err != nil {
		t.Fatalf("Launch returned err: %v", err)
	}
	if !result.StdoutTruncated {
		t.Fatal("expected StdoutTruncated=true")
	}
	if len(result.Stdout) != 10 {
		t.Fatalf("expected stdout capped at 10 bytes, got %d", len(result.Stdout))
	}
}
