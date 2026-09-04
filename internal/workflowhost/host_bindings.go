package workflowhost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/go-workflow/diagnostic"
	"github.com/hollis-labs/go-workflow/graph"
	"github.com/hollis-labs/go-workflow/stepkind"
	"github.com/hollis-labs/go-workflow/values"
	workflowwait "github.com/hollis-labs/go-workflow/wait"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
)

// LoopStepHost is the narrow boundary from the shared workflow runtime to
// Nanite's existing LoopRun orchestrator. Implementations own LoopRun
// creation and lifecycle; the workflow host owns only the containing node and
// its durable correlation.
type LoopStepHost interface {
	LaunchWorkflowLoop(context.Context, LoopStepLaunchRequest) (LoopStepResult, error)
	ObserveWorkflowLoop(context.Context, string) (LoopStepResult, error)
}

type LoopStepLaunchRequest struct {
	IdempotencyKey     string
	WorkflowName       string
	GoalID             string
	Goal               *LoopGoal
	Budget             LoopBudget
	ContinuationPolicy LoopContinuationPolicy
	WorkflowParams     map[string]any
	AgentProfileID     string
	ProjectID          string
	ParentSessionID    string
	TimeoutSeconds     int
}

type LoopGoal struct {
	ParentGoalID       string
	Intent             string
	DesiredState       []string
	Constraints        []string
	AcceptanceCriteria []string
	Invariants         []string
	Priority           string
	Scope              string
	Owner              string
	Source             string
}

type LoopBudget struct {
	MaxIterations           int
	MaxFailures             int
	MaxRuntimeSeconds       int
	MaxNoProgressIterations int
	OnExhausted             string
}

type LoopContinuationPolicy struct {
	Provider  string
	Model     string
	AgentID   string
	SessionID string
	Tools     []string
}

type LoopStepResult struct {
	LoopRunID        string
	Status           string
	CurrentIteration int
}

// TeamStepHost owns Nanite TeamRun membership, signal evaluation, authority,
// and stand-down. The workflow host deliberately does not reproduce those
// semantics; it asks this collaborator whether a persisted signal wait may be
// resolved.
type TeamStepHost interface {
	ResolveWorkflowTeamStep(context.Context, TeamStepResolveRequest) (TeamStepResolveResult, error)
}

type TeamStepResolveRequest struct {
	WorkflowRunID string
	StepID        string
	Config        map[string]any
}

type TeamStepResolveResult struct {
	Resolved           bool
	Output             string
	ResponderReference string
}

// ExternalStepHost invokes an existing Nanite external engine as one durable
// StepKind. External frameworks never compete with go-workflow for lifecycle
// ownership.
type ExternalStepHost interface {
	ExecuteWorkflowExternalStep(context.Context, ExternalStepRequest) (ExternalStepResult, error)
}

type ExternalStepRequest struct {
	IdempotencyKey string
	Engine         string
	WorkflowName   string
	Params         map[string]any
	SessionID      string
}

type ExternalStepResult struct {
	Output  string
	IsError bool
}

type hostBindings struct {
	store    *WorkflowStateStore
	loop     LoopStepHost
	team     TeamStepHost
	external ExternalStepHost
}

func (e *Engine) WithLoopStepHost(host LoopStepHost) *Engine {
	e.loopHost = host
	return e
}

func (e *Engine) WithTeamStepHost(host TeamStepHost) *Engine {
	e.teamHost = host
	return e
}

func (e *Engine) WithExternalStepHost(host ExternalStepHost) *Engine {
	e.externalHost = host
	return e
}

type naniteLoopKind struct {
	host  LoopStepHost
	store *WorkflowStateStore
}

func (*naniteLoopKind) Spec() stepkind.StepKindSpec {
	spec := baseKindSpec(StepKindLoop, graph.EffectMutate, true)
	spec.Idempotency = graph.IdempotencyKeyed
	spec.RetrySafety = stepkind.RetryRequiresIdempotency
	return spec
}

func (*naniteLoopKind) ValidateConfig(context.Context, graph.Config) []diagnostic.Diagnostic {
	return nil
}

