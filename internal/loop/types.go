package loop

// TASKS/loops/08-loop-engine-core.md -- the launch-time/return-value shapes
// LoopEngine.Run/.Resume (engine.go) use, per that task's own "What to do"
// #1. Kept in a separate file from engine.go/decide.go for the same reason
// agentworkflow splits its own types.go from dag.go/registry.go -- pure data
// shapes, no behavior, easy to scan independent of the engine logic that
// consumes them.

import (
	"github.com/hollis-labs/nanite/internal/store"
)

// LoopDefinition names the WorkflowDefinition every iteration of a Loop
// launches under -- docs/engineering/architecture/21-loops.md's
// loop_runs.definition_name field ("the initial iteration's
// WorkflowDefinition name (or a preset name, e.g. 'ralph')"). WorkflowName
// must already be registered on the *agentworkflow.Registry LoopEngine was
// constructed with -- Run/Resume never compile or register a definition
// themselves (unlike TeamRunLauncher's CompileTeam, which builds one fresh
// per launch; Decision 1's "iteration execution is fully reused" means a
// Loop's iteration is just an ordinary, already-defined WorkflowDefinition,
// nothing loop-specific about its shape).
//
// v1 scope note (see engine.go's own doc comment for the full reasoning):
// every iteration of one LoopRun launches under this same WorkflowName,
// including a REPLAN iteration. 21-loops.md's own "What this session did
// not decide" leaves REPLAN's/REARCHITECT's concrete "different approach"
// mechanism unresolved; Decision.Revision (decide.go) only ever carries a
// Goal revision (REARCHITECT), never an alternate WorkflowDefinition. This
// task does not invent one -- a future task that does can extend
// LoopDefinition/the continuation-policy Decision type together when a real
// mechanism exists.
type LoopDefinition struct {
	WorkflowName string
}

// LoopGoalSpec is an inline goal specification LoopInput may carry instead
// of an existing GoalID -- 21-loops.md's Decision 2: "LoopLaunchRequest
// accepts either an existing goal_id or an inline goal spec, which the
// launcher upserts into goals first." Run upserts this into a real
// store.Goal row (store.CreateGoal) before creating the loop_runs row.
// Mirrors store.Goal's own authoring-time fields -- see that type's own doc
// comments in internal/store/goals.go for what each one means.
type LoopGoalSpec struct {
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

// LoopInput is Run's launch-time parameters -- the design doc's own
// "GoalID or inline goal spec, Budget overrides" plus everything else
// WorkflowLaunchRequest needs for every iteration a Run/Resume cycle
// launches (see WorkflowLaunchRequest's own doc comments,
// internal/service/workflow_launch.go, for what each forwarded field
// means).
type LoopInput struct {
	// IdempotencyKey, when set, makes repeated launches recover the same
	// LoopRun rather than create a second child after a caller crash.
	IdempotencyKey string

	// GoalID references an existing goals row. Exactly one of GoalID/Goal
	// must be set -- Run rejects both empty and both set.
	GoalID string
	// Goal is an inline goal spec Run upserts before creating the
	// loop_runs row. See LoopGoalSpec's own doc comment.
	Goal *LoopGoalSpec

	// Budget overrides loop_runs.budget_json for this LoopRun. A zero
	// value means "no cap on any dimension" (store.Budget's own
	// documented zero-value convention -- see budgetExhausted's doc
	// comment in decide.go) -- an explicit, intentional shape for a
	// caller that wants an unbounded loop, not a missing-input error.
	Budget store.Budget

	// ContinuationPolicy configures Decide's reasoning-fallback branch
	// (Provider/Model/AgentID/SessionID/Tools) -- see that type's own doc
	// comment in decide.go.
	ContinuationPolicy ContinuationPolicy

	// WorkflowParams is forwarded to every iteration's
	// WorkflowLaunchRequest.Params.
	WorkflowParams map[string]any

	// AgentProfileID / ProjectID / ParentSessionID / TimeoutSeconds
	// forward straight through to every iteration's
	// WorkflowLaunchRequest. AgentProfileID is required -- Launch
	// hard-errors without it (durable_agent_instances.profile_id is a
	// required FK).
	//
	// These four fields (plus WorkflowParams and ContinuationPolicy
	// above) are also what a later Resume call needs to relaunch a
	// further iteration, but Resume's own signature
	// (Resume(ctx, loopRunID) -- no other parameters, per
	// 21-loops.md's own bare interface sketch) has no way to receive them
	// again. See engine.go's own doc comment (loopRunPersistentConfig)
	// for how Run persists them so Resume can recover them without a
	// schema change.
	AgentProfileID  string
	ProjectID       string
	ParentSessionID string
	TimeoutSeconds  int
}

// LoopResult is Run/Resume's return value -- the LoopRun's status once this
// call has made all the progress it currently can, mirroring
// agentworkflow.WorkflowResult's own "terminal-for-this-call" framing (a
// LoopResult is not always a LoopRun's true final state -- a paused status
// is a real, valid return, exactly like WorkflowResult.Status can be
// RunStatusWaiting).
type LoopResult struct {
	LoopRunID string
	// Status is one of the store.LoopRunStatus* constants
	// (internal/store/loop_runs.go).
	Status           string
	CurrentIteration int
	// LastDecision is the most recently completed iteration's
	// continuation-policy verdict. Zero-value (empty Kind) if this call
	// returned before any iteration finished evaluation -- e.g. the
	// one-active-LoopRun-per-goal_id check rejected the call before a
	// first iteration ever launched, or the current iteration's own
	// WorkflowRun is itself still mid-flight (LoopRunStatusWaitingOnGate)
	// and was never evaluated/decided this call.
	LastDecision Decision
}
