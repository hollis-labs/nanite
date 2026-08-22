package service

// TASKS/loops/09-stepkindloop-executor-and-waiting-status.md — the real
// loop-step (StepKindLoop) executor: what happens on first entry (launch
// the contained LoopRun, this file's startLoopStep) and what resolves a
// waiting_on_loop step (recheckLoopStep, dispatched from execute()'s
// per-level loop in workflow_engine.go). Mirrors workflow_engine_flex.go's
// own file-per-step-kind convention exactly.
//
// # The import-cycle resolution this task's Context section asked to be
// verified, not assumed
//
// The design doc's own recommendation (a small OuterResumeNotifier
// interface LoopEngine takes as a constructor dependency, implemented in
// internal/service) is the right shape, but the parenthetical reasoning the
// task file gave for it ("internal/service likely already imports
// internal/loop for task 08's own wiring, so a direct internal/loop ->
// internal/service import would cycle") is backwards from what this
// session confirmed directly against the real code: internal/loop already
// imports internal/service (internal/loop/engine.go's own WorkflowLauncher
// dependency, task 08) — NOT the other way around. There is, in fact, zero
// cycle risk in the "notify" direction this task's Context text focused
// on: internal/loop calling back into internal/service to resume an outer
// WorkflowRun compiles fine today even with a concrete type reference,
// since internal/loop -> internal/service is already a real, one-way edge.
//
// The genuine cycle risk is in the OTHER direction, the one actually
// exercised by this file: internal/service's own StepKindLoop executor
// (startLoopStep, below) needs to LAUNCH a Loop by calling
// internal/loop.LoopEngine.Run — and internal/service importing
// internal/loop for that would create exactly
// internal/service -> internal/loop -> internal/service, a real cycle,
// given internal/loop's existing import of internal/service.
//
// This file's LoopStepLauncher interface is the fix for THAT direction:
// declared here (the consumer — mirrors TeamMembershipStore/
// FlexStepStateCollector's own "consumer declares the narrow interface"
// precedent in workflow_engine_flex.go), using only internal/service-native
// request/result types (LoopStepLaunchRequest/LoopStepLaunchResult below —
// no internal/loop type ever appears in this file's exported surface),
// satisfied structurally by *loop.LoopEngine itself via a LaunchLoop method
// added directly to that type (internal/loop/step_launcher.go) — legal
// because internal/loop already imports internal/service, so that method
// can freely reference LoopStepLaunchRequest/LoopStepLaunchResult without
// creating the reverse edge.
//
// For symmetry, and because internal/loop calling back into
// internal/service to resume an outer run genuinely does need a narrow,
// swappable surface for LoopEngine's own tests (not because of a cycle),
// this file also provides the concrete implementation
// (LoopResumeNotifier, below) of internal/loop's own OuterResumeNotifier
// interface (internal/loop/outer_resume.go) — declared over there, the
// consumer, per that same precedent, one layer down.
//
// # Config-schema resolution (item 4, "your call, document it")
//
// See parseLoopStepConfig's own doc comment for the full schema and the
// documented default for "how outer-workflow params map to the inner
// Loop's overrides": workflow_params, when the step's own Config omits it,
// defaults to forwarding the OUTER run's own WorkflowInput.Params
// verbatim.
//
// # Where loop_run_id gets recorded on the step (item 4, "your call,
// document it")
//
// A real column, workflow_run_steps.loop_run_id (migration 141), not a
// JSON field — this step needs the association looked up in both
// directions (given the step, read its loop_run_id off the row; given a
// terminal loop_run_id, find the step waiting on it —
// GetWorkflowRunStepByLoopRunID, internal/store/workflow_runs.go), and
// only a real, indexable column supports the second direction.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

// LoopRunStatusStore is the narrow loop_runs read surface a StepKindLoop
// step needs to re-check whether its contained LoopRun has reached a
// terminal state. *store.Store satisfies this structurally
// (internal/store/loop_runs.go) — narrowed to this one method, matching
// TeamMembershipStore/FlexStepStateCollector's own narrow-interface
// precedent in workflow_engine_flex.go.
type LoopRunStatusStore interface {
	GetLoopRun(ctx context.Context, id string) (*store.LoopRun, error)
}