func (k *naniteLoopKind) Execute(ctx context.Context, prepared stepkind.PreparedInvocation) (stepkind.StepResult, error) {
	if continuation := prepared.Invocation.Continuation; continuation != nil {
		if value, ok := continuation.Values["resume"]; ok {
			if payload, ok := value.Inline.(map[string]any); ok {
				status, _ := payload["status"].(string)
				if status != "" && status != "completed" {
					return stepkind.StepResult{}, permanentExecutionError("nanite_loop_terminal", fmt.Errorf("LoopRun %v ended %s", payload["loop_run_id"], status))
				}
			}
		}
		return completedWaitResult(prepared, continuation.Values)
	}
	if k.host == nil || k.store == nil {
		return stepkind.StepResult{}, permanentExecutionError("nanite_loop_host_missing", errors.New("nanite LoopRun host is not configured"))
	}
	cfg, err := resolvedNaniteConfig(prepared.Invocation)
	if err != nil {
		return stepkind.StepResult{}, permanentExecutionError("nanite_loop_config", err)
	}
	request, err := parseLoopStepLaunchRequest(cfg, prepared.Invocation.Inputs)
	if err != nil {
		return stepkind.StepResult{}, permanentExecutionError("nanite_loop_config", err)
	}
	request.IdempotencyKey = hostEffectIdempotencyKey("nanite:workflow-loop", prepared.Invocation.Identity)
	result, err := k.host.LaunchWorkflowLoop(ctx, request)
	if err != nil {
		return stepkind.StepResult{}, adapterExecutionError("nanite_loop_launch", err)
	}
	if strings.TrimSpace(result.LoopRunID) == "" {
		return stepkind.StepResult{}, permanentExecutionError("nanite_loop_identity", errors.New("LoopRun host returned no loop_run_id"))
	}
	if loopStatusTerminal(result.Status) {
		if result.Status != "completed" {
			return stepkind.StepResult{}, permanentExecutionError("nanite_loop_terminal", fmt.Errorf("LoopRun %s ended %s", result.LoopRunID, result.Status))
		}
		return completedHostResult(prepared, fmt.Sprintf("LoopRun %s completed", result.LoopRunID))
	}
	schema, err := workflowwait.NewSchemaRef(graph.Schema{})
	if err != nil {
		return stepkind.StepResult{}, err
	}
	return stepkind.StepResult{Outcome: stepkind.StepWaiting, Wait: &stepkind.WaitResult{
		ID: fmt.Sprintf("wait-%s-%s-%s-%d", prepared.Invocation.Identity.RunID, prepared.Invocation.Identity.NodeID,
			invocationIterationDigest(prepared.Invocation.Identity.Iteration), prepared.Invocation.Identity.Attempt),
		Record: workflowwait.Record{
			Kind: workflowwait.KindChildRun, Correlation: result.LoopRunID, ResumeSchema: schema,
			Visibility: workflowwait.VisibilityPrivate,
			Authority:  workflowwait.ResponderAuthority{Kind: "nanite", Reference: result.LoopRunID, Attributes: map[string]string{"responder_kind": "loop-host"}},
			WakeSource: workflowwait.WakeChildRun, Status: workflowwait.StatusOpen,
		},
	}}, nil
}

type naniteTeamKind struct{ host TeamStepHost }

func (*naniteTeamKind) Spec() stepkind.StepKindSpec {
	return baseKindSpec(StepKindTeam, graph.EffectMutate, true)
}

func (*naniteTeamKind) ValidateConfig(context.Context, graph.Config) []diagnostic.Diagnostic {
	return nil
}

