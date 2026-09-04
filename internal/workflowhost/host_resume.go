package workflowhost

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
	workflowwait "github.com/hollis-labs/go-workflow/wait"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
)

type CallbackResumeRequest struct {
	RunID                  string
	StepID                 string
	Token                  string
	Payload                any
	AuthenticatedKind      string
	AuthenticatedPrincipal string
	IdempotencyKey         string
	ReceivedAt             time.Time
}

// ResumeCallback is the explicit authenticated and idempotent host seam for
// callback waits. Authentication happens at the calling boundary; the exact
// principal is checked again against the immutable wait authority.
func (e *Engine) ResumeCallback(ctx context.Context, request CallbackResumeRequest, exec agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	if strings.TrimSpace(request.RunID) == "" || strings.TrimSpace(request.StepID) == "" ||
		strings.TrimSpace(request.AuthenticatedKind) == "" || strings.TrimSpace(request.AuthenticatedPrincipal) == "" ||
		strings.TrimSpace(request.IdempotencyKey) == "" || request.ReceivedAt.IsZero() {
		return agentworkflow.WorkflowResult{}, errors.New("callback resume requires run, step, authenticated principal, idempotency key, and received_at")
	}
	wait, err := e.store.loadProductWait(ctx, request.RunID, request.StepID, false)
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	if wait.Kind != workflowwait.KindCallback || wait.WakeSource != workflowwait.WakeCallback {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("workflow step %q is not a callback wait", request.StepID)
	}
	responder := workflowwait.Responder{Kind: request.AuthenticatedKind, Reference: request.AuthenticatedPrincipal}
	if err := (NaniteResponderAuthorizer{}).AuthorizeResume(ctx, workflowwait.AuthorizationRequest{
		Record: wait.Record, Source: wait.WakeSource, Responder: responder,
	}); err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	return e.ResumeWait(ctx, ResumeWaitRequest{
		WaitID: string(wait.Ref.ID), Token: request.Token, Payload: request.Payload,
		ResponderKind: request.AuthenticatedKind, ResponderReference: request.AuthenticatedPrincipal,
		IdempotencyKey: request.IdempotencyKey, ReceivedAt: request.ReceivedAt,
	}, exec)
}

// ResolveTeamSignal delegates all flex/TeamRun semantics to TeamStepHost,
// then closes the canonical signal wait only after that collaborator reports
// a real, authorized resolution (including member stand-down).
func (e *Engine) ResolveTeamSignal(ctx context.Context, runID, stepID string, exec agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	if e.teamHost == nil {
		return agentworkflow.WorkflowResult{}, errors.New("nanite TeamRun host is not configured")
	}
	wait, err := e.store.loadProductWait(ctx, runID, stepID, false)
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	if wait.Kind != workflowwait.KindSignal || wait.WakeSource != workflowwait.WakeSignal {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("workflow step %q is not a TeamRun signal wait", stepID)
	}
	if wait.Status != workflowruntime.WaitOpen {
		// Resolution/stand-down already committed. Drive any ready continuation
		// without invoking the TeamRun collaborator a second time.
		return e.Resume(ctx, runID, exec)
	}
	result, err := e.resolveOpenTeamSignal(ctx, wait, exec)
	return result, err
}

func (e *Engine) resolveOpenTeamSignal(ctx context.Context, wait workflowruntime.WaitSnapshot, exec agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	runID := string(wait.Invocation.RunID)
	stepID := wait.Invocation.NodeID
	registry, err := e.registryForRun(ctx, wait.Invocation.RunID, exec)
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	run, material, _, err := e.recoverRun(ctx, wait.Invocation.RunID, registry)
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	node, ok := graphNode(material.Plan.Graph, wait.Invocation.NodeID)
	if !ok {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("TeamRun node %q is absent from exact plan", wait.Invocation.NodeID)
	}
	resolution, err := e.teamHost.ResolveWorkflowTeamStep(ctx, TeamStepResolveRequest{
		WorkflowRunID: runID, StepID: stepID, Config: map[string]any(node.Config),
	})
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	if !resolution.Resolved {
		result, collectErr := e.collectResult(ctx, run, material.Plan)
		return result, collectErr
	}
	// The immutable wait authority is the owning workflow run. The exact Team
	// member that fired is retained in the service's durable resolution receipt
	// and output; it is not allowed to impersonate the run-scoped host authority.
	result, resumeErr := e.ResumeWait(ctx, ResumeWaitRequest{
		WaitID: string(wait.Ref.ID), Payload: resolution.Output,
		ResponderKind: "team-host", ResponderReference: runID,
		IdempotencyKey: "nanite:team-signal:" + runID + ":" + stepID,
		ReceivedAt:     deterministicResumeTime(wait),
	}, exec)
	// ResumeWait can return a downstream drive error after the wait transition
	// itself committed. Acknowledge the Team receipt based on authoritative wait
	// state, not merely on a nil method error.
	closed, loadErr := e.store.LoadWait(context.WithoutCancel(ctx), wait.Ref.ID)
	if resumeErr == nil || (loadErr == nil && closed.Status != workflowruntime.WaitOpen) {
		if completer, ok := e.teamHost.(interface {
			CompleteWorkflowTeamStep(context.Context, string, string) error
		}); ok {
			completeErr := completer.CompleteWorkflowTeamStep(context.WithoutCancel(ctx), runID, stepID)
			if completeErr != nil {
				return result, errors.Join(resumeErr, fmt.Errorf("complete TeamRun signal %s/%s: %w", runID, stepID, completeErr))
			}
		}
	}
	return result, resumeErr
}

