package workflowhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/go-workflow/compile"
	"github.com/hollis-labs/go-workflow/graph"
	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
	"github.com/hollis-labs/go-workflow/values"
	workflowwait "github.com/hollis-labs/go-workflow/wait"
	"github.com/oklog/ulid/v2"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
)

const defaultMaxHostSteps = 10000

// Engine is Nanite's explicit, durable embedded go-workflow host. It contains no
// in-memory correctness state: every drive and resume reconstructs the exact
// plan from WorkflowStateStore and checks the current executable catalog
// against its immutable snapshot.
type Engine struct {
	store        *WorkflowStateStore
	maxSteps     int
	waits        *workflowruntime.WaitCoordinator
	artifacts    values.ArtifactStore
	loopHost     LoopStepHost
	teamHost     TeamStepHost
	externalHost ExternalStepHost
	recoveryMu   sync.Mutex
	recoveryNext workflowruntime.RunID
}

// WithArtifactStore attaches the exact artifact adapter attested by persisted
// host identity. Production composition calls this before publishing Engine.
func (e *Engine) WithArtifactStore(store values.ArtifactStore) *Engine {
	if store != nil {
		e.artifacts = store
	}
	return e
}

// ArtifactStore exposes the installed adapter to Nanite value-capture and
// resolution boundaries without weakening the engine's storage ownership.
func (e *Engine) ArtifactStore() values.ArtifactStore { return e.artifacts }

// NewEngine constructs the production SQLite-backed engine.
func NewEngine(store *WorkflowStateStore) (*Engine, error) {
	if store == nil || store.db == nil {
		return nil, errors.New("go-workflow engine requires a SQLite WorkflowStateStore")
	}
	return &Engine{
		store: store, maxSteps: defaultMaxHostSteps,
		waits: &workflowruntime.WaitCoordinator{Store: store},
	}, nil
}

// WithWaitCoordinator attaches Nanite's scheduler/materializer/authorizer
// integrations while retaining the same SQLite wait state as the engine.
func (e *Engine) WithWaitCoordinator(coordinator *workflowruntime.WaitCoordinator) *Engine {
	if coordinator != nil {
		e.waits = coordinator
	}
	return e
}

func (*Engine) Name() string { return "go_workflow" }

func (e *Engine) bindings() hostBindings {
	return hostBindings{store: e.store, loop: e.loopHost, team: e.teamHost, external: e.externalHost}
}

// Run translates Nanite's established workflow definition into canonical
// Graph IR, compiles it through CompileGraph, persists the exact material, and
// drives the public go-workflow runtime until terminal or durably waiting.
func (e *Engine) Run(ctx context.Context, definition agentworkflow.WorkflowDefinition, input agentworkflow.WorkflowInput, exec agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	return e.run(ctx, "", definition, input, exec)
}

// RunKeyed launches or resumes the one run owned by idempotencyKey. It is the
// durable boundary used when a product operation must survive a crash after
// the workflow row commits but before its surrounding projections do.
func (e *Engine) RunKeyed(ctx context.Context, idempotencyKey string, definition agentworkflow.WorkflowDefinition, input agentworkflow.WorkflowInput, exec agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return agentworkflow.WorkflowResult{}, errors.New("keyed workflow run requires an idempotency key")
	}
	return e.run(ctx, idempotencyKey, definition, input, exec)
}

