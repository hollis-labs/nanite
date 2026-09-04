package workflowhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/hollis-labs/go-workflow/conformance"
	"github.com/hollis-labs/go-workflow/graph"
	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
	"github.com/hollis-labs/go-workflow/values"
)

type naniteControlFlowFixture struct {
	Scenario           string                                 `json:"scenario"`
	Status             workflowruntime.NodeStatus             `json:"status"`
	CleanupStatus      workflowruntime.NodeStatus             `json:"cleanup_status"`
	FailureCode        string                                 `json:"failure_code"`
	Timeout            workflowruntime.TimeoutKind            `json:"timeout"`
	Catch              []graph.CatchRule                      `json:"catch"`
	Switch             *graph.SwitchSpec                      `json:"switch"`
	Graph              graph.Graph                            `json:"graph"`
	WantOutcome        workflowruntime.ControlDecisionOutcome `json:"want_outcome"`
	WantTargets        []string                               `json:"want_targets"`
	WantFinalizers     []string                               `json:"want_finalizers"`
	WantOrders         []int                                  `json:"want_orders"`
	WantIntendedStatus workflowruntime.RunStatus              `json:"want_intended_status"`
	WantRunStatus      workflowruntime.RunStatus              `json:"want_run_status"`
}

func runNaniteControlFlowFixture(ctx context.Context, fixture conformance.Fixture, state *WorkflowStateStore) error {
	var input naniteControlFlowFixture
	if err := json.Unmarshal(fixture.Input, &input); err != nil {
		return err
	}
	switch input.Scenario {
	case "switch":
		return runNaniteSwitchFixture(ctx, state, input)
	case "catch":
		return runNaniteCatchFixture(ctx, state, input)
	case "finally":
		return runNaniteFinallyFixture(ctx, state, input)
	case "completion":
		return runNaniteCompletionFixture(ctx, state, input)
	default:
		return fmt.Errorf("unknown control-flow scenario %q", input.Scenario)
	}
}

func newNaniteControlFixture(ctx context.Context, state *WorkflowStateStore, suffix string) (workflowruntime.RunID, time.Time, error) {
	base := qualificationTime()
	run, err := createQualificationRun(ctx, state, workflowruntime.RunID("control-"+suffix), base)
	return run.ID, base, err
}

func runNaniteSwitchFixture(ctx context.Context, state *WorkflowStateStore, input naniteControlFlowFixture) error {
	runID, base, err := newNaniteControlFixture(ctx, state, "switch")
	if err != nil {
		return err
	}
	targetSet := make(map[string]struct{})
	for _, arm := range input.Switch.Arms {
		for _, target := range arm.Targets {
			targetSet[target] = struct{}{}
		}
	}
	for _, target := range input.Switch.Default {
		targetSet[target] = struct{}{}
	}
	for node := range targetSet {
		if _, nodeErr := createQualificationNode(ctx, state, workflowruntime.NodeInvocationID{RunID: runID, NodeID: node}, base, false); nodeErr != nil {
			return nodeErr
		}
	}
	sourceID := workflowruntime.NodeInvocationID{RunID: runID, NodeID: "source"}
	if _, nodeErr := createQualificationNode(ctx, state, sourceID, base, false); nodeErr != nil {
		return nodeErr
	}
	if finishErr := finishQualificationNode(ctx, state, sourceID, workflowruntime.NodeSucceeded, "", base.Add(time.Second)); finishErr != nil {
		return finishErr
	}
	source := graph.Node{ID: "source", Switch: input.Switch}
	coordinator := workflowruntime.NewControlFlowCoordinator(state, state, nil)
	result, err := coordinator.DecideSwitch(ctx, workflowruntime.DecideSwitchRequest{Source: sourceID, Node: source, At: base.Add(2 * time.Second)})
	if err != nil || result.Decision.Outcome != input.WantOutcome || !slices.Equal(naniteControlTargetNames(result.Decision.Targets), input.WantTargets) {
		return fmt.Errorf("switch decision = %#v: %w", result.Decision, err)
	}
	targets := make([]string, 0, len(targetSet))
	for target := range targetSet {
		targets = append(targets, target)
	}
	slices.Sort(targets)
	graphNodes := []graph.Node{source}
	for _, target := range targets {
		graphNodes = append(graphNodes, graph.Node{ID: target})
	}
	for _, target := range targets {
		progress, err := coordinator.ProgressControlNode(ctx, workflowruntime.ProgressControlNodeRequest{Graph: graph.Graph{Nodes: graphNodes}, InvocationID: workflowruntime.NodeInvocationID{RunID: runID, NodeID: target}, At: base.Add(3 * time.Second)})
		want := workflowruntime.NodeSkipped
		if slices.Contains(input.WantTargets, target) {
			want = workflowruntime.NodeReady
		}
		if err != nil || progress.Snapshot.Status != want {
			return fmt.Errorf("switch target %s = %#v: %w", target, progress, err)
		}
	}
	return nil
}