// ResumeLoopTerminal verifies the authoritative LoopRun is terminal before it
// resolves the containing workflow node. Replays use one deterministic key.
func (e *Engine) ResumeLoopTerminal(ctx context.Context, loopRunID string, exec agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	if e.loopHost == nil {
		return agentworkflow.WorkflowResult{}, errors.New("nanite LoopRun host is not configured")
	}
	wait, err := e.store.loadLoopWait(ctx, loopRunID)
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	observed, err := e.loopHost.ObserveWorkflowLoop(ctx, loopRunID)
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	if !loopStatusTerminal(observed.Status) {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("LoopRun %s is not terminal (status %s)", loopRunID, observed.Status)
	}
	payload := map[string]any{"loop_run_id": loopRunID, "status": observed.Status, "current_iteration": observed.CurrentIteration}
	return e.ResumeWait(ctx, ResumeWaitRequest{
		WaitID: string(wait.Ref.ID), Payload: payload,
		ResponderKind: "loop-host", ResponderReference: loopRunID,
		IdempotencyKey: "nanite:loop-terminal:" + loopRunID,
		ReceivedAt:     deterministicResumeTime(wait),
	}, exec)
}

func deterministicResumeTime(wait workflowruntime.WaitSnapshot) time.Time {
	return wait.CreatedAt.UTC().Add(time.Nanosecond)
}

// reconcileTeamSignalWaits evaluates persisted Team signals whenever the
// ordinary host Resume path runs. The service's periodic/startup reconciler
// calls that same Resume surface, so Team progress does not depend on a
// concrete Engine cast or an in-memory notification.
func (e *Engine) reconcileTeamSignalWaits(ctx context.Context, runID workflowruntime.RunID, exec agentworkflow.StepExecutor) error {
	if e.teamHost == nil {
		return nil
	}
	waits, err := e.store.RecoverOpenWaits(ctx, workflowruntime.OpenWaitQuery{RunID: runID})
	if err != nil {
		return err
	}
	for _, wait := range waits {
		if wait.Kind != workflowwait.KindSignal || wait.WakeSource != workflowwait.WakeSignal ||
			wait.Authority.Attributes["responder_kind"] != "team-host" {
			continue
		}
		_, resolveErr := e.resolveOpenTeamSignal(ctx, wait, exec)
		if resolveErr != nil && !concurrentRuntimeProgress(resolveErr) {
			return fmt.Errorf("reconcile TeamRun signal %s/%s: %w", runID, wait.Invocation.NodeID, resolveErr)
		}
		// A resolved signal may drive the run into another Team wait. Let the
		// next bounded reconciliation pass evaluate that newly persisted state.
		return nil
	}
	return nil
}

// reconcileTerminalLoopWaits lets the ordinary WorkflowEngine.Resume seam
// consume a LoopRun terminal notification without a concrete Engine cast.
// Only waits minted by the current loop StepKind are eligible; the pilot's
// generic child-run waits retain their frozen recovery behavior.
func (e *Engine) reconcileTerminalLoopWaits(ctx context.Context, runID workflowruntime.RunID, exec agentworkflow.StepExecutor) error {
	if e.loopHost == nil {
		return nil
	}
	waits, err := e.store.RecoverOpenWaits(ctx, workflowruntime.OpenWaitQuery{RunID: runID})
	if err != nil {
		return err
	}
	for _, wait := range waits {
		if wait.Kind != workflowwait.KindChildRun || wait.WakeSource != workflowwait.WakeChildRun ||
			wait.Authority.Attributes["responder_kind"] != "loop-host" {
			continue
		}
		observed, observeErr := e.loopHost.ObserveWorkflowLoop(ctx, wait.Correlation)
		if observeErr != nil {
			return fmt.Errorf("observe LoopRun %s: %w", wait.Correlation, observeErr)
		}
		if !loopStatusTerminal(observed.Status) {
			continue
		}
		result, resumeErr := e.ResumeWait(ctx, ResumeWaitRequest{
			WaitID: string(wait.Ref.ID),
			Payload: map[string]any{
				"loop_run_id": wait.Correlation, "status": observed.Status,
				"current_iteration": observed.CurrentIteration,
			},
			ResponderKind: "loop-host", ResponderReference: wait.Correlation,
			IdempotencyKey: "nanite:loop-terminal:" + wait.Correlation,
			ReceivedAt:     deterministicResumeTime(wait),
		}, exec)
		if resumeErr != nil && !concurrentRuntimeProgress(resumeErr) {
			return resumeErr
		}
		if resumeErr == nil && (result.Status == agentworkflow.RunStatusCompleted || result.Status == agentworkflow.RunStatusFailed || result.Status == agentworkflow.RunStatusCanceled) {
			return nil
		}
	}
	return nil
}

