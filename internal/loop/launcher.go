package loop

// TASKS/loops/10-loop-launcher-and-api.md -- the Manual/API launch trigger
// surface docs/engineering/architecture/21-loops.md names: "LoopLauncher.
// Launch, mirroring WorkflowLauncher.Launch," plus the LoopRun-level analog
// of A2A's ProvideTaskInput/resumeWorkflowRun gate-resolution pattern for
// the "Human resolution of waiting_on_escalation" trigger surface
// (internal/service/a2a_task_manager.go:694-762, confirmed this session per
// this task's own Context section).
//
// LoopLauncher is a thin struct wrapping *LoopEngine (task 08) plus
// *store.Store -- the same "struct wrapping its collaborators" shape
// engine.go's own doc comment already cites for LoopEngine itself
// (TeamRunLauncher/WorkflowLauncher precedent), one level up. It does not
// duplicate LoopEngine.Run's own logic:
//
//   - Launch's GoalID/InlineGoal union and the one-active-LoopRun-per-
//     goal_id check are both left entirely to LoopEngine.Run's own
//     resolveGoal/active-run check (engine.go) -- this task's own Context
//     section is explicit that Run "may already enforce this... prefer
//     delegating, avoid duplicating the check in two places." The same
//     delegation call is made for the GoalID/InlineGoal union: Run's
//     resolveGoal already rejects "both set"/"neither set" with a clear
//     error, and already calls store.CreateGoal for the inline case --
//     Launch forwards LoopLaunchRequest.InlineGoal straight into
//     LoopInput.Goal rather than calling CreateGoal a second time itself.
//   - ResolveEscalation's Replan override reuses LoopEngine's own
//     unexported applyRearchitect helper (engine.go, task 08's
//     DecisionRearchitect branch) for the Goal-revision half of a replan,
//     rather than re-implementing that logic -- legal since this file is
//     in the same package.
//
// Cancel and ResolveEscalation's ForceComplete/ForceCancel branches call
// LoopEngine's own unexported notifyOuterOnTerminal (engine.go, task 09)
// after persisting a terminal status directly -- an operator-forced
// terminal transition is exactly as real as one Decide reaches on its own,
// and skipping this notify would leave a StepKindLoop step's outer
// WorkflowRun stuck in waiting_on_loop forever with no automatic push.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// GoalSpec aliases LoopGoalSpec (types.go) -- this task's own "What to do"
// #1 names the field `InlineGoal *GoalSpec`; GoalSpec is exactly "whatever
// subset of Goal's fields a caller can supply inline" that LoopGoalSpec
// already is (see that type's own doc comment), so this is a plain alias,
// not a second, parallel struct definition.
type GoalSpec = LoopGoalSpec

// LoopLaunchRequest is LoopLauncher.Launch's request shape -- this task's
// own "What to do" #1 names GoalID/InlineGoal/DefinitionName/
// BudgetOverrides explicitly (the fields this planning session's design
// decisions bear on: Goal CRUD/inline-goal-spec upsert, budget override);
// extended here with the remaining LoopInput fields Run() itself hard-
// requires or forwards per-iteration (types.go's own LoopInput is, per this
// task's own briefing, "the real API you wrap" -- Launch cannot construct a
// valid LoopInput without also accepting these).
type LoopLaunchRequest struct {
	// GoalID references an existing goals row. Exactly one of GoalID/
	// InlineGoal must be set -- see this file's own package doc comment for
	// why that union check is left to LoopEngine.Run's own resolveGoal
	// rather than duplicated here.
	GoalID *string
	// InlineGoal is an inline goal spec the launcher upserts into goals
	// first, per 21-loops.md's Decision 2 -- forwarded straight through as
	// LoopInput.Goal; LoopEngine.Run's own resolveGoal is what actually
	// calls store.CreateGoal for it.
	InlineGoal *GoalSpec

	// DefinitionName is the WorkflowDefinition every iteration launches
	// under -- becomes LoopDefinition.WorkflowName.
	DefinitionName string

	// BudgetOverrides overrides LoopInput.Budget for this LoopRun. nil
	// means the zero-value Budget{} (types.go's own documented "no cap on
	// any dimension, an explicit intentional shape" convention for an
	// unbounded loop) -- not a missing-input error.
	BudgetOverrides *Budget

	// ContinuationPolicy / WorkflowParams / AgentProfileID / ProjectID /
	// ParentSessionID / TimeoutSeconds forward straight through to
	// LoopInput's identically named fields -- see that type's own doc
	// comments (types.go) for what each one means. AgentProfileID is
	// required -- LoopEngine.Run hard-errors without it.
	ContinuationPolicy ContinuationPolicy
	WorkflowParams     map[string]any
	AgentProfileID     string
	ProjectID          string
	ParentSessionID    string
	TimeoutSeconds     int
}