func runNaniteCatchFixture(ctx context.Context, state *WorkflowStateStore, input naniteControlFlowFixture) error {
	runID, base, err := newNaniteControlFixture(ctx, state, "catch-"+string(input.Status))
	if err != nil {
		return err
	}
	sourceID := workflowruntime.NodeInvocationID{RunID: runID, NodeID: "source"}
	if _, nodeErr := createQualificationNode(ctx, state, sourceID, base, false); nodeErr != nil {
		return nodeErr
	}
	targetSet := make(map[string]struct{})
	for _, rule := range input.Catch {
		for _, target := range rule.Targets {
			targetSet[target] = struct{}{}
			if _, nodeErr := createQualificationNode(ctx, state, workflowruntime.NodeInvocationID{RunID: runID, NodeID: target}, base, false); nodeErr != nil {
				return nodeErr
			}
		}
	}
	if finishErr := finishQualificationNode(ctx, state, sourceID, input.Status, input.FailureCode, base.Add(time.Second)); finishErr != nil {
		return finishErr
	}
	source := graph.Node{ID: "source", Catch: input.Catch}
	coordinator := workflowruntime.NewControlFlowCoordinator(state, state, nil)
	result, err := coordinator.DecideCatch(ctx, workflowruntime.DecideCatchRequest{Source: sourceID, Node: source, Timeout: input.Timeout, At: base.Add(2 * time.Second)})
	if err != nil || result.Decision.Outcome != input.WantOutcome || !slices.Equal(naniteControlTargetNames(result.Decision.Targets), input.WantTargets) {
		return fmt.Errorf("catch decision = %#v: %w", result.Decision, err)
	}
	if result.Decision.Error == nil {
		return errors.New("catch decision omitted typed error")
	}
	set, err := state.LoadValues(ctx, *result.Decision.Error)
	if err != nil {
		return err
	}
	if input.Timeout != "" {
		payload, ok := set["error"].Inline.(map[string]any)
		if !ok || payload["timeout_kind"] != string(input.Timeout) {
			return fmt.Errorf("timeout error = %#v", set["error"].Inline)
		}
	}
	if result.Decision.Outcome == workflowruntime.ControlSelected {
		nodes := []graph.Node{source}
		for target := range targetSet {
			targetNode := graph.Node{ID: target}
			if result.Decision.BindAs != "" {
				targetNode.If = &graph.Expression{Text: result.Decision.BindAs + `.code == "` + input.FailureCode + `"`}
			}
			nodes = append(nodes, targetNode)
		}
		progress, err := coordinator.ProgressControlNode(ctx, workflowruntime.ProgressControlNodeRequest{Graph: graph.Graph{Nodes: nodes}, InvocationID: workflowruntime.NodeInvocationID{RunID: runID, NodeID: input.WantTargets[0]}, At: base.Add(3 * time.Second)})
		if err != nil || progress.Snapshot.Status != workflowruntime.NodeReady {
			return fmt.Errorf("catch handler = %#v: %w", progress, err)
		}
		if result.Decision.BindAs != "" {
			name, binding, err := workflowruntime.CatchBinding(ctx, state, state, result.Decision.ID)
			if err != nil || name != result.Decision.BindAs || binding[name].Type != values.TypeObject {
				return fmt.Errorf("catch binding %s = %#v: %w", name, binding, err)
			}
		}
	}
	if result.Decision.Outcome == workflowruntime.ControlContinued {
		run, intent, err := coordinator.ReconcileRunCompletion(ctx, graph.Graph{Nodes: []graph.Node{source}}, runID, "continue-completion", base.Add(3*time.Second))
		if err != nil || intent != nil || run.Status != workflowruntime.RunSucceeded {
			return fmt.Errorf("continued completion = %#v/%#v: %w", run, intent, err)
		}
	}
	return nil
}

