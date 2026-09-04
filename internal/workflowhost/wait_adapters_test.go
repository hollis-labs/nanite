package workflowhost

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gosched "github.com/hollis-labs/go-scheduler"
	"github.com/hollis-labs/go-workflow/graph"
	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
	"github.com/hollis-labs/go-workflow/stepkind"
	workflowwait "github.com/hollis-labs/go-workflow/wait"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	nanitescheduler "github.com/hollis-labs/nanite/internal/scheduler"
	nanitestore "github.com/hollis-labs/nanite/internal/store"
)

func TestApprovalWaitMaterializationAuthorizationAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approval-wait.db")
	productStore, err := nanitestore.New(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := NewWorkflowStateStore(productStore)
	if err != nil {
		t.Fatal(err)
	}
	projectionClock := time.Date(2026, time.September, 4, 17, 0, 0, 0, time.UTC)
	materializer := &WaitMaterializer{Store: productStore, Now: func() time.Time { return projectionClock }}
	waits := &workflowruntime.WaitCoordinator{Store: state, Materializer: materializer, Authorizer: NaniteResponderAuthorizer{}}
	engine, err := NewEngine(state)
	if err != nil {
		t.Fatal(err)
	}
	engine.WithWaitCoordinator(waits)
	executor := &recordingStepExecutor{}
	waiting, err := engine.Run(t.Context(), agentworkflow.WorkflowDefinition{
		Name: "authorized approval", Engine: agentworkflow.EngineHadron,
		Steps: []agentworkflow.StepDefinition{{ID: "approval", Kind: agentworkflow.StepKindGate, Config: map[string]any{
			"correlation": "release-approval", "authority_ref": "release-manager",
		}}},
	}, agentworkflow.WorkflowInput{}, executor)
	if err != nil || waiting.Status != agentworkflow.RunStatusWaiting {
		t.Fatalf("Run = %+v, %v", waiting, err)
	}
	openWaits, err := state.RecoverOpenWaits(t.Context(), workflowruntime.OpenWaitQuery{RunID: workflowruntime.RunID(waiting.RunID)})
	if err != nil || len(openWaits) != 1 {
		t.Fatalf("RecoverOpenWaits = %+v, %v", openWaits, err)
	}
	waitID := string(openWaits[0].Ref.ID)
	material, err := productStore.GetWorkflowWaitMaterialization(t.Context(), waitID)
	if err != nil || material.Status != "open" || material.Kind != string(workflowwait.KindGate) {
		t.Fatalf("materialized approval = %+v, %v", material, err)
	}

	const rawSecret = "raw-resume-token-must-not-persist"
	if materializeErr := materializer.Materialize(t.Context(), workflowwait.Materialization{
		WaitID: waitID, Kind: workflowwait.KindGate, Opaque: rawSecret,
	}); materializeErr != nil {
		t.Fatalf("idempotent materializer replay: %v", materializeErr)
	}
	material, err = productStore.GetWorkflowWaitMaterialization(t.Context(), waitID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(material.WaitID+material.Kind+material.ResumeURL+material.ExpiresAt+material.Status+material.CreatedAt+material.UpdatedAt, rawSecret) {
		t.Fatal("materialized wait persisted host-opaque raw secret")
	}
	columns, err := productStore.DB.QueryContext(t.Context(), `PRAGMA table_info(workflow_wait_materializations)`)
	if err != nil {
		t.Fatal(err)
	}
	defer columns.Close()
	for columns.Next() {
		var cid int
		var name, dataType string
		var notNull, primaryKey int
		var defaultValue any
		if scanErr := columns.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); scanErr != nil {
			t.Fatal(scanErr)
		}
		if strings.Contains(strings.ToLower(name), "token") || strings.Contains(strings.ToLower(name), "opaque") {
			t.Fatalf("materialization schema exposes sensitive column %q", name)
		}
	}

	if closeErr := productStore.Close(t.Context()); closeErr != nil {
		t.Fatal(closeErr)
	}
	reopened, err := nanitestore.New(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	restartedState, err := NewWorkflowStateStore(reopened)
	if err != nil {
		t.Fatal(err)
	}
	restartedWaits := &workflowruntime.WaitCoordinator{
		Store: restartedState, Materializer: &WaitMaterializer{Store: reopened, Now: func() time.Time { return projectionClock.Add(time.Minute) }},
		Authorizer: NaniteResponderAuthorizer{},
	}
	restarted, err := NewEngine(restartedState)
	if err != nil {
		t.Fatal(err)
	}
	restarted.WithWaitCoordinator(restartedWaits)

	request := ResumeWaitRequest{
		WaitID: waitID, Payload: "approved", ResponderKind: "agent",
		ResponderReference: "intruder", IdempotencyKey: "approval-one", ReceivedAt: time.Now().UTC(),
	}
	if _, resumeErr := restarted.ResumeWait(t.Context(), request, executor); !errors.Is(resumeErr, ErrWorkflowWaitUnauthorized) {
		t.Fatalf("unauthorized ResumeWait error = %v", resumeErr)
	}
	stillOpen, err := restartedState.LoadWait(t.Context(), workflowruntime.WaitID(waitID))
	if err != nil || stillOpen.Status != workflowruntime.WaitOpen {
		t.Fatalf("unauthorized responder changed wait = %+v, %v", stillOpen, err)
	}
	request.ResponderReference = "release-manager"
	completed, err := restarted.ResumeWait(t.Context(), request, executor)
	if err != nil || completed.Status != agentworkflow.RunStatusCompleted {
		t.Fatalf("authorized ResumeWait = %+v, %v", completed, err)
	}
	material, err = reopened.GetWorkflowWaitMaterialization(t.Context(), waitID)
	if err != nil || material.Status != "resolved" {
		t.Fatalf("resolved approval materialization = %+v, %v", material, err)
	}
}

type fixedActivationClock struct{ now time.Time }

func (c fixedActivationClock) Now() time.Time { return c.now }
func (fixedActivationClock) NewTicker(time.Duration) gosched.Ticker {
	return inertActivationTicker{channel: make(chan time.Time)}
}

type inertActivationTicker struct{ channel chan time.Time }

func (t inertActivationTicker) C() <-chan time.Time { return t.channel }
func (inertActivationTicker) Stop()                 {}

type recordingWorkflowActivationEngine struct {
	calls  int
	runIDs []string
}

func (e *recordingWorkflowActivationEngine) Resume(_ context.Context, runID string, _ agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	e.calls++
	e.runIDs = append(e.runIDs, runID)
	return agentworkflow.WorkflowResult{RunID: runID, Status: agentworkflow.RunStatusCompleted}, nil
}

func TestTimerActivationProductionRouteIsRestartDurableAndIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timer-activation.db")
	productStore, err := nanitestore.New(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := NewWorkflowStateStore(productStore)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, time.September, 4, 18, 0, 0, 0, time.UTC)
	wakeAt := base.Add(time.Minute)
	fixture := prepareWorkflowSQLiteWait(t, state, "timer-activation", base, 0)
	schema, err := workflowwait.NewSchemaRef(graph.Schema{})
	if err != nil {
		t.Fatal(err)
	}
	fixture.request.Wait.Record = workflowwait.Record{
		Kind: workflowwait.KindTimer, Correlation: fixture.correlation, WakeAt: wakeAt,
		ResumeSchema: schema, Visibility: workflowwait.VisibilityPrivate,
		Authority:  workflowwait.ResponderAuthority{Kind: "system_timer", Reference: "runtime"},
		WakeSource: workflowwait.WakeTimer, Status: workflowruntime.WaitOpen,
	}
	activationScheduler := &ActivationScheduler{Store: productStore, Now: func() time.Time { return base }}
	coordinator := &workflowruntime.WaitCoordinator{
		Store: state, Scheduler: activationScheduler,
		Materializer: &WaitMaterializer{Store: productStore, Now: func() time.Time { return base }},
		Authorizer:   NaniteResponderAuthorizer{},
	}
	suspended, err := coordinator.Suspend(t.Context(), workflowruntime.SuspendCommand{Request: fixture.request})
	if err != nil || suspended.Wait.Status != workflowruntime.WaitOpen {
		t.Fatalf("Suspend timer = %+v, %v", suspended, err)
	}
	activation := workflowruntime.TimerActivation(suspended.Wait)
	scheduleID := nanitestore.WorkflowActivationScheduleID(string(activation.ID))
	schedule, err := productStore.GetWorkflowActivationSchedule(t.Context(), scheduleID)
	if err != nil || schedule.ActivationID != string(activation.ID) || schedule.NextRun != wakeAt.Format(time.RFC3339Nano) {
		t.Fatalf("timer activation schedule = %+v, %v", schedule, err)
	}
	if scheduleErr := activationScheduler.Schedule(t.Context(), activation); scheduleErr != nil {
		t.Fatalf("exact activation schedule replay: %v", scheduleErr)
	}

	if closeErr := productStore.Close(t.Context()); closeErr != nil {
		t.Fatal(closeErr)
	}
	reopened, err := nanitestore.New(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	reopenedState, err := NewWorkflowStateStore(reopened)
	if err != nil {
		t.Fatal(err)
	}
	restartedScheduler := &ActivationScheduler{Store: reopened, Now: func() time.Time { return wakeAt }}
	restartedWaits := &workflowruntime.WaitCoordinator{
		Store: reopenedState, Scheduler: restartedScheduler,
		Materializer: &WaitMaterializer{Store: reopened, Now: func() time.Time { return wakeAt }},
		Authorizer:   NaniteResponderAuthorizer{},
	}
	resumeEngine := &recordingWorkflowActivationEngine{}
	dispatcher := &ActivationDispatcher{State: reopenedState, Waits: restartedWaits, Engine: resumeEngine, Executor: &recordingStepExecutor{}}
	storeAdapter := &nanitescheduler.StoreAdapter{Store: reopened, Logger: slog.Default()}
	runner := &nanitescheduler.RunnerAdapter{WorkflowActivations: dispatcher, Dispatches: reopened}
	clock := fixedActivationClock{now: wakeAt.Add(time.Second)}
	schedulerEngine := gosched.New(storeAdapter, runner, gosched.WithClock(clock), gosched.WithClaimLease(time.Minute))
	if tickErr := schedulerEngine.TickNow(t.Context()); tickErr != nil {
		t.Fatalf("TickNow after restart: %v", tickErr)
	}
	if resumeEngine.calls != 1 || len(resumeEngine.runIDs) != 1 || resumeEngine.runIDs[0] != string(suspended.Wait.Invocation.RunID) {
		t.Fatalf("workflow resume calls = %d, run IDs = %v", resumeEngine.calls, resumeEngine.runIDs)
	}
	resolved, err := reopenedState.LoadWait(t.Context(), suspended.Wait.Ref.ID)
	if err != nil || resolved.Status != workflowruntime.WaitResumed || !resolved.ResolvedAt.Equal(wakeAt) {
		t.Fatalf("resolved timer = %+v, %v", resolved, err)
	}
	material, err := reopened.GetWorkflowWaitMaterialization(t.Context(), string(suspended.Wait.Ref.ID))
	if err != nil || material.Status != "resolved" {
		t.Fatalf("resolved timer materialization = %+v, %v", material, err)
	}
	fireID := gosched.DeriveFireID(scheduleID, wakeAt)
	fire, err := reopened.GetWorkflowActivationFire(t.Context(), fireID)
	if err != nil || fire.Status != nanitestore.ScheduleFireStatusSucceeded || fire.AttemptCount != 1 {
		t.Fatalf("timer Fire = %+v, %v", fire, err)
	}
	if accepted, receiptErr := reopened.IsWorkflowActivationDispatchAccepted(t.Context(), fireID); receiptErr != nil || !accepted {
		t.Fatalf("timer dispatch receipt = (%v, %v)", accepted, receiptErr)
	}
	if tickErr := schedulerEngine.TickNow(t.Context()); tickErr != nil {
		t.Fatalf("idempotent TickNow: %v", tickErr)
	}
	if resumeEngine.calls != 1 {
		t.Fatalf("terminal Fire redelivered before restart: calls=%d", resumeEngine.calls)
	}
	if closeErr := reopened.Close(t.Context()); closeErr != nil {
		t.Fatal(closeErr)
	}

	secondRestart, err := nanitestore.New(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondRestart.Close(context.Background()) })
	secondState, err := NewWorkflowStateStore(secondRestart)
	if err != nil {
		t.Fatal(err)
	}
	secondWaits := &workflowruntime.WaitCoordinator{
		Store: secondState, Scheduler: &ActivationScheduler{Store: secondRestart},
		Materializer: &WaitMaterializer{Store: secondRestart}, Authorizer: NaniteResponderAuthorizer{},
	}
	secondDispatcher := &ActivationDispatcher{State: secondState, Waits: secondWaits, Engine: resumeEngine, Executor: &recordingStepExecutor{}}
	secondEngine := gosched.New(
		&nanitescheduler.StoreAdapter{Store: secondRestart, Logger: slog.Default()},
		&nanitescheduler.RunnerAdapter{WorkflowActivations: secondDispatcher, Dispatches: secondRestart},
		gosched.WithClock(clock),
	)
	if err := secondEngine.TickNow(t.Context()); err != nil {
		t.Fatalf("TickNow after second restart: %v", err)
	}
	if resumeEngine.calls != 1 {
		t.Fatalf("terminal Fire redelivered after restart: calls=%d", resumeEngine.calls)
	}
}