// LoopStepLauncher is the narrow internal/loop.LoopEngine.Run surface a
// StepKindLoop step needs to actually launch its contained Loop — the
// load-bearing import-cycle fix this file's own package doc comment above
// works through in detail. Declared here (the consumer), speaking only
// internal/service-native types, satisfied structurally by
// *loop.LoopEngine (internal/loop/step_launcher.go's LaunchLoop method),
// wired at container-build time (cmd/nanite/main.go), never by
// internal/service importing internal/loop directly.
type LoopStepLauncher interface {
	LaunchLoop(ctx context.Context, req LoopStepLaunchRequest) (LoopStepLaunchResult, error)
}

// LoopStepContinuationPolicy mirrors internal/loop.ContinuationPolicy's own
// four fields exactly (Provider/Model/AgentID/SessionID/Tools) — duplicated
// here, not aliased, since aliasing would require importing internal/loop.
// internal/loop/step_launcher.go's LaunchLoop is the one place that
// translates between this and the real loop.ContinuationPolicy.
type LoopStepContinuationPolicy struct {
	Provider  string
	Model     string
	AgentID   string
	SessionID string
	Tools     []string
}

// LoopStepLaunchRequest is what a StepKindLoop step's resolved Config
// becomes when handed to LoopStepLauncher.LaunchLoop — see
// parseLoopStepConfig's doc comment for the Config schema this is parsed
// from. Field-for-field, this mirrors internal/loop.LoopInput plus
// LoopDefinition.WorkflowName and LoopGoalSpec's own fields flattened in
// (the inline-goal union), deliberately not just embedding those types
// (which would require importing internal/loop).
type LoopStepLaunchRequest struct {
	WorkflowName string

	// GoalID references an existing goals row. Exactly one of GoalID or
	// the Goal* fields below must be set — mirrors loop.LoopInput's own
	// GoalID/Goal union rule exactly.
	GoalID string

	// Goal* mirror loop.LoopGoalSpec's own fields — an inline goal spec,
	// used only when GoalID is empty.
	GoalIntent             string
	GoalDesiredState       []string
	GoalConstraints        []string
	GoalAcceptanceCriteria []string
	GoalInvariants         []string
	GoalParentID           string
	GoalPriority           string
	GoalScope              string
	GoalOwner              string
	GoalSource             string

	Budget             store.Budget
	ContinuationPolicy LoopStepContinuationPolicy

	// WorkflowParams is forwarded to every iteration's
	// WorkflowLaunchRequest.Params. parseLoopStepConfig defaults this to
	// the OUTER run's own WorkflowInput.Params when the step's Config
	// doesn't set its own — see that function's doc comment.
	WorkflowParams map[string]any

	AgentProfileID  string
	ProjectID       string
	ParentSessionID string
	TimeoutSeconds  int
}

// LoopStepLaunchResult is LaunchLoop's return value — mirrors
// internal/loop.LoopResult's own three fields relevant to a StepKindLoop
// step (LastDecision is not included: a loop step cares only about the
// LoopRun's status, not the continuation-policy verdict that produced it).
type LoopStepLaunchResult struct {
	LoopRunID string
	// Status is one of the store.LoopRunStatus* constants.
	Status           string
	CurrentIteration int
}