func (s *WorkflowStateStore) loadLoopWait(ctx context.Context, loopRunID string) (workflowruntime.WaitSnapshot, error) {
	var waitID string
	err := s.db.QueryRowContext(ctx, `
SELECT w.wait_id
FROM workflow_waits w
JOIN workflow_runs r ON r.id=w.run_id
JOIN workflow_plan_node_projections p ON p.plan_digest=r.plan_digest AND p.node_id=w.node_id
WHERE p.wait_class='loop' AND json_extract(w.record_json,'$.correlation')=?
ORDER BY w.created_at DESC,w.wait_id DESC LIMIT 1`, loopRunID).Scan(&waitID)
	if errors.Is(err, sql.ErrNoRows) {
		return workflowruntime.WaitSnapshot{}, fmt.Errorf("%w: LoopRun wait %q", workflowruntime.ErrNotFound, loopRunID)
	}
	if err != nil {
		return workflowruntime.WaitSnapshot{}, err
	}
	return s.LoadWait(ctx, workflowruntime.WaitID(waitID))
}

type ActiveRecoveryReport struct {
	Inspected int
	Resumed   int
	Failures  []error
}

// RecoverActive performs one bounded fair pass. Canonical runtime recovery
// owns ready/crashed/resumed-wait transitions; this host layer additionally
// bridges terminal Nanite LoopRuns and true nested workflow children. The
// active-run cursor wraps so a permanently waiting low-ID run cannot starve
// later runs when this method is called periodically with a bounded limit.
func (e *Engine) RecoverActive(ctx context.Context, exec agentworkflow.StepExecutor, limit int) (ActiveRecoveryReport, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > workflowruntime.MaximumRunQueryLimit {
		return ActiveRecoveryReport{}, fmt.Errorf("recovery limit %d exceeds maximum %d", limit, workflowruntime.MaximumRunQueryLimit)
	}
	report := ActiveRecoveryReport{}
	loopIDs, err := e.store.recoverTerminalLoopIDs(ctx, limit)
	if err != nil {
		return report, err
	}
	for _, loopID := range loopIDs {
		report.Inspected++
		if _, resumeErr := e.ResumeLoopTerminal(ctx, loopID, exec); resumeErr != nil {
			if !concurrentRuntimeProgress(resumeErr) && !errors.Is(resumeErr, workflowruntime.ErrNotFound) {
				report.Failures = append(report.Failures, fmt.Errorf("recover LoopRun %s: %w", loopID, resumeErr))
			}
		} else {
			report.Resumed++
		}
	}
	children, err := e.store.RecoverChildTerminalWaits(ctx, limit)
	if err != nil {
		return report, err
	}
	for _, child := range children {
		report.Inspected++
		kind := child.Wait.Authority.Attributes["responder_kind"]
		if kind == "" {
			kind = "child-run-host"
		}
		_, resumeErr := e.ResumeWait(ctx, ResumeWaitRequest{
			WaitID: string(child.Wait.Ref.ID), Payload: map[string]any{"child_run_id": child.Child.ID, "status": child.Child.Status},
			ResponderKind: kind, ResponderReference: child.Wait.Authority.Reference,
			IdempotencyKey: "nanite:child-terminal:" + string(child.Child.ID), ReceivedAt: deterministicResumeTime(child.Wait),
		}, exec)
		if resumeErr != nil {
			if !concurrentRuntimeProgress(resumeErr) && !errors.Is(resumeErr, workflowruntime.ErrNotFound) {
				report.Failures = append(report.Failures, fmt.Errorf("recover child %s: %w", child.Child.ID, resumeErr))
			}
		} else {
			report.Resumed++
		}
	}
	activeRunIDs, err := e.nextActiveRecoveryRunIDs(ctx, limit)
	if err != nil {
		return report, err
	}
	for _, runID := range activeRunIDs {
		identity, identityErr := e.store.LoadRunEngineIdentity(ctx, runID)
		if identityErr != nil {
			report.Failures = append(report.Failures, identityErr)
			continue
		}
		if identity.Kind != EngineKindGoWorkflow && identity.Kind != EngineKindPilotHadron {
			continue
		}
		report.Inspected++
		if _, resumeErr := e.Resume(ctx, string(runID), exec); resumeErr != nil {
			if !concurrentRuntimeProgress(resumeErr) {
				report.Failures = append(report.Failures, fmt.Errorf("recover workflow %s: %w", runID, resumeErr))
			}
		} else {
			report.Resumed++
		}
	}
	return report, errors.Join(report.Failures...)
}

