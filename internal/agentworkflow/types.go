package agentworkflow

import (
	llmtypes "github.com/hollis-labs/go-llm-types"
)

// StepKind identifies which of the three step kinds (design doc, "Built-in
// engine" section) a StepDefinition is.
type StepKind string

const (
	// StepKindLLM is a capability-restricted agent turn.
	StepKindLLM StepKind = "llm"
	// StepKindTool is an engine-owned call that never goes through an LLM.
	StepKindTool StepKind = "tool"
	// StepKindGate is a human-in-the-loop pause.
	StepKindGate StepKind = "gate"
)

// VerifyMode selects how a step's output is checked. Verify is a modifier
// on an llm/tool step, not a fourth step kind (design doc).
type VerifyMode string

const (
	// VerifyModeEngine runs a deterministic, named check — no LLM involved.
	VerifyModeEngine VerifyMode = "engine"
	// VerifyModeAgent spawns a second, independent llm step as reviewer.
	VerifyModeAgent VerifyMode = "agent"
)

// RunStatus is a workflow run's terminal-for-this-call status. A run isn't
// always fully resolved when a WorkflowEngine.Run (or Resume) call returns —
// RunStatusWaiting covers the built-in engine's gate pause (design doc:
// "gate steps persist a pending/waiting state and block that branch of the
// DAG until externally resolved").
type RunStatus string

const (
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
	RunStatusCancelled RunStatus = "cancelled"
	// RunStatusWaiting means the run made all the progress it currently
	// can — every remaining step is blocked behind an unresolved gate.
	RunStatusWaiting RunStatus = "waiting_on_gate"
)

// ToolCallRecord is one literal tool invocation made during an llm step's
// tool-call loop. Verify (mode: engine) and reviewer llm steps (mode:
// agent) inspect this list to confirm real tool activity happened, rather
// than trusting a step's self-reported text — the design doc's motivating
// example (an agent fabricating a Torque fetch instead of calling it).
type ToolCallRecord struct {
	Tool    string
	Input   map[string]any
	Output  string
	IsError bool
}

// LLMStepRequest is the input to StepExecutor.ExecuteLLMStep — one
// capability-restricted agent turn.
type LLMStepRequest struct {
	// WorkflowRunID and StepID identify the calling context for logging
	// and for a future engine's persistence layer (out of scope here —
	// StepExecutor itself is stateless). Optional; ExecuteLLMStep does not
	// require them to function.
	WorkflowRunID string
	StepID        string

	// SessionID, when non-empty, scopes this call to an existing session
	// for telemetry/audit correlation. ExecuteLLMStep does not create,
	// look up, or persist a session — provisioning one (if a caller wants
	// full ContextService-based context assembly / memory recall) is the
	// caller's responsibility, consistent with this ticket's "no DB
	// schema / persistence" scope.
	SessionID string

	// AgentID identifies the calling identity for permission checks
	// (passed straight through to ToolService.Execute, which applies the
	// existing permission engine) and tool-metadata lookups.
	AgentID string

	// Provider and Model select which LLM backend executes the turn.
	Provider string
	Model    string

	// SystemPrompt and Messages are the fully-assembled turn context.
	// Composing memory recall / prior-step results into these is the
	// caller's job — ExecuteLLMStep does not assemble context itself.
	SystemPrompt string
	Messages     []llmtypes.ChatMessage

	// Tools is the capability-restricted tool surface: the exact set of
	// tool names this step's LLM turn may call. ExecuteLLMStep resolves
	// definitions for exactly these names and offers no others — there is
	// no fallback to an agent profile's broader default. Empty means no
	// tools are offered.
	Tools []string

	// MaxToolIterations bounds the tool-call loop. 0 uses
	// DefaultMaxToolIterations.
	MaxToolIterations int
}

// LLMStepResult is the output of one ExecuteLLMStep call.
type LLMStepResult struct {
	Text       string
	ToolCalls  []ToolCallRecord
	Usage      *llmtypes.Usage
	StopReason string
}