// parseLoopStepConfig decodes and validates one StepKindLoop step's Config
// into the LoopStepLaunchRequest startLoopStep needs — deliberately NOT
// run through resolveStepConfig's {{ }} templating, mirroring
// StepKindGate/StepKindFlex: neither templates its own Config either, both
// being engine-native pausing steps whose Config is read as literal
// author-time configuration.
//
// Config schema (this task's own documented default for the "how
// outer-workflow params map to the inner Loop's goal_id/overrides"
// question):
//
//	config:
//	  workflow_name: "..."       # required -- LoopDefinition.WorkflowName
//	  agent_profile_id: "..."    # required -- forwarded to every iteration's launch
//	  goal_id: "..."             # exactly one of goal_id / goal required
//	  goal:
//	    intent: "..."
//	    desired_state: [...]
//	    constraints: [...]
//	    acceptance_criteria: [...]
//	    invariants: [...]
//	    parent_goal_id: "..."
//	    priority: "..."
//	    scope: "..."
//	    owner: "..."
//	    source: "..."
//	  budget:
//	    max_iterations: N
//	    max_failures: N
//	    max_runtime_seconds: N
//	    max_no_progress_iterations: N
//	    on_exhausted: "escalate"|"fail"
//	  continuation_policy:
//	    provider: "..."
//	    model: "..."
//	    agent_id: "..."
//	    session_id: "..."
//	    tools: [...]
//	  workflow_params: {...}     # forwarded verbatim to every iteration's
//	                              # WorkflowLaunchRequest.Params -- DEFAULTS
//	                              # to the OUTER run's own
//	                              # WorkflowInput.Params verbatim when
//	                              # omitted (the documented default for
//	                              # "how outer-workflow params map to the
//	                              # inner Loop's overrides")
//	  project_id: "..."
//	  parent_session_id: "..."
//	  timeout_seconds: N
func parseLoopStepConfig(cfg map[string]any, outerParams map[string]any) (LoopStepLaunchRequest, error) {
	var out LoopStepLaunchRequest

	out.WorkflowName = configString(cfg, "workflow_name")
	if out.WorkflowName == "" {
		return out, fmt.Errorf("loop step config requires a non-empty \"workflow_name\"")
	}
	out.AgentProfileID = configString(cfg, "agent_profile_id")
	if out.AgentProfileID == "" {
		return out, fmt.Errorf("loop step config requires a non-empty \"agent_profile_id\"")
	}

	goalID := configString(cfg, "goal_id")
	goalCfg := configMap(cfg, "goal")
	switch {
	case goalID != "" && goalCfg != nil:
		return out, fmt.Errorf("loop step config: exactly one of \"goal_id\" or \"goal\" must be set, not both")
	case goalID != "":
		out.GoalID = goalID
	case goalCfg != nil:
		out.GoalIntent = configString(goalCfg, "intent")
		out.GoalDesiredState = configStringSlice(goalCfg, "desired_state")
		out.GoalConstraints = configStringSlice(goalCfg, "constraints")
		out.GoalAcceptanceCriteria = configStringSlice(goalCfg, "acceptance_criteria")
		out.GoalInvariants = configStringSlice(goalCfg, "invariants")
		out.GoalParentID = configString(goalCfg, "parent_goal_id")
		out.GoalPriority = configString(goalCfg, "priority")
		out.GoalScope = configString(goalCfg, "scope")
		out.GoalOwner = configString(goalCfg, "owner")
		out.GoalSource = configString(goalCfg, "source")
	default:
		return out, fmt.Errorf("loop step config requires exactly one of \"goal_id\" or \"goal\"")
	}

	if budgetCfg := configMap(cfg, "budget"); budgetCfg != nil {
		out.Budget = store.Budget{
			MaxIterations:           configInt(budgetCfg, "max_iterations"),
			MaxFailures:             configInt(budgetCfg, "max_failures"),
			MaxRuntimeSeconds:       configInt(budgetCfg, "max_runtime_seconds"),
			MaxNoProgressIterations: configInt(budgetCfg, "max_no_progress_iterations"),
			OnExhausted:             configString(budgetCfg, "on_exhausted"),
		}
	}

	if policyCfg := configMap(cfg, "continuation_policy"); policyCfg != nil {
		out.ContinuationPolicy = LoopStepContinuationPolicy{
			Provider:  configString(policyCfg, "provider"),
			Model:     configString(policyCfg, "model"),
			AgentID:   configString(policyCfg, "agent_id"),
			SessionID: configString(policyCfg, "session_id"),
			Tools:     configStringSlice(policyCfg, "tools"),
		}
	}

	if wp := configMap(cfg, "workflow_params"); wp != nil {
		out.WorkflowParams = wp
	} else {
		out.WorkflowParams = outerParams
	}

	out.ProjectID = configString(cfg, "project_id")
	out.ParentSessionID = configString(cfg, "parent_session_id")
	out.TimeoutSeconds = configInt(cfg, "timeout_seconds")

	return out, nil
}

// isTerminalLoopStatus reports whether status is one of the three genuine
// terminal loop_runs.status values (completed/failed/cancelled) — distinct
// from the two paused values (waiting_on_gate/waiting_on_escalation),
// which keep a StepKindLoop step in waiting_on_loop.
func isTerminalLoopStatus(status string) bool {
	switch status {
	case store.LoopRunStatusCompleted, store.LoopRunStatusFailed, store.LoopRunStatusCancelled:
		return true
	default:
		return false
	}
}

