package agentworkflow

import "context"

// WorkflowEngine decides sequencing only — which step runs when. It never
// executes a unit of work itself; every LLM turn, tool call, and
// verification check runs through the StepExecutor it is given. This holds
// for the built-in engine and for external engines (LangGraph, CrewAI)
// identically (design doc, "The one principle everything else follows
// from"). Mirrors Dependencies.ProviderAdapter from
// internal/runtime/agent — a thin resolver/adapter shape, not a new
// architectural pattern for this codebase.
type WorkflowEngine interface {
	Name() string
	Run(ctx context.Context, wf WorkflowDefinition, input WorkflowInput, exec StepExecutor) (WorkflowResult, error)
}

// StepExecutor is the single execution substrate every WorkflowEngine —
// built-in or external — calls into for real work. Its implementation
// lives in internal/service as plain functions (see
// workflow_step_executor.go), reusing the harness's existing turn
// execution, tool broker, and permission engine rather than reimplementing
// them. Capability restriction (an llm step gets exactly the tool surface
// its request specifies, never an agent profile's broader default) and
// tool steps never touching model inference are load-bearing invariants
// of this interface, not implementation details left to callers.
type StepExecutor interface {
	// ExecuteLLMStep runs one capability-restricted agent turn.
	ExecuteLLMStep(ctx context.Context, req LLMStepRequest) (LLMStepResult, error)

	// ExecuteToolStep calls a named tool directly with the given args.
	// This never goes through an LLM — the call happens regardless of
	// what any model would have decided.
	ExecuteToolStep(ctx context.Context, req ToolStepRequest) (ToolStepResult, error)

	// Verify checks a prior step's result. mode: engine runs a
	// deterministic, registered check; mode: agent spawns a second,
	// independent ExecuteLLMStep call with a reviewer-oriented prompt.
	Verify(ctx context.Context, req VerifyRequest) (VerifyResult, error)
}