// ToolStepRequest is the input to StepExecutor.ExecuteToolStep — a single
// engine-owned tool call that never touches model inference.
type ToolStepRequest struct {
	WorkflowRunID string
	StepID        string

	// AgentID identifies the calling identity for permission checks.
	AgentID string

	Tool string
	Args map[string]any
}

// ToolStepResult is the literal (success or error) result of one
// ExecuteToolStep call.
type ToolStepResult struct {
	Output  string
	IsError bool
}

// VerifySpec is the static, author-time verify configuration attached to
// an llm/tool StepDefinition. An engine combines it with the step's actual
// runtime output to build a VerifyRequest.
type VerifySpec struct {
	Mode VerifyMode

	// EngineCheck names a registered deterministic check (mode: engine).
	// The set of valid names is intentionally not enumerated here (design
	// doc explicit non-goal: don't anticipate every check kind up front —
	// start narrow, grow as real workflows need more). Verify errors on
	// an unrecognized name.
	EngineCheck  string
	EngineParams map[string]any

	// Reviewer* configure the independent second llm step Verify spawns
	// when Mode == VerifyModeAgent — literally a nested ExecuteLLMStep
	// call, not separate logic (design doc).
	ReviewerPrompt    string
	ReviewerAgentID   string
	ReviewerProvider  string
	ReviewerModel     string
	ReviewerTools     []string
	ReviewerSessionID string
}

// VerifySubject is what's being verified: a prior step's literal runtime
// output, passed to both engine checks and the agent-reviewer prompt.
type VerifySubject struct {
	StepKind  StepKind
	Output    string
	IsError   bool
	ToolCalls []ToolCallRecord
}

// VerifyRequest is the input to StepExecutor.Verify.
type VerifyRequest struct {
	WorkflowRunID string
	// StepID is the verify step's own identity, for logging/audit.
	StepID string
	// SubjectStepID is the step whose output is being verified.
	SubjectStepID string

	VerifySpec

	Subject VerifySubject
}

// VerifyResult is the outcome of one Verify call.
type VerifyResult struct {
	Passed bool
	Reason string

	// ReviewerResult is set when Mode == VerifyModeAgent — the raw nested
	// ExecuteLLMStep result, kept for audit/debugging.
	ReviewerResult *LLMStepResult
}

// StepDefinition is one node in a WorkflowDefinition's DAG.
type StepDefinition struct {
	ID        string
	Kind      StepKind
	DependsOn []string

	// Config carries the step's author-time configuration in whatever
	// shape Kind implies (a prompt template + tool surface for llm, a
	// tool name + arg template for tool, human-gate parameters for gate).
	// Deliberately untyped here: the built-in engine (downstream of this
	// ticket) owns the authoring/templating format — how a static step
	// config becomes a runtime LLMStepRequest/ToolStepRequest (prompt
	// templating, dependency-result interpolation) — and should design
	// that shape against real workflow definitions rather than have it
	// guessed at here before any engine consumes it.
	Config map[string]any

	// Verify is an optional modifier (design doc: "verify is a modifier
	// on llm/tool steps, not a fourth step kind"). nil means no
	// verification.
	Verify *VerifySpec
}

// WorkflowDefinition describes a workflow's steps and dependencies for a
// WorkflowEngine to sequence. DAG only, no cycles (design doc scope).
type WorkflowDefinition struct {
	Name  string
	Steps []StepDefinition
}

// WorkflowInput is the caller-supplied input to a workflow run.
type WorkflowInput struct {
	// Params carries the run's initial arguments, keyed by name.
	Params map[string]any
}

// StepResult is one step's literal, typed outcome within a WorkflowResult.
type StepResult struct {
	StepID  string
	Kind    StepKind
	Output  string
	IsError bool

	// ToolCalls is populated for llm steps.
	ToolCalls []ToolCallRecord
	// VerifyResult is nil when the step had no verify modifier.
	VerifyResult *VerifyResult
}

// WorkflowResult is a completed workflow run's terminal outcome.
type WorkflowResult struct {
	Status RunStatus
	// StepResults holds each step's result keyed by step ID.
	StepResults map[string]StepResult
	Error       string
}