func (k *naniteTeamKind) Execute(_ context.Context, prepared stepkind.PreparedInvocation) (stepkind.StepResult, error) {
	if continuation := prepared.Invocation.Continuation; continuation != nil {
		return completedWaitResult(prepared, continuation.Values)
	}
	if k.host == nil {
		return stepkind.StepResult{}, permanentExecutionError("nanite_team_host_missing", errors.New("nanite TeamRun host is not configured"))
	}
	schema, err := workflowwait.NewSchemaRef(graph.Schema{})
	if err != nil {
		return stepkind.StepResult{}, err
	}
	runID := prepared.Invocation.Identity.RunID
	return stepkind.StepResult{Outcome: stepkind.StepWaiting, Wait: &stepkind.WaitResult{
		ID: fmt.Sprintf("wait-%s-%s-%d", runID, prepared.Invocation.Identity.NodeID, prepared.Invocation.Identity.Attempt),
		Record: workflowwait.Record{
			Kind: workflowwait.KindSignal, Correlation: runID + ":" + prepared.Invocation.Identity.NodeID,
			ResumeSchema: schema, Visibility: workflowwait.VisibilityPrivate,
			Authority:  workflowwait.ResponderAuthority{Kind: "nanite", Reference: runID, Attributes: map[string]string{"responder_kind": "team-host"}},
			WakeSource: workflowwait.WakeSignal, Status: workflowwait.StatusOpen,
		},
	}}, nil
}

type naniteExternalKind struct {
	host  ExternalStepHost
	store *WorkflowStateStore
}

func (*naniteExternalKind) Spec() stepkind.StepKindSpec {
	spec := baseKindSpec(StepKindExternalEngine, graph.EffectMutate, false)
	spec.Idempotency = graph.IdempotencyKeyed
	spec.RetrySafety = stepkind.RetryRequiresIdempotency
	spec.Observation = stepkind.ObservationSpec{Mode: stepkind.ObservationPoll}
	return spec
}

func (*naniteExternalKind) ValidateConfig(context.Context, graph.Config) []diagnostic.Diagnostic {
	return nil
}

func (k *naniteExternalKind) Execute(ctx context.Context, prepared stepkind.PreparedInvocation) (stepkind.StepResult, error) {
	if prepared.Invocation.Continuation != nil {
		return stepkind.StepResult{}, permanentExecutionError("nanite_external_continuation", errors.New("external engine step cannot receive a continuation"))
	}
	if k.host == nil || k.store == nil {
		return stepkind.StepResult{}, permanentExecutionError("nanite_external_host_missing", errors.New("nanite external engine host is not configured"))
	}
	cfg, err := resolvedNaniteConfig(prepared.Invocation)
	if err != nil {
		return stepkind.StepResult{}, permanentExecutionError("nanite_external_config", err)
	}
	request := ExternalStepRequest{
		IdempotencyKey: hostEffectIdempotencyKey("nanite:external-engine", prepared.Invocation.Identity),
		Engine:         configStringValue(cfg, "external_engine"), WorkflowName: configStringValue(cfg, "workflow_name"),
		Params: configMapValue(cfg, "params"), SessionID: inputString(prepared.Invocation.Inputs, "workflow-session-id"),
	}
	if request.Params == nil {
		if value, ok := prepared.Invocation.Inputs["workflow-input"]; ok {
			request.Params, _ = value.Inline.(map[string]any)
		}
	}
	if request.Engine == "" || request.WorkflowName == "" {
		return stepkind.StepResult{}, permanentExecutionError("nanite_external_config", errors.New("external_engine and workflow_name are required"))
	}
	receipt, err := k.store.prepareExternalExecution(ctx,
		prepared.Invocation.Identity.RunID,
		prepared.Invocation.Identity.NodeID,
		prepared.Invocation.Identity.Iteration,
		externalExecutionRequest{
			Request: request, NodeID: prepared.Invocation.Identity.NodeID,
			ProductStepID: configStringValue(prepared.Invocation.Config, "product_step_id"),
			ProductKind:   configStringValue(prepared.Invocation.Config, "product_kind"),
		}, time.Now().UTC())
	if err != nil {
		return stepkind.StepResult{}, adapterExecutionError("nanite_external_prepare", err)
	}
	return stepkind.StepResult{Outcome: stepkind.StepExternal, External: &stepkind.ExternalOperationRef{
		Kind: "nanite-external-subprocess", ID: receipt.ID,
		Metadata: map[string]string{
			"request_digest":   receipt.RequestDigest,
			"receipt_contract": "nanite-external-execution@v1",
		},
	}}, nil
}