// EscalationOverride is ResolveEscalation's optional override payload --
// 21-loops.md's "Human resolution of waiting_on_escalation": "an operator
// resolve call... calling LoopEngine.Resume with an optional decision
// override (force COMPLETE, force CANCEL, or supply a replan)." nil, or a
// non-nil override with every field left at its zero value (the shape a
// bare `{}` JSON request body decodes to), both mean "resume normally" --
// re-entering Decide with fresh state. ForceComplete/ForceCancel/Replan are
// checked in that fixed order when more than one happens to be set on the
// same request -- no dedicated ambiguous-request error, matching this
// design's general "first-match-wins, document the order" discipline for a
// multi-flag combining decision (reflexes.Resolve's own deterministic-first
// ordering, 10-reflex-action-taxonomy.md, is the closest existing
// precedent this design already cites for continuation policy generally).
type EscalationOverride struct {
	ForceComplete bool            `json:"force_complete,omitempty"`
	ForceCancel   bool            `json:"force_cancel,omitempty"`
	Replan        *ReplanOverride `json:"replan,omitempty"`
}

// ReplanOverride is EscalationOverride's replan payload -- "a revised
// definition_name and/or goal fields" (this task's own "What to do" #1).
// DesiredState/AcceptanceCriteria mirror GoalRevision's own REARCHITECT
// payload shape (decide.go) exactly -- applyReplan (below) applies them via
// LoopEngine's own unexported applyRearchitect helper, the identical logic
// task 08's own DecisionRearchitect branch already uses, rather than
// duplicating it. DefinitionName, when set, is persisted via
// store.UpdateLoopRunDefinitionName (this task's own narrow store addition)
// so every iteration Resume subsequently launches uses it.
type ReplanOverride struct {
	DefinitionName     string   `json:"definition_name,omitempty"`
	DesiredState       []string `json:"desired_state,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
}

// ErrLoopRunNotWaitingOnEscalation is returned by ResolveEscalation when
// loopRunID's current status is not waiting_on_escalation -- this
// endpoint's own documented precondition (this task's own "What to do" #1:
// "given a loop_run_id in waiting_on_escalation status"), checked once up
// front, uniformly, for every override mode including the no-override
// (resume normally) case -- LoopEngine.Resume's own broader "which paused
// statuses are resumable at all" check (it also accepts
// waiting_on_gate) stays authoritative for direct Resume callers (task
// 11/12's reflex-fired/scheduled-tick resumes); this is the narrower,
// escalation-resolution-specific precondition this one endpoint documents.
var ErrLoopRunNotWaitingOnEscalation = errors.New("loop: loop run is not waiting_on_escalation")

// LoopLauncher wraps *LoopEngine and *store.Store -- see this file's own
// package doc comment for the full design context.
type LoopLauncher struct {
	engine *LoopEngine
	store  *store.Store
}

// NewLoopLauncher constructs a LoopLauncher. Both arguments are required --
// every method below returns a clear error rather than panicking if either
// is nil.
func NewLoopLauncher(engine *LoopEngine, st *store.Store) *LoopLauncher {
	return &LoopLauncher{engine: engine, store: st}
}

func (l *LoopLauncher) configured() error {
	if l == nil || l.engine == nil || l.store == nil {
		return fmt.Errorf("loop: launcher not fully configured")
	}
	return nil
}

// Launch resolves req's DefinitionName (either a preset name or an ordinary
// WorkflowDefinition name -- see resolvePresetDefaults, below) and Budget
// override into a LoopInput and delegates straight to LoopEngine.Run -- see
// this file's own package doc comment for why the GoalID/InlineGoal union
// check and the one-active-LoopRun-per-goal_id check are both left to Run
// rather than duplicated here. Returns loop.ErrLoopRunAlreadyActive
// (unwrapped via errors.Is) when goal_id already has an active LoopRun, or
// ErrPresetNotImplemented (unwrapped via errors.Is) when req.DefinitionName
// names a registered-but-stubbed preset (presets.go).
func (l *LoopLauncher) Launch(ctx context.Context, req LoopLaunchRequest) (LoopResult, error) {
	if err := l.configured(); err != nil {
		return LoopResult{}, err
	}
	if req.DefinitionName == "" {
		return LoopResult{}, fmt.Errorf("loop: launch: definition_name is required")
	}

	defName, budget, policy, err := l.resolvePresetDefaults(req)
	if err != nil {
		return LoopResult{}, err
	}

	input := LoopInput{
		GoalID:             stringFromPtr(req.GoalID),
		Goal:               req.InlineGoal,
		Budget:             budget,
		ContinuationPolicy: policy,
		WorkflowParams:     req.WorkflowParams,
		AgentProfileID:     req.AgentProfileID,
		ProjectID:          req.ProjectID,
		ParentSessionID:    req.ParentSessionID,
		TimeoutSeconds:     req.TimeoutSeconds,
	}

	return l.engine.Run(ctx, LoopDefinition{WorkflowName: defName}, input)
}

// resolvePresetDefaults implements TASKS/loops/13-loop-presets.md's own
// "What to do" #4: "DefinitionName field should accept either a real
// WorkflowDefinition name (as today) or a preset name, resolved via
// GetPreset first before falling back to the registry lookup." Preset names
// are checked FIRST and win any collision -- that task's own recommendation,
// which this task's own collision check (below) confirmed is safe against
// the current codebase.
//
// Collision check performed (TASKS/loops/13-loop-presets.md's own explicit
// instruction, "confirm no existing registered WorkflowDefinition is
// actually named ralph/test-fix/.../self-improve"): grepped every *.yaml/
// *.yml under the repo for a `name:` field matching any of the seven preset
// names, and grepped every *.go file for a string literal matching one of
// them used as a WorkflowName/workflow definition Name. The only hits were
// (1) internal/store/loop_runs.go's own doc comment, which already uses
// "ralph" purely as an illustrative example of a preset name, not a real
// registered definition, and (2) the unrelated word "durable" appearing
// throughout internal/store/teams.go and internal/service/team_run_launcher.go
// as a TeamSlotDefinition.Resolution enum VALUE ("durable"/"fresh") -- a
// completely different namespace (a Team slot's resolution mode, not a
// WorkflowDefinition or LoopPreset name) with no actual collision, even
// though the bare word is shared. The repo's one example WorkflowDefinition
// (examples/workflow-definitions/worker-reviewer-gate.yaml) is named
// "worker-reviewer-gate," not any of the seven. No real collision found --
// preset-name-first precedence is safe as implemented.
//
// When req.DefinitionName names a registered preset, this also applies that
// preset's own Budget/ContinuationPolicy as defaults -- but ONLY when the
// caller left the corresponding LoopLaunchRequest field unset (nil
// BudgetOverrides / zero-value ContinuationPolicy) -- an explicit
// caller-supplied value always wins over a preset's own default, mirroring
// BudgetOverrides' own existing "explicit beats default" doc comment.
func (l *LoopLauncher) resolvePresetDefaults(req LoopLaunchRequest) (defName string, budget Budget, policy ContinuationPolicy, err error) {
	budget = Budget{}
	if req.BudgetOverrides != nil {
		budget = *req.BudgetOverrides
	}
	policy = req.ContinuationPolicy

	preset, ok := GetPreset(req.DefinitionName)
	if !ok {
		// Not a preset name -- treat req.DefinitionName as an ordinary,
		// already-registered WorkflowDefinition name, exactly as before this
		// task. LoopEngine.Run/the underlying WorkflowLauncher.Launch
		// surface "unknown workflow" clearly if it isn't actually registered.
		return req.DefinitionName, budget, policy, nil
	}
	if !preset.implemented() {
		return "", Budget{}, ContinuationPolicy{}, fmt.Errorf("%w: %q", ErrPresetNotImplemented, req.DefinitionName)
	}

	// Idempotent registration: the same preset name is reused, unchanged,
	// across every launch (unlike TeamRun's per-launch-unique compiled
	// definition names) -- register the template into the shared registry
	// only the first time this preset is actually launched in this process.
	// See presets.go's own package doc comment ("Wiring into
	// LoopLaunchRequest") for the known process-restart limitation this
	// leaves in place (out of this task's own stated scope, which touches
	// only presets.go and this file).
	if _, found := l.engine.registry.Get(preset.Name); !found {
		if regErr := l.engine.registry.Register(preset.DefinitionTemplate); regErr != nil {
			return "", Budget{}, ContinuationPolicy{}, fmt.Errorf("loop: launch: register preset %q workflow definition: %w", req.DefinitionName, regErr)
		}
	}

	if req.BudgetOverrides == nil {
		budget = preset.Budget
	}
	if isZeroContinuationPolicy(policy) {
		policy = preset.ContinuationPolicy
	}
	return preset.Name, budget, policy, nil
}

// isZeroContinuationPolicy reports whether p is the ContinuationPolicy zero
// value. Not a plain p == ContinuationPolicy{} comparison -- ContinuationPolicy
// carries a Tools []string field, and Go does not allow == on a struct with
// a slice field.
func isZeroContinuationPolicy(p ContinuationPolicy) bool {
	return p.Provider == "" && p.Model == "" && p.AgentID == "" && p.SessionID == "" && len(p.Tools) == 0
}

// Cancel sets loop_runs.status = 'canceled' directly -- an operator-
// initiated hard stop, per this task's own "What to do" #1 ("no Decide call
// needed"). Notifies any outer WorkflowRun waiting on this LoopRun via a
// StepKindLoop step (task 09) exactly as LoopEngine's own terminal
// transitions do (engine.go's evaluateDecideAndAct) -- see this file's own
// package doc comment for why that notify is not optional here.
func (l *LoopLauncher) Cancel(ctx context.Context, loopRunID string) error {
	if err := l.configured(); err != nil {
		return err
	}
	if loopRunID == "" {
		return fmt.Errorf("loop: cancel: loop_run_id is required")
	}
	now := time.Now().UTC()
	if err := l.store.UpdateLoopRunStatus(ctx, loopRunID, store.LoopRunStatusCanceled, &now); err != nil {
		return fmt.Errorf("loop: cancel %s: %w", loopRunID, err)
	}
	l.engine.notifyOuterOnTerminal(ctx, loopRunID, store.LoopRunStatusCanceled)
	return nil
}

// ResolveEscalation is the "Human resolution of waiting_on_escalation"
// trigger surface (21-loops.md's Trigger surface section) -- the LoopRun-
// level analog of A2A's ProvideTaskInput/resumeWorkflowRun pattern: look up
// the waiting LoopRun, resolve it (normally, or via override),
// resume/persist as appropriate. Returns ErrLoopRunNotWaitingOnEscalation
// (unwrapped via errors.Is) if loopRunID's current status is not
// waiting_on_escalation.
func (l *LoopLauncher) ResolveEscalation(ctx context.Context, loopRunID string, override *EscalationOverride) (LoopResult, error) {
	if err := l.configured(); err != nil {
		return LoopResult{}, err
	}
	if loopRunID == "" {
		return LoopResult{}, fmt.Errorf("loop: resolve escalation: loop_run_id is required")
	}

	lr, err := l.store.GetLoopRun(ctx, loopRunID)
	if err != nil {
		return LoopResult{}, fmt.Errorf("loop: resolve escalation %s: %w", loopRunID, err)
	}
	if lr.Status != store.LoopRunStatusWaitingOnEscalation {
		return LoopResult{}, fmt.Errorf("%w: %s is %q", ErrLoopRunNotWaitingOnEscalation, loopRunID, lr.Status)
	}

	switch {
	case override != nil && override.ForceComplete:
		now := time.Now().UTC()
		if err := l.store.UpdateLoopRunStatus(ctx, loopRunID, store.LoopRunStatusCompleted, &now); err != nil {
			return LoopResult{}, fmt.Errorf("loop: resolve escalation %s: force complete: %w", loopRunID, err)
		}
		l.engine.notifyOuterOnTerminal(ctx, loopRunID, store.LoopRunStatusCompleted)
		return LoopResult{LoopRunID: loopRunID, Status: store.LoopRunStatusCompleted, CurrentIteration: lr.CurrentIteration}, nil

	case override != nil && override.ForceCancel:
		if err := l.Cancel(ctx, loopRunID); err != nil {
			return LoopResult{}, err
		}
		return LoopResult{LoopRunID: loopRunID, Status: store.LoopRunStatusCanceled, CurrentIteration: lr.CurrentIteration}, nil

	case override != nil && override.Replan != nil:
		if err := l.applyReplan(ctx, lr, override.Replan); err != nil {
			return LoopResult{}, fmt.Errorf("loop: resolve escalation %s: apply replan: %w", loopRunID, err)
		}
		return l.engine.Resume(ctx, loopRunID)

	default:
		// override is nil, or non-nil with every field left at its zero
		// value (a bare `{}` request body) -- resume normally, re-entering
		// Decide with fresh state.
		return l.engine.Resume(ctx, loopRunID)
	}
}

// applyReplan implements EscalationOverride.Replan -- see ReplanOverride's
// own doc comment.
func (l *LoopLauncher) applyReplan(ctx context.Context, lr *store.LoopRun, replan *ReplanOverride) error {
	if replan.DefinitionName != "" {
		if err := l.store.UpdateLoopRunDefinitionName(ctx, lr.ID, replan.DefinitionName); err != nil {
			return fmt.Errorf("update definition_name: %w", err)
		}
	}
	if len(replan.DesiredState) > 0 || len(replan.AcceptanceCriteria) > 0 {
		goal, err := l.store.GetGoal(ctx, lr.GoalID)
		if err != nil {
			return fmt.Errorf("load goal %s: %w", lr.GoalID, err)
		}
		revision := &GoalRevision{DesiredState: replan.DesiredState, AcceptanceCriteria: replan.AcceptanceCriteria}
		if err := l.engine.applyRearchitect(ctx, goal, revision); err != nil {
			return fmt.Errorf("apply goal revision: %w", err)
		}
	}
	return nil
}

func stringFromPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
