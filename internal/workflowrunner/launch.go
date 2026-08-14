// Package workflowrunner spawns a one-shot external workflow-runner
// subprocess (a Python LangGraph/CrewAI process, or a minimal test
// script standing in for one) that calls back into Nanite's harness via
// the workflow_execute_llm_step / workflow_execute_tool_step /
// workflow_verify_step MCP tools (CW-20260813-0011, design doc "How
// external engines integrate").
//
// This is deliberately its own launch path, NOT a reuse of
// internal/runtime/agent.Boot — that package boots CLI *coding* agents
// (CLAUDE.md injection, agent-shaped boot directories); a workflow
// runner is a different, much smaller shape: spawn one process, hand it
// a .mcp.json and its input, wait for it to exit, capture what it
// reported. Lifecycle matches ModeOneShot conceptually (single
// invocation, auto-fires, stops) without depending on that enum.
package workflowrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/fsutil"
)

const (
	// DefaultTimeout bounds a launch when Config.Timeout is unset — the
	// one-shot process must report a result within this window.
	DefaultTimeout = 10 * time.Minute
	// DefaultMaxOutputBytes caps captured stdout/stderr per stream.
	// Output beyond this is silently dropped, not failed — mirrors
	// internal/background's output-cap behavior (a runaway workflow
	// runner cannot OOM the host).
	DefaultMaxOutputBytes = 1 << 20 // 1 MiB

	// DefaultMCPServerID names the entry under .mcp.json's "mcpServers"
	// key when Config.MCPServerID is unset.
	DefaultMCPServerID = "nanite"
)

// Config configures one workflow-runner launch. PythonPath, ScriptPath,
// NaniteBinaryPath, and DBPath are required and never defaulted to a
// hardcoded path — an unset value is a caller wiring bug surfaced by
// Launch's error return, not something this package guesses at.
type Config struct {
	// PythonPath is the interpreter to run (e.g. "python3", or a venv's
	// absolute path). Required.
	PythonPath string
	// ScriptPath is the workflow-runner script to execute. Required.
	ScriptPath string
	// ExtraArgs are appended to argv after ScriptPath, before the input
	// file path.
	ExtraArgs []string

	// NaniteBinaryPath is the nanite binary planted into .mcp.json as
	// the MCP server's launch command — the same binary CLI-launched
	// agents point at (internal/runtime/agent's renderMCPJSON). Required.
	NaniteBinaryPath string
	// DBPath is the SQLite database path passed to `nanite mcp --db`.
	// Required.
	DBPath string
	// SessionID scopes the spawned MCP server to a session, mirroring
	// the CLI-agent .mcp.json shape. Optional.
	SessionID string
	// APIBaseURL, when set, is planted as NANITE_API_URL so the
	// subprocess's `nanite mcp` server forwards self-tool calls
	// (including the workflow_* callback tools) to a live, already-
	// running harness instead of a bare store with no StepExecutor
	// wired — the same live-harness proxy mechanism CLI-launched chat
	// agents use (internal/runtime/agent's mcpOverlay /
	// renderMCPJSON). Required in practice for the callback tools to
	// return real results; left optional here so Launch itself stays
	// usable for pure launch-mechanics testing.
	APIBaseURL string
	// MCPServerID names the entry under .mcp.json's "mcpServers" key.
	// Defaults to DefaultMCPServerID.
	MCPServerID string

	// WorkDir is the directory the subprocess runs in and where
	// .mcp.json / the input file are planted. A temp directory is
	// created and removed automatically when left empty.
	WorkDir string
	// KeepWorkDir skips removing an auto-created WorkDir after the run
	// — useful when debugging a failed launch. Ignored when WorkDir was
	// caller-supplied (the caller owns cleanup in that case).
	KeepWorkDir bool

	// Timeout bounds the whole one-shot run. 0 uses DefaultTimeout.
	Timeout time.Duration
	// MaxOutputBytes caps captured stdout/stderr per stream. 0 uses
	// DefaultMaxOutputBytes.
	MaxOutputBytes int
	// Env carries additional "KEY=VALUE" entries appended to the
	// subprocess's environment (which otherwise inherits os.Environ()).
	Env []string
}

// Result is a completed one-shot launch's outcome. A non-zero ExitCode
// is not surfaced as a Go error from Launch — Launch's error return is
// reserved for launch-level failures (bad config, spawn failure,
// timeout/ctx cancellation). Capturing "what the process reported"
// without interpreting it is the point: this package is a launcher, not
// a workflow-result parser.
type Result struct {
	Stdout          string
	Stderr          string
	StdoutTruncated bool
	StderrTruncated bool
	ExitCode        int
	TimedOut        bool
	Duration        time.Duration
	WorkDir         string
	InputPath       string
}

func (cfg Config) validate() error {
	if cfg.PythonPath == "" {
		return errors.New("workflowrunner: PythonPath is required")
	}
	if cfg.ScriptPath == "" {
		return errors.New("workflowrunner: ScriptPath is required")
	}
	if cfg.NaniteBinaryPath == "" {
		return errors.New("workflowrunner: NaniteBinaryPath is required")
	}
	if cfg.DBPath == "" {
		return errors.New("workflowrunner: DBPath is required")
	}
	return nil
}

