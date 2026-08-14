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
// these tests exercise ExternalWorkflowEngine.Run end to end through a real
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

// TestExternalWorkflowEngine_Run_Success proves a clean-exit subprocess maps
// onto RunStatusCompleted, an empty RunID (external engines don't persist a
// workflow_runs row), and the subprocess's stdout captured under the
// synthetic "run" StepResult.
func TestExternalWorkflowEngine_Run_Success(t *testing.T) {
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

	wf := agentworkflow.WorkflowDefinition{Name: "langgraph-workflow", Engine: agentworkflow.EngineLangGraph}
	result, err := e.Run(context.Background(), wf, agentworkflow.WorkflowInput{Params: map[string]any{"greeting": "hi"}}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.RunID != "" {
		t.Errorf("RunID = %q, want empty (external engines don't persist a run row)", result.RunID)
	}
	if result.Status != agentworkflow.RunStatusCompleted {
		t.Errorf("Status = %q, want completed", result.Status)
	}
	sr, ok := result.StepResults[externalRunStepID]
	if !ok {
		t.Fatalf("StepResults[%q] missing: %+v", externalRunStepID, result.StepResults)
	}
	if sr.IsError {
		t.Errorf("StepResults[%q].IsError = true, want false", externalRunStepID)
	}
	if !strings.Contains(sr.Output, `"greeting": "hi"`) {
		t.Errorf("Output = %q, want it to contain the round-tripped params", sr.Output)
	}
}

// TestExternalWorkflowEngine_Run_NonZeroExit proves a non-zero exit is a
// normal (non-Go-error) RunStatusFailed outcome, with stderr folded into
// the step's Output for debuggability — never a Go error, matching
// workflowrunner.Launch's own "non-zero exit is not a Go error" contract.
func TestExternalWorkflowEngine_Run_NonZeroExit(t *testing.T) {
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

	wf := agentworkflow.WorkflowDefinition{Name: "langgraph-workflow", Engine: agentworkflow.EngineLangGraph}
	result, err := e.Run(context.Background(), wf, agentworkflow.WorkflowInput{}, nil)
	if err != nil {
		t.Fatalf("Run returned a Go error for a plain non-zero exit: %v", err)
	}
	if result.Status != agentworkflow.RunStatusFailed {
		t.Errorf("Status = %q, want failed", result.Status)
	}
	if result.Error == "" {
		t.Error("Error is empty, want a failure message")
	}
	sr := result.StepResults[externalRunStepID]
	if !sr.IsError {
		t.Error("StepResults[run].IsError = false, want true")
	}
	if !strings.Contains(sr.Output, "boom") {
		t.Errorf("Output = %q, want stderr (\"boom\") folded in", sr.Output)
	}
}

// TestExternalWorkflowEngine_Run_LaunchFailureIsGoError proves a
// launch-level failure (a PythonPath that doesn't resolve to a real
// interpreter — a spawn failure, distinct from a script that fails once
// running) surfaces as a Go error, not a WorkflowResult — the same
// infra-vs-semantic-failure split BuiltinWorkflowEngine.Run follows,
// load-bearing for WorkflowLauncher.Launch's shared finalize-on-any-error
// handling.
func TestExternalWorkflowEngine_Run_LaunchFailureIsGoError(t *testing.T) {
	script := writeExternalEngineScript(t, `print("unreachable")`)
	cfg := baseExternalEngineConfig(t, script)
	cfg.PythonPath = filepath.Join(t.TempDir(), "no-such-interpreter")
	e, err := NewExternalWorkflowEngine(cfg)
	if err != nil {
		t.Fatalf("NewExternalWorkflowEngine: %v", err)
	}

	wf := agentworkflow.WorkflowDefinition{Name: "langgraph-workflow", Engine: agentworkflow.EngineLangGraph}
	result, runErr := e.Run(context.Background(), wf, agentworkflow.WorkflowInput{}, nil)
	if runErr == nil {
		t.Fatal("expected a Go error for a missing script path")
	}
	if result.Status != "" || result.StepResults != nil {
		t.Errorf("result = %+v, want zero-value WorkflowResult on infra error", result)
	}
}
