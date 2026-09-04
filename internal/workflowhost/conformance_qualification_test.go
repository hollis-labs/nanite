package workflowhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	workflowcompile "github.com/hollis-labs/go-workflow/compile"
	"github.com/hollis-labs/go-workflow/conformance"
	"github.com/hollis-labs/go-workflow/diagnostic"
	"github.com/hollis-labs/go-workflow/graph"
	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
	"github.com/hollis-labs/go-workflow/stepkind"
	"github.com/hollis-labs/go-workflow/stepkind/stepkindtest"
	"github.com/hollis-labs/go-workflow/values"
	"github.com/hollis-labs/go-workflow/verification"
	workflowwait "github.com/hollis-labs/go-workflow/wait"

	nanitestore "github.com/hollis-labs/nanite/internal/store"
)

// naniteConformanceHost deliberately opens the same migrated SQLite store used
// by the product for every stateful fixture. This is an adoption qualification,
// not a substitute implementation of Hadron semantics.
type naniteConformanceHost struct{ t *testing.T }

type naniteSQLiteRunner struct {
	store *nanitestore.Store
	state *WorkflowStateStore
	run   func(context.Context, conformance.Fixture, *WorkflowStateStore) error
}

func (r *naniteSQLiteRunner) Run(ctx context.Context, fixture conformance.Fixture) error {
	defer func() { _ = r.store.Close(context.Background()) }()
	return r.run(ctx, fixture, r.state)
}

func (h naniteConformanceHost) sqliteFactory(run func(context.Context, conformance.Fixture, *WorkflowStateStore) error) conformance.Factory {
	return func() (conformance.Runner, error) {
		store, err := nanitestore.New(context.Background(), filepath.Join(h.t.TempDir(), "conformance.db"))
		if err != nil {
			return nil, err
		}
		state, err := NewWorkflowStateStore(store)
		if err != nil {
			_ = store.Close(context.Background())
			return nil, err
		}
		return &naniteSQLiteRunner{store: store, state: state, run: run}, nil
	}
}

func (h naniteConformanceHost) CompilerFactory() conformance.Factory {
	return func() (conformance.Runner, error) { return conformance.RunnerFunc(runNaniteCompilerFixture), nil }
}
func (h naniteConformanceHost) StateStoreFactory() conformance.Factory {
	return h.sqliteFactory(runNaniteValueFixture)
}
func (h naniteConformanceHost) SchedulerFactory() conformance.Factory {
	return h.sqliteFactory(runNaniteSchedulerFixture)
}
func (h naniteConformanceHost) WaitFactory() conformance.Factory {
	return h.sqliteFactory(runNaniteWaitFixture)
}
func (h naniteConformanceHost) StepKindRegistryFactory() conformance.Factory {
	return func() (conformance.Runner, error) { return conformance.RunnerFunc(runNaniteStepKindFixture), nil }
}
func (h naniteConformanceHost) VerificationFactory() conformance.Factory {
	return h.sqliteFactory(runNaniteVerificationFixture)
}
func (h naniteConformanceHost) MemoizationFactory() conformance.Factory {
	return h.sqliteFactory(runNaniteMemoFixture)
}
func (h naniteConformanceHost) CompensationFactory() conformance.Factory {
	return h.sqliteFactory(runNaniteCompensationFixture)
}

func TestNaniteHadronRunComplete(t *testing.T) {
	conformance.RunComplete(t, conformance.EmbeddedFixtures(), naniteConformanceHost{t: t})
}

func TestNaniteHadronRunExhaustive(t *testing.T) {
	conformance.RunExhaustive(t, conformance.EmbeddedFixtures(), naniteConformanceHost{t: t})
}