// RunActiveRecoveryLoop is the production lifecycle for ongoing shared-host
// reconciliation. A failed pass is reported but never stops later passes;
// terminal LoopRun notifications and other transient failures therefore do
// not require a process restart to make progress.
func (e *Engine) RunActiveRecoveryLoop(
	ctx context.Context,
	exec agentworkflow.StepExecutor,
	interval time.Duration,
	limit int,
	observe func(ActiveRecoveryReport, error),
) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			report, err := e.RecoverActive(ctx, exec, limit)
			if observe != nil {
				observe(report, err)
			}
		}
	}
}

func (e *Engine) nextActiveRecoveryRunIDs(ctx context.Context, limit int) ([]workflowruntime.RunID, error) {
	e.recoveryMu.Lock()
	defer e.recoveryMu.Unlock()
	ids, err := e.store.recoverActiveRunIDsAfter(ctx, e.recoveryNext, limit)
	if err != nil {
		return nil, err
	}
	if len(ids) != 0 {
		e.recoveryNext = ids[len(ids)-1]
	}
	return ids, nil
}

func (s *WorkflowStateStore) recoverActiveRunIDsAfter(
	ctx context.Context,
	after workflowruntime.RunID,
	limit int,
) ([]workflowruntime.RunID, error) {
	if err := checkWorkflowContext(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > workflowruntime.MaximumRunQueryLimit {
		return nil, workflowInvalid(fmt.Errorf("active recovery limit %d is out of range", limit))
	}
	result, err := s.queryActiveRunIDs(ctx, after, limit, false)
	if err != nil {
		return nil, err
	}
	if after == "" || len(result) == limit {
		return result, nil
	}
	wrapped, err := s.queryActiveRunIDs(ctx, after, limit-len(result), true)
	if err != nil {
		return nil, err
	}
	return append(result, wrapped...), nil
}

func (s *WorkflowStateStore) queryActiveRunIDs(ctx context.Context, boundary workflowruntime.RunID, limit int, wrap bool) ([]workflowruntime.RunID, error) {
	args := []any{
		workflowruntime.RunPending, workflowruntime.RunRunning, workflowruntime.RunWaiting,
		EngineKindGoWorkflow, EngineKindPilotHadron, boundary, limit,
	}
	var rows *sql.Rows
	var err error
	if wrap {
		rows, err = s.db.QueryContext(ctx, `
SELECT id FROM workflow_runs
WHERE runtime_status IN (?,?,?) AND engine_kind IN (?,?) AND id <= ? ORDER BY id LIMIT ?`, args...)
	} else {
		rows, err = s.db.QueryContext(ctx, `
SELECT id FROM workflow_runs
WHERE runtime_status IN (?,?,?) AND engine_kind IN (?,?) AND id > ? ORDER BY id LIMIT ?`, args...)
	}
	if err != nil {
		return nil, fmt.Errorf("query active workflow recovery batch: %w", err)
	}
	defer closeRows(rows)
	result := make([]workflowruntime.RunID, 0, limit)
	for rows.Next() {
		var id workflowruntime.RunID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan active workflow recovery run: %w", err)
		}
		result = append(result, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query active workflow recovery batch: %w", err)
	}
	return result, nil
}

func (s *WorkflowStateStore) recoverTerminalLoopIDs(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT json_extract(w.record_json,'$.correlation')
FROM workflow_waits w
JOIN workflow_runs r ON r.id=w.run_id
JOIN workflow_plan_node_projections p ON p.plan_digest=r.plan_digest AND p.node_id=w.node_id
JOIN loop_runs l ON l.id=json_extract(w.record_json,'$.correlation')
WHERE p.wait_class='loop' AND w.status='open' AND l.status IN ('completed','failed','canceled')
ORDER BY 1 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	var result []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}