func TestNaniteResponderAuthorizerPolicy(t *testing.T) {
	timer := workflowwait.AuthorizationRequest{
		Record: workflowwait.Record{Kind: workflowwait.KindTimer, WakeSource: workflowwait.WakeTimer, Authority: workflowwait.ResponderAuthority{Kind: "system_timer", Reference: "runtime"}},
		Source: workflowwait.WakeTimer, Responder: workflowwait.Responder{Kind: "system", Reference: "wait-timer"},
	}
	approval := workflowwait.AuthorizationRequest{
		Record: workflowwait.Record{Kind: workflowwait.KindGate, WakeSource: workflowwait.WakeGate, Authority: workflowwait.ResponderAuthority{
			Kind: "nanite", Reference: "release-manager", Attributes: map[string]string{"project": "nanite", "responder_kind": "agent"},
		}},
		Source: workflowwait.WakeGate, Responder: workflowwait.Responder{Kind: "agent", Reference: "release-manager", Attributes: map[string]string{"project": "nanite"}},
	}
	for _, test := range []struct {
		name    string
		request workflowwait.AuthorizationRequest
		wantErr bool
	}{
		{name: "runtime timer", request: timer},
		{name: "nanite approval", request: approval},
		{name: "timer principal mismatch", request: func() workflowwait.AuthorizationRequest {
			value := timer
			value.Responder.Reference = "operator"
			return value
		}(), wantErr: true},
		{name: "timer authority mismatch", request: func() workflowwait.AuthorizationRequest {
			value := timer
			value.Record.Authority.Reference = "other-runtime"
			return value
		}(), wantErr: true},
		{name: "wake source mismatch", request: func() workflowwait.AuthorizationRequest {
			value := approval
			value.Source = workflowwait.WakeCallback
			return value
		}(), wantErr: true},
		{name: "authority mismatch", request: func() workflowwait.AuthorizationRequest {
			value := approval
			value.Responder.Reference = "other"
			return value
		}(), wantErr: true},
		{name: "attribute mismatch", request: func() workflowwait.AuthorizationRequest {
			value := approval
			value.Responder.Attributes = map[string]string{"project": "other"}
			return value
		}(), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := (NaniteResponderAuthorizer{}).AuthorizeResume(t.Context(), test.request)
			if test.wantErr && !errors.Is(err, ErrWorkflowWaitUnauthorized) {
				t.Fatalf("AuthorizeResume error = %v", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("AuthorizeResume: %v", err)
			}
		})
	}
}

