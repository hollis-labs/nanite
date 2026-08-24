package selftools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/mcp"
)

// self_tools_workflow.go implements the three MCP callback tools —
// workflow_execute_llm_step, workflow_execute_tool_step,
// workflow_verify_step — that let an external workflow engine (LangGraph,
// CrewAI) run real work through Nanite's harness instead of a second,
// parallel one (CW-20260813-0011; design doc "How external engines
// integrate" / "The one principle everything else follows from"). The
// built-in engine (internal/service/workflow_engine.go) calls
// agentworkflow.StepExecutor directly, in-process; these three tools are
// the identical call reached over MCP by a Python subprocess.
//
// Each handler is a thin wrapper: decode the MCP call's arguments
// directly into the matching agentworkflow request type (their JSON tags
// ARE this tool surface's wire contract) and call the single StepExecutor
// implementation (internal/service/workflow_step_executor.go,
// CW-20260813-0009). No logic beyond argument marshaling belongs here —
// anything more belongs in internal/service.

func workflowExecuteLLMStepToolDefinition() mcp.Tool {
	return mcp.Tool{
		Name: "workflow_execute_llm_step",
		Description: "Run one capability-restricted agent turn through Nanite's harness (provider/tool-broker/permission-engine) on behalf of a workflow engine (LangGraph, CrewAI, or the built-in engine).\n\n" +
			"**When to use:** From a workflow node/task that needs an LLM to do real work. The tool surface offered to the model is EXACTLY the `tools` you pass here — no fallback to a broader default (capability restriction is load-bearing, not an implementation detail).\n\n" +
			"**Required context:** `provider`, `model`, and `messages` (array of {role, content} chat turns). Composing memory recall / prior-step results into `messages` is the caller's job by default — this tool does not assemble context itself unless `enable_context_assembly` is set.\n\n" +
			"**Context assembly (opt-in):** set `enable_context_assembly` with both `session_id` and `agent_id` to layer the harness's session history, agent/mode/workspace prompt, and Tesseract memory recall ahead of `system_prompt`/`messages`. Off by default — most steps (narrow classification/extraction) don't need it and it adds latency/token cost.\n\n" +
			"**Output shape:** {text, tool_calls: [{tool, input, output, is_error}], usage, stop_reason}.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"workflow_run_id": map[string]any{"type": "string", "description": "Identifies the calling workflow run, for logging/audit. Optional."},
				"step_id":         map[string]any{"type": "string", "description": "This step's own ID, for logging/audit. Optional."},
				"session_id":      map[string]any{"type": "string", "description": "Existing session ID to scope this call to, for telemetry/audit correlation. Optional — does not create or persist a session. Required when enable_context_assembly is true."},
				"agent_id":        map[string]any{"type": "string", "description": "Calling identity for permission checks and tool-metadata lookups. Optional. Required when enable_context_assembly is true."},
				"provider":        map[string]any{"type": "string", "description": "LLM provider backend (e.g. 'anthropic', 'openai')."},
				"model":           map[string]any{"type": "string", "description": "Model name."},
				"system_prompt":   map[string]any{"type": "string", "description": "System prompt for the turn. Optional."},
				"enable_context_assembly": map[string]any{
					"type":        "boolean",
					"description": "Opt into the harness's existing context-assembly + memory-recall pipeline, scoped by session_id/agent_id. Default false — the turn runs on system_prompt/messages alone.",
				},
				"messages": map[string]any{
					"type":        "array",
					"description": "The turn's chat messages, in order: [{role, content}, ...].",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"role":    map[string]any{"type": "string", "description": "e.g. 'user', 'assistant'."},
							"content": map[string]any{"type": "string"},
						},
						"required": []string{"role"},
					},
				},
				"tools": map[string]any{
					"type":        "array",
					"description": "The capability-restricted tool surface: exact tool names this step's LLM turn may call. Omit/empty for no tools.",
					"items":       map[string]any{"type": "string"},
				},
				"max_tool_iterations": map[string]any{"type": "integer", "description": "Bounds the tool-call loop. 0/omitted uses the executor default."},
			},
			"required": []string{"provider", "model", "messages"},
		},
	}
}

