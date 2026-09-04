package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
)

// skipIfNoPython3 mirrors internal/workflowrunner/launch_test.go's guard —
// these tests exercise ExternalWorkflowEngine.ExecuteWorkflowStep end to end through a real
// subprocess, same as workflowrunner's own tests do.
func skipIfNoPython3(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found on PATH — skipping ExternalWorkflowEngine tests")
	}
}

func writeExternalEngineScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "runner.py")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

func baseExternalEngineConfig(t *testing.T, scriptPath string) ExternalWorkflowEngineConfig {
	t.Helper()
	return ExternalWorkflowEngineConfig{
		EngineName:       agentworkflow.EngineLangGraph,
		PythonPath:       "python3",
		ScriptPath:       scriptPath,
		NaniteBinaryPath: "/usr/bin/true", // never actually invoked by these tests
		DBPath:           filepath.Join(t.TempDir(), "test.db"),
		APIBaseURL:       "http://127.0.0.1:9999",
	}
}

func TestNewExternalWorkflowEngine_RequiresConfig(t *testing.T) {
	if _, err := NewExternalWorkflowEngine(ExternalWorkflowEngineConfig{}); err == nil {
		t.Fatal("expected an error for an empty config")
	}
}

func TestExternalWorkflowEngine_Name(t *testing.T) {
	e, err := NewExternalWorkflowEngine(baseExternalEngineConfig(t, "unused.py"))
	if err != nil {
		t.Fatalf("NewExternalWorkflowEngine: %v", err)
	}
	if e.Name() != agentworkflow.EngineLangGraph {
		t.Fatalf("Name() = %q, want %q", e.Name(), agentworkflow.EngineLangGraph)
	}
}

func TestExternalWorkflowEngine_ExecuteWorkflowStep_Success(t *testing.T) {
	skipIfNoPython3(t)

	script := writeExternalEngineScript(t, `
import json, sys
with open(sys.argv[1]) as f:
    payload = json.load(f)
print(json.dumps({"received_params": payload.get("params", {})}))
`)
	e, err := NewExternalWorkflowEngine(baseExternalEngineConfig(t, script))
	if err != nil {
		t.Fatalf("NewExternalWorkflowEngine: %v", err)
	}

	result, err := e.ExecuteWorkflowStep(context.Background(), "run:external", "langgraph-workflow", map[string]any{"greeting": "hi"}, "")
	if err != nil {
		t.Fatalf("ExecuteWorkflowStep: %v", err)
	}
	if result.IsError {
		t.Fatal("IsError = true, want false")
	}
	if !strings.Contains(result.Output, `"greeting": "hi"`) || !strings.Contains(result.Output, `"_nanite_idempotency_key": "run:external"`) {
		t.Errorf("Output = %q, want params and stable key", result.Output)
	}
}

// TestExternalWorkflowEngine_ExecuteWorkflowStep_NonZeroExit proves a non-zero exit is a
// normal (non-Go-error) step failure, with stderr folded into
// the step's Output for debuggability — never a Go error, matching
// workflowrunner.Launch's own "non-zero exit is not a Go error" contract.
func TestExternalWorkflowEngine_ExecuteWorkflowStep_NonZeroExit(t *testing.T) {
	skipIfNoPython3(t)

	script := writeExternalEngineScript(t, `
import sys
sys.stderr.write("boom\n")
sys.exit(3)
`)
	e, err := NewExternalWorkflowEngine(baseExternalEngineConfig(t, script))
	if err != nil {
		t.Fatalf("NewExternalWorkflowEngine: %v", err)
	}

	result, err := e.ExecuteWorkflowStep(context.Background(), "run:external", "langgraph-workflow", nil, "")
	if err != nil {
		t.Fatalf("Run returned a Go error for a plain non-zero exit: %v", err)
	}
	if !result.IsError {
		t.Error("IsError = false, want true")
	}
	if !strings.Contains(result.Output, "boom") {
		t.Errorf("Output = %q, want stderr (\"boom\") folded in", result.Output)
	}
}

// TestExternalWorkflowEngine_ExecuteWorkflowStep_LaunchFailureIsGoError proves a
// launch-level failure (a PythonPath that doesn't resolve to a real
// interpreter — a spawn failure, distinct from a script that fails once
// running) surfaces as a Go error rather than a semantic step failure.
func TestExternalWorkflowEngine_ExecuteWorkflowStep_LaunchFailureIsGoError(t *testing.T) {
	script := writeExternalEngineScript(t, `print("unreachable")`)
	cfg := baseExternalEngineConfig(t, script)
	cfg.PythonPath = filepath.Join(t.TempDir(), "no-such-interpreter")
	e, err := NewExternalWorkflowEngine(cfg)
	if err != nil {
		t.Fatalf("NewExternalWorkflowEngine: %v", err)
	}

	result, runErr := e.ExecuteWorkflowStep(context.Background(), "run:external", "langgraph-workflow", nil, "")
	if runErr == nil {
		t.Fatal("expected a Go error for a missing script path")
	}
	if result != (ExternalWorkflowStepResult{}) {
		t.Errorf("result = %+v, want zero-value step result on infra error", result)
	}
}