func TestRetryCoordinatorProductionActivationRouteSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "retry-activation.db")
	productStore, err := nanitestore.New(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := NewWorkflowStateStore(productStore)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, time.September, 4, 21, 0, 0, 0, time.UTC)
	fixture := prepareWorkflowSQLiteWait(t, state, "retry-activation", base, time.Hour)
	node, err := state.LoadNodeInvocation(t.Context(), fixture.invocation)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := state.LoadAttempt(t.Context(), workflowruntime.AttemptID{Invocation: fixture.invocation, Number: node.LatestAttempt})
	if err != nil {
		t.Fatal(err)
	}
	activationScheduler := &ActivationScheduler{Store: productStore, Now: func() time.Time { return base }}
	retries := workflowruntime.RetryCoordinator{Store: state, Scheduler: activationScheduler}
	scheduled, decision, err := retries.Schedule(t.Context(), workflowruntime.ScheduleRetryCommand{
		Node: graph.Node{ID: fixture.invocation.NodeID, Retry: &graph.RetryPolicy{
			Attempts: 2, On: []string{"temporary"},
			Backoff: graph.BackoffPolicy{Strategy: graph.BackoffFixed, InitialDelay: "5s"},
		}},
		Spec: stepkind.StepKindSpec{
			Effects: graph.EffectSet{graph.EffectRead}, Idempotency: graph.IdempotencyIntrinsic,
			RetrySafety: stepkind.RetrySafe,
		},
		NodeSnapshot: node, Attempt: attempt, Claim: fixture.request.Claim,
		Failure:       workflowruntime.Failure{Code: "temporary", Message: "retry after restart", Retryable: true},
		AttemptStatus: workflowruntime.NodeFailed, IdempotencyKey: "step-effect-one", At: base.Add(3 * time.Second),
	})
	if err != nil || !decision.Retry || scheduled.Activation.Status != workflowruntime.RetryScheduled || scheduled.Node.Status != workflowruntime.NodeWaiting {
		t.Fatalf("RetryCoordinator.Schedule = %+v decision=%+v err=%v", scheduled, decision, err)
	}
	activation := workflowruntime.RetryActivation(scheduled.Activation)
	activationScheduleID := nanitestore.WorkflowActivationScheduleID(string(activation.ID))
	persistedSchedule, err := productStore.GetWorkflowActivationSchedule(t.Context(), activationScheduleID)
	if err != nil || persistedSchedule.ActivationID != scheduled.Activation.ID || persistedSchedule.NextRun != decision.FireAt.Format(time.RFC3339Nano) {
		t.Fatalf("persisted retry activation = %+v, %v", persistedSchedule, err)
	}
	if closeErr := productStore.Close(t.Context()); closeErr != nil {
		t.Fatal(closeErr)
	}

	reopened, err := nanitestore.New(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	reopenedState, err := NewWorkflowStateStore(reopened)
	if err != nil {
		t.Fatal(err)
	}
	waits := &workflowruntime.WaitCoordinator{Store: reopenedState, Scheduler: &ActivationScheduler{Store: reopened}}
	resumeEngine := &recordingWorkflowActivationEngine{}
	dispatcher := &ActivationDispatcher{State: reopenedState, Waits: waits, Engine: resumeEngine, Executor: &recordingStepExecutor{}}
	clock := fixedActivationClock{now: decision.FireAt.Add(time.Second)}
	schedulerEngine := gosched.New(
		&nanitescheduler.StoreAdapter{Store: reopened, Logger: slog.Default()},
		&nanitescheduler.RunnerAdapter{WorkflowActivations: dispatcher, Dispatches: reopened},
		gosched.WithClock(clock),
	)
	if tickErr := schedulerEngine.TickNow(t.Context()); tickErr != nil {
		t.Fatalf("retry TickNow after restart: %v", tickErr)
	}
	if resumeEngine.calls != 1 || len(resumeEngine.runIDs) != 1 || resumeEngine.runIDs[0] != string(fixture.invocation.RunID) {
		t.Fatalf("retry resume calls = %d, run IDs = %v", resumeEngine.calls, resumeEngine.runIDs)
	}
	retrySnapshot, err := reopenedState.LoadRetryActivation(t.Context(), scheduled.Activation.ID)
	if err != nil || retrySnapshot.Status != workflowruntime.RetryActivated {
		t.Fatalf("activated retry = %+v, %v", retrySnapshot, err)
	}
	readyNode, err := reopenedState.LoadNodeInvocation(t.Context(), fixture.invocation)
	if err != nil || readyNode.Status != workflowruntime.NodeReady || readyNode.Generation <= scheduled.Node.Generation {
		t.Fatalf("retry-ready node = %+v, %v", readyNode, err)
	}
	fireID := gosched.DeriveFireID(activationScheduleID, decision.FireAt)
	fire, err := reopened.GetWorkflowActivationFire(t.Context(), fireID)
	if err != nil || fire.Status != nanitestore.ScheduleFireStatusSucceeded || fire.AttemptCount != 1 {
		t.Fatalf("retry Fire = %+v, %v", fire, err)
	}
	if err := schedulerEngine.TickNow(t.Context()); err != nil {
		t.Fatal(err)
	}
	if resumeEngine.calls != 1 {
		t.Fatalf("retry Fire redelivered: calls=%d", resumeEngine.calls)
	}
}