func (e *Engine) run(ctx context.Context, idempotencyKey string, definition agentworkflow.WorkflowDefinition, input agentworkflow.WorkflowInput, exec agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	registry, err := newFrozenRegistry(exec, e.bindings())
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	compiled, err := compileWorkflowDefinition(ctx, definition, registry)
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	boundInputs := map[string]any{
		"params": input.Params, "session": input.SessionID,
	}
	if idempotencyKey == "" {
		return e.launch(ctx, compiled.material, compiled.projections, boundInputs, registry)
	}
	runID := workflowruntime.RunID("keyed-" + values.SHA256Digest([]byte(idempotencyKey)))
	if existing, loadErr := e.store.LoadRun(ctx, runID); loadErr == nil {
		if existing.Plan != planRef(compiled.material.Plan) {
			return agentworkflow.WorkflowResult{}, fmt.Errorf("keyed workflow run %q conflicts with persisted plan", idempotencyKey)
		}
		return e.Resume(ctx, string(runID), exec)
	} else if !errors.Is(loadErr, workflowruntime.ErrNotFound) {
		return agentworkflow.WorkflowResult{}, loadErr
	}
	result, launchErr := e.launchWithID(ctx, runID, compiled.material, compiled.projections, boundInputs, registry)
	if launchErr == nil {
		return result, nil
	}
	// A concurrent caller or a canceled request may have committed the keyed
	// run before the local call observed its outcome. Recover that exact row
	// instead of manufacturing a second run.
	if existing, loadErr := e.store.LoadRun(context.WithoutCancel(ctx), runID); loadErr == nil {
		if existing.Plan != planRef(compiled.material.Plan) {
			return agentworkflow.WorkflowResult{RunID: string(runID)}, fmt.Errorf("keyed workflow run %q conflicts with persisted plan", idempotencyKey)
		}
		recovered, resumeErr := e.Resume(context.WithoutCancel(ctx), string(runID), exec)
		if resumeErr == nil {
			return recovered, nil
		}
		return agentworkflow.WorkflowResult{RunID: string(runID)}, errors.Join(launchErr, resumeErr)
	}
	return result, launchErr
}

// RunSource is the graph-native pilot entrypoint. It exercises the public
// LoadBytes -> Compile -> InferValueDependencies -> ValidatePlan sequence.
func (e *Engine) RunSource(ctx context.Context, locator string, source []byte, input agentworkflow.WorkflowInput, exec agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	registry, err := newFrozenRegistry(exec, e.bindings())
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	material, err := CompileSource(ctx, locator, source, registry)
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	projections, err := projectionsForPlan(material.Plan)
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	boundInputs := make(map[string]any, len(input.Params)+1)
	for key, value := range input.Params {
		boundInputs[key] = value
	}
	for _, declaration := range material.Plan.Graph.Inputs {
		if declaration.Name == "session" {
			boundInputs["session"] = input.SessionID
		}
	}
	return e.launch(ctx, material, projections, boundInputs, registry)
}

func (e *Engine) launch(ctx context.Context, material PlanMaterial, projections []PlanNodeProjection, boundInputs map[string]any, registry *frozenRegistry) (agentworkflow.WorkflowResult, error) {
	return e.launchWithID(ctx, workflowruntime.RunID(ulid.Make().String()), material, projections, boundInputs, registry)
}

func (e *Engine) launchWithID(ctx context.Context, runID workflowruntime.RunID, material PlanMaterial, projections []PlanNodeProjection, boundInputs map[string]any, registry *frozenRegistry) (agentworkflow.WorkflowResult, error) {
	if err := e.store.RecordPlanMaterial(ctx, material); err != nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("record exact go-workflow plan: %w", err)
	}
	ref := planRef(material.Plan)
	if err := e.store.RecordPlanNodeProjections(ctx, ref, projections); err != nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("record go-workflow product projections: %w", err)
	}
	if _, err := e.store.publishDefinition(ctx, material); err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	for _, input := range material.Plan.Graph.Inputs {
		if input.Name == hostInvocationKeyInput {
			boundInputs[hostInvocationKeyInput] = string(runID)
			break
		}
	}
	clock := newHostClock(time.Now().UTC())
	bound, err := workflowruntime.BindRun(ctx, e.store, workflowruntime.BindRunRequest{
		ID: runID, Plan: &material.Plan,
		Inputs:    boundInputs,
		CreatedAt: clock.next(),
	})
	if err != nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("bind go-workflow run: %w", err)
	}
	if bound.Run == nil || hasDiagnosticErrors(bound.Diagnostics) {
		return agentworkflow.WorkflowResult{}, diagnosticsError("bind go-workflow run", bound.Diagnostics)
	}
	started, _, err := workflowruntime.StartBoundRun(ctx, e.store, *bound.Run, "nanite:start:"+string(runID))
	if err != nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("start go-workflow run: %w", err)
	}
	for _, node := range sortedGraphNodes(material.Plan.Graph.Nodes) {
		if _, err := e.store.CreateNodeInvocation(ctx, workflowruntime.CreateNodeInvocationRequest{Snapshot: workflowruntime.NodeInvocationSnapshot{
			ID:     workflowruntime.NodeInvocationID{RunID: runID, NodeID: node.ID},
			Status: workflowruntime.NodePending, CreatedAt: clock.next(), UpdatedAt: clock.current(),
		}}); err != nil && !errors.Is(err, workflowruntime.ErrAlreadyExists) {
			return agentworkflow.WorkflowResult{}, fmt.Errorf("materialize go-workflow node %q: %w", node.ID, err)
		}
	}
	if started.Status == workflowruntime.RunPending {
		transitioned, err := e.store.TransitionRun(ctx, workflowruntime.RunTransitionRequest{
			RunID: runID, ExpectedGeneration: started.Generation,
			To: workflowruntime.RunRunning, At: clock.next(),
		})
		if err != nil {
			return agentworkflow.WorkflowResult{}, fmt.Errorf("start go-workflow execution: %w", err)
		}
		started = transitioned.Snapshot
	}
	return e.drive(ctx, started, material, *bound.Run, registry, clock)
}

