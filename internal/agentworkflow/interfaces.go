package agentworkflow

import "context"

// StepExecutor is the single Nanite execution substrate the shared workflow
// host calls for real work. Its implementation lives in internal/service (see
// workflow_step_executor.go), reusing the harness's existing tool broker
// and permission engine rather than reimplementing them. Context assembly
// and memory recall (the ContextService.AssembleSlots/Tesseract path
// Chat/GUI/CLI turns get) are not wired in by default — ExecuteLLMStep can
// opt into them per request via LLMStepRequest, but a step that doesn't ask
// for it runs without prior session context or long-term memory. Capability
// restriction (an llm step gets exactly the tool surface its request
// specifies, never an agent profile's broader default) and tool steps
// never touching model inference are load-bearing invariants of this
// interface, not implementation details left to callers.
type StepExecutor interface {
	// ExecuteLLMStep runs a bounded, capability-restricted Run composed of Turns.
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