// loopStepResultForOutcome builds the StepResult a StepKindLoop step
// resolves to once its contained LoopRun reaches a terminal state — shared
// by startLoopStep's immediate-resolve path and recheckLoopStep's
// wait-resolution path so both produce an identically-shaped result for
// the same underlying loop_runs.status.
func loopStepResultForOutcome(step agentworkflow.StepDefinition, loopRunID, status string, currentIteration int) agentworkflow.StepResult {
	if status == store.LoopRunStatusCompleted {
		return agentworkflow.StepResult{
			StepID: step.ID, Kind: step.Kind,
			Output: fmt.Sprintf("loop_run %s completed after %d iteration(s)", loopRunID, currentIteration),
		}
	}
	return agentworkflow.StepResult{
		StepID: step.ID, Kind: step.Kind, IsError: true,
		Output: fmt.Sprintf("loop_run %s ended with status %q", loopRunID, status),
	}
}

// startLoopStep is runStep's entry point on first reaching a StepKindLoop
// step (workflow_engine.go): resolve config, launch the contained LoopRun,
// and either resolve this step immediately — the contained LoopRun already
// reached a terminal state synchronously within this one launch call,
// exactly the trivial-bounded-loop (Ralph-shaped) common case, per
// internal/loop's own "Run drives iteration-to-iteration synchronously
// within one call" framing — or record the loop_run_id and mark
// waiting_on_loop for the still-in-flight case (the LoopRun's first
// iteration itself paused, e.g. on a gate).
//
// A config error or a LaunchLoop failure (e.g. ErrLoopRunAlreadyActive, a
// missing required field the launcher itself rejects) resolves this step
// to a normal, persisted failure — matching executeLLMOrTool's own
// "config resolution error is a step failure, not an infra Err" precedent
// — rather than aborting the whole outer run at the stepRunOutcome.Err
// level.
func (e *BuiltinWorkflowEngine) startLoopStep(ctx context.Context, runID string, step agentworkflow.StepDefinition, input agentworkflow.WorkflowInput) stepRunOutcome {
	if e.loopLauncher == nil {
		// Defensive: an engine constructed without WithLoopSupport reached
		// a loop step anyway. Production wiring (cmd/nanite/main.go)
		// always calls WithLoopSupport; this is a clear, terminal config
		// error rather than an infinite wait for a caller that built the
		// engine bare.
		return e.persistLoopStepFailure(runID, step, "loop step: BuiltinWorkflowEngine.WithLoopSupport was never called -- no LoopStepLauncher configured")
	}

	req, cfgErr := parseLoopStepConfig(step.Config, input.Params)
	if cfgErr != nil {
		return e.persistLoopStepFailure(runID, step, fmt.Sprintf("loop step config: %v", cfgErr))
	}

	result, launchErr := e.loopLauncher.LaunchLoop(ctx, req)
	if launchErr != nil {
		return e.persistLoopStepFailure(runID, step, fmt.Sprintf("loop step: launch loop: %v", launchErr))
	}

	if isTerminalLoopStatus(result.Status) {
		sr := loopStepResultForOutcome(step, result.LoopRunID, result.Status, result.CurrentIteration)
		status := "completed"
		if sr.IsError {
			status = "failed"
		}
		loopRunID := result.LoopRunID
		if err := e.store.UpsertWorkflowRunStep(&store.WorkflowRunStepRow{
			WorkflowRunID: runID, StepID: step.ID, Kind: string(step.Kind), Status: status,
			Output: sr.Output, IsError: sr.IsError, LoopRunID: &loopRunID, CompletedAt: time.Now().UTC(),
		}); err != nil {
			return stepRunOutcome{Err: fmt.Errorf("loop step %q: persist immediate resolution: %w", step.ID, err)}
		}
		return stepRunOutcome{Kind: outcomeCompleted, Result: sr}
	}

	loopRunID := result.LoopRunID
	if err := e.store.UpsertWorkflowRunStep(&store.WorkflowRunStepRow{
		WorkflowRunID: runID, StepID: step.ID, Kind: string(step.Kind), Status: "waiting_on_loop",
		LoopRunID: &loopRunID,
	}); err != nil {
		return stepRunOutcome{Err: fmt.Errorf("loop step %q: mark waiting_on_loop: %w", step.ID, err)}
	}
	return stepRunOutcome{Kind: outcomeWaiting, Result: agentworkflow.StepResult{StepID: step.ID, Kind: step.Kind}}
}