func workflowExecuteToolStepToolDefinition() mcp.Tool {
	return mcp.Tool{
		Name: "workflow_execute_tool_step",
		Description: "Call a named tool directly, on behalf of a workflow engine (LangGraph, CrewAI, or the built-in engine). This NEVER goes through an LLM — the call happens regardless of what any model would have decided.\n\n" +
			"**When to use:** From a workflow node/task whose definition specifies a `tool` step (engine-owned call, per the design doc's three step kinds — llm, tool, gate).\n\n" +
			"**Required context:** `tool` (the tool name). `args` carries the tool's arguments.\n\n" +
			"**Output shape:** {output, is_error} — the literal (success or error) result, never an LLM's interpretation of it.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"workflow_run_id": map[string]any{"type": "string", "description": "Identifies the calling workflow run, for logging/audit. Optional."},
				"step_id":         map[string]any{"type": "string", "description": "This step's own ID, for logging/audit. Optional."},
				"agent_id":        map[string]any{"type": "string", "description": "Calling identity for permission checks. Optional."},
				"tool":            map[string]any{"type": "string", "description": "Tool name to call."},
				"args":            map[string]any{"type": "object", "description": "Tool arguments. Omit for a no-arg tool."},
			},
			"required": []string{"tool"},
		},
	}
}

func workflowVerifyStepToolDefinition() mcp.Tool {
	return mcp.Tool{
		Name: "workflow_verify_step",
		Description: "Check a prior step's literal result — never trust what a step self-reports. mode=engine runs a deterministic, named check; mode=agent spawns a second, independent LLM step as reviewer (a nested workflow_execute_llm_step call under the hood).\n\n" +
			"**When to use:** From a workflow node/task whose step definition carries a `verify` modifier (design doc: verify is a modifier on llm/tool steps, not a fourth step kind).\n\n" +
			"**Required context:** `mode` (engine|agent) and `subject` (the prior step's literal output: {step_kind, output, is_error, tool_calls}). mode=engine also needs `engine_check` (e.g. 'no_error', 'tool_called', 'output_contains'); mode=agent also needs the `reviewer_*` fields.\n\n" +
			"**Output shape:** {passed, reason, reviewer_result?} — reviewer_result is set only for mode=agent (the nested LLM step's raw result, kept for audit).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"workflow_run_id": map[string]any{"type": "string", "description": "Identifies the calling workflow run, for logging/audit. Optional."},
				"step_id":         map[string]any{"type": "string", "description": "This verify step's own ID, for logging/audit. Optional."},
				"subject_step_id": map[string]any{"type": "string", "description": "The step whose output is being verified. Optional."},
				"mode":            map[string]any{"type": "string", "enum": []string{"engine", "agent"}, "description": "engine runs a deterministic named check; agent spawns an independent reviewer LLM step."},
				"engine_check":    map[string]any{"type": "string", "description": "Registered check name (mode=engine). E.g. 'no_error', 'tool_called', 'output_contains'."},
				"engine_params":   map[string]any{"type": "object", "description": "Params for the named engine check (mode=engine)."},
				"reviewer_prompt": map[string]any{"type": "string", "description": "Review instructions for the reviewer LLM step (mode=agent)."},
				"reviewer_agent_id": map[string]any{
					"type": "string", "description": "Calling identity for the reviewer step's permission checks (mode=agent).",
				},
				"reviewer_provider": map[string]any{"type": "string", "description": "LLM provider for the reviewer step (mode=agent)."},
				"reviewer_model":    map[string]any{"type": "string", "description": "Model for the reviewer step (mode=agent)."},
				"reviewer_tools": map[string]any{
					"type": "array", "items": map[string]any{"type": "string"},
					"description": "Capability-restricted tool surface for the reviewer step (mode=agent).",
				},
				"reviewer_session_id": map[string]any{"type": "string", "description": "Session ID to scope the reviewer step to (mode=agent). Optional."},
				"subject": map[string]any{
					"type":        "object",
					"description": "The prior step's literal runtime output being verified.",
					"properties": map[string]any{
						"step_kind": map[string]any{"type": "string", "enum": []string{"llm", "tool"}},
						"output":    map[string]any{"type": "string"},
						"is_error":  map[string]any{"type": "boolean"},
						"tool_calls": map[string]any{
							"type":        "array",
							"description": "Tool calls recorded during the subject step, if any.",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"tool":     map[string]any{"type": "string"},
									"input":    map[string]any{"type": "object"},
									"output":   map[string]any{"type": "string"},
									"is_error": map[string]any{"type": "boolean"},
								},
							},
						},
					},
					// is_error is required, not just documented: Verify's
					// entire job is distinguishing a passing subject from a
					// failing one, and an omitted flag decoding to Go's
					// bool zero value (false) would silently make a failed
					// subject look like it passed (e.g. engine_check=
					// no_error incorrectly passing). callWorkflowVerifyStep
					// also enforces this server-side against the raw args,
					// not just via this schema.
					"required": []string{"output", "is_error"},
				},
			},
			"required": []string{"mode", "subject"},
		},
	}
}

