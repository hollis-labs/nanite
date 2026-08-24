package loop

// TASKS/loops/08-loop-engine-core.md -- the LoopEngine core:
// docs/engineering/architecture/21-loops.md's Guardrail diagram, realized:
//
//	Goal (first-class, own lifecycle)
//	    v launch or resume
//	LoopRun
//	    v evaluate prior iteration (Verify, reused)
//	    v decide (deterministic-first, reasoning fallback = one ExecuteLLMStep call)
//	    v launch next iteration
//	    v ordinary WorkflowRun (agentworkflow, unchanged) does the actual work
//
// Every arrow above except "decide" (task 07's Decide, internal/loop/
// decide.go) is this file.
//
// # LoopEngine is a concrete struct, not an interface -- a documented
// deviation from 21-loops.md's own bare illustrative sketch
//
// The design doc's illustrative shape is a `LoopEngine` *interface*
// (`Run`/`Resume`). This file makes it a concrete struct instead, per this
// task's own Context section citing the direct, already-shipped structural
// precedent: internal/service/team_run_launcher.go's TeamRunLauncher is a
// struct wrapping its collaborators (store, registry, launcher, durable),
// not an interface with one implementation. LoopEngine follows the
// identical shape one level up -- a struct wrapping *store.Store,
// *agentworkflow.Registry, and *service.WorkflowLauncher (Decision 1:
// "LoopEngine does not get its own StepExecutor" -- its only executor
// dependency is the existing WorkflowLauncher). Nothing in this task
// requires a second, swappable LoopEngine implementation the way
// agentworkflow.WorkflowEngine genuinely has more than one real
// implementation (builtin, langgraph, crewai, ...); inventing an interface
// for a single implementation would be exactly the kind of un-asked-for
// abstraction this codebase's own conventions (see CompileTeam's doc
// comment: "if implementing this ever seemed to need a
// TeamExecutionEngine/..., that would be the Guardrail's own drift signal")
// warn against.
//
// # WorkflowLauncher has no bare .Resume method -- confirmed directly
// against the real code this session, not assumed from the design doc's
// paraphrase
//
// WorkflowLauncher.Launch (internal/service/workflow_launch.go:122) is
// real; there is no WorkflowLauncher.Resume. Resuming a specific,
// already-launched WorkflowRun goes through WorkflowLauncher.GetEngine
// (line 101) to get the concrete engine, then that engine's own
// .Resume(ctx, runID, wf, exec) -- BuiltinWorkflowEngine.Resume
// (internal/service/workflow_engine.go:125), type-asserted off the
// generic agentworkflow.WorkflowEngine interface since Resume isn't part
// of that interface (only gate/flex/loop-capable engines have it). The one
// real caller precedent, confirmed directly, is
// internal/service/a2a_task_manager.go's resumeWorkflowRun (lines
// 708-762): re-fetch the WorkflowRun row, look its WorkflowDefinition up
// by name in the *shared* agentworkflow.Registry (a separate field the
// caller holds -- WorkflowLauncher itself exposes no GetRegistry accessor,
// confirmed directly; TaskManager holds its own registry reference,
// injected at construction alongside the launcher, and LoopEngine does
// the same below), then call the concrete engine's own .Resume.
// resumeBlockedIteration (below) follows this exact pattern.
//
// # Resolving TASKS/ESCALATIONS.md's 2026-08-21 goal-evidence-formula
// duplication entry
//
// See decide.go's own updated doc comment (deviation #3) for the full
// resolution: Decide no longer walks []GoalEvidence itself. This file is
// the caller that queries store.EvidenceSatisfiesGoal (evaluateDecideAndAct,
// below) and passes the already-computed bool into Decide.
//
// # loopRunPersistentConfig -- a real gap this task's own implementation
// found, not anticipated by task 07 or by 21-loops.md's illustrative schema
//
// Resume's signature is fixed at Resume(ctx, loopRunID) -- no other
// parameters, per 21-loops.md's own bare interface sketch and this task's
// own "What to do" #2. But relaunching a further iteration
// (WorkflowLauncher.Launch) hard-requires AgentProfileID
// (durable_agent_instances.profile_id is a required, NOT NULL FK), and
// this task has no schema migration in scope to give loop_runs a new
// "launch params" column. loop_runs.continuation_policy_json is the one
// column task 07 deliberately left as an untyped JSON placeholder ("task
// 07 defines and consumes its real shape without this table needing to be
// revisited") -- task 07 itself only ever needed ContinuationPolicy's own
// four fields (Provider/Model/AgentID/SessionID/Tools) for the reasoning
// fallback's own ExecuteLLMStep call. This task is the party that finishes
// defining that column's real shape: loopRunPersistentConfig (below) wraps
// ContinuationPolicy as a nested, unchanged sub-shape (so decide.go's own
// ContinuationPolicy type and its doc comments stay accurate -- nothing
// about what THAT type means changes) alongside the launch-time
// WorkflowLaunchRequest fields every subsequent iteration needs to
// relaunch. This is folding new information into an already-designated
// "not yet fully specified" placeholder column, not repurposing a
// column another task already gave a locked, narrower meaning to.
//
// # Bounding the synchronous loop (this task's own "What to do" #3)
//
// Decide's own budgetExhausted branch (decide.go) already enforces
// MaxIterations/MaxFailures/MaxRuntimeSeconds -- but only at iteration
// *boundaries*, after an iteration's WorkflowRun has already returned.
// Each individual iteration is already separately bounded by its own
// WorkflowLaunchRequest.TimeoutSeconds (WorkflowLauncher.Launch's own
// context.WithTimeout, defaulting to DefaultWorkflowLaunchTimeout). The
// one real gap between those two existing guards: a LoopRun with
// MaxIterations left unbounded (0, "no cap") but MaxRuntimeSeconds set
// could still, in principle, run an unbounded *number* of individually-
// bounded iterations before Decide's own history-elapsed check ever
// fires, since that check only runs between iterations, never inside one.
// driveIterations (below) closes this by deriving a context.Context
// deadline from budget.MaxRuntimeSeconds (when set) that wraps every
// iteration's Launch call and Decide's own ctx -- Go's context package
// takes the earlier of two composed deadlines, so this can also cut off
// an individual overrunning iteration sooner than its own
// TimeoutSeconds would. This is judged a genuine second guard, not
// redundant with Decide's own check: Decide's check is history-based and
// only ever evaluated between iterations; this is wall-clock-based and
// bounds an iteration in flight. MaxIterations, by contrast, is NOT
// duplicated here as a second in-Run check -- Decide already computes
// exactly len(history) >= budget.MaxIterations against the same history
// slice this file builds and hands it; a second copy of that exact
// comparison inside Run would be true redundancy (no new information
// Run has that Decide doesn't), not a genuine second guard. A LoopRun
// configured with both MaxIterations==0 and MaxRuntimeSeconds==0 is an
// explicit, intentional "run until COMPLETE/FAIL" configuration
// (matching budgetExhausted's own "0 means no cap" convention) -- this
// file does not invent a hidden default iteration ceiling to override
// that operator choice, since the task's own wording only asks to guard
// against an *accidental* effectively-unbounded loop, not to second-guess
// a deliberate one.
//
// # Known limitation: a context deadline expiring mid-Launch/mid-Decide
// surfaces as a plain error, not a gracefully persisted ESCALATE/FAIL
//
// The bounded-context check above only reliably fires at the TOP of the
// iteration loop, before starting a new iteration. If budget.
// MaxRuntimeSeconds expires while a Launch call or Decide's own
// reasoning-fallback LLM call is already in flight, that call returns a
// context-deadline error and Run/Resume propagate it as a plain Go error,
// not a persisted, resumable LoopRun status. Handling that race fully
// (persisting a paused status from inside an interrupted Launch/Decide
// call) is real additional complexity this task's own scope does not
// require -- the common, tested path (the check firing between
// iterations) is what "Done means" asks for.
//
// # Review fix: distinguishing a caller-context death from genuine
// MaxRuntimeSeconds exhaustion at the loop-top check
//
// A review of this task found that the loop-top check above originally read
// bare `runCtx.Err() != nil` and, on any non-nil error, unconditionally
// called escalateOnBoundedContextExceeded -- treating the event as "this
// LoopRun's own budget exhausted" no matter the real cause. runCtx.Err()
// alone cannot distinguish the budget.MaxRuntimeSeconds-derived deadline
// above genuinely elapsing from the caller-supplied ctx itself dying for an
// unrelated reason (an HTTP handler's request context on client disconnect,
// a reverse-proxy timeout, a graceful-shutdown cancellation). This was worst
// when budget.MaxRuntimeSeconds == 0 ("no cap," this file's own documented
// "run until COMPLETE/FAIL" configuration): runCtx := ctx is then a literal
// alias, not a derived child context, so the check fired purely off the
// caller's own context lifecycle -- silently imposing a hidden ceiling this
// file explicitly promises not to impose -- and, with
// budget.OnExhausted == "fail", the pre-fix code would still misdiagnose the
// event as budget exhaustion and attempt to persist a failed status on a
// LoopRun that never actually exhausted anything (e.g. task 10's future
// escalation-resolution endpoint calling Run/Resume with r.Context(), per
// the idiomatic Go HTTP pattern). Note one nuance confirmed while writing
// the regression test: escalateOnBoundedContextExceeded's own
// UpdateLoopRunStatus call is itself given the same dead ctx, so with THIS
// store's driver (modernc.org/sqlite) that write also fails closed rather
// than actually corrupting loop_runs.status -- the persisted row often ends
// up unchanged either way. That does not make the pre-fix bug harmless: (1)
// nothing about this file's own control flow guarantees that outcome, only
// an incidental property of one DB driver's ctx-checking, so it is not a
// safety net to design around; (2) the pre-fix code's returned error still
// misdiagnoses the cause (literally claims "max_runtime_seconds exceeded"
// when it did not), which matters for logs/monitoring even when the write
// itself is blocked. Fixed by checking ctx.Err() first: if the
// caller's own context already died, that is never genuine budget
// exhaustion regardless of whether MaxRuntimeSeconds is set, so this
// propagates a plain error without touching loop_runs.status at all --
// exactly the same treatment the "Known limitation" comment above already
// gives a context deadline expiring mid-Launch/mid-Decide, just applied at
// the loop-top check instead of mid-call. Only once ctx.Err() is nil does a
// non-nil runCtx.Err() unambiguously mean the derived MaxRuntimeSeconds
// timeout itself fired. Regression test:
// TestLoopEngine_DriveIterations_CallerContextDead_DoesNotEscalateOrFailBudget
// (engine_test.go) -- see that test's own doc comment for why it calls
// driveIterations directly rather than through Run/Resume.
//
// # v1 scope: REPLAN relaunches the same WorkflowDefinition as CONTINUE/RETRY
//
// See types.go's own LoopDefinition doc comment -- 21-loops.md's own "What
// this session did not decide" leaves REPLAN's/REARCHITECT's concrete
// "different approach" mechanism unresolved, and Decide's own Decision
// type (decide.go) carries no alternate-definition payload for REPLAN
// (only REARCHITECT gets a Revision, and that revision only ever touches
// the Goal's own desired_state/acceptance_criteria, never a
// WorkflowDefinition). This file does not invent a mechanism the design
// session itself left open; REPLAN today behaves identically to RETRY at
// the launch-definition level, distinguished only by the decision string
// persisted on that iteration's own loop_run_iterations row.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// ErrLoopRunAlreadyActive is returned by Run when goal_id already has an
// active LoopRun (store.LoopRunActiveStatuses) -- 21-loops.md's own "What
// this session did not decide" leaves concurrent LoopRuns against one goal
// unaddressed; this planning session's later, separate decision (this
// task's own Context section) is that exactly one active LoopRun per
// goal_id is enforced, and enforced here (Run) rather than only by a later
// HTTP launcher, so a direct caller of Run can't bypass it.
var ErrLoopRunAlreadyActive = errors.New("loop: goal already has an active loop run")