// Observe launches a prepared subprocess only after go-workflow has durably
// suspended the containing attempt as an external operation. A surviving
// pending receipt is ambiguous: it is reported for operator resolution and is
// never invoked again automatically.
func (k *naniteExternalKind) Observe(ctx context.Context, ref stepkind.ExternalOperationRef) (stepkind.Observation, error) {
	if k.host == nil || k.store == nil {
		return stepkind.Observation{}, permanentExecutionError("nanite_external_host_missing", errors.New("nanite external engine host is not configured"))
	}
	if ref.Kind != "nanite-external-subprocess" || strings.TrimSpace(ref.ID) == "" || strings.TrimSpace(ref.Metadata["request_digest"]) == "" {
		return stepkind.Observation{}, permanentExecutionError("nanite_external_ref", errors.New("external subprocess reference is invalid"))
	}
	receipt, call, err := k.store.beginExternalExecution(context.WithoutCancel(ctx), ref.ID, ref.Metadata["request_digest"], time.Now().UTC())
	if err != nil {
		return stepkind.Observation{}, adapterExecutionError("nanite_external_begin", err)
	}
	if call {
		result, callErr := k.host.ExecuteWorkflowExternalStep(ctx, receipt.Request)
		if callErr != nil {
			receipt, err = k.store.markExternalExecutionAmbiguous(context.WithoutCancel(ctx), receipt.ID,
				"subprocess returned without an authoritative workflow result", time.Now().UTC())
			if err != nil {
				return stepkind.Observation{}, adapterExecutionError("nanite_external_ambiguous", errors.Join(callErr, err))
			}
			return externalReceiptObservation(receipt)
		}
		receipt, err = k.store.completeExternalExecution(context.WithoutCancel(ctx), receipt.ID, result, time.Now().UTC())
		if err != nil {
			return stepkind.Observation{}, adapterExecutionError("nanite_external_complete", err)
		}
	}
	return externalReceiptObservation(receipt)
}

func externalReceiptObservation(receipt ExternalExecutionReceipt) (stepkind.Observation, error) {
	switch receipt.State {
	case ExternalExecutionAmbiguous:
		return stepkind.Observation{State: stepkind.ObservationPending, Progress: map[string]string{
			"execution_state": "ambiguous", "operator_resolution": "required", "receipt_id": receipt.ID,
		}}, nil
	case ExternalExecutionCompleted:
		if receipt.Result == nil {
			return stepkind.Observation{}, permanentExecutionError("nanite_external_receipt", errors.New("completed external receipt has no result"))
		}
		if receipt.Result.IsError {
			return stepkind.Observation{State: stepkind.ObservationFailed, Failure: &stepkind.ExecutionError{
				Code: "nanite_external_failed", Message: receipt.Result.Output, Classification: stepkind.RetryPermanent,
			}}, nil
		}
		prepared := stepkind.PreparedInvocation{Invocation: stepkind.Invocation{
			Identity: stepkind.InvocationIdentity{NodeID: receipt.NodeID},
			Config: graph.Config{
				"product_step_id": receipt.ProductStepID,
				"product_kind":    receipt.ProductKind,
			},
		}}
		result, err := completedHostResult(prepared, receipt.Result.Output)
		if err != nil {
			return stepkind.Observation{}, err
		}
		return stepkind.Observation{State: stepkind.ObservationSucceeded, Result: &result}, nil
	case ExternalExecutionPrepared, ExternalExecutionPending:
		return stepkind.Observation{State: stepkind.ObservationPending, Progress: map[string]string{
			"execution_state": string(receipt.State), "receipt_id": receipt.ID,
		}}, nil
	default:
		return stepkind.Observation{}, permanentExecutionError("nanite_external_receipt", fmt.Errorf("unsupported external receipt state %q", receipt.State))
	}
}

var _ stepkind.Observer = (*naniteExternalKind)(nil)

// hostEffectIdempotencyKey identifies one logical dynamic invocation. Attempts
// deliberately do not participate: every retry of the same invocation must
// reuse the original side effect, while fan-out iterations must never alias.
func hostEffectIdempotencyKey(namespace string, identity stepkind.InvocationIdentity) string {
	return namespace + ":" + identity.RunID + ":" + identity.NodeID + ":" + invocationIterationDigest(identity.Iteration)
}

func invocationIterationDigest(iteration string) string {
	return strings.TrimPrefix(values.SHA256Digest([]byte(iteration)), "sha256:")
}