// Resume reconstructs a run solely from its persisted PlanRef and immutable
// PlanMaterial. A catalog mismatch after deploy is an explicit hard failure.
func (e *Engine) Resume(ctx context.Context, runID string, exec agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	if err := e.reconcileTeamSignalWaits(ctx, workflowruntime.RunID(runID), exec); err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	if err := e.reconcileTerminalLoopWaits(ctx, workflowruntime.RunID(runID), exec); err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	registry, err := e.registryForRun(ctx, workflowruntime.RunID(runID), exec)
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	if _, reconcileErr := e.reconcileExternalOperations(ctx, workflowruntime.RunID(runID), registry, 100); reconcileErr != nil {
		return agentworkflow.WorkflowResult{}, reconcileErr
	}
	run, material, bound, err := e.recoverRun(ctx, workflowruntime.RunID(runID), registry)
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	clockFloor := run.UpdatedAt
	if run.Status == workflowruntime.RunWaiting {
		invocations, listErr := e.store.ListRunInvocations(ctx, run.ID)
		if listErr != nil {
			return agentworkflow.WorkflowResult{}, listErr
		}
		for _, invocation := range invocations {
			clockFloor = maxTime(clockFloor, invocation.UpdatedAt)
		}
		clock := newHostClock(clockFloor)
		if hasReadyInvocation(invocations) {
			transitioned, transitionErr := e.store.TransitionRun(ctx, workflowruntime.RunTransitionRequest{
				RunID: run.ID, ExpectedGeneration: run.Generation,
				To: workflowruntime.RunRunning, At: clock.next(),
			})
			if transitionErr != nil {
				return agentworkflow.WorkflowResult{}, transitionErr
			}
			run = transitioned.Snapshot
		}
	}
	clock := newHostClock(maxTime(clockFloor, run.UpdatedAt))
	return e.drive(ctx, run, material, bound, registry, clock)
}

// ResumeWait authorizes no principals itself; callers are the Nanite host
// boundary and must supply the already-authorized responder provenance.
func (e *Engine) ResumeWait(ctx context.Context, request ResumeWaitRequest, exec agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	if request.WaitID == "" || strings.TrimSpace(request.ResponderKind) == "" || strings.TrimSpace(request.ResponderReference) == "" {
		return agentworkflow.WorkflowResult{}, errors.New("resume wait requires wait id and responder provenance")
	}
	wait, err := e.store.LoadWait(ctx, workflowruntime.WaitID(request.WaitID))
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	registry, err := e.registryForRun(ctx, wait.Invocation.RunID, exec)
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	payload, err := values.NewInline(request.Payload, values.Metadata{
		Producer:  values.Producer{Kind: "nanite-wait-response", Reference: request.ResponderReference, Output: "resume"},
		MediaType: "application/json", Redaction: values.RedactionPrivate, Retention: values.RetentionRun,
	})
	if err != nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("encode wait response: %w", err)
	}
	receivedAt := request.ReceivedAt.UTC()
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	resumed, err := e.waits.Resume(ctx, workflowruntime.ResumeCommand{
		WaitID: wait.Ref.ID, Correlation: wait.Correlation, Token: request.Token,
		WakeSource: wait.WakeSource,
		Responder:  workflowwait.Responder{Kind: request.ResponderKind, Reference: request.ResponderReference},
		Payload:    payload, IdempotencyKey: request.IdempotencyKey, ReceivedAt: receivedAt,
	})
	if err != nil && resumed.Wait.Generation == 0 {
		return agentworkflow.WorkflowResult{}, err
	}
	run, material, bound, recoverErr := e.recoverRun(context.WithoutCancel(ctx), wait.Invocation.RunID, registry)
	if recoverErr != nil {
		return agentworkflow.WorkflowResult{}, recoverErr
	}
	clock := newHostClock(maxTime(run.UpdatedAt, resumed.Wait.UpdatedAt))
	if run.Status == workflowruntime.RunWaiting {
		transitioned, transitionErr := e.store.TransitionRun(context.WithoutCancel(ctx), workflowruntime.RunTransitionRequest{
			RunID: run.ID, ExpectedGeneration: run.Generation,
			To: workflowruntime.RunRunning, At: clock.next(),
		})
		if transitionErr != nil {
			return agentworkflow.WorkflowResult{}, transitionErr
		}
		run = transitioned.Snapshot
	}
	result, driveErr := e.drive(ctx, run, material, bound, registry, clock)
	if driveErr != nil {
		return result, driveErr
	}
	return result, err
}