// persistLoopStepFailure persists step as a normal, terminal step failure
// with msg as its output — the shared tail of every non-infra error path
// in startLoopStep.
func (e *BuiltinWorkflowEngine) persistLoopStepFailure(runID string, step agentworkflow.StepDefinition, msg string) stepRunOutcome {
	sr := agentworkflow.StepResult{StepID: step.ID, Kind: step.Kind, IsError: true, Output: msg}
	if err := e.store.UpsertWorkflowRunStep(&store.WorkflowRunStepRow{
		WorkflowRunID: runID, StepID: step.ID, Kind: string(step.Kind), Status: "failed",
		Output: sr.Output, IsError: true, CompletedAt: time.Now().UTC(),
	}); err != nil {
		return stepRunOutcome{Err: fmt.Errorf("loop step %q: persist failure: %w", step.ID, err)}
	}
	return stepRunOutcome{Kind: outcomeCompleted, Result: sr}
}

// recheckLoopStep is execute()'s entry point for a loop step already
// sitting in waiting_on_loop (workflow_engine.go's per-level dispatch
// loop): re-read this step's own recorded loop_run_id and the contained
// LoopRun's live status, and — if it has reached a genuine terminal state
// — persist the resolution. Returns resolved=false to mean "still
// legitimately waiting" (a no-op — no store write happens, and the step
// stays waiting_on_loop for the next Resume-driven pass). err is reserved
// for infra-level failures (a store read failing) that should abort the
// whole run, matching recheckFlexStep's own Err-vs-Result.IsError split —
// a missing loop_run_id or an unconfigured LoopRunStatusStore is instead
// folded into a resolved=true, IsError=true StepResult, since neither will
// ever change on a later retry and the run should fail cleanly rather than
// wait forever.
func (e *BuiltinWorkflowEngine) recheckLoopStep(ctx context.Context, runID string, step agentworkflow.StepDefinition) (resolved bool, sr agentworkflow.StepResult, err error) {
	rows, listErr := e.store.ListWorkflowRunSteps(runID)
	if listErr != nil {
		return false, agentworkflow.StepResult{}, fmt.Errorf("loop step %q: list workflow_run_steps: %w", step.ID, listErr)
	}
	var loopRunID string
	for _, r := range rows {
		if r.StepID == step.ID {
			if r.LoopRunID != nil {
				loopRunID = *r.LoopRunID
			}
			break
		}
	}

	var result agentworkflow.StepResult
	switch {
	case loopRunID == "":
		result = agentworkflow.StepResult{
			StepID: step.ID, Kind: step.Kind, IsError: true,
			Output: "loop step: waiting_on_loop but no loop_run_id recorded on this step row",
		}
	case e.loopRuns == nil:
		result = agentworkflow.StepResult{
			StepID: step.ID, Kind: step.Kind, IsError: true,
			Output: "loop step: BuiltinWorkflowEngine.WithLoopSupport was never called -- no LoopRunStatusStore configured",
		}
	default:
		lr, getErr := e.loopRuns.GetLoopRun(ctx, loopRunID)
		if getErr != nil {
			return false, agentworkflow.StepResult{}, fmt.Errorf("loop step %q: get loop_run %s: %w", step.ID, loopRunID, getErr)
		}
		if !isTerminalLoopStatus(lr.Status) {
			return false, agentworkflow.StepResult{}, nil // still legitimately waiting.
		}
		result = loopStepResultForOutcome(step, lr.ID, lr.Status, lr.CurrentIteration)
	}

	status := "completed"
	if result.IsError {
		status = "failed"
	}
	upsert := &store.WorkflowRunStepRow{
		WorkflowRunID: runID, StepID: step.ID, Kind: string(step.Kind), Status: status,
		Output: result.Output, IsError: result.IsError, CompletedAt: time.Now().UTC(),
	}
	if loopRunID != "" {
		upsert.LoopRunID = &loopRunID
	}
	if err := e.store.UpsertWorkflowRunStep(upsert); err != nil {
		return false, agentworkflow.StepResult{}, fmt.Errorf("loop step %q: persist resolution: %w", step.ID, err)
	}
	return true, result, nil
}

// --- OuterResumeNotifier's real implementation ---

// LoopStepOuterStore is the narrow store surface LoopResumeNotifier needs:
// find the outer step waiting on a terminal loop_run_id, then load its
// parent workflow_runs row. *store.Store satisfies this structurally.
type LoopStepOuterStore interface {
	GetWorkflowRunStepByLoopRunID(loopRunID string) (*store.WorkflowRunStepRow, error)
	GetWorkflowRun(id string) (*store.WorkflowRunRow, error)
}

