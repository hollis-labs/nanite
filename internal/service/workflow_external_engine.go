package service

import (
	"context"
	"fmt"
	"time"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/workflowrunner"
)

// externalRunStepID is the synthetic StepResult key an ExternalWorkflowEngine
// reports its run under. External engines have no per-step granularity
// visible to Go — LangGraph/CrewAI's internal node/task structure is opaque
// to Nanite (design doc, POC scope: no compiler from Nanite's step format to
// theirs) — so the whole subprocess run is reported as exactly one step.
const externalRunStepID = "run"

// ExternalWorkflowEngineConfig configures one ExternalWorkflowEngine — one
// instance per external framework (langgraph, crewai), each pinned to its
// own hand-authored runner script.
type ExternalWorkflowEngineConfig struct {
	// EngineName is this engine's identity — the value a
	// WorkflowDefinition.Engine field selects against
	// (agentworkflow.EngineLangGraph / EngineCrewAI). Required.
	EngineName string

	// PythonPath is the interpreter to run (e.g. "python3", or a venv's
	// absolute path). Required.
	PythonPath string
	// ScriptPath is this engine's runner script — a materialized copy of
	// one of internal/workflowrunner's embedded scripts. Required.
	ScriptPath string
	// ExtraArgs are appended to argv after ScriptPath, before the input
	// file path.
	ExtraArgs []string

	// NaniteBinaryPath, DBPath, APIBaseURL, MCPServerID configure the
	// planted .mcp.json — see workflowrunner.Config's identically-named
	// fields. NaniteBinaryPath and DBPath are required.
	NaniteBinaryPath string
	DBPath           string
	APIBaseURL       string
	MCPServerID      string

	// Timeout bounds the whole one-shot run. 0 uses
	// workflowrunner.DefaultTimeout.
	Timeout time.Duration
	// MaxOutputBytes caps captured stdout/stderr per stream. 0 uses
	// workflowrunner.DefaultMaxOutputBytes.
	MaxOutputBytes int
	// Env carries additional "KEY=VALUE" entries for the subprocess.
	Env []string
}

func (cfg ExternalWorkflowEngineConfig) validate() error {
	if cfg.EngineName == "" {
		return fmt.Errorf("workflow: ExternalWorkflowEngineConfig.EngineName is required")
	}
	if cfg.PythonPath == "" {
		return fmt.Errorf("workflow: ExternalWorkflowEngineConfig.PythonPath is required")
	}
	if cfg.ScriptPath == "" {
		return fmt.Errorf("workflow: ExternalWorkflowEngineConfig.ScriptPath is required")
	}
	if cfg.NaniteBinaryPath == "" {
		return fmt.Errorf("workflow: ExternalWorkflowEngineConfig.NaniteBinaryPath is required")
	}
	if cfg.DBPath == "" {
		return fmt.Errorf("workflow: ExternalWorkflowEngineConfig.DBPath is required")
	}
	return nil
}

// ExternalWorkflowEngine is the agentworkflow.WorkflowEngine adapter for a
// one-shot external Python framework (LangGraph, CrewAI), launched via
// internal/workflowrunner.Launch (design doc, "How external engines
// integrate").
//
// Run's own `exec agentworkflow.StepExecutor` parameter is deliberately
// unused: this engine's real work never calls it directly (the built-in
// engine's in-process shortcut). Instead, the spawned subprocess reaches
// the exact same StepExecutor instance indirectly, through the
// workflow_execute_llm_step / workflow_execute_tool_step /
// workflow_verify_step MCP tools — those tools are thin wrappers over
// selfTools.WorkflowExecutor, wired to the same instance passed as exec
// here at the composition root (cmd/nanite/main.go). "Engines only
// sequence, harness always executes" holds identically for both paths
// because both paths terminate in the same Go value, not merely
// equivalent code.
type ExternalWorkflowEngine struct {
	cfg ExternalWorkflowEngineConfig
}

// NewExternalWorkflowEngine constructs an ExternalWorkflowEngine. Returns
// an error (not a panic) on missing required config — a startup wiring
// bug, surfaced the same way NewWorkflowLauncher's dependency checks are.
func NewExternalWorkflowEngine(cfg ExternalWorkflowEngineConfig) (*ExternalWorkflowEngine, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &ExternalWorkflowEngine{cfg: cfg}, nil
}

var _ agentworkflow.WorkflowEngine = (*ExternalWorkflowEngine)(nil)

// Name identifies this engine — see ExternalWorkflowEngineConfig.EngineName.
func (e *ExternalWorkflowEngine) Name() string { return e.cfg.EngineName }

// Run launches this engine's runner script as a one-shot subprocess and
// maps its outcome onto agentworkflow.WorkflowResult. wf.Steps is not
// consulted — see WorkflowDefinition.Engine's doc comment. input.SessionID,
// when set, scopes the spawned MCP subprocess (workflowrunner.Config.
// SessionID) to the run's durable-agent session for audit correlation.
func (e *ExternalWorkflowEngine) Run(ctx context.Context, wf agentworkflow.WorkflowDefinition, input agentworkflow.WorkflowInput, _ agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	rcfg := workflowrunner.Config{
		PythonPath:       e.cfg.PythonPath,
		ScriptPath:       e.cfg.ScriptPath,
		ExtraArgs:        e.cfg.ExtraArgs,
		NaniteBinaryPath: e.cfg.NaniteBinaryPath,
		DBPath:           e.cfg.DBPath,
		SessionID:        input.SessionID,
		APIBaseURL:       e.cfg.APIBaseURL,
		MCPServerID:      e.cfg.MCPServerID,
		Timeout:          e.cfg.Timeout,
		MaxOutputBytes:   e.cfg.MaxOutputBytes,
		Env:              e.cfg.Env,
	}

	result, err := workflowrunner.Launch(ctx, rcfg, input)
	if err != nil {
		// A Launch-level error (bad config, spawn failure, timeout) is
		// infra-level, exactly like a persistence failure is for
		// BuiltinWorkflowEngine.Run — returned as a Go error so
		// WorkflowLauncher.Launch's shared infra-error handling (finalize
		// the instance, omit workflow_run_id) fires identically for both
		// engines.
		return agentworkflow.WorkflowResult{}, fmt.Errorf("workflow: external engine %q: %w", e.cfg.EngineName, err)
	}

	sr := agentworkflow.StepResult{
		StepID:  externalRunStepID,
		Output:  result.Stdout,
		IsError: result.ExitCode != 0,
	}
	status := agentworkflow.RunStatusCompleted
	errMsg := ""
	if sr.IsError {
		status = agentworkflow.RunStatusFailed
		errMsg = fmt.Sprintf("%s runner exited %d", e.cfg.EngineName, result.ExitCode)
		if result.Stderr != "" {
			sr.Output += "\n\nstderr:\n" + result.Stderr
		}
	}

	return agentworkflow.WorkflowResult{
		// RunID intentionally empty — external engines don't persist a
		// workflow_runs row (WorkflowResult.RunID doc comment).
		Status: status,
		Error:  errMsg,
		StepResults: map[string]agentworkflow.StepResult{
			externalRunStepID: sr,
		},
	}, nil
}
