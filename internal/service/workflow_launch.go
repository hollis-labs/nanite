package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/hollis-labs/agentkit/agentruntime/runtimekind"
	"github.com/oklog/ulid/v2"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

// DefaultWorkflowLaunchTimeout bounds a workflow run's wall time when the
// caller passes TimeoutSeconds <= 0. Matches internal/subagent's compiled-in
// DefaultTimeoutSeconds (1800s) — a workflow run is the same class of
// long-running, potentially-hung operation a chat turn is blocking on.
const DefaultWorkflowLaunchTimeout = 1800 * time.Second

// WorkflowLaunchRequest is the input to WorkflowLauncher.Launch.
type WorkflowLaunchRequest struct {
	// WorkflowName identifies the registered workflow definition to run.
	WorkflowName string
	// Params carries the run's initial arguments, forwarded to the engine
	// as agentworkflow.WorkflowInput.Params.
	Params map[string]any

	// WorkspaceID is required — durable_agents.go's Start() requires it
	// when creating a fresh session (ErrDurableAgentWorkspaceRequired),
	// and a template-class instance's fresh_one_shot session policy
	// always creates fresh.
	WorkspaceID string
	// ProjectID is optional, forwarded to Start's session-creation request.
	ProjectID string
	// AgentProfileID identifies the agent_profiles row the launched
	// durable_agent_instance is scoped under — required, since
	// durable_agent_instances.profile_id is a NOT NULL FK. Callers thread
	// through whichever profile initiated the workflow run (mirrors how
	// dispatch.SpawnRequest.AgentProfileID is threaded for the H1 trust
	// gate elsewhere in dispatch).
	AgentProfileID string
	// ParentSessionID is the calling chat session, if any — informational,
	// stamped into the created instance's Name for operator visibility.
	ParentSessionID string
	// TimeoutSeconds caps the run's wall time. <= 0 uses
	// DefaultWorkflowLaunchTimeout.
	TimeoutSeconds int
}

// WorkflowLaunchResult is a completed workflow run's outcome.
type WorkflowLaunchResult struct {
	InstanceID  string
	RunID       string
	WorkflowName string
	Status      agentworkflow.RunStatus
	StepResults map[string]agentworkflow.StepResult
	Error       string
}

// WorkflowLauncher maps a workflow run onto the existing template-class
// durable-agent lifecycle (design doc, "Integration with the rest of
// Nanite" — CW-20260813-0014): a workflow run IS a template-class durable
// agent, reusing durable_agents.go's instance/event-log machinery for
// audit/bookkeeping rather than a second, parallel tracking system.
//
// Deliberately does NOT go through driveBootSession or a chat turn. The
// built-in engine calls StepExecutor directly, in-process (design doc:
// "no wrapper needed, same binary") — it never needs a booted CLI/HTTP
// runtime session, so Start() here only produces the session-attach
// bookkeeping / durable_agent_events audit trail; the engine runs
// synchronously against l.exec right after.
//
// No dedicated FK links a workflow_runs row back to the durable_agent
// instance that launched it — the link lives in the instance's
// metadata_json (`workflow_run_id`), stamped after Run() returns (the
// engine generates its own run id internally and only exposes it on the
// returned WorkflowResult).
type WorkflowLauncher struct {
	registry *agentworkflow.Registry
	// engines is keyed by each WorkflowEngine's own Name() — the identity
	// a WorkflowDefinition.Engine field selects against (CW-20260814-0003:
	// engine selection so LangGraph/CrewAI are reachable alongside the
	// built-in engine, not just it).
	engines map[string]agentworkflow.WorkflowEngine
	exec    agentworkflow.StepExecutor
	durable DurableAgentService
}

// NewWorkflowLauncher constructs a WorkflowLauncher. registry, exec, and
// durable are required; engines must contain at least one entry keyed
// under agentworkflow.EngineBuiltin — checked at call time so a wiring bug
// surfaces as a typed error rather than a nil-pointer panic or a
// launch-time "unknown engine" surprise for the common case.
func NewWorkflowLauncher(registry *agentworkflow.Registry, engines map[string]agentworkflow.WorkflowEngine, exec agentworkflow.StepExecutor, durable DurableAgentService) *WorkflowLauncher {
	return &WorkflowLauncher{registry: registry, engines: engines, exec: exec, durable: durable}
}

