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
	Tool    string         `json:"tool"`
	Input   map[string]any `json:"input"`
	Output  string         `json:"output"`
	IsError bool           `json:"is_error"`
}

// LLMStepRequest is the input to StepExecutor.ExecuteLLMStep — one
// capability-restricted agent turn.
type LLMStepRequest struct {
	// WorkflowRunID and StepID identify the calling context for logging
	// and for a future engine's persistence layer (out of scope here —
	// StepExecutor itself is stateless). Optional; ExecuteLLMStep does not
	// require them to function.
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
	StepID        string `json:"step_id,omitempty"`

	// SessionID, when non-empty, scopes this call to an existing session
	// for telemetry/audit correlation. When EnableContextAssembly is also
	// set, SessionID additionally identifies the store.Session the
	// harness resolves session history and memory-recall namespacing
	// from — ExecuteLLMStep does not create or persist a session itself,
	// only looks one up.
	SessionID string `json:"session_id,omitempty"`

	// AgentID identifies the calling identity for permission checks
	// (passed straight through to ToolService.Execute, which applies the
	// existing permission engine) and tool-metadata lookups. When
	// EnableContextAssembly is also set, AgentID additionally identifies
	// the store.AgentProfile whose system prompt / skills the harness
	// composes into the assembled context.
	AgentID string `json:"agent_id,omitempty"`

	// Provider and Model select which LLM backend executes the turn.
	Provider string `json:"provider"`
	Model    string `json:"model"`

	// SystemPrompt and Messages are this step's own turn content. By
	// default they are the fully-assembled turn context as-is — composing
	// memory recall / prior-step results into them is the caller's job,
	// and ExecuteLLMStep does not assemble context itself.
	//
	// When EnableContextAssembly is true, they are layered on top of the
	// harness-assembled context instead of used verbatim: the assembled
	// system prompt leads (SystemPrompt follows as this step's own
	// instructions), and the assembled session history leads (Messages
	// follows as this step's own turn).
	SystemPrompt string                 `json:"system_prompt,omitempty"`
	Messages     []llmtypes.ChatMessage `json:"messages"`

	// EnableContextAssembly opts this call into the harness's existing
	// turn-context pipeline — ContextService.AssembleSlots
	// (internal/service/context.go), the same slot-based assembly method
	// Chat/GUI/CLI turns call (internal/service/chat_generate.go). It
	// composes session history, agent/mode/workspace prompt content
	// (including the Universal, Rules, Permissions, Workspace, and
	// session-level Mode slots), and Tesseract memory recall
	// (contextbroker.MemorySource, one of the ContextBroker's sources) —
	// genuine parity with the assembly a Chat/GUI/CLI turn gets, not a
	// thinner subset. Reused via the existing code path, not reimplemented.
	//
	// Default false: every step runs exactly as before, using only
	// SystemPrompt/Messages as supplied, with no session history and no
	// memory recall. This is opt-in rather than default-on because not
	// every step wants the added latency/token cost — a narrow
	// classification or extraction step can skip it entirely, while a
	// step that benefits from prior context or long-term facts can ask
	// for it. Requires SessionID and AgentID; ExecuteLLMStep errors if
	// either is empty or no WorkflowContextAssembler is configured.
	EnableContextAssembly bool `json:"enable_context_assembly,omitempty"`

	// Tools is the capability-restricted tool surface: the exact set of
	// tool names this step's LLM turn may call. ExecuteLLMStep resolves
	// definitions for exactly these names and offers no others — there is
	// no fallback to an agent profile's broader default. Empty means no
	// tools are offered.
	Tools []string `json:"tools,omitempty"`

	// MaxToolIterations bounds the tool-call loop. 0 uses
	// DefaultMaxToolIterations.
	MaxToolIterations int `json:"max_tool_iterations,omitempty"`
}

// LLMStepResult is the output of one ExecuteLLMStep call.
type LLMStepResult struct {
	Text       string           `json:"text"`
	ToolCalls  []ToolCallRecord `json:"tool_calls,omitempty"`
	Usage      *llmtypes.Usage  `json:"usage,omitempty"`
	StopReason string           `json:"stop_reason,omitempty"`
}

// ToolStepRequest is the input to StepExecutor.ExecuteToolStep — a single
// engine-owned tool call that never touches model inference.
type ToolStepRequest struct {
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
	StepID        string `json:"step_id,omitempty"`

	// AgentID identifies the calling identity for permission checks.
	AgentID string `json:"agent_id,omitempty"`

	Tool string         `json:"tool"`
	Args map[string]any `json:"args,omitempty"`
}

// ToolStepResult is the literal (success or error) result of one
// ExecuteToolStep call.
type ToolStepResult struct {
	Output  string `json:"output"`
	IsError bool   `json:"is_error"`
}