type retryActivationStateFake struct {
	retry        workflowruntime.RetryActivationSnapshot
	node         workflowruntime.NodeInvocationSnapshot
	activate     workflowruntime.ActivateNodeRetryRequest
	activateCall int
}

func (*retryActivationStateFake) LoadWait(context.Context, workflowruntime.WaitID) (workflowruntime.WaitSnapshot, error) {
	return workflowruntime.WaitSnapshot{}, errors.New("unexpected wait load")
}
func (s *retryActivationStateFake) LoadRetryActivation(context.Context, string) (workflowruntime.RetryActivationSnapshot, error) {
	return s.retry, nil
}
func (s *retryActivationStateFake) LoadNodeInvocation(context.Context, workflowruntime.NodeInvocationID) (workflowruntime.NodeInvocationSnapshot, error) {
	return s.node, nil
}
func (s *retryActivationStateFake) ActivateNodeRetry(_ context.Context, request workflowruntime.ActivateNodeRetryRequest) (workflowruntime.ActivateNodeRetryResult, error) {
	s.activateCall++
	s.activate = request
	s.retry.Status = workflowruntime.RetryActivated
	return workflowruntime.ActivateNodeRetryResult{Activation: s.retry, Node: s.node}, nil
}

func TestActivationDispatcherRoutesRetryAndFencesCanceledReplay(t *testing.T) {
	fireAt := time.Date(2026, time.September, 4, 19, 0, 0, 0, time.UTC)
	invocation := workflowruntime.NodeInvocationID{RunID: "run-retry", NodeID: "node-retry"}
	state := &retryActivationStateFake{
		retry: workflowruntime.RetryActivationSnapshot{
			ID: "retry-activation", Attempt: workflowruntime.AttemptID{Invocation: invocation, Number: 1},
			Status: workflowruntime.RetryScheduled, Generation: 4,
		},
		node: workflowruntime.NodeInvocationSnapshot{ID: invocation, Generation: 7},
	}
	engine := &recordingWorkflowActivationEngine{}
	dispatcher := &ActivationDispatcher{
		State: state, Waits: &workflowruntime.WaitCoordinator{}, Engine: engine, Executor: &recordingStepExecutor{},
	}
	activation := workflowwait.Activation{
		ID: "retry-activation", Kind: "node_retry", RunID: "run-retry", NodeID: "node-retry",
		FireAt: fireAt, DedupKey: "retry-dedup",
	}
	applied, err := dispatcher.DispatchWorkflowActivation(t.Context(), activation, fireAt)
	if err != nil || !applied || state.activateCall != 1 || engine.calls != 1 {
		t.Fatalf("retry dispatch = (%v, %v), activates=%d resumes=%d", applied, err, state.activateCall, engine.calls)
	}
	if state.activate.ActivationID != string(activation.ID) || state.activate.ExpectedActivationGeneration != 4 || state.activate.ExpectedNodeGeneration != 7 || state.activate.IdempotencyKey != "nanite:workflow-activation:retry-dedup" || !state.activate.Now.Equal(fireAt) {
		t.Fatalf("retry activation CAS = %+v", state.activate)
	}
	applied, err = dispatcher.DispatchWorkflowActivation(t.Context(), activation, fireAt.Add(time.Second))
	if err != nil || !applied || state.activateCall != 1 || engine.calls != 2 {
		t.Fatalf("activated retry recovery = (%v, %v), activates=%d resumes=%d", applied, err, state.activateCall, engine.calls)
	}
	state.retry.Status = workflowruntime.RetryCanceled
	applied, err = dispatcher.DispatchWorkflowActivation(t.Context(), activation, fireAt.Add(2*time.Second))
	if err != nil || applied || engine.calls != 2 {
		t.Fatalf("canceled retry replay = (%v, %v), resumes=%d", applied, err, engine.calls)
	}
}