// LoopEngine drives Goal-directed iteration over ordinary WorkflowRuns --
// see this file's own package-level doc comment for the full design
// context. Its three collaborators mirror TeamRunLauncher's own dependency
// shape one level up.
type LoopEngine struct {
	store    *store.Store
	registry *agentworkflow.Registry
	launcher *service.WorkflowLauncher

	// outerResume is the push mechanism a LoopRun launched from a
	// StepKindLoop step needs — TASKS/loops/
	// 09-stepkindloop-executor-and-waiting-status.md, see outer_resume.go.
	// nil until WithOuterResumeNotifier is called; every existing Run/
	// Resume behavior is unaffected either way (notifyOuterOnTerminal is a
	// no-op with no notifier configured).
	outerResume OuterResumeNotifier
}

// NewLoopEngine constructs a LoopEngine. All three arguments are required
// -- Run/Resume return a clear error rather than panicking if any is nil.
func NewLoopEngine(st *store.Store, registry *agentworkflow.Registry, launcher *service.WorkflowLauncher) *LoopEngine {
	return &LoopEngine{store: st, registry: registry, launcher: launcher}
}

func (e *LoopEngine) configured() error {
	if e == nil || e.store == nil || e.registry == nil || e.launcher == nil {
		return fmt.Errorf("loop: engine not fully configured")
	}
	return nil
}

