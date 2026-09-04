package service

import (
	"context"
	"fmt"
	"time"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/workflowrunner"
)

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

// ExternalWorkflowEngine is a one-shot external Python framework runner,
// launched via internal/workflowrunner.Launch. The shared host invokes it as
// one StepKind; it never owns or selects workflow lifecycle sequencing.
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

// Name identifies the external framework adapter.
func (e *ExternalWorkflowEngine) Name() string { return e.cfg.EngineName }

// ExecuteWorkflowStep invokes this external framework as one keyed node under
// the shared durable workflow host. The stable key is included in the exact
// external input so framework-side effect boundaries can deduplicate retries.
func (e *ExternalWorkflowEngine) ExecuteWorkflowStep(ctx context.Context, idempotencyKey, workflowName string, params map[string]any, sessionID string) (ExternalWorkflowStepResult, error) {
	if idempotencyKey == "" {
		return ExternalWorkflowStepResult{}, fmt.Errorf("workflow: external engine %q: idempotency key is required", e.cfg.EngineName)
	}
	keyedParams := make(map[string]any, len(params)+1)
	for key, value := range params {
		keyedParams[key] = value
	}
	keyedParams["_nanite_idempotency_key"] = idempotencyKey
	rcfg := workflowrunner.Config{
		PythonPath:       e.cfg.PythonPath,
		ScriptPath:       e.cfg.ScriptPath,
		ExtraArgs:        e.cfg.ExtraArgs,
		NaniteBinaryPath: e.cfg.NaniteBinaryPath,
		DBPath:           e.cfg.DBPath,
		SessionID:        sessionID,
		APIBaseURL:       e.cfg.APIBaseURL,
		MCPServerID:      e.cfg.MCPServerID,
		Timeout:          e.cfg.Timeout,
		MaxOutputBytes:   e.cfg.MaxOutputBytes,
		Env:              e.cfg.Env,
	}

	result, err := workflowrunner.Launch(ctx, rcfg, agentworkflow.WorkflowInput{Params: keyedParams, SessionID: sessionID})
	if err != nil {
		return ExternalWorkflowStepResult{}, fmt.Errorf("workflow: external engine %q workflow %q: %w", e.cfg.EngineName, workflowName, err)
	}

	stepResult := ExternalWorkflowStepResult{Output: result.Stdout, IsError: result.ExitCode != 0}
	if stepResult.IsError {
		if result.Stderr != "" {
			stepResult.Output += "\n\nstderr:\n" + result.Stderr
		}
	}
	return stepResult, nil
}

var _ ExternalWorkflowStepEngine = (*ExternalWorkflowEngine)(nil)
