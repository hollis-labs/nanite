package selftools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/mcp"
)

// self_tools_workflow_run.go implements workflow_run — the dispatch-facing
// MCP self-tool CW-20260813-0014 adds alongside task_execute. Where
// task_execute always spawns a freeform Worker/Planner agent, workflow_run
// runs a named, defined workflow to completion through the built-in engine
// (CW-20260813-0010), reusing the SAME WorkflowLauncher a reflex-routed
// task_execute call reaches via dispatch.ExecuteTask's RoleWorkflow branch
// (execute.go) — one execution path, two entry points.
//
// Like task_execute, the result is wrapped in a chat.Envelope before
// returning — raw workflow step output never reaches the calling agent's
// context window in an unstructured form.

func workflowRunToolDefinition() mcp.Tool {
	return mcp.Tool{
		Name: "workflow_run",
		Description: "Run a named, defined workflow to completion (CW-20260813-0014). Use this instead of task_execute when the request matches a rigid, repeatable process that already has a registered workflow definition — a fixed sequence of steps that should run in that order regardless of what an LLM would decide on its own, optionally with independent verification of each step's result.\n\n" +
			"**When to use:** Only when `workflow_name` names a workflow already registered in the workflow-definitions directory. Do not guess a name — if unsure whether one exists for this task, use task_execute instead.\n\n" +
			"**What happens:** A template-class durable-agent instance is created for audit/bookkeeping, then the workflow's steps run through the built-in DAG engine (llm/tool/gate steps, with optional per-step verification) until completion or an unresolved gate. This call blocks until the run reaches a terminal state (or times out) — it does not return partial progress.\n\n" +
			"**Output shape:** An envelope summarizing the run's status and each step's outcome, same channel as task_execute.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id":    map[string]any{"type": "string", "description": "The current session ID. Informational — stamped on the launched durable-agent instance for operator visibility."},
				"workflow_name": map[string]any{"type": "string", "description": "The registered workflow definition's name to run."},
				"params": map[string]any{
					"type":        "object",
					"description": "Initial arguments for the run, available to step configs via {{input.<key>}} template references.",
				},
				"timeout_seconds": map[string]any{"type": "integer", "description": "Wall-time cap for the run. 0 uses the launcher default (1800s)."},
			},
			"required": []string{"workflow_name"},
		},
	}
}

func (st *SelfToolsTransport) callWorkflowRun(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.WorkflowLauncher == nil {
		return mcp.ErrorResult("workflow launcher is not configured"), nil
	}

	name := strArg(args, "workflow_name", "")
	if name == "" {
		return mcp.ErrorResult("workflow_name is required"), nil
	}
	params, _ := args["params"].(map[string]any)

	// CW-20260815-0022: fail fast with a concrete, actionable error when
	// params is missing a key the named workflow's steps actually
	// reference via {{input.<key>}} — rather than letting the run start
	// and fail deep inside step-config resolution with the engine's
	// generic "reference to unknown input" error. Best-effort (see
	// agentworkflow.RequiredInputs's doc comment) and skipped entirely
	// when WorkflowRegistry isn't wired.
	if st.WorkflowRegistry != nil {
		if def, ok := st.WorkflowRegistry.Get(name); ok {
			if missing := missingRequiredInputs(def, params); len(missing) > 0 {
				return mcp.ErrorResult(fmt.Sprintf(
					"workflow_run: workflow %q requires input(s) not present in params: %s. Supply them, e.g. workflow_run(workflow_name: %q, params: {%s: \"...\"}).",
					name, strings.Join(missing, ", "), name, missing[0],
				)), nil
			}
		}
		// A name absent from the registry is deliberately NOT rejected
		// here — that's WorkflowLauncher.Launch's job (it knows about
		// non-built-in-engine sources this registry doesn't), so let the
		// call proceed and surface whatever error Launch produces.
	}

	// H1 trust resolution (CW-20260421-0014), same as task_execute:
	// AgentProfileID comes from the caller-profile ctx stamped by the
	// service layer, not an LLM-suppliable arg. A workflow launch requires
	// it — durable_agent_instances.profile_id is a NOT NULL FK. Phase 0
	// item 20 (retire workspaces): Start() used to also require a
	// workspace to create a fresh session; sessions.workspace_id is gone.
	apID := mcp.CallerProfileFromContext(ctx)
	if apID == "" {
		return mcp.ErrorResult("workflow_run: no resolvable agent profile for this session"), nil
	}

	result, err := st.WorkflowLauncher.Launch(ctx, dispatch.WorkflowLaunchRequest{
		WorkflowName:    name,
		Params:          params,
		ParentSessionID: strArg(args, "session_id", ""),
		AgentProfileID:  apID,
		TimeoutSeconds:  mcp.IntArg(args, "timeout_seconds", 0),
	})
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}

	wrapper := st.DispatchWrapper
	if wrapper == nil {
		wrapper = dispatch.DefaultEnvelopeWrapper{}
	}
	env, err := wrapper.Wrap(dispatch.RoleWorkflow, result)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("workflow_run: wrap envelope: %v", err)), nil
	}
	envJSON, err := json.Marshal(env)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("workflow_run: marshal envelope: %v", err)), nil
	}
	return mcp.TextResult(string(envJSON)), nil
}

// missingRequiredInputs returns the subset of def's statically-referenced
// {{input.<key>}} keys (agentworkflow.RequiredInputs) that params does not
// supply, in the same first-seen order RequiredInputs returns them.
func missingRequiredInputs(def agentworkflow.WorkflowDefinition, params map[string]any) []string {
	var missing []string
	for _, key := range agentworkflow.RequiredInputs(def) {
		if _, ok := params[key]; !ok {
			missing = append(missing, key)
		}
	}
	return missing
}