// ResumeGate is the product-facing A2A approval seam. The caller supplies its
// authenticated task identity; the go-workflow wait authorizer checks it against
// the immutable authority captured when the node suspended.
func (e *Engine) ResumeGate(ctx context.Context, runID, stepID, input, responderReference string, exec agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	wait, err := e.store.loadOpenProductWait(ctx, runID, stepID)
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	if wait.Kind != workflowwait.KindGate || wait.WakeSource != workflowwait.WakeGate {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("workflow step %q is not an open approval gate", stepID)
	}
	return e.ResumeWait(ctx, ResumeWaitRequest{
		WaitID: string(wait.Ref.ID), Payload: input,
		ResponderKind: "a2a-task", ResponderReference: responderReference,
		IdempotencyKey: fmt.Sprintf("nanite:a2a-gate:%s:%s:%s", runID, stepID, responderReference),
	}, exec)
}

// Cancel atomically records cancellation for the run, all unfinished nodes,
// and open waits. The public store contract is authoritative across restart.
func (e *Engine) Cancel(ctx context.Context, runID, reason string) (agentworkflow.WorkflowResult, error) {
	run, err := e.store.LoadRun(ctx, workflowruntime.RunID(runID))
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	if run.Status.Terminal() {
		material, loadErr := e.store.LoadPlanMaterial(ctx, run.Plan.Digest)
		if loadErr != nil {
			return agentworkflow.WorkflowResult{}, loadErr
		}
		collected, collectErr := e.collectResult(ctx, run, material.Plan)
		return collected, errors.Join(collectErr, e.cancelTeamRunMembers(context.WithoutCancel(ctx), runID))
	}
	if strings.TrimSpace(reason) == "" {
		reason = "workflow canceled by Nanite host"
	}
	result, err := e.store.RequestRunCancellation(context.WithoutCancel(ctx), workflowruntime.RequestRunCancellationRequest{
		RunID: run.ID, ExpectedGeneration: run.Generation,
		IdempotencyKey: "nanite:cancel:" + runID,
		Reason:         workflowruntime.Failure{Code: "nanite_canceled", Message: reason},
		At:             maxTime(time.Now().UTC(), run.UpdatedAt),
	})
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	material, err := e.store.LoadPlanMaterial(context.WithoutCancel(ctx), run.Plan.Digest)
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	collected, collectErr := e.collectResult(context.WithoutCancel(ctx), result.Run, material.Plan)
	return collected, errors.Join(collectErr, e.cancelTeamRunMembers(context.WithoutCancel(ctx), runID))
}

func (e *Engine) cancelTeamRunMembers(ctx context.Context, runID string) error {
	canceler, ok := e.teamHost.(interface {
		CancelWorkflowTeamRun(context.Context, string) error
	})
	if !ok {
		return nil
	}
	return canceler.CancelWorkflowTeamRun(ctx, runID)
}

type ResumeWaitRequest struct {
	WaitID             string
	Token              string
	Payload            any
	ResponderKind      string
	ResponderReference string
	IdempotencyKey     string
	ReceivedAt         time.Time
}