// Launch looks up req.WorkflowName, boots a template-class durable-agent
// instance for it, runs the engine to completion, and finalizes the
// instance's lifecycle (stopped, with the run id stashed in metadata_json)
// regardless of whether the run itself succeeded.
func (l *WorkflowLauncher) Launch(ctx context.Context, req WorkflowLaunchRequest) (*WorkflowLaunchResult, error) {
	if l == nil || l.registry == nil || len(l.engines) == 0 || l.exec == nil || l.durable == nil {
		return nil, fmt.Errorf("workflow: launcher not fully configured")
	}
	if req.WorkflowName == "" {
		return nil, fmt.Errorf("workflow: workflow name is required")
	}
	wf, ok := l.registry.Get(req.WorkflowName)
	if !ok {
		return nil, fmt.Errorf("workflow: unknown workflow %q", req.WorkflowName)
	}
	engineName := wf.Engine
	if engineName == "" {
		engineName = agentworkflow.EngineBuiltin
	}
	engine, ok := l.engines[engineName]
	if !ok {
		return nil, fmt.Errorf("workflow: workflow %q targets engine %q, which is not registered on this launcher", wf.Name, engineName)
	}
	if req.WorkspaceID == "" {
		return nil, ErrDurableAgentWorkspaceRequired
	}
	if req.AgentProfileID == "" {
		return nil, fmt.Errorf("workflow: agent_profile_id is required (durable_agent_instances.profile_id is a required FK)")
	}

	instName := fmt.Sprintf("workflow: %s", wf.Name)
	if req.ParentSessionID != "" {
		instName = fmt.Sprintf("workflow: %s (session %s)", wf.Name, req.ParentSessionID)
	}
	inst := &store.DurableAgentInstance{
		Name:             instName,
		Slug:             workflowInstanceSlug(wf.Name),
		ProfileID:        req.AgentProfileID,
		LifecycleClass:   store.DurableAgentClassTemplate,
		RuntimeKind:      string(runtimekind.API),
		LaunchSourceType: store.DurableAgentLaunchTaskTemplateRun,
		LaunchSourceID:   wf.Name,
	}
	if err := l.durable.Create(ctx, inst); err != nil {
		return nil, fmt.Errorf("workflow: create durable agent instance: %w", err)
	}

	startResult, err := l.durable.Start(ctx, inst.ID, DurableAgentStartRequest{
		WorkspaceID: req.WorkspaceID,
		ProjectID:   req.ProjectID,
		WakePayload: DurableAgentWakePayload{Reason: DurableAgentWakeManual},
	})
	if err != nil {
		return nil, fmt.Errorf("workflow: start durable agent instance %s: %w", inst.ID, err)
	}
	// Threaded into WorkflowInput.SessionID below so an external engine's
	// spawned MCP subprocess scopes its callback tool calls to this run's
	// own session — audit correlation, mirroring CLI-launched agents.
	// BuiltinWorkflowEngine does not read WorkflowInput.SessionID.
	sessionID := ""
	if startResult != nil && startResult.Session != nil {
		sessionID = startResult.Session.ID
	}

	timeout := DefaultWorkflowLaunchTimeout
	if req.TimeoutSeconds > 0 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, runErr := engine.Run(runCtx, wf, agentworkflow.WorkflowInput{Params: req.Params, SessionID: sessionID}, l.exec)

	// Finalize the instance's lifecycle regardless of runErr — an
	// infra-level engine failure still leaves an instance that must not be
	// left dangling in "active". BuiltinWorkflowEngine returns a zero-value
	// WorkflowResult on an infra error, so RunID is omitted (not stamped as
	// a misleading empty string) when there was no real run to link to.
	metaBytes, marshalErr := json.Marshal(workflowInstanceMetadata{
		WorkflowName:  wf.Name,
		WorkflowRunID: result.RunID,
	})
	if marshalErr != nil {
		slog.Warn("workflow: marshal instance metadata failed",
			"instance_id", inst.ID, "run_id", result.RunID, "err", marshalErr)
	} else {
		metaJSON := string(metaBytes)
		if _, updErr := l.durable.Update(ctx, inst.ID, store.DurableAgentInstanceUpdate{MetadataJSON: &metaJSON}); updErr != nil {
			slog.Warn("workflow: stash workflow_run_id on instance metadata failed",
				"instance_id", inst.ID, "run_id", result.RunID, "err", updErr)
		}
	}
	if _, stopErr := l.durable.RequestStop(ctx, inst.ID); stopErr != nil {
		slog.Warn("workflow: request-stop after run completion failed",
			"instance_id", inst.ID, "err", stopErr)
	}

	if runErr != nil {
		return nil, fmt.Errorf("workflow: run %q: %w", wf.Name, runErr)
	}

	return &WorkflowLaunchResult{
		InstanceID:   inst.ID,
		RunID:        result.RunID,
		WorkflowName: wf.Name,
		Status:       result.Status,
		StepResults:  result.StepResults,
		Error:        result.Error,
	}, nil
}

// workflowInstanceMetadata is the shape stashed into a workflow-run
// durable_agent_instance's metadata_json. WorkflowRunID is omitted (not
// present as an empty string) when the engine never produced a run — an
// infra-level failure before/during Run() leaves no run to link to.
type workflowInstanceMetadata struct {
	WorkflowName  string `json:"workflow_name"`
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
}

var workflowSlugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

// workflowInstanceSlug builds a unique, DB-safe slug for a workflow-run
// durable_agent_instance (durable_agent_instances.slug is UNIQUE). Every
// call produces a fresh slug even for the same workflow name — a workflow
// can run more than once concurrently.
func workflowInstanceSlug(workflowName string) string {
	clean := workflowSlugUnsafe.ReplaceAllString(strings.ToLower(workflowName), "-")
	clean = strings.Trim(clean, "-")
	if clean == "" {
		clean = "workflow"
	}
	return fmt.Sprintf("workflow-%s-%s", clean, strings.ToLower(ulid.Make().String()))
}