// loopRunPersistentConfig is the real JSON shape this file stores in
// LoopRun.ContinuationPolicyJSON -- see this file's own package-level doc
// comment for the full reasoning. ContinuationPolicy is nested unchanged
// (task 07's own type, decide.go); AgentProfileID/ProjectID/
// ParentSessionID/TimeoutSeconds/WorkflowParams are this task's own
// addition, needed so a later Resume call (no input parameters, per
// 21-loops.md's own interface sketch) can relaunch a further iteration
// without a schema change.
type loopRunPersistentConfig struct {
	ContinuationPolicy ContinuationPolicy `json:"continuation_policy"`
	AgentProfileID     string             `json:"agent_profile_id"`
	ProjectID          string             `json:"project_id,omitempty"`
	ParentSessionID    string             `json:"parent_session_id,omitempty"`
	TimeoutSeconds     int                `json:"timeout_seconds,omitempty"`
	WorkflowParams     map[string]any     `json:"workflow_params,omitempty"`
}

func encodeLoopRunPersistentConfig(cfg loopRunPersistentConfig) (string, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("loop: encode continuation policy config: %w", err)
	}
	return string(b), nil
}

func decodeLoopRunPersistentConfig(raw string) (loopRunPersistentConfig, error) {
	if raw == "" || raw == "{}" {
		return loopRunPersistentConfig{}, nil
	}
	var cfg loopRunPersistentConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return loopRunPersistentConfig{}, fmt.Errorf("loop: decode continuation policy config: %w", err)
	}
	return cfg, nil
}

// Run launches a new LoopRun against input's Goal (existing or inline) and
// drives it, iteration by iteration, synchronously within this one call --
// 21-loops.md: "for a bounded loop ... Run drives iteration-to-iteration
// synchronously within one call, exactly as BuiltinWorkflowEngine.Run
// already iterates over ready DAG nodes in one call." Returns once the
// LoopRun reaches a paused (waiting_on_gate/waiting_on_escalation) or
// terminal (completed/failed) status.
func (e *LoopEngine) Run(ctx context.Context, def LoopDefinition, input LoopInput) (LoopResult, error) {
	if err := e.configured(); err != nil {
		return LoopResult{}, err
	}
	if def.WorkflowName == "" {
		return LoopResult{}, fmt.Errorf("loop: run: workflow name is required")
	}
	if input.AgentProfileID == "" {
		return LoopResult{}, fmt.Errorf("loop: run: agent_profile_id is required")
	}

	goal, err := e.resolveGoal(ctx, input)
	if err != nil {
		return LoopResult{}, err
	}

	// One-active-LoopRun-per-goal_id -- enforced here, not only by a later
	// HTTP launcher (task 10). See ErrLoopRunAlreadyActive's own doc
	// comment.
	active, err := e.store.ListLoopRuns(ctx, store.LoopRunFilter{GoalID: goal.ID, Statuses: store.LoopRunActiveStatuses})
	if err != nil {
		return LoopResult{}, fmt.Errorf("loop: run: check active loop runs for goal %s: %w", goal.ID, err)
	}
	if len(active) > 0 {
		return LoopResult{}, fmt.Errorf("%w: goal %s already has an active loop run %s (status %s)", ErrLoopRunAlreadyActive, goal.ID, active[0].ID, active[0].Status)
	}

	cfg := loopRunPersistentConfig{
		ContinuationPolicy: input.ContinuationPolicy,
		AgentProfileID:     input.AgentProfileID,
		ProjectID:          input.ProjectID,
		ParentSessionID:    input.ParentSessionID,
		TimeoutSeconds:     input.TimeoutSeconds,
		WorkflowParams:     input.WorkflowParams,
	}
	cfgJSON, err := encodeLoopRunPersistentConfig(cfg)
	if err != nil {
		return LoopResult{}, fmt.Errorf("loop: run: %w", err)
	}

	lr := &store.LoopRun{
		GoalID:                 goal.ID,
		DefinitionName:         def.WorkflowName,
		ContinuationPolicyJSON: cfgJSON,
	}
	if err := lr.SetBudget(input.Budget); err != nil {
		return LoopResult{}, fmt.Errorf("loop: run: set budget: %w", err)
	}
	if err := e.store.CreateLoopRun(ctx, lr); err != nil {
		return LoopResult{}, fmt.Errorf("loop: run: create loop_run: %w", err)
	}

	return e.driveIterations(ctx, lr, goal, def.WorkflowName, cfg, nil)
}