func (e *Engine) registryForRun(ctx context.Context, runID workflowruntime.RunID, exec agentworkflow.StepExecutor) (*frozenRegistry, error) {
	identity, err := e.store.LoadRunEngineIdentity(ctx, runID)
	if err != nil {
		return nil, err
	}
	switch identity {
	case (WorkflowEngineIdentity{Kind: EngineKindGoWorkflow, ContractVersion: EngineContractVersion}):
		return newFrozenRegistry(exec, e.bindings())
	case (WorkflowEngineIdentity{Kind: EngineKindPilotHadron, ContractVersion: PilotContractVersion}):
		return newPilotRegistry(exec)
	default:
		return nil, fmt.Errorf("unsupported embedded workflow engine identity %q@%q", identity.Kind, identity.ContractVersion)
	}
}

func (e *Engine) recoverRun(ctx context.Context, runID workflowruntime.RunID, registry *frozenRegistry) (workflowruntime.RunSnapshot, PlanMaterial, workflowruntime.BoundRun, error) {
	run, err := e.store.LoadRun(ctx, runID)
	if err != nil {
		return workflowruntime.RunSnapshot{}, PlanMaterial{}, workflowruntime.BoundRun{}, err
	}
	material, err := e.store.LoadPlanMaterial(ctx, run.Plan.Digest)
	if err != nil {
		return workflowruntime.RunSnapshot{}, PlanMaterial{}, workflowruntime.BoundRun{}, err
	}
	if _, err := verifyInstalledExecutionIdentity(material, registry); err != nil {
		return workflowruntime.RunSnapshot{}, PlanMaterial{}, workflowruntime.BoundRun{}, err
	}
	if run.Inputs == nil {
		return workflowruntime.RunSnapshot{}, PlanMaterial{}, workflowruntime.BoundRun{}, errors.New("go-workflow run has no bound input reference")
	}
	bound := workflowruntime.BoundRun{
		ID: run.ID, Plan: run.Plan, InputsRef: *run.Inputs,
		CreatedAt: run.CreatedAt, Provenance: material.Plan.Provenance,
	}
	if err := bound.Validate(); err != nil {
		return workflowruntime.RunSnapshot{}, PlanMaterial{}, workflowruntime.BoundRun{}, err
	}
	return run, material, bound, nil
}