type naniteValidationFixture struct {
	Graph               graph.Graph `json:"graph"`
	AnalyzeDependencies bool        `json:"analyze_dependencies"`
	RegisteredKinds     []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"registered_kinds"`
	ExpectedDataEdges  []string                                   `json:"expected_data_edges"`
	ExpectedDeferred   []workflowcompile.DeferredDependencyReason `json:"expected_deferred"`
	ExpectedVisibility map[string][]string                        `json:"expected_visibility"`
}

func runNaniteCompilerFixture(ctx context.Context, fixture conformance.Fixture) error {
	if fixture.Set == conformance.SourceMapFixtures {
		var input struct {
			Accepted bool `json:"accepted"`
		}
		if err := json.Unmarshal(fixture.Input, &input); err != nil {
			return err
		}
		source := []byte("workflow: {id: qualified-source, version: v1}\nsteps:\n  - {id: work, kind: noop, kind_version: v1, config: {}}\n")
		if !input.Accepted {
			source = []byte("workflow: [not-a-workflow\n")
		}
		loaded := workflowcompile.LoadBytes("nanite-qualification.workflow.yaml", source)
		if loaded.Source == nil {
			return errors.New("source loader rejected fixture with source diagnostics")
		}
		compiled := workflowcompile.Compile(loaded.Source)
		if compiled.Plan == nil || len(compiled.Diagnostics) != 0 || compiled.Plan.SourceMap.Graph == nil {
			return errors.New("compiler did not retain a source map")
		}
		return nil
	}

	var input naniteValidationFixture
	if err := json.Unmarshal(fixture.Input, &input); err != nil {
		return fmt.Errorf("decode graph-validation fixture: %w", err)
	}
	registry := stepkind.NewRegistry()
	for _, registered := range input.RegisteredKinds {
		if err := registry.Register(stepkindtest.NewNoopKind(registered.Name, registered.Version)); err != nil {
			return err
		}
	}
	value := input.Graph
	if input.AnalyzeDependencies {
		inferred := workflowcompile.InferValueDependencies(&workflowcompile.ExecutionPlan{
			SchemaVersion: workflowcompile.ExecutionPlanSchemaVersion,
			ID:            value.ID, Definition: graph.DefinitionRef{Kind: "workflow", ID: value.ID, Version: value.Version},
			Graph: value, SourceMap: value.SourceMap,
		}, workflowcompile.DependencyOptions{})
		if inferred.Plan == nil || len(inferred.Diagnostics) != 0 {
			return errors.New("dependency inference failed")
		}
		value = inferred.Plan.Graph
		var gotEdges []string
		for _, edge := range value.Edges {
			if edge.Kind == graph.EdgeData {
				gotEdges = append(gotEdges, workflowcompile.EdgeSourceKey(edge.From, edge.To, edge.Kind))
			}
		}
		sort.Strings(gotEdges)
		wantEdges := append([]string(nil), input.ExpectedDataEdges...)
		sort.Strings(wantEdges)
		if !slices.Equal(gotEdges, wantEdges) {
			return fmt.Errorf("data edges = %v, want %v", gotEdges, wantEdges)
		}
		deferred := make([]workflowcompile.DeferredDependencyReason, len(inferred.Deferred))
		for i := range inferred.Deferred {
			deferred[i] = inferred.Deferred[i].Reason
		}
		sort.Slice(deferred, func(i, j int) bool { return deferred[i] < deferred[j] })
		wantDeferred := append([]workflowcompile.DeferredDependencyReason(nil), input.ExpectedDeferred...)
		sort.Slice(wantDeferred, func(i, j int) bool { return wantDeferred[i] < wantDeferred[j] })
		if !slices.Equal(deferred, wantDeferred) {
			return fmt.Errorf("deferred reasons = %v, want %v", deferred, wantDeferred)
		}
		for nodeID, want := range input.ExpectedVisibility {
			if got := inferred.Visibility.Nodes[nodeID].Producers; !reflect.DeepEqual(got, want) {
				return fmt.Errorf("visibility for %s = %v, want %v", nodeID, got, want)
			}
		}
	}
	if findings := workflowcompile.ValidateGraph(ctx, value, workflowcompile.ValidationOptions{StepKinds: registry}); len(findings) != 0 {
		return errors.New("graph validation produced diagnostics")
	}
	return nil
}

func runNaniteValueFixture(ctx context.Context, fixture conformance.Fixture, state *WorkflowStateStore) error {
	var input struct {
		Accepted bool `json:"accepted"`
	}
	if err := json.Unmarshal(fixture.Input, &input); err != nil {
		return err
	}
	run, _, err := state.CreateRun(ctx, workflowruntime.CreateRunRequest{
		ID: "value-fixture", Plan: qualificationPlan("values"), Status: workflowruntime.RunPending,
		StartIdempotencyKey: "value-fixture", CreatedAt: qualificationTime(),
	})
	if err != nil {
		return err
	}
	value, err := values.NewInline("durable", qualificationMetadata("value-fixture"))
	if err != nil {
		return err
	}
	owner := workflowruntime.ValueOwner{Kind: "run-inputs", RunID: run.ID}
	if !input.Accepted {
		owner.Kind = ""
	}
	ref, err := state.SaveValues(ctx, workflowruntime.SaveValuesRequest{Owner: owner, Values: values.ValueSet{"value": value}})
	if err != nil {
		return err
	}
	loaded, err := state.LoadValues(ctx, ref)
	if err != nil || loaded["value"].Inline != "durable" {
		return fmt.Errorf("durable value round trip = %#v: %w", loaded, err)
	}
	return nil
}

type naniteSchedulerFixture struct {
	Scenario     string                       `json:"scenario"`
	Rule         graph.ReadyRule              `json:"rule"`
	Dependencies []workflowruntime.NodeStatus `json:"dependencies"`
	Completion   graph.RunCompletionMode      `json:"completion"`
	Want         string                       `json:"want"`
}

func runNaniteSchedulerFixture(ctx context.Context, fixture conformance.Fixture, state *WorkflowStateStore) error {
	if fixture.Set == conformance.ControlFlowFixtures {
		return runNaniteControlFlowFixture(ctx, fixture, state)
	}
	var input naniteSchedulerFixture
	if err := json.Unmarshal(fixture.Input, &input); err != nil {
		return err
	}
	switch input.Scenario {
	case "resource":
		return runNaniteResourceFixture(ctx, state, input)
	case "run_policy":
		return runNaniteRunPolicyFixture(ctx, state, input)
	case "":
		dependencies := make([]workflowruntime.DependencyState, len(input.Dependencies))
		for i, status := range input.Dependencies {
			dependencies[i] = workflowruntime.DependencyState{InvocationID: workflowruntime.NodeInvocationID{RunID: "readiness", NodeID: fmt.Sprintf("dep-%d", i)}, Status: status}
		}
		result, err := workflowruntime.EvaluateReadiness(input.Rule, dependencies)
		if err != nil {
			return err
		}
		if string(result.Disposition) != input.Want {
			return fmt.Errorf("readiness = %q, want %q", result.Disposition, input.Want)
		}
		return nil
	default:
		return fmt.Errorf("unknown scheduler scenario %q", input.Scenario)
	}
}

func runNaniteResourceFixture(ctx context.Context, state *WorkflowStateStore, input naniteSchedulerFixture) error {
	base := qualificationTime()
	ids := []workflowruntime.NodeInvocationID{{RunID: "resource-a", NodeID: "work"}, {RunID: "resource-b", NodeID: "work"}}
	for _, id := range ids {
		if _, err := createQualificationRun(ctx, state, id.RunID, base); err != nil {
			return err
		}
		if _, err := createQualificationNode(ctx, state, id, base, true); err != nil {
			return err
		}
	}
	limits := workflowruntime.SchedulerLimits{Workers: 4, Named: map[string]int{"fixture-key": 1}}
	results := make([]workflowruntime.AdmitNodeResult, len(ids))
	for i, id := range ids {
		requirements, err := workflowruntime.BuildSchedulerRequirements(id.RunID, limits, workflowruntime.SchedulerDemand{Concurrency: []graph.ConcurrencyClaim{{Resource: "fixture-key"}}})
		if err != nil {
			return err
		}
		results[i], err = state.AdmitNode(ctx, workflowruntime.AdmitNodeRequest{Claim: workflowruntime.ClaimNodeRequest{
			InvocationID: id, Owner: fmt.Sprintf("worker-%d", i), Token: fmt.Sprintf("token-%d", i),
			IdempotencyKey: fmt.Sprintf("admit-%d", i), Now: base.Add(time.Second), LeaseUntil: base.Add(time.Minute),
		}, Requirements: requirements, EnqueuedAt: base})
		if err != nil {
			return err
		}
	}
	if input.Want != "blocked" || !results[0].Claim.Acquired || results[1].Claim.Acquired || len(results[1].Blocked) != 1 {
		return fmt.Errorf("resource admission = %#v", results)
	}
	diagnostics, err := state.InspectSchedulerResources(ctx, workflowruntime.SchedulerResourceQuery{Now: base.Add(time.Second)})
	if err != nil || len(diagnostics.Waiters) != 1 {
		return fmt.Errorf("resource diagnostics = %#v: %w", diagnostics, err)
	}
	return nil
}

func runNaniteRunPolicyFixture(ctx context.Context, state *WorkflowStateStore, input naniteSchedulerFixture) error {
	base := qualificationTime()
	runID := workflowruntime.RunID("policy-" + string(input.Completion))
	run, err := createQualificationRun(ctx, state, runID, base)
	if err != nil {
		return err
	}
	source := workflowruntime.NodeInvocationID{RunID: runID, NodeID: "source"}
	independent := workflowruntime.NodeInvocationID{RunID: runID, NodeID: "independent"}
	for _, id := range []workflowruntime.NodeInvocationID{source, independent} {
		if _, nodeErr := createQualificationNode(ctx, state, id, base, true); nodeErr != nil {
			return nodeErr
		}
	}
	if finishErr := finishQualificationNode(ctx, state, source, workflowruntime.NodeFailed, "fixture_failed", base.Add(time.Second)); finishErr != nil {
		return finishErr
	}
	workflow := graph.Graph{ID: run.Plan.ID, Version: run.Plan.Version, Completion: &graph.RunCompletionPolicy{Mode: input.Completion}, Nodes: []graph.Node{{ID: "source"}, {ID: "independent"}}}
	result, err := workflowruntime.NewRunPolicyCoordinator(state, state, state).HandleFailure(ctx, workflow, source, "policy-fixture", base.Add(3*time.Second))
	if err != nil {
		return err
	}
	if string(result.Disposition) != input.Want {
		return fmt.Errorf("run policy = %q, want %q", result.Disposition, input.Want)
	}
	stored, err := state.LoadNodeInvocation(ctx, independent)
	if err != nil {
		return err
	}
	if input.Completion == graph.CompletionFailFast && stored.Status != workflowruntime.NodeCanceled {
		return fmt.Errorf("fail-fast left independent node %q", stored.Status)
	}
	if input.Completion == graph.CompletionRunToCompletion && stored.Status != workflowruntime.NodeReady {
		return fmt.Errorf("run-to-completion left independent node %q", stored.Status)
	}
	return nil
}

func runNaniteWaitFixture(ctx context.Context, fixture conformance.Fixture, state *WorkflowStateStore) error {
	var input struct {
		Kind        workflowwait.Kind       `json:"kind"`
		WakeSource  workflowwait.WakeSource `json:"wake_source"`
		Correlation string                  `json:"correlation"`
	}
	if err := json.Unmarshal(fixture.Input, &input); err != nil {
		return err
	}
	base := qualificationTime()
	run, err := createQualificationRun(ctx, state, "wait-run", base)
	if err != nil {
		return err
	}
	id := workflowruntime.NodeInvocationID{RunID: run.ID, NodeID: "wait"}
	if _, nodeErr := createQualificationNode(ctx, state, id, base, true); nodeErr != nil {
		return nodeErr
	}
	node, _ := state.LoadNodeInvocation(ctx, id)
	claim, err := state.ClaimNode(ctx, workflowruntime.ClaimNodeRequest{InvocationID: id, ExpectedClaimGeneration: node.ClaimGeneration, Owner: "wait-worker", Token: "claim-token", IdempotencyKey: "wait-claim", Now: base.Add(time.Second), LeaseUntil: base.Add(time.Hour)})
	if err != nil || claim.Lease == nil {
		return fmt.Errorf("claim wait node: %w", err)
	}
	proof := workflowruntime.ClaimProof{Owner: claim.Lease.Owner, Token: claim.Lease.Token, Generation: claim.Lease.Generation}
	claimed, _ := state.LoadNodeInvocation(ctx, id)
	started, err := state.StartNodeAttempt(ctx, workflowruntime.StartNodeAttemptRequest{InvocationID: id, ExpectedNodeGeneration: claimed.Generation, Claim: proof, Executor: workflowruntime.ExecutorMetadata{Kind: "wait", Version: "v1"}, At: base.Add(2 * time.Second)})
	if err != nil {
		return err
	}
	resumeSchema := graph.Schema{"type": "string"}
	if input.Kind == workflowwait.KindTimer {
		resumeSchema = graph.Schema{"type": "object"}
	}
	schema, err := workflowwait.NewSchemaRef(resumeSchema)
	if err != nil {
		return err
	}
	token := "resume-token"
	tokenDigest := ""
	if input.Kind != workflowwait.KindTimer {
		tokenDigest, err = workflowwait.DigestToken(token)
		if err != nil {
			return err
		}
	}
	record := workflowwait.Record{Kind: input.Kind, Correlation: input.Correlation, Deadline: base.Add(time.Hour), ResumeSchema: schema, ResumeTokenDigest: tokenDigest, Visibility: workflowwait.VisibilityPrivate, Authority: workflowwait.ResponderAuthority{Kind: "fixture", Reference: "nanite"}, WakeSource: input.WakeSource, Status: workflowruntime.WaitOpen}
	if input.Kind == workflowwait.KindTimer {
		record.WakeAt = base.Add(10 * time.Second)
	}
	waitID := workflowruntime.WaitID("qualified-wait")
	coordinator := workflowruntime.WaitCoordinator{Store: state}
	resumeToken := token
	if input.Kind == workflowwait.KindTimer {
		resumeToken = ""
	}
	suspended, err := coordinator.Suspend(ctx, workflowruntime.SuspendCommand{Request: workflowruntime.SuspendNodeWaitRequest{
		Wait:                   workflowruntime.WaitSnapshot{Ref: workflowruntime.WaitRef{ID: waitID}, Invocation: id, Record: record},
		ExpectedNodeGeneration: started.Node.Generation, ExpectedAttemptGeneration: started.Attempt.Generation, Claim: proof, At: base.Add(3 * time.Second),
	}, ResumeToken: resumeToken})
	if err != nil {
		return err
	}
	loaded, err := state.LoadWait(ctx, waitID)
	if err != nil || loaded.Status != workflowruntime.WaitOpen || suspended.Node.Status != workflowruntime.NodeWaiting {
		return fmt.Errorf("durable wait = %#v: %w", loaded, err)
	}
	if input.Kind == workflowwait.KindTimer {
		woken, wakeErr := coordinator.WakeTimer(ctx, workflowruntime.TimerWakeCommand{WaitID: waitID, FiredAt: record.WakeAt})
		if wakeErr != nil || woken.Wait.Status != workflowruntime.WaitResumed {
			return fmt.Errorf("timer wake = %#v: %w", woken, wakeErr)
		}
		return nil
	}
	payload, err := values.NewInline("accepted", qualificationMetadata("wait-response"))
	if err != nil {
		return err
	}
	resumed, err := coordinator.Resume(ctx, workflowruntime.ResumeCommand{WaitID: waitID, Correlation: input.Correlation, Token: token, WakeSource: input.WakeSource, Responder: workflowwait.Responder{Kind: "fixture", Reference: "nanite"}, Payload: payload, IdempotencyKey: "resume", ReceivedAt: base.Add(4 * time.Second)})
	if err != nil || resumed.Wait.Status != workflowruntime.WaitResumed || resumed.Node.Status != workflowruntime.NodeReady {
		return fmt.Errorf("resume = %#v: %w", resumed, err)
	}
	return nil
}

func runNaniteStepKindFixture(ctx context.Context, fixture conformance.Fixture) error {
	var input struct {
		Operation string                `json:"operation"`
		Name      string                `json:"name"`
		Version   string                `json:"version"`
		Config    graph.Config          `json:"config"`
		Spec      stepkind.StepKindSpec `json:"spec"`
	}
	if err := json.Unmarshal(fixture.Input, &input); err != nil {
		return err
	}
	switch input.Operation {
	case "":
		return stepkind.ValidateSpec(input.Spec)
	case "duplicate_registration":
		registry := stepkind.NewRegistry()
		if err := registry.Register(stepkindtest.NewNoopKind(input.Name, input.Version)); err != nil {
			return err
		}
		return registry.Register(stepkindtest.NewNoopKind(input.Name, input.Version))
	case "resolve":
		_, _, err := stepkind.Resolve(stepkind.NewRegistry(), input.Name, input.Version)
		return err
	case "validate_config":
		kind := stepkindtest.NewNoopKind(input.Name, input.Version)
		kind.ValidateConfigFunc = func(_ context.Context, config graph.Config) []diagnostic.Diagnostic {
			if accepted, _ := config["accepted"].(bool); accepted {
				return nil
			}
			return []diagnostic.Diagnostic{{Severity: diagnostic.SeverityError, Code: stepkind.CodeInvalidConfig, Message: "config rejected"}}
		}
		if findings := kind.ValidateConfig(ctx, input.Config); len(findings) != 0 {
			return errors.New(findings[0].Message)
		}
		return nil
	case "optional_lifecycle":
		kind := stepkindtest.NewLifecycleKind(input.Name, input.Version)
		if err := stepkind.NewRegistry().Register(kind); err != nil {
			return err
		}
		_, prepares := any(kind).(stepkind.Preparer)
		_, observes := any(kind).(stepkind.Observer)
		_, heartbeats := any(kind).(stepkind.Heartbeater)
		_, cancels := any(kind).(stepkind.Canceler)
		_, finalizes := any(kind).(stepkind.Finalizer)
		if !prepares || !observes || !heartbeats || !cancels || !finalizes {
			return errors.New("optional lifecycle incomplete")
		}
		return nil
	case "immutable_snapshot":
		registry := stepkind.NewRegistry()
		kind := stepkindtest.NewNoopKind(input.Name, input.Version)
		if err := registry.Register(kind); err != nil {
			return err
		}
		kind.SpecValue.Name = "mutated"
		_, spec, err := stepkind.Resolve(registry, input.Name, input.Version)
		if err != nil || spec.Name != input.Name {
			return errors.New("registered snapshot mutated")
		}
		return nil
	default:
		return fmt.Errorf("unknown StepKind operation %q", input.Operation)
	}
}

func runNaniteVerificationFixture(ctx context.Context, fixture conformance.Fixture, state *WorkflowStateStore) error {
	var input struct {
		Scenario string `json:"scenario"`
	}
	if err := json.Unmarshal(fixture.Input, &input); err != nil {
		return err
	}
	switch input.Scenario {
	case "deterministic_pass", "deterministic_fail":
		value, err := values.NewInline(input.Scenario == "deterministic_pass", qualificationMetadata("verification"))
		if err != nil {
			return err
		}
		verifier, _, err := verification.Resolve(verification.NewDefaultRegistry(), verification.CheckPredicate)
		if err != nil {
			return err
		}
		result, err := verifier.Verify(ctx, verification.Request{Check: graph.VerificationCheck{Kind: verification.CheckPredicate, Config: graph.Config{"expression": "inputs.ok"}}, Outputs: values.ValueSet{"ok": value}})
		if err != nil {
			return err
		}
		if result.Outcome != verification.CheckPassed {
			return errors.New(result.Code)
		}
		return nil
	case "missing_evidence":
		verifier, _, err := verification.Resolve(verification.NewDefaultRegistry(), verification.CheckExpectedToolCall)
		if err != nil {
			return err
		}
		result, err := verifier.Verify(ctx, verification.Request{Check: graph.VerificationCheck{Kind: verification.CheckExpectedToolCall, Config: graph.Config{"tool": "write"}}})
		if err != nil {
			return err
		}
		if result.Outcome != verification.CheckPassed {
			return errors.New(result.Code)
		}
		return nil
	case "retry_safety":
		decision, err := (workflowruntime.RetryEvaluator{}).Evaluate(ctx, workflowruntime.RetryEvaluationRequest{
			Node:          graph.Node{ID: "unsafe", Retry: &graph.RetryPolicy{Attempts: 2, On: []string{"verification_failed"}}},
			Spec:          stepkind.StepKindSpec{Effects: graph.EffectSet{graph.EffectDestructive}, Idempotency: graph.IdempotencyKeyed, RetrySafety: stepkind.RetryRequiresIdempotency},
			AttemptNumber: 1, Failure: workflowruntime.Failure{Code: "verification_failed", Message: "verification failed", Retryable: true}, AttemptStatus: workflowruntime.NodeFailed, FailedAt: qualificationTime(),
		})
		if err != nil {
			return err
		}
		if decision.Retry || decision.Reason != workflowruntime.RetryReasonIdempotencyMissing {
			return fmt.Errorf("retry decision = %#v", decision)
		}
		return nil
	case "catch_route":
		return runNaniteVerificationCatch(ctx, state)
	case "reviewer_malformed":
		_, err := verification.ParseReviewerDecision([]byte(`{"passed":true,"passed":false,"code":"ambiguous","message":"duplicate"}`))
		return err
	default:
		return fmt.Errorf("unknown verification scenario %q", input.Scenario)
	}
}

func runNaniteVerificationCatch(ctx context.Context, state *WorkflowStateStore) error {
	base := qualificationTime()
	run, err := createQualificationRun(ctx, state, "verification-catch", base)
	if err != nil {
		return err
	}
	for _, nodeID := range []string{"source", "handler"} {
		if _, nodeErr := createQualificationNode(ctx, state, workflowruntime.NodeInvocationID{RunID: run.ID, NodeID: nodeID}, base, false); nodeErr != nil {
			return nodeErr
		}
	}
	source := workflowruntime.NodeInvocationID{RunID: run.ID, NodeID: "source"}
	if finishErr := finishQualificationNode(ctx, state, source, workflowruntime.NodeFailed, "verification_failed", base.Add(time.Second)); finishErr != nil {
		return finishErr
	}
	decision, err := workflowruntime.NewControlFlowCoordinator(state, state, nil).DecideCatch(ctx, workflowruntime.DecideCatchRequest{Source: source, Node: graph.Node{ID: "source", Catch: []graph.CatchRule{{Errors: []string{"verification_failed"}, Targets: []string{"handler"}}}}, At: base.Add(2 * time.Second)})
	if err != nil || decision.Decision.Outcome != workflowruntime.ControlSelected || len(decision.Decision.Targets) != 1 {
		return fmt.Errorf("verification catch = %#v: %w", decision, err)
	}
	return nil
}

func runNaniteMemoFixture(ctx context.Context, fixture conformance.Fixture, state *WorkflowStateStore) error {
	var input struct {
		Scenario string `json:"scenario"`
	}
	if err := json.Unmarshal(fixture.Input, &input); err != nil {
		return err
	}
	base := qualificationTime()
	switch input.Scenario {
	case "safe_entry":
		entry, err := seedNaniteMemoSource(ctx, state, base)
		if err != nil {
			return err
		}
		recorded, outcome, err := state.RecordMemoEntry(ctx, entry)
		if err != nil || outcome != workflowruntime.IdempotencyApplied {
			return fmt.Errorf("record memo = %#v/%q: %w", recorded, outcome, err)
		}
		loaded, err := state.LoadMemoEntry(ctx, entry.Key)
		if err != nil || loaded.Outputs != entry.Outputs {
			return fmt.Errorf("load memo = %#v: %w", loaded, err)
		}
		return nil
	case "expired":
		entry := qualificationMemoEntry(base)
		return entry.FreshAt(base.Add(2*time.Hour), time.Hour)
	case "pin_binding":
		entry, err := seedNaniteMemoSource(ctx, state, base)
		if err != nil {
			return err
		}
		targetRun, err := state.LoadRun(ctx, entry.Source.RunID)
		if err != nil {
			return err
		}
		targetID := workflowruntime.NodeInvocationID{RunID: targetRun.ID, NodeID: "pin-target"}
		target, err := createQualificationNode(ctx, state, targetID, base, false)
		if err != nil {
			return err
		}
		binding := workflowruntime.PinBinding{Target: targetID, PlanDigest: targetRun.Plan.Digest, Outputs: entry.Outputs, OutputSchemaDigest: entry.OutputSchemaDigest, Source: entry.Source, SourcePlanDigest: entry.PlanDigest, SourceOrigin: entry.SourceOrigin, Authority: workflowruntime.ReuseAuthority{Principal: "developer"}, Policy: workflowruntime.ReusePolicyDecision{Allow: true, Code: "pin", Reason: "qualified"}, BoundAt: base.Add(5 * time.Second)}
		bound, err := state.BindPin(ctx, workflowruntime.BindPinRequest{Binding: binding, ExpectedGeneration: target.Generation, IdempotencyKey: "pin-binding"})
		if err != nil || bound.Outcome != workflowruntime.IdempotencyApplied {
			return fmt.Errorf("bind pin = %#v: %w", bound, err)
		}
		loaded, err := state.LoadPin(ctx, targetID)
		if err != nil || loaded.Outputs != entry.Outputs {
			return fmt.Errorf("load pin = %#v: %w", loaded, err)
		}
		return nil
	case "unsafe_effect":
		kind := stepkindtest.NewNoopKind("writer", "v1")
		kind.SpecValue.Effects = graph.EffectSet{graph.EffectMutate}
		registry := stepkind.NewRegistry()
		if err := registry.Register(kind); err != nil {
			return err
		}
		workflow := graph.Graph{ID: "unsafe", Version: "v1", Nodes: []graph.Node{{ID: "write", Kind: "writer", KindVersion: "v1", Memoization: &graph.MemoizationSpec{Key: graph.Expression{Text: "inputs.key"}, MaxAge: "1h"}}}}
		findings := workflowcompile.ValidateGraph(ctx, workflow, workflowcompile.ValidationOptions{StepKinds: registry, Verifiers: verification.NewDefaultRegistry()})
		if len(findings) == 0 {
			return nil
		}
		return errors.New(string(findings[0].Code))
	default:
		return fmt.Errorf("unknown memo scenario %q", input.Scenario)
	}
}

func seedNaniteMemoSource(ctx context.Context, state *WorkflowStateStore, base time.Time) (workflowruntime.MemoEntry, error) {
	run, err := createQualificationRun(ctx, state, "memo-source", base)
	if err != nil {
		return workflowruntime.MemoEntry{}, err
	}
	id := workflowruntime.NodeInvocationID{RunID: run.ID, NodeID: "work"}
	node, err := createQualificationNode(ctx, state, id, base, false)
	if err != nil {
		return workflowruntime.MemoEntry{}, err
	}
	memoDigest := values.SHA256Digest([]byte("memo-expression"))
	inputValue, _ := values.NewInline("input", qualificationMetadata("memo-input"))
	bound, err := state.BindNodeInputs(ctx, workflowruntime.BindNodeInputsRequest{InvocationID: id, ExpectedGeneration: node.Generation, IdempotencyKey: "memo-inputs", Values: values.ValueSet{"input": inputValue}, MemoKeyDigest: memoDigest, At: base})
	if err != nil {
		return workflowruntime.MemoEntry{}, err
	}
	ready, err := state.TransitionNode(ctx, workflowruntime.NodeTransitionRequest{InvocationID: id, ExpectedGeneration: bound.Node.Generation, To: workflowruntime.NodeReady, At: base.Add(time.Second)})
	if err != nil {
		return workflowruntime.MemoEntry{}, err
	}
	claim, err := state.ClaimNode(ctx, workflowruntime.ClaimNodeRequest{InvocationID: id, ExpectedClaimGeneration: ready.Snapshot.ClaimGeneration, Owner: "memo-worker", Token: "memo-token", IdempotencyKey: "memo-claim", Now: base.Add(2 * time.Second), LeaseUntil: base.Add(time.Hour)})
	if err != nil || claim.Lease == nil {
		return workflowruntime.MemoEntry{}, fmt.Errorf("claim memo source: %w", err)
	}
	proof := workflowruntime.ClaimProof{Owner: claim.Lease.Owner, Token: claim.Lease.Token, Generation: claim.Lease.Generation}
	claimed, _ := state.LoadNodeInvocation(ctx, id)
	started, err := state.StartNodeAttempt(ctx, workflowruntime.StartNodeAttemptRequest{InvocationID: id, ExpectedNodeGeneration: claimed.Generation, Claim: proof, Executor: workflowruntime.ExecutorMetadata{Kind: "transform", Version: "v1"}, Inputs: &bound.Inputs, At: base.Add(3 * time.Second)})
	if err != nil {
		return workflowruntime.MemoEntry{}, err
	}
	outputMetadata := qualificationMetadata("memo-output")
	outputMetadata.Retention = values.RetentionProject
	outputValue, _ := values.NewInline("output", outputMetadata)
	outputRef, err := state.SaveValues(ctx, workflowruntime.SaveValuesRequest{Owner: workflowruntime.ValueOwner{Kind: "node-attempt-outputs", RunID: run.ID, Invocation: &id, Attempt: &started.Attempt.ID}, Values: values.ValueSet{"result": outputValue}})
	if err != nil {
		return workflowruntime.MemoEntry{}, err
	}
	if _, err := state.FinishNodeAttempt(ctx, workflowruntime.FinishNodeAttemptRequest{InvocationID: id, AttemptNumber: started.Attempt.ID.Number, ExpectedNodeGeneration: started.Node.Generation, ExpectedAttemptGeneration: started.Attempt.Generation, Claim: proof, AttemptStatus: workflowruntime.NodeSucceeded, NextNodeStatus: workflowruntime.NodeSucceeded, Outputs: &outputRef, At: base.Add(4 * time.Second)}); err != nil {
		return workflowruntime.MemoEntry{}, err
	}
	entry := qualificationMemoEntry(base.Add(4 * time.Second))
	entry.Key = values.SHA256Digest([]byte("memo-key"))
	entry.PlanDigest = run.Plan.Digest
	entry.MemoKeyDigest = memoDigest
	entry.InputDigest = bound.Inputs.Digest
	entry.OutputDigest = outputRef.Digest
	entry.Outputs = outputRef
	entry.Source = id
	entry.SourceAttempt = started.Attempt.ID
	return entry, nil
}

func qualificationMemoEntry(at time.Time) workflowruntime.MemoEntry {
	digest := values.SHA256Digest([]byte("qualification"))
	source := workflowruntime.NodeInvocationID{RunID: "source", NodeID: "work"}
	return workflowruntime.MemoEntry{Key: digest, PlanDigest: digest, NodeID: "work", Kind: "transform", KindVersion: "v1", MemoKeyDigest: digest, InputDigest: digest, OutputSchemaDigest: values.SHA256Digest([]byte("schema")), OutputDigest: digest, Outputs: values.ValueSetRef{ID: "values-1", Digest: digest}, Source: source, SourceAttempt: workflowruntime.AttemptID{Invocation: source, Number: 1}, SourceOrigin: workflowruntime.OriginExecuted, Effects: graph.EffectSet{graph.EffectCompute}, Policy: workflowruntime.ReusePolicyDecision{Allow: true, Code: "safe", Reason: "compute"}, CreatedAt: at, ExpiresAt: at.Add(time.Hour)}
}

func qualificationTime() time.Time { return time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC) }

func qualificationPlan(suffix string) workflowruntime.PlanRef {
	suffix = strings.ReplaceAll(suffix, "_", "-")
	return workflowruntime.PlanRef{ID: "qualification-" + suffix, Version: "v1", Digest: values.SHA256Digest([]byte("qualification-" + suffix)), SchemaVersion: workflowcompile.ExecutionPlanSchemaVersion}
}

func qualificationMetadata(reference string) values.Metadata {
	return values.Metadata{Producer: values.Producer{Kind: "nanite-conformance", Reference: reference}, MediaType: "application/json", Redaction: values.RedactionPrivate, Retention: values.RetentionRun}
}

func createQualificationRun(ctx context.Context, state *WorkflowStateStore, id workflowruntime.RunID, at time.Time) (workflowruntime.RunSnapshot, error) {
	run, _, err := state.CreateRun(ctx, workflowruntime.CreateRunRequest{ID: id, Plan: qualificationPlan(string(id)), Status: workflowruntime.RunPending, StartIdempotencyKey: "start-" + string(id), CreatedAt: at})
	if err != nil {
		return workflowruntime.RunSnapshot{}, err
	}
	running, err := state.TransitionRun(ctx, workflowruntime.RunTransitionRequest{RunID: id, ExpectedGeneration: run.Generation, To: workflowruntime.RunRunning, At: at})
	if err != nil {
		return workflowruntime.RunSnapshot{}, err
	}
	return running.Snapshot, nil
}

func createQualificationNode(ctx context.Context, state *WorkflowStateStore, id workflowruntime.NodeInvocationID, at time.Time, ready bool) (workflowruntime.NodeInvocationSnapshot, error) {
	node, err := state.CreateNodeInvocation(ctx, workflowruntime.CreateNodeInvocationRequest{Snapshot: workflowruntime.NodeInvocationSnapshot{ID: id, Status: workflowruntime.NodePending, CreatedAt: at, UpdatedAt: at}})
	if err != nil || !ready {
		return node, err
	}
	transition, err := state.TransitionNode(ctx, workflowruntime.NodeTransitionRequest{InvocationID: id, ExpectedGeneration: node.Generation, To: workflowruntime.NodeReady, At: at})
	return transition.Snapshot, err
}

func finishQualificationNode(ctx context.Context, state *WorkflowStateStore, id workflowruntime.NodeInvocationID, status workflowruntime.NodeStatus, failureCode string, at time.Time) error {
	node, err := state.LoadNodeInvocation(ctx, id)
	if err != nil {
		return err
	}
	if node.Status == workflowruntime.NodePending {
		ready, transitionErr := state.TransitionNode(ctx, workflowruntime.NodeTransitionRequest{InvocationID: id, ExpectedGeneration: node.Generation, To: workflowruntime.NodeReady, At: at})
		if transitionErr != nil {
			return transitionErr
		}
		node = ready.Snapshot
	}
	claim, err := state.ClaimNode(ctx, workflowruntime.ClaimNodeRequest{InvocationID: id, ExpectedClaimGeneration: node.ClaimGeneration, Owner: "fixture", Token: "token-" + id.NodeID, IdempotencyKey: "claim-" + id.NodeID, Now: at, LeaseUntil: at.Add(time.Hour)})
	if err != nil || claim.Lease == nil {
		return fmt.Errorf("claim %s: %w", id.NodeID, err)
	}
	proof := workflowruntime.ClaimProof{Owner: claim.Lease.Owner, Token: claim.Lease.Token, Generation: claim.Lease.Generation}
	claimed, _ := state.LoadNodeInvocation(ctx, id)
	started, err := state.StartNodeAttempt(ctx, workflowruntime.StartNodeAttemptRequest{InvocationID: id, ExpectedNodeGeneration: claimed.Generation, Claim: proof, Executor: workflowruntime.ExecutorMetadata{Kind: "fixture", Version: "v1"}, At: at})
	if err != nil {
		return err
	}
	var failure *workflowruntime.Failure
	if status != workflowruntime.NodeSucceeded {
		failure = &workflowruntime.Failure{Code: failureCode, Message: "fixture failure"}
		if status == workflowruntime.NodeTimedOut {
			failure.Details = map[string]string{"timeout_kind": string(workflowruntime.TimeoutExecution)}
		}
	}
	_, err = state.FinishNodeAttempt(ctx, workflowruntime.FinishNodeAttemptRequest{InvocationID: id, AttemptNumber: started.Attempt.ID.Number, ExpectedNodeGeneration: started.Node.Generation, ExpectedAttemptGeneration: started.Attempt.Generation, Claim: proof, AttemptStatus: status, NextNodeStatus: status, Failure: failure, At: at})
	return err
}