func completedWaitResult(prepared stepkind.PreparedInvocation, set values.ValueSet) (stepkind.StepResult, error) {
	resumed := any(nil)
	if value, ok := set["resume"]; ok {
		resumed = value.Inline
	}
	return completedHostResult(prepared, inlineOutput(resumed))
}

func completedHostResult(prepared stepkind.PreparedInvocation, output string) (stepkind.StepResult, error) {
	result := agentworkflow.StepResult{
		StepID: configStringValue(prepared.Invocation.Config, "product_step_id"),
		Kind:   agentworkflow.StepKind(configStringValue(prepared.Invocation.Config, "product_kind")), Output: output,
	}
	if result.StepID == "" {
		result.StepID = prepared.Invocation.Identity.NodeID
	}
	if result.Kind == "" {
		result.Kind = agentworkflow.StepKindTool
	}
	value, err := resultValue(result, prepared.Invocation.Identity.NodeID)
	if err != nil {
		return stepkind.StepResult{}, permanentExecutionError("nanite_host_output_invalid", err)
	}
	return stepkind.StepResult{Outcome: stepkind.StepCompleted, Outputs: values.ValueSet{"result": value}}, nil
}

func loopStatusTerminal(status string) bool {
	switch status {
	case "completed", "failed", "canceled":
		return true
	default:
		return false
	}
}

func parseLoopStepLaunchRequest(cfg map[string]any, inputs values.ValueSet) (LoopStepLaunchRequest, error) {
	request := LoopStepLaunchRequest{
		WorkflowName:    configStringValue(cfg, "workflow_name"),
		GoalID:          configStringValue(cfg, "goal_id"),
		AgentProfileID:  configStringValue(cfg, "agent_profile_id"),
		ProjectID:       configStringValue(cfg, "project_id"),
		ParentSessionID: configStringValue(cfg, "parent_session_id"),
		TimeoutSeconds:  configIntValue(cfg, "timeout_seconds"),
		WorkflowParams:  configMapValue(cfg, "workflow_params"),
	}
	if request.WorkflowName == "" || request.AgentProfileID == "" {
		return LoopStepLaunchRequest{}, errors.New("workflow_name and agent_profile_id are required")
	}
	if request.WorkflowParams == nil {
		if value, ok := inputs["workflow-input"]; ok {
			request.WorkflowParams, _ = value.Inline.(map[string]any)
		}
	}
	if goal := configMapValue(cfg, "goal"); goal != nil {
		request.Goal = &LoopGoal{
			ParentGoalID: configStringValue(goal, "parent_goal_id"), Intent: configStringValue(goal, "intent"),
			DesiredState: configStringSliceValue(goal, "desired_state"), Constraints: configStringSliceValue(goal, "constraints"),
			AcceptanceCriteria: configStringSliceValue(goal, "acceptance_criteria"), Invariants: configStringSliceValue(goal, "invariants"),
			Priority: configStringValue(goal, "priority"), Scope: configStringValue(goal, "scope"),
			Owner: configStringValue(goal, "owner"), Source: configStringValue(goal, "source"),
		}
	}
	if (request.GoalID == "") == (request.Goal == nil) {
		return LoopStepLaunchRequest{}, errors.New("exactly one of goal_id or goal is required")
	}
	budget := configMapValue(cfg, "budget")
	request.Budget = LoopBudget{
		MaxIterations: configIntValue(budget, "max_iterations"), MaxFailures: configIntValue(budget, "max_failures"),
		MaxRuntimeSeconds:       configIntValue(budget, "max_runtime_seconds"),
		MaxNoProgressIterations: configIntValue(budget, "max_no_progress_iterations"),
		OnExhausted:             configStringValue(budget, "on_exhausted"),
	}
	continuation := configMapValue(cfg, "continuation_policy")
	request.ContinuationPolicy = LoopContinuationPolicy{
		Provider: configStringValue(continuation, "provider"), Model: configStringValue(continuation, "model"),
		AgentID: configStringValue(continuation, "agent_id"), SessionID: configStringValue(continuation, "session_id"),
		Tools: configStringSliceValue(continuation, "tools"),
	}
	return request, nil
}