func (e *Engine) drive(ctx context.Context, initial workflowruntime.RunSnapshot, material PlanMaterial, bound workflowruntime.BoundRun, registry *frozenRegistry, clock *hostClock) (agentworkflow.WorkflowResult, error) {
	verifiers, err := verifyInstalledExecutionIdentity(material, registry)
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	if initial.Status.Terminal() {
		return e.collectResult(ctx, initial, material.Plan)
	}
	recoveryPlan := workflowruntime.RecoveryPlan{Ref: initial.Plan, Plan: material.Plan, Visibility: material.Visibility}
	if validationErr := recoveryPlan.Validate(); validationErr != nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("invalid persisted go-workflow recovery plan: %w", validationErr)
	}
	recovery := &workflowruntime.RecoveryCoordinator{
		Store: e.store, Recovery: e.store, Inputs: e.store, Control: e.store,
		Plans: e.store, Registry: registry, Policy: naniteRepeatPolicy{}, Policies: e.store, Waits: e.waits,
	}
	dispatcher, err := workflowruntime.NewStepDispatcher(workflowruntime.DispatcherOptions{
		Store: e.store, Registry: registry, Now: clock.next,
		WaitCoordinator: e.waits,
		RetryCoordinator: &workflowruntime.RetryCoordinator{
			Store: e.store, Scheduler: e.waits.Scheduler,
			Evaluator: workflowruntime.RetryEvaluator{Authorizer: naniteRetryAuthorizer{}},
		},
		Verifiers: verifiers,
	})
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	queue := workflowruntime.NewReadyQueueCoordinator(e.store, nil)
	control := workflowruntime.NewControlFlowCoordinator(e.store, e.store, nil)
	driveID := ulid.Make().String()
	for sequence := 1; sequence <= e.maxSteps; sequence++ {
		if err := ctx.Err(); err != nil {
			return agentworkflow.WorkflowResult{}, err
		}
		if _, err := recovery.Recover(ctx, workflowruntime.RecoveryRequest{Now: clock.next()}); err != nil && !concurrentRuntimeProgress(err) {
			return agentworkflow.WorkflowResult{}, err
		}
		current, err := e.store.LoadRun(ctx, initial.ID)
		if err != nil {
			return agentworkflow.WorkflowResult{}, err
		}
		if progressed, reconcileErr := e.reconcileExternalOperations(ctx, current.ID, registry, 100); reconcileErr != nil {
			return agentworkflow.WorkflowResult{}, reconcileErr
		} else if progressed > 0 {
			continue
		}
		if current.Status.Terminal() {
			return e.collectResult(ctx, current, material.Plan)
		}
		claimAt := clock.next()
		claim, ok, err := queue.ClaimNext(ctx, workflowruntime.ReadyClaimRequest{
			RunID: current.ID, Owner: "nanite-go-workflow-host:" + driveID,
			Token:          fmt.Sprintf("%s-%s-%d", current.ID, driveID, sequence),
			IdempotencyKey: fmt.Sprintf("nanite:claim:%s:%s:%d", current.ID, driveID, sequence),
			Now:            claimAt, LeaseUntil: claimAt.Add(time.Minute),
		})
		if err != nil {
			return agentworkflow.WorkflowResult{}, err
		}
		if ok {
			node, found := graphNode(material.Plan.Graph, claim.Candidate.InvocationID.NodeID)
			if !found {
				return agentworkflow.WorkflowResult{}, fmt.Errorf("claimed node %q is absent from exact plan", claim.Candidate.InvocationID.NodeID)
			}
			dispatched, dispatchErr := dispatcher.Dispatch(ctx, workflowruntime.DispatchRequest{
				Claim: claim, Node: node,
				IdempotencyKey: "nanite:invoke:" + string(current.ID) + ":" + node.ID + ":" +
					invocationIterationDigest(claim.Candidate.InvocationID.Iteration),
			})
			if dispatchErr != nil && dispatched.Attempt.Generation == 0 {
				return agentworkflow.WorkflowResult{}, dispatchErr
			}
			continue
		}
		invocations, err := e.store.ListRunInvocations(ctx, current.ID)
		if err != nil {
			return agentworkflow.WorkflowResult{}, err
		}
		// A different process may still own an unexpired claim when Nanite
		// restarts. That is quiescent active work, not a stalled graph: return
		// control to startup and let periodic recovery revisit it after expiry.
		// Reclaiming it here would duplicate a side effect; spinning until the
		// host-step ceiling would make an otherwise healthy restart fatal.
		if hasUnexpiredRunningClaim(invocations, claimAt) {
			return e.collectResult(ctx, current, material.Plan)
		}
		if hasWaitingInvocation(invocations) {
			if current.Status != workflowruntime.RunWaiting {
				transitioned, transitionErr := e.store.TransitionRun(ctx, workflowruntime.RunTransitionRequest{
					RunID: current.ID, ExpectedGeneration: current.Generation,
					To: workflowruntime.RunWaiting, At: clock.next(),
				})
				if transitionErr != nil {
					return agentworkflow.WorkflowResult{}, transitionErr
				}
				current = transitioned.Snapshot
			}
			return e.collectResult(ctx, current, material.Plan)
		}
		completed, _, completionErr := control.ReconcileRunCompletion(ctx, material.Plan.Graph, current.ID, "nanite:complete:"+string(current.ID), clock.next())
		if errors.Is(completionErr, workflowruntime.ErrControlFlowPending) {
			continue
		}
		if errors.Is(completionErr, workflowruntime.ErrRunOutputsPending) {
			expression, expressionErr := workflowruntime.BuildExpressionContext(ctx, e.store, e.store, material.Plan.Graph, current.ID)
			if expressionErr != nil {
				return agentworkflow.WorkflowResult{}, expressionErr
			}
			finalized, finalizeErr := workflowruntime.FinalizeRunOutputs(ctx, e.store, workflowruntime.FinalizeRunRequest{
				BoundRun: bound, Run: completed, Plan: &material.Plan,
				Context: expression, Control: e.store, At: clock.next(),
			})
			if finalizeErr != nil {
				return agentworkflow.WorkflowResult{}, finalizeErr
			}
			if hasDiagnosticErrors(finalized.Diagnostics) {
				return agentworkflow.WorkflowResult{}, diagnosticsError("finalize go-workflow outputs", finalized.Diagnostics)
			}
			continue
		}
		if completionErr != nil && !concurrentRuntimeProgress(completionErr) {
			return agentworkflow.WorkflowResult{}, completionErr
		}
		if completed.Status.Terminal() {
			continue
		}
		return agentworkflow.WorkflowResult{}, fmt.Errorf("go-workflow run %s stalled with nodes %v", current.ID, summarizeInvocations(invocations))
	}
	return agentworkflow.WorkflowResult{}, fmt.Errorf("go-workflow run %s exceeded %d host steps", initial.ID, e.maxSteps)
}