// decodeStepArgs decodes an MCP tool call's args map directly into out (a
// pointer to one of the agentworkflow request types) via a JSON
// round-trip. args is already the JSON-decoded form of the call's raw
// wire arguments (mcpserver.makeTransportHandler / the HTTP tool-call
// path both produce map[string]any); re-marshaling it and unmarshaling
// into the typed struct is the "parse the MCP tool call's arguments into
// the corresponding StepExecutor request type" step the ticket scopes —
// nothing more.
func decodeStepArgs(args map[string]any, out any) error {
	raw, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("marshal call arguments: %w", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode call arguments: %w", err)
	}
	return nil
}

// jsonToolResult marshals v (one of the agentworkflow result types) and
// wraps it as a text ToolResult. Marshaling cannot realistically fail —
// no channels/funcs/cycles in these fields — but a failure falls back to
// a structured error so the caller still gets a uniform shape.
func jsonToolResult(v any) (*mcp.ToolResult, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("marshal result: %v", err)), nil
	}
	return mcp.TextResult(string(body)), nil
}

func (st *SelfToolsTransport) callWorkflowExecuteLLMStep(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.WorkflowExecutor == nil {
		return mcp.ErrorResult("workflow executor is not configured"), nil
	}

	var req agentworkflow.LLMStepRequest
	if err := decodeStepArgs(args, &req); err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	if req.Provider == "" {
		return mcp.ErrorResult("provider is required"), nil
	}

	result, err := st.WorkflowExecutor.ExecuteLLMStep(ctx, req)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	return jsonToolResult(result)
}

func (st *SelfToolsTransport) callWorkflowExecuteToolStep(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.WorkflowExecutor == nil {
		return mcp.ErrorResult("workflow executor is not configured"), nil
	}

	var req agentworkflow.ToolStepRequest
	if err := decodeStepArgs(args, &req); err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	if req.Tool == "" {
		return mcp.ErrorResult("tool is required"), nil
	}

	result, err := st.WorkflowExecutor.ExecuteToolStep(ctx, req)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	return jsonToolResult(result)
}

func (st *SelfToolsTransport) callWorkflowVerifyStep(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.WorkflowExecutor == nil {
		return mcp.ErrorResult("workflow executor is not configured"), nil
	}

	// Fail closed on a missing subject.is_error against the RAW args map,
	// before decodeStepArgs's JSON round-trip silently fills it in as
	// Go's bool zero value (false). A caller-omitted is_error must not
	// be indistinguishable from an explicit is_error=false — Verify's
	// whole job is telling those apart (e.g. engine_check=no_error must
	// not incorrectly pass a subject whose error status was never
	// reported). The InputSchema also marks it required; this is
	// defense in depth against a caller that doesn't validate schemas.
	subject, ok := args["subject"].(map[string]any)
	if !ok {
		return mcp.ErrorResult("subject is required: {step_kind, output, is_error, tool_calls}"), nil
	}
	if _, present := subject["is_error"]; !present {
		return mcp.ErrorResult("subject.is_error is required (true or false) — it must not be omitted"), nil
	}

	var req agentworkflow.VerifyRequest
	if err := decodeStepArgs(args, &req); err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	if req.Mode == "" {
		return mcp.ErrorResult("mode is required (engine|agent)"), nil
	}

	result, err := st.WorkflowExecutor.Verify(ctx, req)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	return jsonToolResult(result)
}