// Launch spawns the configured Python workflow-runner as a one-shot
// subprocess: plant .mcp.json plus the input file into a work dir, run
// the process to completion in that dir, capture what it reported, and
// clean up. Blocks until the process exits, the configured timeout
// elapses, or ctx is cancelled.
func Launch(ctx context.Context, cfg Config, input agentworkflow.WorkflowInput) (Result, error) {
	if err := cfg.validate(); err != nil {
		return Result{}, err
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	maxOutput := cfg.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = DefaultMaxOutputBytes
	}

	workDir := cfg.WorkDir
	ownWorkDir := workDir == ""
	if ownWorkDir {
		dir, err := os.MkdirTemp("", "nanite-workflowrunner-*")
		if err != nil {
			return Result{}, fmt.Errorf("workflowrunner: create work dir: %w", err)
		}
		workDir = dir
		if !cfg.KeepWorkDir {
			defer os.RemoveAll(workDir)
		}
	} else if err := os.MkdirAll(workDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("workflowrunner: prepare work dir: %w", err)
	}

	mcpJSON, err := renderMCPJSON(cfg)
	if err != nil {
		return Result{}, err
	}
	if err := fsutil.AtomicWriteFile(filepath.Join(workDir, ".mcp.json"), []byte(mcpJSON), 0o644); err != nil {
		return Result{}, fmt.Errorf("workflowrunner: plant .mcp.json: %w", err)
	}

	inputBody, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("workflowrunner: marshal input: %w", err)
	}
	inputPath := filepath.Join(workDir, "input.json")
	if err := fsutil.AtomicWriteFile(inputPath, inputBody, 0o644); err != nil {
		return Result{}, fmt.Errorf("workflowrunner: write input file: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	argv := append(append([]string{cfg.ScriptPath}, cfg.ExtraArgs...), inputPath)
	cmd := exec.CommandContext(runCtx, cfg.PythonPath, argv...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), cfg.Env...)

	stdoutW := &limitedBuffer{max: maxOutput}
	stderrW := &limitedBuffer{max: maxOutput}
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	start := time.Now()
	runErr := cmd.Run()

	result := Result{
		Stdout:          stdoutW.buf.String(),
		Stderr:          stderrW.buf.String(),
		StdoutTruncated: stdoutW.truncated,
		StderrTruncated: stderrW.truncated,
		Duration:        time.Since(start),
		WorkDir:         workDir,
		InputPath:       inputPath,
	}

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		result.TimedOut = true
		return result, fmt.Errorf("workflowrunner: %s exceeded timeout %s", cfg.ScriptPath, timeout)
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}

	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		result.ExitCode = 0
	case errors.As(runErr, &exitErr):
		result.ExitCode = exitErr.ExitCode()
	default:
		// Spawn failure (binary not found, permission denied, etc.) — a
		// launch-level failure, not a process result to interpret.
		return result, fmt.Errorf("workflowrunner: run %s: %w", cfg.ScriptPath, runErr)
	}

	return result, nil
}

// renderMCPJSON mirrors internal/runtime/agent's renderMCPJSON — the
// same "point a subprocess at `nanite mcp --db ... --session ...`"
// shape CLI-launched agents (claude/codex/opencode) use, so a
// workflow-runner subprocess discovers Nanite's MCP server identically.
// Deliberately re-derived here rather than imported: renderMCPJSON is
// unexported and scoped to the CLI-coding-agent boot package, and the
// design doc explicitly calls for a separate, smaller launch path
// rather than reusing agent.Boot's mechanism for this.
func renderMCPJSON(cfg Config) (string, error) {
	serverID := cfg.MCPServerID
	if serverID == "" {
		serverID = DefaultMCPServerID
	}

	args := []string{"mcp", "--db", cfg.DBPath}
	if cfg.SessionID != "" {
		args = append(args, "--session", cfg.SessionID)
	}

	env := map[string]any{}
	if cfg.APIBaseURL != "" {
		env["NANITE_API_URL"] = cfg.APIBaseURL
	}

	mcpConfig := map[string]any{
		"mcpServers": map[string]any{
			serverID: map[string]any{
				"command": cfg.NaniteBinaryPath,
				"args":    args,
				"env":     env,
			},
		},
	}

	data, err := json.MarshalIndent(mcpConfig, "", "  ")
	if err != nil {
		return "", fmt.Errorf("workflowrunner: marshal .mcp.json: %w", err)
	}
	return string(data), nil
}

// limitedBuffer caps how many bytes a subprocess pipe can accumulate,
// mirroring internal/background's output-cap behavior — a runaway
// workflow-runner process cannot OOM the host. Bytes past the cap are
// dropped silently; Result.Std{out,err}Truncated reports whether that
// happened.
type limitedBuffer struct {
	buf       bytes.Buffer
	max       int
	written   int
	truncated bool
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	if w.written >= w.max {
		w.truncated = true
		return len(p), nil
	}
	remaining := w.max - w.written
	if len(p) > remaining {
		w.buf.Write(p[:remaining])
		w.written = w.max
		w.truncated = true
		return len(p), nil
	}
	n, err := w.buf.Write(p)
	w.written += n
	return n, err
}