func (e *Engine) collectResult(ctx context.Context, run workflowruntime.RunSnapshot, plan compile.ExecutionPlan) (agentworkflow.WorkflowResult, error) {
	result := agentworkflow.WorkflowResult{RunID: string(run.ID), StepResults: make(map[string]agentworkflow.StepResult)}
	switch run.Status {
	case workflowruntime.RunSucceeded:
		result.Status = agentworkflow.RunStatusCompleted
	case workflowruntime.RunCanceled:
		result.Status = agentworkflow.RunStatusCanceled
	case workflowruntime.RunWaiting:
		result.Status = e.waitingProductStatus(ctx, run.ID, plan)
	case workflowruntime.RunPending, workflowruntime.RunRunning:
		result.Status = agentworkflow.RunStatusRunning
	case workflowruntime.RunFailed, workflowruntime.RunTimedOut, workflowruntime.RunCrashed:
		result.Status = agentworkflow.RunStatusFailed
	default:
		result.Status = agentworkflow.RunStatusFailed
	}
	invocations, err := e.store.ListRunInvocations(ctx, run.ID)
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	for _, invocation := range invocations {
		if invocation.ID.Iteration != "" || invocation.Phase != workflowruntime.InvocationForward {
			continue
		}
		node, found := graphNode(plan.Graph, invocation.ID.NodeID)
		if !found {
			continue
		}
		stepID := configStringValue(node.Config, "product_step_id")
		if stepID == "" {
			stepID = node.ID
		}
		kindText, _ := productKindForWorkflow(node.Kind)
		if configured := configStringValue(node.Config, "product_kind"); configured != "" {
			kindText = configured
		}
		stepResult := agentworkflow.StepResult{StepID: stepID, Kind: agentworkflow.StepKind(kindText)}
		if invocation.Outputs != nil {
			set, loadErr := e.store.LoadValues(ctx, *invocation.Outputs)
			if loadErr != nil {
				return agentworkflow.WorkflowResult{}, loadErr
			}
			if value, ok := set["result"]; ok {
				decodeProductStepResult(value.Inline, &stepResult)
			}
		}
		if invocation.Status == workflowruntime.NodeFailed || invocation.Status == workflowruntime.NodeTimedOut || invocation.Status == workflowruntime.NodeCrashed {
			stepResult.IsError = true
			if invocation.LatestAttempt > 0 {
				attempt, loadErr := e.store.LoadAttempt(ctx, workflowruntime.AttemptID{Invocation: invocation.ID, Number: invocation.LatestAttempt})
				if loadErr == nil && attempt.Failure != nil {
					stepResult.Output = attempt.Failure.Message
					if result.Error == "" {
						result.Error = fmt.Sprintf("step %q failed: %s", stepID, attempt.Failure.Message)
					}
				}
			}
		}
		if invocation.Status.Terminal() || invocation.Status == workflowruntime.NodeWaiting {
			result.StepResults[stepID] = stepResult
		}
	}
	return result, nil
}

func hasUnexpiredRunningClaim(invocations []workflowruntime.NodeInvocationSnapshot, at time.Time) bool {
	for _, invocation := range invocations {
		if invocation.Status == workflowruntime.NodeRunning && invocation.Lease != nil && invocation.Lease.ExpiresAt.After(at) {
			return true
		}
	}
	return false
}

func (e *Engine) waitingProductStatus(ctx context.Context, runID workflowruntime.RunID, plan compile.ExecutionPlan) agentworkflow.RunStatus {
	waits, waitErr := e.store.RecoverOpenWaits(ctx, workflowruntime.OpenWaitQuery{RunID: runID})
	if waitErr == nil && len(waits) == 0 {
		return agentworkflow.RunStatusRunning
	}
	invocations, err := e.store.ListRunInvocations(ctx, runID)
	if err != nil {
		return agentworkflow.RunStatusWaiting
	}
	sawFlex, sawLoop := false, false
	for _, invocation := range invocations {
		if invocation.Status != workflowruntime.NodeWaiting {
			continue
		}
		node, found := graphNode(plan.Graph, invocation.ID.NodeID)
		if !found {
			return agentworkflow.RunStatusWaiting
		}
		_, class := productKindForWorkflow(node.Kind)
		switch class {
		case "gate":
			return agentworkflow.RunStatusWaiting
		case "flex":
			sawFlex = true
		case "loop":
			sawLoop = true
		default:
			return agentworkflow.RunStatusWaiting
		}
	}
	if sawFlex {
		return agentworkflow.RunStatusWaitingOnFlex
	}
	if sawLoop {
		return agentworkflow.RunStatusWaitingOnLoop
	}
	return agentworkflow.RunStatusWaiting
}

