package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
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
	// IdempotencyKey selects a stable durable-agent and workflow-run identity.
	// It is reserved for product operations with their own persisted request
	// journal, such as TeamRun launch recovery.
	IdempotencyKey string
}

// WorkflowLaunchResult is a completed workflow run's outcome.
type WorkflowLaunchResult struct {
	InstanceID   string
	RunID        string
	WorkflowName string
	Status       agentworkflow.RunStatus
	StepResults  map[string]agentworkflow.StepResult
	Error        string
}

// DurableWorkflowHost is the single execution and control boundary used by
// every product workflow entrypoint. The embedded workflowhost.Engine
// satisfies it; callers never select or type-assert concrete engines.
type DurableWorkflowHost interface {
	Run(context.Context, agentworkflow.WorkflowDefinition, agentworkflow.WorkflowInput, agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error)
	Resume(context.Context, string, agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error)
	ResumeGate(context.Context, string, string, string, string, agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error)
	Cancel(context.Context, string, string) (agentworkflow.WorkflowResult, error)
}

type keyedDurableWorkflowHost interface {
	RunKeyed(context.Context, string, agentworkflow.WorkflowDefinition, agentworkflow.WorkflowInput, agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error)
}

// WorkflowLauncher maps a workflow run onto the existing template-class
// durable-agent lifecycle (design doc, "Integration with the rest of
// Nanite" — CW-20260813-0014): a workflow run IS a template-class durable
// agent, reusing durable_agents.go's instance/event-log machinery for
// audit/bookkeeping rather than a second, parallel tracking system.
//
// Deliberately does NOT go through driveBootSession or a chat turn. The
// shared host calls Nanite StepKind adapters directly, in-process; it never
// needs a booted CLI/HTTP
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
	host     DurableWorkflowHost
	exec     agentworkflow.StepExecutor
	durable  DurableAgentService
}

// NewWorkflowLauncher constructs a launcher over the one durable host.
func NewWorkflowLauncher(registry *agentworkflow.Registry, host DurableWorkflowHost, exec agentworkflow.StepExecutor, durable DurableAgentService) *WorkflowLauncher {
	return &WorkflowLauncher{registry: registry, host: host, exec: exec, durable: durable}
}

// GetStepExecutor returns the StepExecutor the launcher uses for workflow runs.
// CW-20260814-0017: exposed for TaskManager to use when resuming workflows.
func (l *WorkflowLauncher) GetStepExecutor() agentworkflow.StepExecutor {
	if l == nil {
		return nil
	}
	return l.exec
}

// Launch looks up req.WorkflowName, boots a template-class durable-agent
// instance for it, runs the engine to completion, and finalizes the
// instance's lifecycle (stopped, with the run id stashed in metadata_json)
// regardless of whether the run itself succeeded.
func (l *WorkflowLauncher) Launch(ctx context.Context, req WorkflowLaunchRequest) (*WorkflowLaunchResult, error) {
	if l == nil || l.registry == nil {
		return nil, fmt.Errorf("workflow: launcher not fully configured")
	}
	if strings.TrimSpace(req.WorkflowName) == "" {
		return nil, fmt.Errorf("workflow: workflow name is required")
	}
	wf, ok := l.registry.Get(req.WorkflowName)
	if !ok {
		return nil, fmt.Errorf("workflow: unknown workflow %q", req.WorkflowName)
	}
	return l.launchDefinition(ctx, wf, req)
}

// LaunchDefinition launches generated canonical material without publishing
// it into the mutable name registry. The durable host records the compiled
// plan before execution, so later Resume calls need only the run identity.
func (l *WorkflowLauncher) LaunchDefinition(ctx context.Context, definition agentworkflow.WorkflowDefinition, req WorkflowLaunchRequest) (*WorkflowLaunchResult, error) {
	if strings.TrimSpace(definition.Name) == "" {
		return nil, fmt.Errorf("workflow: definition name is required")
	}
	if req.WorkflowName != "" && req.WorkflowName != definition.Name {
		return nil, fmt.Errorf("workflow: request name %q does not match definition name %q", req.WorkflowName, definition.Name)
	}
	req.WorkflowName = definition.Name
	return l.launchDefinition(ctx, definition, req)
}