// Resume re-fetches loopRunID and either resumes a still-in-flight
// iteration's paused WorkflowRun, or re-enters the iteration cycle from a
// fresh iteration -- mirroring a2a_task_manager.go's resumeWorkflowRun
// shape, per this file's own package-level doc comment. Called by task
// 10's escalation-resolution endpoint, task 11's reflex-fired resume, and
// task 12's scheduled tick alike -- all three go through this one method.
func (e *LoopEngine) Resume(ctx context.Context, loopRunID string) (LoopResult, error) {
	if err := e.configured(); err != nil {
		return LoopResult{}, err
	}
	if loopRunID == "" {
		return LoopResult{}, fmt.Errorf("loop: resume: loop_run_id is required")
	}

	lr, err := e.store.GetLoopRun(ctx, loopRunID)
	if err != nil {
		return LoopResult{}, fmt.Errorf("loop: resume %s: %w", loopRunID, err)
	}
	goal, err := e.store.GetGoal(ctx, lr.GoalID)
	if err != nil {
		return LoopResult{}, fmt.Errorf("loop: resume %s: load goal %s: %w", loopRunID, lr.GoalID, err)
	}
	cfg, err := decodeLoopRunPersistentConfig(lr.ContinuationPolicyJSON)
	if err != nil {
		return LoopResult{}, fmt.Errorf("loop: resume %s: %w", loopRunID, err)
	}
	history, err := e.store.ListLoopRunIterations(ctx, lr.ID)
	if err != nil {
		return LoopResult{}, fmt.Errorf("loop: resume %s: list iterations: %w", loopRunID, err)
	}

	switch lr.Status {
	case store.LoopRunStatusWaitingOnGate:
		// The most recent iteration's own WorkflowRun is itself blocked
		// (a gate or flex step inside it) -- resume that specific run,
		// not a fresh iteration. See resumeBlockedIteration's own doc
		// comment.
		return e.resumeBlockedIteration(ctx, lr, *goal, cfg, history)
	case store.LoopRunStatusWaitingOnEscalation:
		// A LoopRun-level WAIT or ESCALATE decision (decide.go's
		// DecisionWait/DecisionEscalate -- see evaluateDecideAndAct's own
		// doc comment for why both land in this one status) now being
		// cleared externally -- re-enter the iteration cycle from a fresh
		// iteration, using the same persisted definition/budget/policy
		// Run originally set up.
		return e.driveIterations(ctx, lr, *goal, lr.DefinitionName, cfg, history)
	default:
		return LoopResult{}, fmt.Errorf("loop: resume %s: loop run status %q is not resumable", loopRunID, lr.Status)
	}
}

// resolveGoal implements LoopInput's "GoalID or inline goal spec" union --
// 21-loops.md's Decision 2. Exactly one of GoalID/Goal must be set.
func (e *LoopEngine) resolveGoal(ctx context.Context, input LoopInput) (store.Goal, error) {
	switch {
	case input.GoalID != "" && input.Goal != nil:
		return store.Goal{}, fmt.Errorf("loop: run: exactly one of GoalID or Goal must be set, not both")
	case input.GoalID != "":
		g, err := e.store.GetGoal(ctx, input.GoalID)
		if err != nil {
			return store.Goal{}, fmt.Errorf("loop: run: load goal %s: %w", input.GoalID, err)
		}
		return *g, nil
	case input.Goal != nil:
		g := store.Goal{
			ParentGoalID: input.Goal.ParentGoalID,
			Intent:       input.Goal.Intent,
			Priority:     input.Goal.Priority,
			Scope:        input.Goal.Scope,
			Owner:        input.Goal.Owner,
			Source:       input.Goal.Source,
		}
		if err := g.SetDesiredState(input.Goal.DesiredState); err != nil {
			return store.Goal{}, fmt.Errorf("loop: run: inline goal desired_state: %w", err)
		}
		if err := g.SetConstraints(input.Goal.Constraints); err != nil {
			return store.Goal{}, fmt.Errorf("loop: run: inline goal constraints: %w", err)
		}
		if err := g.SetAcceptanceCriteria(input.Goal.AcceptanceCriteria); err != nil {
			return store.Goal{}, fmt.Errorf("loop: run: inline goal acceptance_criteria: %w", err)
		}
		if err := g.SetInvariants(input.Goal.Invariants); err != nil {
			return store.Goal{}, fmt.Errorf("loop: run: inline goal invariants: %w", err)
		}
		if err := e.store.CreateGoal(ctx, &g); err != nil {
			return store.Goal{}, fmt.Errorf("loop: run: create inline goal: %w", err)
		}
		return g, nil
	default:
		return store.Goal{}, fmt.Errorf("loop: run: exactly one of GoalID or Goal must be set")
	}
}