func verifyFrozenCatalog(material PlanMaterial, registry *frozenRegistry) error {
	specs, digest, err := registry.snapshot()
	if err != nil {
		return err
	}
	if digest != material.StepKindCatalogDigest || !reflect.DeepEqual(specs, material.StepKindCatalog) {
		return fmt.Errorf("go-workflow StepKind catalog differs from exact plan snapshot: recorded=%s current=%s", material.StepKindCatalogDigest, digest)
	}
	return nil
}

func decodeProductStepResult(input any, target *agentworkflow.StepResult) {
	payload, ok := input.(map[string]any)
	if !ok {
		return
	}
	if value, ok := payload["step_id"].(string); ok && value != "" {
		target.StepID = value
	}
	if value, ok := payload["kind"].(string); ok && value != "" {
		target.Kind = agentworkflow.StepKind(value)
	}
	target.Output, _ = payload["output"].(string)
	target.IsError, _ = payload["is_error"].(bool)
	if calls, ok := payload["tool_calls"].([]any); ok {
		for _, rawCall := range calls {
			call, ok := rawCall.(map[string]any)
			if !ok {
				continue
			}
			target.ToolCalls = append(target.ToolCalls, agentworkflow.ToolCallRecord{
				Tool: configStringValue(call, "tool"), Input: configMapValue(call, "input"),
				Output: configStringValue(call, "output"), IsError: call["is_error"] == true,
			})
		}
	}
	if verify, ok := payload["verify"].(map[string]any); ok {
		encoded, _ := json.Marshal(verify)
		var value agentworkflow.VerifyResult
		if json.Unmarshal(encoded, &value) == nil {
			target.VerifyResult = &value
		}
	}
}

func graphNode(value graph.Graph, nodeID string) (graph.Node, bool) {
	for _, node := range value.Nodes {
		if node.ID == nodeID {
			return node, true
		}
	}
	return graph.Node{}, false
}

func sortedGraphNodes(nodes []graph.Node) []graph.Node {
	result := append([]graph.Node(nil), nodes...)
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func hasWaitingInvocation(invocations []workflowruntime.NodeInvocationSnapshot) bool {
	for _, invocation := range invocations {
		if invocation.Status == workflowruntime.NodeWaiting {
			return true
		}
	}
	return false
}

func hasReadyInvocation(invocations []workflowruntime.NodeInvocationSnapshot) bool {
	for _, invocation := range invocations {
		if invocation.Status == workflowruntime.NodeReady {
			return true
		}
	}
	return false
}

func summarizeInvocations(invocations []workflowruntime.NodeInvocationSnapshot) []string {
	result := make([]string, len(invocations))
	for index, invocation := range invocations {
		result[index] = fmt.Sprintf("%s[%s]=%s", invocation.ID.NodeID, invocation.ID.Iteration, invocation.Status)
	}
	return result
}

func concurrentRuntimeProgress(err error) bool {
	return errors.Is(err, workflowruntime.ErrCASMismatch) ||
		errors.Is(err, workflowruntime.ErrTransitionConflict) ||
		errors.Is(err, workflowruntime.ErrAttemptConflict) ||
		errors.Is(err, workflowruntime.ErrAlreadyExists) ||
		errors.Is(err, workflowruntime.ErrControlFlowPending)
}

type hostClock struct {
	mu   sync.Mutex
	last time.Time
}

func newHostClock(floor time.Time) *hostClock { return &hostClock{last: floor.UTC()} }

func (c *hostClock) next() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().UTC()
	if !now.After(c.last) {
		now = c.last.Add(time.Nanosecond)
	}
	c.last = now
	return now
}

func (c *hostClock) current() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

func maxTime(left, right time.Time) time.Time {
	if right.After(left) {
		return right
	}
	return left
}