func runNaniteFinallyFixture(ctx context.Context, state *WorkflowStateStore, input naniteControlFlowFixture) error {
	scopes, err := workflowruntime.PlanFinalizerScopes(input.Graph, "control-finally")
	if err != nil {
		return err
	}
	ids := make([]string, len(scopes))
	orders := make([]int, len(scopes))
	for i, scope := range scopes {
		ids[i], orders[i] = scope.Invocation.NodeID, scope.Order
	}
	if !slices.Equal(ids, input.WantFinalizers) || !slices.Equal(orders, input.WantOrders) {
		return fmt.Errorf("finalizer plan = %v/%v", ids, orders)
	}
	runID, base, err := newNaniteControlFixture(ctx, state, "finally")
	if err != nil {
		return err
	}
	for _, node := range input.Graph.Nodes {
		id := workflowruntime.NodeInvocationID{RunID: runID, NodeID: node.ID}
		if _, nodeErr := createQualificationNode(ctx, state, id, base, false); nodeErr != nil {
			return nodeErr
		}
		if node.Finally == nil {
			if finishErr := finishQualificationNode(ctx, state, id, workflowruntime.NodeSucceeded, "", base.Add(time.Second)); finishErr != nil {
				return finishErr
			}
		}
	}
	coordinator := workflowruntime.NewControlFlowCoordinator(state, state, nil)
	if _, _, reconcileErr := coordinator.ReconcileRunCompletion(ctx, input.Graph, runID, "finally-completion", base.Add(2*time.Second)); !errors.Is(reconcileErr, workflowruntime.ErrControlFlowPending) {
		return fmt.Errorf("begin finalizer intent: %w", reconcileErr)
	}
	for i, nodeID := range input.WantFinalizers {
		progress, progressErr := coordinator.ProgressFinally(ctx, input.Graph, workflowruntime.NodeInvocationID{RunID: runID, NodeID: nodeID}, values.ExpressionContext{}, values.ExpressionOptions{}, base.Add(time.Duration(3+i*2)*time.Second))
		if progressErr != nil || progress.Snapshot.Status != workflowruntime.NodeReady {
			return fmt.Errorf("progress finalizer %s = %#v: %w", nodeID, progress, progressErr)
		}
		if finishErr := finishQualificationNode(ctx, state, workflowruntime.NodeInvocationID{RunID: runID, NodeID: nodeID}, workflowruntime.NodeSucceeded, "", base.Add(time.Duration(4+i*2)*time.Second)); finishErr != nil {
			return finishErr
		}
	}
	completed, intent, err := coordinator.ReconcileRunCompletion(ctx, input.Graph, runID, "finally-completion", base.Add(time.Duration(4+len(input.WantFinalizers)*2)*time.Second))
	if err != nil || completed.Status != workflowruntime.RunSucceeded || intent == nil || intent.Status != workflowruntime.TerminalIntentCompleted {
		return fmt.Errorf("finalizer completion = %#v/%#v: %w", completed, intent, err)
	}
	return nil
}

func runNaniteCompletionFixture(ctx context.Context, state *WorkflowStateStore, input naniteControlFlowFixture) error {
	runID, base, err := newNaniteControlFixture(ctx, state, "completion")
	if err != nil {
		return err
	}
	workflow := graph.Graph{Nodes: []graph.Node{{ID: "work"}, {ID: "cleanup", Finally: &graph.FinallySpec{}}}}
	for _, node := range workflow.Nodes {
		if _, nodeErr := createQualificationNode(ctx, state, workflowruntime.NodeInvocationID{RunID: runID, NodeID: node.ID}, base, false); nodeErr != nil {
			return nodeErr
		}
	}
	if finishErr := finishQualificationNode(ctx, state, workflowruntime.NodeInvocationID{RunID: runID, NodeID: "work"}, input.Status, "work_failure", base.Add(time.Second)); finishErr != nil {
		return finishErr
	}
	coordinator := workflowruntime.NewControlFlowCoordinator(state, state, nil)
	run, intent, reconcileErr := coordinator.ReconcileRunCompletion(ctx, workflow, runID, "completion", base.Add(2*time.Second))
	if !errors.Is(reconcileErr, workflowruntime.ErrControlFlowPending) || intent == nil || intent.IntendedStatus != input.WantIntendedStatus || !run.Status.Active() {
		return fmt.Errorf("pending completion = %#v/%#v: %w", run, intent, reconcileErr)
	}
	progress, err := coordinator.ProgressFinally(ctx, workflow, workflowruntime.NodeInvocationID{RunID: runID, NodeID: "cleanup"}, values.ExpressionContext{}, values.ExpressionOptions{}, base.Add(3*time.Second))
	if err != nil || progress.Snapshot.Status != workflowruntime.NodeReady {
		return fmt.Errorf("cleanup progression = %#v: %w", progress, err)
	}
	if finishErr := finishQualificationNode(ctx, state, workflowruntime.NodeInvocationID{RunID: runID, NodeID: "cleanup"}, input.CleanupStatus, "cleanup_failure", base.Add(4*time.Second)); finishErr != nil {
		return finishErr
	}
	run, intent, err = coordinator.ReconcileRunCompletion(ctx, workflow, runID, "completion", base.Add(5*time.Second))
	if err != nil || intent == nil || run.Status != input.WantRunStatus {
		return fmt.Errorf("completion = %#v/%#v: %w", run, intent, err)
	}
	return nil
}

func naniteControlTargetNames(ids []workflowruntime.NodeInvocationID) []string {
	result := make([]string, len(ids))
	for i, id := range ids {
		result[i] = id.NodeID
	}
	return result
}