// driveIterations is Run/Resume's shared synchronous iteration loop:
// launch -> (if the iteration's own WorkflowRun is itself blocked, persist
// waiting_on_gate and return) -> evaluate -> decide -> persist -> act. See
// this file's own package-level doc comment ("Bounding the synchronous
// loop") for the context.WithTimeout derivation below.
func (e *LoopEngine) driveIterations(ctx context.Context, lr *store.LoopRun, goal store.Goal, defName string, cfg loopRunPersistentConfig, history []store.LoopRunIteration) (LoopResult, error) {
	budget, err := lr.Budget()
	if err != nil {
		return LoopResult{}, fmt.Errorf("loop: decode budget: %w", err)
	}
	exec := e.launcher.GetStepExecutor()

	runCtx := ctx
	if budget.MaxRuntimeSeconds > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(budget.MaxRuntimeSeconds)*time.Second)
		defer cancel()
	}

	for {
		// runCtx.Err() != nil alone cannot tell apart two different causes:
		// (1) the budget.MaxRuntimeSeconds-derived deadline above genuinely
		// elapsing -- real budget exhaustion, correct to call
		// escalateOnBoundedContextExceeded and persist loop_runs.status; or
		// (2) the caller-supplied ctx itself dying for a reason that has
		// nothing to do with this LoopRun's budget (an HTTP handler's
		// request context on client disconnect, a reverse-proxy timeout, a
		// graceful-shutdown cancellation, ...). When budget.MaxRuntimeSeconds
		// is 0 ("no cap"), runCtx is a literal alias of ctx (see above), so
		// collapsing these two would silently impose a hidden ceiling on a
		// LoopRun this file itself documents as "run until COMPLETE/FAIL."
		// Checking ctx.Err() first disambiguates: if the caller's own ctx
		// already died, that is never genuine budget exhaustion, regardless
		// of whether MaxRuntimeSeconds is set -- propagate a plain error
		// without mutating loop_runs.status (do not call
		// UpdateLoopRunStatus with the now-dead ctx; it would likely fail
		// anyway). This is the same category of event this file's own
		// "Known limitation" comment above already accepts for a context
		// deadline expiring mid-Launch/mid-Decide -- a plain propagated
		// error, not a persisted status -- just caught here at the loop-top
		// check instead of mid-call. Only once ctx.Err() is confirmed nil do
		// we know a non-nil runCtx.Err() can only be the derived
		// MaxRuntimeSeconds timeout genuinely firing.
		if ctx.Err() != nil {
			return LoopResult{}, fmt.Errorf("loop: run %s: caller context canceled: %w", lr.ID, ctx.Err())
		}
		if runCtx.Err() != nil {
			return e.escalateOnBoundedContextExceeded(ctx, lr, budget)
		}

		iterationNumber := len(history) + 1
		iterRow, launchResult, err := e.launchIteration(runCtx, lr, defName, cfg, iterationNumber)
		if err != nil {
			return LoopResult{}, err
		}
		if isPausedRunStatus(launchResult.Status) {
			if err := e.store.UpdateLoopRunStatus(ctx, lr.ID, store.LoopRunStatusWaitingOnGate, nil); err != nil {
				return LoopResult{}, fmt.Errorf("loop: update loop_run status: %w", err)
			}
			return LoopResult{LoopRunID: lr.ID, Status: store.LoopRunStatusWaitingOnGate, CurrentIteration: lr.CurrentIteration}, nil
		}

		result, terminal, updatedHistory, err := e.evaluateDecideAndAct(runCtx, exec, lr, &goal, iterRow, launchResult, cfg.ContinuationPolicy, budget, history)
		if err != nil {
			return LoopResult{}, err
		}
		history = updatedHistory
		if terminal {
			return result, nil
		}
	}
}

// escalateOnBoundedContextExceeded persists a paused/terminal LoopRun
// status once the MaxRuntimeSeconds-derived context deadline (driveIterations,
// above) trips before Decide's own history-based budget-exhausted check
// got a chance to. Defaults to escalate, per budget.OnExhausted -- the same
// on_exhausted-driven escalate/fail choice Decide's own budgetExhausted
// branch makes, applied here since there is no just-completed iteration's
// Decision to consult yet.
func (e *LoopEngine) escalateOnBoundedContextExceeded(ctx context.Context, lr *store.LoopRun, budget store.Budget) (LoopResult, error) {
	status := store.LoopRunStatusWaitingOnEscalation
	var completedAt *time.Time
	if budget.OnExhausted == store.LoopRunOnExhaustedFail {
		status = store.LoopRunStatusFailed
		now := time.Now().UTC()
		completedAt = &now
	}
	if err := e.store.UpdateLoopRunStatus(ctx, lr.ID, status, completedAt); err != nil {
		return LoopResult{}, fmt.Errorf("loop: run: max_runtime_seconds exceeded, and updating loop_run status failed: %w", err)
	}
	// notifyOuterOnTerminal itself checks status against the terminal set
	// (it's a no-op for status == LoopRunStatusWaitingOnEscalation, the
	// escalate branch) — see that function's own doc comment.
	e.notifyOuterOnTerminal(ctx, lr.ID, status)
	return LoopResult{LoopRunID: lr.ID, Status: status, CurrentIteration: lr.CurrentIteration}, nil
}