// VerifySpec is the static, author-time verify configuration attached to
// an llm/tool StepDefinition. An engine combines it with the step's actual
// runtime output to build a VerifyRequest.
type VerifySpec struct {
	Mode VerifyMode `json:"mode"`

	// EngineCheck names a registered deterministic check (mode: engine).
	// The set of valid names is intentionally not enumerated here (design
	// doc explicit non-goal: don't anticipate every check kind up front —
	// start narrow, grow as real workflows need more). Verify errors on
	// an unrecognized name.
	EngineCheck  string         `json:"engine_check,omitempty"`
	EngineParams map[string]any `json:"engine_params,omitempty"`

	// Reviewer* configure the independent second llm step Verify spawns
	// when Mode == VerifyModeAgent — literally a nested ExecuteLLMStep
	// call, not separate logic (design doc).
	ReviewerPrompt    string   `json:"reviewer_prompt,omitempty"`
	ReviewerAgentID   string   `json:"reviewer_agent_id,omitempty"`
	ReviewerProvider  string   `json:"reviewer_provider,omitempty"`
	ReviewerModel     string   `json:"reviewer_model,omitempty"`
	ReviewerTools     []string `json:"reviewer_tools,omitempty"`
	ReviewerSessionID string   `json:"reviewer_session_id,omitempty"`
}

// VerifySubject is what's being verified: a prior step's literal runtime
// output, passed to both engine checks and the agent-reviewer prompt.
type VerifySubject struct {
	StepKind  StepKind         `json:"step_kind,omitempty"`
	Output    string           `json:"output"`
	IsError   bool             `json:"is_error"`
	ToolCalls []ToolCallRecord `json:"tool_calls,omitempty"`
}

// VerifyRequest is the input to StepExecutor.Verify.
type VerifyRequest struct {
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
	// StepID is the verify step's own identity, for logging/audit.
	StepID string `json:"step_id,omitempty"`
	// SubjectStepID is the step whose output is being verified.
	SubjectStepID string `json:"subject_step_id,omitempty"`

	VerifySpec

	Subject VerifySubject `json:"subject"`
}

// VerifyResult is the outcome of one Verify call.
type VerifyResult struct {
	Passed bool   `json:"passed"`
	Reason string `json:"reason"`

	// ReviewerResult is set when Mode == VerifyModeAgent — the raw nested
	// ExecuteLLMStep result, kept for audit/debugging.
	ReviewerResult *LLMStepResult `json:"reviewer_result,omitempty"`
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

// Engine name constants — the values a WorkflowDefinition.Engine field
// selects and a WorkflowEngine.Name() returns, shared so both sides of the
// selection stay in sync (design doc, "How external engines integrate").
const (
	// EngineBuiltin is the DAG-executing in-process engine (design doc,
	// "Built-in engine"). WorkflowDefinition.Engine defaults to this when
	// left empty.
	EngineBuiltin = "builtin"
	// EngineLangGraph runs the hand-authored LangGraph POC graph
	// (CW-20260813-0012) via internal/workflowrunner.
	EngineLangGraph = "langgraph"
	// EngineCrewAI runs the hand-authored CrewAI POC crew
	// (CW-20260813-0013) via internal/workflowrunner.
	EngineCrewAI = "crewai"
)

// WorkflowDefinition describes a workflow's steps and dependencies for a
// WorkflowEngine to sequence. DAG only, no cycles (design doc scope).
type WorkflowDefinition struct {
	Name string

	// Engine selects which registered WorkflowEngine runs this definition
	// — EngineBuiltin, EngineLangGraph, or EngineCrewAI. Empty defaults to
	// EngineBuiltin, so every workflow defined before this field existed
	// is unaffected. A name with no matching registered engine is a
	// launch-time error (WorkflowLauncher.Launch), not a load-time one —
	// which engines are actually available is a per-process wiring
	// concern (e.g. no Python on PATH), not a property of the definition
	// itself.
	//
	// When Engine names an external engine, Steps is NOT consumed by that
	// engine — the DAG shape for LangGraph/CrewAI lives in the hand-
	// authored Python graph/crew itself (design doc, POC scope: "not
	// building a compiler" from this format to theirs). Steps must still
	// be non-empty to satisfy Validate; author it as documentation of
	// intent for a human reading the definition.
	Engine string

	Steps []StepDefinition
}

// WorkflowInput is the caller-supplied input to a workflow run.
type WorkflowInput struct {
	// Params carries the run's initial arguments, keyed by name.
	Params map[string]any `json:"params,omitempty"`

	// SessionID, when set, is threaded through to an external engine's
	// spawned MCP subprocess (workflowrunner.Config.SessionID) so its
	// callback tool calls carry the same session the launching
	// WorkflowLauncher.Launch created for this run's durable-agent
	// instance — audit/telemetry correlation, mirroring how CLI-launched
	// agents scope their MCP server. Set by WorkflowLauncher, not by
	// callers of Launch directly. BuiltinWorkflowEngine does not read
	// this field: a built-in step's session_id comes from its own
	// author-time Config, not from here.
	SessionID string `json:"session_id,omitempty"`
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
	// RunID identifies the persisted workflow_runs row (built-in engine
	// only — external engines that don't persist a run row leave this
	// empty). Lets a caller link the run back to whatever launched it
	// (CW-20260813-0014: a template-class durable-agent instance).
	RunID  string
	Status RunStatus
	// StepResults holds each step's result keyed by step ID.
	StepResults map[string]StepResult
	Error       string
}