// LoopResumeNotifier implements internal/loop's OuterResumeNotifier
// interface structurally (this file's own package doc comment explains
// why internal/service does not, and need not, import internal/loop to do
// this — NotifyLoopRunTerminal's signature uses only primitive types).
//
// Finds the specific outer workflow_run_steps row (if any) waiting on a
// given terminal loop_run_id, then resumes its outer WorkflowRun via the
// identical GetEngine+concrete-engine-.Resume two-step
// a2a_task_manager.go's resumeWorkflowRun and internal/loop/engine.go's
// own resumeBlockedIteration already established — this is the third real
// caller of that same shape.
type LoopResumeNotifier struct {
	store    LoopStepOuterStore
	registry *agentworkflow.Registry
	launcher *WorkflowLauncher
}

// NewLoopResumeNotifier constructs a LoopResumeNotifier. All three
// arguments are required for real production behavior, but
// NotifyLoopRunTerminal degrades to a returned (non-panicking) error on an
// incompletely-configured notifier rather than panicking — see that
// method's own doc comment for why LoopEngine treats most of its errors as
// log-and-continue, not run-failing.
func NewLoopResumeNotifier(st LoopStepOuterStore, registry *agentworkflow.Registry, launcher *WorkflowLauncher) *LoopResumeNotifier {
	return &LoopResumeNotifier{store: st, registry: registry, launcher: launcher}
}

// NotifyLoopRunTerminal implements internal/loop's OuterResumeNotifier.
// This is the real, callable push this task's Context section requires —
// not a lazy re-check piggybacked on some unrelated caller's own Resume
// call (the pattern StepKindFlex uses, explicitly ruled out here):
// internal/loop.LoopEngine calls this directly, synchronously, the moment
// it persists a genuine terminal loop_runs.status.
//
// The overwhelming majority of LoopRuns are launched directly (Manual/API
// launch, docs/engineering/architecture/21-loops.md's "Trigger surface"),
// never from a StepKindLoop step — GetWorkflowRunStepByLoopRunID returning
// ErrWorkflowRunStepNotFound is that ordinary case, treated as success
// (nil error), not a failure to report. This is also the path a
// StepKindLoop step's own contained LoopRun takes when it happens to reach
// a terminal state synchronously within its very first LaunchLoop call
// (startLoopStep, workflow_engine_loop.go): the notifier fires from
// *inside* that same LoopEngine.Run call, before LaunchLoop has even
// returned to startLoopStep, so before the outer step's row has been
// stamped with this loop_run_id at all — a benign, expected race, not a
// bug, resolved the same way as the "no outer step at all" case.
func (n *LoopResumeNotifier) NotifyLoopRunTerminal(ctx context.Context, loopRunID string) error {
	if n == nil || n.store == nil || n.registry == nil || n.launcher == nil {
		return fmt.Errorf("loop resume notifier: not fully configured")
	}
	stepRow, err := n.store.GetWorkflowRunStepByLoopRunID(loopRunID)
	if err != nil {
		if errors.Is(err, store.ErrWorkflowRunStepNotFound) {
			return nil // ordinary case -- see this method's own doc comment.
		}
		return fmt.Errorf("loop resume notifier: find outer step for loop_run %s: %w", loopRunID, err)
	}
	if stepRow.Status != "waiting_on_loop" {
		// The outer step already resolved via some other path (e.g. an
		// earlier notify call already handled it, or the outer run failed/
		// was abandoned for an unrelated reason) -- nothing to do.
		return nil
	}

	runRow, err := n.store.GetWorkflowRun(stepRow.WorkflowRunID)
	if err != nil {
		return fmt.Errorf("loop resume notifier: get outer workflow_run %s: %w", stepRow.WorkflowRunID, err)
	}
	wf, ok := n.registry.Get(runRow.DefinitionName)
	if !ok {
		return fmt.Errorf("loop resume notifier: outer workflow definition %q not found in registry", runRow.DefinitionName)
	}
	genericEngine, ok := n.launcher.GetEngine(agentworkflow.EngineBuiltin)
	if !ok {
		return fmt.Errorf("loop resume notifier: built-in workflow engine not available")
	}
	builtinEngine, ok := genericEngine.(*BuiltinWorkflowEngine)
	if !ok {
		return fmt.Errorf("loop resume notifier: workflow engine registered for %q does not support resume", agentworkflow.EngineBuiltin)
	}
	if _, err := builtinEngine.Resume(ctx, stepRow.WorkflowRunID, wf, n.launcher.GetStepExecutor()); err != nil {
		return fmt.Errorf("loop resume notifier: resume outer workflow_run %s: %w", stepRow.WorkflowRunID, err)
	}
	return nil
}