// launchIteration bumps loop_runs.current_iteration, launches this
// iteration's WorkflowRun (WorkflowLauncher.Launch), scopes it to lr
// (task 05's UpdateWorkflowRunLoopScope), and records the loop_run_iterations
// row -- with WorkflowRunID already populated at insert time, since Launch
// has already returned by this point (task 04's CreateLoopRunIteration's
// own doc comment explicitly allows this: "a caller that already knows all
// three fields up front ... may set them directly"; 21-loops.md's own
// illustrative loop_run_iterations example shows exactly this shape too --
// iteration 3, in flight, already carries `workflow_run_id: wr_ghi` with
// `decision: null`). This ordering (launch, THEN create the iteration row)
// is what lets Resume later find and resume a still-in-flight iteration's
// WorkflowRun by loop_run_iterations.workflow_run_id even when that
// iteration paused before ever being evaluated/decided --
// resumeBlockedIteration, below, depends on this.
func (e *LoopEngine) launchIteration(ctx context.Context, lr *store.LoopRun, defName string, cfg loopRunPersistentConfig, iterationNumber int) (*store.LoopRunIteration, *service.WorkflowLaunchResult, error) {
	if err := e.store.BumpLoopRunIteration(ctx, lr.ID); err != nil {
		return nil, nil, fmt.Errorf("loop: bump loop_run iteration: %w", err)
	}
	lr.CurrentIteration++

	launchResult, err := e.launcher.Launch(ctx, service.WorkflowLaunchRequest{
		WorkflowName:    defName,
		Params:          cfg.WorkflowParams,
		ProjectID:       cfg.ProjectID,
		AgentProfileID:  cfg.AgentProfileID,
		ParentSessionID: cfg.ParentSessionID,
		TimeoutSeconds:  cfg.TimeoutSeconds,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("loop: launch iteration %d: %w", iterationNumber, err)
	}
	if launchResult == nil || launchResult.RunID == "" {
		return nil, nil, fmt.Errorf("loop: launch iteration %d: launcher returned no run id", iterationNumber)
	}
	if err := e.store.UpdateWorkflowRunLoopScope(ctx, launchResult.RunID, lr.ID, iterationNumber); err != nil {
		return nil, launchResult, fmt.Errorf("loop: scope workflow run %s to loop_run %s: %w", launchResult.RunID, lr.ID, err)
	}

	iterRow := &store.LoopRunIteration{
		LoopRunID:       lr.ID,
		IterationNumber: iterationNumber,
		WorkflowRunID:   launchResult.RunID,
	}
	if err := e.store.CreateLoopRunIteration(ctx, iterRow); err != nil {
		return nil, launchResult, fmt.Errorf("loop: create loop_run_iteration %d: %w", iterationNumber, err)
	}
	return iterRow, launchResult, nil
}

// evaluateDecideAndAct rolls this iteration's WorkflowLaunchResult up into
// an Evaluation/progress classification (classifyIterationProgress,
// below), queries store.EvidenceSatisfiesGoal for the caller-supplied
// goalMet bool Decide now takes (this file's own resolution of
// TASKS/ESCALATIONS.md's 2026-08-21 entry -- see this file's own
// package-level doc comment), calls Decide, persists the result
// (CompleteLoopRunIteration + UpdateLoopRunNoProgressStreak), and acts on
// the decision. Returns (result, terminal, updatedHistory, err) --
// terminal is true when the LoopRun paused or reached a terminal status
// this call (result is meaningful only then); false means "continue the
// iteration loop" (driveIterations' own loop condition).
func (e *LoopEngine) evaluateDecideAndAct(
	ctx context.Context,
	exec agentworkflow.StepExecutor,
	lr *store.LoopRun,
	goal *store.Goal,
	iterRow *store.LoopRunIteration,
	launchResult *service.WorkflowLaunchResult,
	policy ContinuationPolicy,
	budget store.Budget,
	priorHistory []store.LoopRunIteration,
) (LoopResult, bool, []store.LoopRunIteration, error) {
	evaluation, progressState := classifyIterationProgress(launchResult)

	met, _, err := e.store.EvidenceSatisfiesGoal(ctx, goal.ID)
	if err != nil {
		return LoopResult{}, true, priorHistory, fmt.Errorf("loop: evaluate goal evidence for %s: %w", goal.ID, err)
	}
	if met {
		progressState = store.LoopRunIterationProgressGoalMet
	}

	thisIteration := *iterRow
	thisIteration.ProgressState = progressState
	thisIteration.CompletedAt = time.Now().UTC().Format(time.RFC3339)
	if err := thisIteration.SetEvaluation(evaluation); err != nil {
		return LoopResult{}, true, priorHistory, fmt.Errorf("loop: encode iteration %d evaluation: %w", iterRow.IterationNumber, err)
	}

	fullHistory := make([]store.LoopRunIteration, 0, len(priorHistory)+1)
	fullHistory = append(fullHistory, priorHistory...)
	fullHistory = append(fullHistory, thisIteration)

	decision, err := Decide(ctx, exec, *goal, met, evaluation, fullHistory, budget, policy)
	if err != nil {
		return LoopResult{}, true, priorHistory, fmt.Errorf("loop: decide iteration %d: %w", iterRow.IterationNumber, err)
	}

	// Outcome bookkeeping must survive cancellation of the loop iteration it records.
	if err := e.store.CompleteLoopRunIteration(context.WithoutCancel(ctx), iterRow.ID, launchResult.RunID, string(decision.Kind), progressState, evaluation); err != nil {
		return LoopResult{}, true, priorHistory, fmt.Errorf("loop: complete loop_run_iteration %d: %w", iterRow.IterationNumber, err)
	}
	thisIteration.Decision = string(decision.Kind)
	fullHistory[len(fullHistory)-1] = thisIteration

	streak := noProgressStreak(fullHistory)
	if err := e.store.UpdateLoopRunNoProgressStreak(ctx, lr.ID, streak); err != nil {
		return LoopResult{}, true, fullHistory, fmt.Errorf("loop: update no_progress_streak: %w", err)
	}
	lr.NoProgressStreak = streak

	switch decision.Kind {
	case DecisionContinue, DecisionRetry, DecisionReplan:
		// See this file's own package-level doc comment ("v1 scope:
		// REPLAN relaunches the same WorkflowDefinition as CONTINUE/
		// RETRY") for why REPLAN is grouped here rather than launching a
		// different definition.
		return LoopResult{}, false, fullHistory, nil

	case DecisionRearchitect:
		if err := e.applyRearchitect(ctx, goal, decision.Revision); err != nil {
			return LoopResult{}, true, fullHistory, fmt.Errorf("loop: apply rearchitect revision (iteration %d): %w", iterRow.IterationNumber, err)
		}
		return LoopResult{}, false, fullHistory, nil

	case DecisionWait, DecisionEscalate:
		// Both land in LoopRunStatusWaitingOnEscalation -- loop_runs.status
		// (migration 141) has exactly two pause buckets
		// (waiting_on_gate/waiting_on_escalation), one fewer than
		// decide.go's own WAIT/ESCALATE distinction. This task has no
		// schema migration in scope to add a third; loop_run_iterations.
		// decision (just persisted above) already carries the exact,
		// undegraded "wait" vs "escalate" distinction for any caller that
		// needs it (e.g. an operator dashboard), so this collapse loses
		// no information, only coarsens loop_runs.status itself. Real,
		// documented follow-up candidate: a future migration could add a
		// distinct waiting status for WAIT, mirroring
		// RunStatusWaitingOnFlex's own precedent for exactly this
		// "downstream consumer needs to tell two pause reasons apart"
		// problem -- not done here since it's a schema change and this
		// task has none in scope.
		if err := e.store.UpdateLoopRunStatus(ctx, lr.ID, store.LoopRunStatusWaitingOnEscalation, nil); err != nil {
			return LoopResult{}, true, fullHistory, fmt.Errorf("loop: update loop_run status: %w", err)
		}
		if decision.Kind == DecisionWait {
			// TASKS/loops/12-loop-run-tick-scheduled-trigger.md, item 5:
			// the real (and, confirmed this session, only) creator of a
			// loop_run_tick agent_schedules row -- see tick_schedule.go's
			// own package doc comment for why WAIT specifically (not
			// ESCALATE) is the trigger-surface moment this corresponds to,
			// and why it is not gated on a "durable preset" marker that
			// does not exist in the codebase yet.
			cfg, cfgErr := decodeLoopRunPersistentConfig(lr.ContinuationPolicyJSON)
			if cfgErr != nil {
				return LoopResult{}, true, fullHistory, fmt.Errorf("loop: schedule loop_run_tick: decode continuation policy for %s: %w", lr.ID, cfgErr)
			}
			if err := e.scheduleLoopRunTick(ctx, lr.ID, cfg.AgentProfileID); err != nil {
				return LoopResult{}, true, fullHistory, err
			}
		}
		return LoopResult{LoopRunID: lr.ID, Status: store.LoopRunStatusWaitingOnEscalation, CurrentIteration: lr.CurrentIteration, LastDecision: decision}, true, fullHistory, nil

	case DecisionComplete:
		now := time.Now().UTC()
		if err := e.store.UpdateLoopRunStatus(ctx, lr.ID, store.LoopRunStatusCompleted, &now); err != nil {
			return LoopResult{}, true, fullHistory, fmt.Errorf("loop: update loop_run status: %w", err)
		}
		// TASKS/loops/09-stepkindloop-executor-and-waiting-status.md: the
		// real push — if a StepKindLoop step's outer WorkflowRun is
		// waiting on this LoopRun, tell it to resume now, synchronously,
		// rather than leaving it to a lazy re-check some unrelated caller
		// might never trigger.
		e.notifyOuterOnTerminal(ctx, lr.ID, store.LoopRunStatusCompleted)
		return LoopResult{LoopRunID: lr.ID, Status: store.LoopRunStatusCompleted, CurrentIteration: lr.CurrentIteration, LastDecision: decision}, true, fullHistory, nil

	case DecisionFail:
		now := time.Now().UTC()
		if err := e.store.UpdateLoopRunStatus(ctx, lr.ID, store.LoopRunStatusFailed, &now); err != nil {
			return LoopResult{}, true, fullHistory, fmt.Errorf("loop: update loop_run status: %w", err)
		}
		e.notifyOuterOnTerminal(ctx, lr.ID, store.LoopRunStatusFailed)
		return LoopResult{LoopRunID: lr.ID, Status: store.LoopRunStatusFailed, CurrentIteration: lr.CurrentIteration, LastDecision: decision}, true, fullHistory, nil

	default:
		return LoopResult{}, true, fullHistory, fmt.Errorf("loop: iteration %d: unknown decision kind %q", iterRow.IterationNumber, decision.Kind)
	}
}

// applyRearchitect implements REARCHITECT: "a REPLAN that also revises the
// Goal's own desired_state/acceptance_criteria" (decide.go's GoalRevision
// doc comment) -- Decide itself never touches the DB (decide.go: "this
// package does not call UpdateGoal itself"); this is that call. Only
// overwrites a field when the revision actually carries a non-empty
// replacement for it, rather than blanking Goal state on a partial/
// malformed reasoning-fallback response that only revised one of the two
// fields.
func (e *LoopEngine) applyRearchitect(ctx context.Context, goal *store.Goal, revision *GoalRevision) error {
	if revision == nil {
		return fmt.Errorf("loop: rearchitect: decision carried no revision payload")
	}
	if len(revision.DesiredState) > 0 {
		if err := goal.SetDesiredState(revision.DesiredState); err != nil {
			return fmt.Errorf("rearchitect desired_state: %w", err)
		}
	}
	if len(revision.AcceptanceCriteria) > 0 {
		if err := goal.SetAcceptanceCriteria(revision.AcceptanceCriteria); err != nil {
			return fmt.Errorf("rearchitect acceptance_criteria: %w", err)
		}
	}
	if err := e.store.UpdateGoal(ctx, goal); err != nil {
		return fmt.Errorf("update goal: %w", err)
	}
	return nil
}

// resumeBlockedIteration resumes the most recent iteration's own
// WorkflowRun (still in flight -- WorkflowRunID set, Decision empty, per
// launchIteration's own doc comment) via GetEngine+concrete-engine
// .Resume, mirroring a2a_task_manager.go's resumeWorkflowRun shape exactly
// (see this file's own package-level doc comment). If that WorkflowRun is
// still paused after resuming, loop_runs.status is left as
// waiting_on_gate and this returns without further progress. Otherwise it
// falls through into the same evaluate/decide/persist/act sequence a
// freshly launched iteration goes through (evaluateDecideAndAct), then
// continues the ordinary iteration cycle for any further iteration
// (driveIterations) exactly as ordinary CONTINUE/RETRY/REPLAN would.
func (e *LoopEngine) resumeBlockedIteration(ctx context.Context, lr *store.LoopRun, goal store.Goal, cfg loopRunPersistentConfig, history []store.LoopRunIteration) (LoopResult, error) {
	if len(history) == 0 {
		return LoopResult{}, fmt.Errorf("loop: resume %s: status is %q but no iterations exist", lr.ID, lr.Status)
	}
	last := history[len(history)-1]
	if last.Decision != "" || last.WorkflowRunID == "" {
		return LoopResult{}, fmt.Errorf("loop: resume %s: status is %q but the most recent iteration %d is not a genuinely in-flight workflow run", lr.ID, lr.Status, last.IterationNumber)
	}

	runRow, err := e.store.GetWorkflowRun(ctx, last.WorkflowRunID)
	if err != nil {
		return LoopResult{}, fmt.Errorf("loop: resume %s: load workflow run %s: %w", lr.ID, last.WorkflowRunID, err)
	}
	wf, ok := e.registry.Get(runRow.DefinitionName)
	if !ok {
		return LoopResult{}, fmt.Errorf("loop: resume %s: workflow definition %q not found in registry", lr.ID, runRow.DefinitionName)
	}
	genericEngine, ok := e.launcher.GetEngine(agentworkflow.EngineBuiltin)
	if !ok {
		return LoopResult{}, fmt.Errorf("loop: resume %s: built-in workflow engine not available", lr.ID)
	}
	builtinEngine, ok := genericEngine.(*service.BuiltinWorkflowEngine)
	if !ok {
		return LoopResult{}, fmt.Errorf("loop: resume %s: workflow engine registered for %q does not support resume", lr.ID, agentworkflow.EngineBuiltin)
	}

	result, err := builtinEngine.Resume(ctx, last.WorkflowRunID, wf, e.launcher.GetStepExecutor())
	if err != nil {
		return LoopResult{}, fmt.Errorf("loop: resume %s: resume workflow run %s: %w", lr.ID, last.WorkflowRunID, err)
	}
	launchResult := &service.WorkflowLaunchResult{
		RunID:        result.RunID,
		WorkflowName: wf.Name,
		Status:       result.Status,
		StepResults:  result.StepResults,
		Error:        result.Error,
	}

	if isPausedRunStatus(launchResult.Status) {
		return LoopResult{LoopRunID: lr.ID, Status: lr.Status, CurrentIteration: lr.CurrentIteration}, nil
	}

	budget, err := lr.Budget()
	if err != nil {
		return LoopResult{}, fmt.Errorf("loop: resume %s: decode budget: %w", lr.ID, err)
	}
	exec := e.launcher.GetStepExecutor()
	priorHistory := history[:len(history)-1]
	iterRow := last

	result2, terminal, updatedHistory, err := e.evaluateDecideAndAct(ctx, exec, lr, &goal, &iterRow, launchResult, cfg.ContinuationPolicy, budget, priorHistory)
	if err != nil {
		return LoopResult{}, err
	}
	if terminal {
		return result2, nil
	}
	return e.driveIterations(ctx, lr, goal, lr.DefinitionName, cfg, updatedHistory)
}

// isPausedRunStatus reports whether status means "this WorkflowRun made
// all the progress it currently can, but is not done" -- the three
// non-terminal RunStatus values a built-in-engine run can return
// (agentworkflow/types.go). RunStatusWaitingOnLoop (task 09, StepKindLoop's
// own execution behavior) is included for the nested case: one loop
// iteration's own WorkflowRun can itself contain a StepKindLoop step, and
// a paused iteration must not be evaluated/decided (evaluateDecideAndAct)
// until it's genuinely resolved, exactly like the existing gate/flex
// cases.
func isPausedRunStatus(status agentworkflow.RunStatus) bool {
	switch status {
	case agentworkflow.RunStatusWaiting, agentworkflow.RunStatusWaitingOnFlex, agentworkflow.RunStatusWaitingOnLoop:
		return true
	default:
		return false
	}
}

// classifyIterationProgress rolls one iteration's WorkflowLaunchResult up
// into an Evaluation and a loop_run_iterations.progress_state classification
// (progress|no_progress|regression|blocked -- goal_met is never returned
// here; evaluateDecideAndAct overlays it separately once it knows the
// caller-supplied goalMet bool) -- 21-loops.md: "reuses Verify wholesale...
// not a second evaluator subsystem." Reads WorkflowLaunchResult.StepResults
// directly, each already carrying its own decoded VerifyResult (the
// built-in engine populates this from workflow_run_steps.verify_json
// itself, internal/service/workflow_engine.go's stepResultFromRow) -- no
// separate DB query needed.
//
// This is a deliberately minimal v1 heuristic, matching 21-loops.md's own
// "No-progress / convergence detection... minimal v1... deeper heuristics
// ... deferred" framing for the whole area: a step that errored or whose
// Verify failed counts as a "regression" data point; a completed run with
// zero such data points (including a run with no Verify configured at all
// -- the common Ralph one-llm-step-no-verify case) is optimistically
// PROGRESS, since there is no declared failure signal to the contrary.
func classifyIterationProgress(res *service.WorkflowLaunchResult) (store.Evaluation, string) {
	if res == nil {
		return store.Evaluation{RemainingDelta: "no workflow launch result"}, store.LoopRunIterationProgressBlocked
	}

	switch res.Status {
	case agentworkflow.RunStatusWaiting, agentworkflow.RunStatusWaitingOnFlex, agentworkflow.RunStatusWaitingOnLoop:
		// Defensive, not reached in practice for the same reason
		// isPausedRunStatus's own doc comment gives: driveIterations/
		// resumeBlockedIteration both already filter every paused status
		// out via isPausedRunStatus before classifyIterationProgress is
		// ever called (task 09 added RunStatusWaitingOnLoop to that same
		// filter for the nested-loop-in-a-loop-iteration case). Kept here
		// for three-way symmetry with that filter, not because this
		// branch is exercised.
		return store.Evaluation{RemainingDelta: fmt.Sprintf("iteration workflow run is itself waiting (%s)", res.Status)}, store.LoopRunIterationProgressBlocked
	case agentworkflow.RunStatusCanceled:
		return store.Evaluation{RemainingDelta: "iteration workflow run was canceled"}, store.LoopRunIterationProgressBlocked
	}

	var passed, failed int
	var regressions []string
	for stepID, sr := range res.StepResults {
		switch {
		case sr.IsError:
			failed++
			regressions = append(regressions, stepID+": step errored")
		case sr.VerifyResult == nil:
			// No verify modifier on this step -- no data point either way.
		case sr.VerifyResult.Passed:
			passed++
		default:
			failed++
			regressions = append(regressions, fmt.Sprintf("%s: %s", stepID, sr.VerifyResult.Reason))
		}
	}
	sort.Strings(regressions) // map iteration order is random; keep this deterministic.

	confidence := 1.0
	if total := passed + failed; total > 0 {
		confidence = float64(passed) / float64(total)
	}
	eval := store.Evaluation{Confidence: confidence, Regressions: regressions}

	switch {
	case res.Status == agentworkflow.RunStatusFailed:
		eval.RemainingDelta = "iteration workflow run failed: " + res.Error
		return eval, store.LoopRunIterationProgressRegression
	case failed > 0 && passed == 0:
		eval.RemainingDelta = fmt.Sprintf("%d of %d verified steps failed, none passed", failed, failed+passed)
		return eval, store.LoopRunIterationProgressRegression
	case failed > 0:
		eval.RemainingDelta = fmt.Sprintf("%d of %d verified steps failed", failed, failed+passed)
		return eval, store.LoopRunIterationProgressNoProgress
	default:
		eval.RemainingDelta = "iteration completed with no unresolved verify failures"
		return eval, store.LoopRunIterationProgressProgress
	}
}