func (l *WorkflowLauncher) launchDefinition(ctx context.Context, wf agentworkflow.WorkflowDefinition, req WorkflowLaunchRequest) (*WorkflowLaunchResult, error) {
	if l == nil || l.host == nil || l.exec == nil || l.durable == nil {
		return nil, fmt.Errorf("workflow: launcher not fully configured")
	}
	if err := agentworkflow.Validate(wf); err != nil {
		return nil, fmt.Errorf("workflow: invalid definition: %w", err)
	}
	if strings.TrimSpace(req.AgentProfileID) == "" {
		return nil, fmt.Errorf("workflow: agent_profile_id is required (durable_agent_instances.profile_id is a required FK)")
	}
	if missing := missingWorkflowInputs(wf, req.Params); len(missing) != 0 {
		return nil, fmt.Errorf("workflow: workflow %q requires input(s) not present in params: %s", wf.Name, strings.Join(missing, ", "))
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
	keyed := strings.TrimSpace(req.IdempotencyKey) != ""
	if keyed {
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(req.IdempotencyKey)))
		inst.ID = "workflow-" + digest
		inst.Slug = "workflow-keyed-" + digest
	}
	if !keyed {
		if err := l.durable.Create(ctx, inst); err != nil {
			return nil, fmt.Errorf("workflow: create durable agent instance: %w", err)
		}
	} else {
		existing, getErr := l.durable.Get(ctx, inst.ID)
		switch {
		case getErr == nil:
			if existing.ProfileID != inst.ProfileID || existing.LaunchSourceID != inst.LaunchSourceID {
				return nil, fmt.Errorf("workflow: idempotency key belongs to a different durable workflow launch")
			}
			inst = existing
		case !errors.Is(getErr, store.ErrDurableAgentInstanceNotFound):
			return nil, fmt.Errorf("workflow: inspect durable agent instance: %w", getErr)
		default:
			if err := l.durable.Create(ctx, inst); err != nil {
				existing, getErr = l.durable.Get(context.WithoutCancel(ctx), inst.ID)
				if getErr != nil || existing.ProfileID != inst.ProfileID || existing.LaunchSourceID != inst.LaunchSourceID {
					return nil, fmt.Errorf("workflow: create keyed durable agent instance: %w", err)
				}
				inst = existing
			}
		}
	}
	// Threaded into WorkflowInput.SessionID below so an external engine's
	// spawned MCP subprocess scopes its callback tool calls to this run's
	// own session — audit correlation, mirroring CLI-launched agents.
	// The shared host threads WorkflowInput.SessionID into step invocations.
	sessionID := inst.CurrentSessionID
	if sessionID == "" {
		stableSessionID := ""
		if keyed {
			digest := fmt.Sprintf("%x", sha256.Sum256([]byte(req.IdempotencyKey+"\x00workflow-session")))
			stableSessionID = "workflow-session-" + digest[:32]
		}
		startResult, err := l.durable.Start(ctx, inst.ID, DurableAgentStartRequest{
			ProjectID:   req.ProjectID,
			SessionID:   stableSessionID,
			WakePayload: DurableAgentWakePayload{Reason: DurableAgentWakeManual},
		})
		if err != nil {
			return nil, fmt.Errorf("workflow: start durable agent instance %s: %w", inst.ID, err)
		}
		if startResult != nil && startResult.Session != nil {
			sessionID = startResult.Session.ID
		}
	}

	timeout := DefaultWorkflowLaunchTimeout
	if req.TimeoutSeconds > 0 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var result agentworkflow.WorkflowResult
	var runErr error
	if keyed {
		host, ok := l.host.(keyedDurableWorkflowHost)
		if !ok {
			return nil, errors.New("workflow: durable host does not support keyed launch")
		}
		result, runErr = host.RunKeyed(runCtx, req.IdempotencyKey, wf, agentworkflow.WorkflowInput{Params: req.Params, SessionID: sessionID}, l.exec)
	} else {
		result, runErr = l.host.Run(runCtx, wf, agentworkflow.WorkflowInput{Params: req.Params, SessionID: sessionID}, l.exec)
	}

	// Finalize the instance's lifecycle regardless of runErr — an
	// infra-level host failure still leaves an instance that must not be
	// left dangling in "active". A failure before durable run creation returns
	// a zero-value WorkflowResult, so RunID is omitted (not stamped as
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

	launchResult := &WorkflowLaunchResult{
		InstanceID:   inst.ID,
		RunID:        result.RunID,
		WorkflowName: wf.Name,
		Status:       result.Status,
		StepResults:  result.StepResults,
		Error:        result.Error,
	}
	if runErr != nil {
		return launchResult, fmt.Errorf("workflow: run %q: %w", wf.Name, runErr)
	}

	return launchResult, nil
}

// Resume continues a persisted run through the same durable host used for
// launch. No workflow definition registry lookup is involved.
func (l *WorkflowLauncher) Resume(ctx context.Context, runID string) (agentworkflow.WorkflowResult, error) {
	if l == nil || l.host == nil || l.exec == nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("workflow: launcher not fully configured")
	}
	if strings.TrimSpace(runID) == "" {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("workflow: run id is required")
	}
	return l.host.Resume(ctx, runID, l.exec)
}

// ResumeGate authorizes and resolves one persisted human gate, then resumes
// the run through the durable host.
func (l *WorkflowLauncher) ResumeGate(ctx context.Context, runID, stepID, input, responderReference string) (agentworkflow.WorkflowResult, error) {
	if l == nil || l.host == nil || l.exec == nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("workflow: launcher not fully configured")
	}
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(stepID) == "" || strings.TrimSpace(responderReference) == "" {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("workflow: run id, step id, and responder reference are required")
	}
	return l.host.ResumeGate(ctx, runID, stepID, input, responderReference, l.exec)
}

// Cancel cancels one persisted run through the durable host.
func (l *WorkflowLauncher) Cancel(ctx context.Context, runID, reason string) (agentworkflow.WorkflowResult, error) {
	if l == nil || l.host == nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("workflow: launcher not fully configured")
	}
	if strings.TrimSpace(runID) == "" {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("workflow: run id is required")
	}
	return l.host.Cancel(ctx, runID, reason)
}

func missingWorkflowInputs(definition agentworkflow.WorkflowDefinition, params map[string]any) []string {
	var missing []string
	for _, key := range agentworkflow.RequiredInputs(definition) {
		if _, ok := params[key]; !ok {
			missing = append(missing, key)
		}
	}
	return missing
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
