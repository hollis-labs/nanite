package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/oklog/ulid/v2"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

// WorkflowRunStore is the narrow persistence surface the built-in engine
// needs. *store.Store satisfies it structurally; tests supply a fake.
type WorkflowRunStore interface {
	CreateWorkflowRun(row *store.WorkflowRunRow) error
	SetWorkflowRunStatus(id, status, errMsg string, completedAt time.Time) error
	GetWorkflowRun(id string) (*store.WorkflowRunRow, error)
	UpsertWorkflowRunStep(row *store.WorkflowRunStepRow) error
	ListWorkflowRunSteps(runID string) ([]*store.WorkflowRunStepRow, error)
}

// BuiltinWorkflowEngine is the DAG-executing agentworkflow.WorkflowEngine
// implementation (design doc, "Built-in engine"): topological leveling
// with goroutine fan-out per level — apps/hadron's internal/pipeline.Runner
// mechanism (TopoSort + per-level sync.WaitGroup fan-out), applied here at
// per-step granularity instead of Hadron's per-blueprint granularity — and
// every step transition persisted immediately for crash durability.
type BuiltinWorkflowEngine struct {
	store WorkflowRunStore

	// teamMembers / flexState are the Teams-specific dependencies a flex
	// step (StepKindFlex, TASKS/teams/06-stepkindflex-executor.md) needs
	// to resolve its active team_run_members and evaluate its exit
	// trigger. Both nil until WithFlexSupport is called — every existing
	// llm/tool/gate-only workflow is unaffected either way; only a
	// WorkflowDefinition that actually contains a flex step needs them
	// wired. See workflow_engine_flex.go.
	teamMembers TeamMembershipStore
	flexState   FlexStepStateCollector

	// loopRuns / loopLauncher are the StepKindLoop (TASKS/loops/
	// 09-stepkindloop-executor-and-waiting-status.md) dependencies a loop
	// step needs: loopLauncher.LaunchLoop actually starts the contained
	// LoopRun (a bridge to internal/loop.LoopEngine.Run that avoids an
	// internal/service <-> internal/loop import cycle — see
	// workflow_engine_loop.go's own package-level doc comment for the full
	// reasoning), and loopRuns reads a loop_runs row's live status back to
	// decide whether a waiting_on_loop step can now resolve. Both nil
	// until WithLoopSupport is called — every existing llm/tool/gate/flex
	// workflow is unaffected either way.
	loopRuns     LoopRunStatusStore
	loopLauncher LoopStepLauncher
}

// NewBuiltinWorkflowEngine constructs the built-in WorkflowEngine.
func NewBuiltinWorkflowEngine(runStore WorkflowRunStore) *BuiltinWorkflowEngine {
	return &BuiltinWorkflowEngine{store: runStore}
}

// WithFlexSupport attaches the team_run_members lookup and reflex
// exit-trigger state collector a flex step needs to ever resolve out of
// its waiting state — kept as a post-construction setter (not a
// NewBuiltinWorkflowEngine parameter) so the many pre-existing call sites
// that never touch Teams don't all need to thread through a new
// dependency. cmd/nanite/main.go's real production wiring always calls
// this; an engine constructed without it still runs every llm/tool/gate
// workflow exactly as before, and a flex step reached without it
// resolves to a clear, terminal config error rather than hanging forever
// (see evaluateFlexExit). Returns e for convenient chaining at the call
// site.
func (e *BuiltinWorkflowEngine) WithFlexSupport(teamMembers TeamMembershipStore, stateCollector FlexStepStateCollector) *BuiltinWorkflowEngine {
	e.teamMembers = teamMembers
	e.flexState = stateCollector
	return e
}

// WithLoopSupport attaches the loop_runs read surface and the
// LoopEngine.Run bridge (LoopStepLauncher) a StepKindLoop step needs to
// ever launch or resolve (TASKS/loops/09-stepkindloop-executor-and-
// waiting-status.md). Kept as a post-construction setter — not a
// NewBuiltinWorkflowEngine parameter — mirroring WithFlexSupport's own
// reasoning exactly: the many pre-existing call sites that never touch a
// StepKindLoop step don't all need to thread through a new dependency. An
// engine constructed without it still runs every llm/tool/gate/flex
// workflow exactly as before; a loop step reached without it resolves to a
// clear, terminal config error rather than hanging forever (see
// startLoopStep/recheckLoopStep in workflow_engine_loop.go). Returns e for
// convenient chaining at the call site.
func (e *BuiltinWorkflowEngine) WithLoopSupport(loopRuns LoopRunStatusStore, launcher LoopStepLauncher) *BuiltinWorkflowEngine {
	e.loopRuns = loopRuns
	e.loopLauncher = launcher
	return e
}

var _ agentworkflow.WorkflowEngine = (*BuiltinWorkflowEngine)(nil)

// Name identifies this engine to Chat's dispatch (design doc: "New
// workflow_run MCP self-tool ... route a task to a named, defined
// workflow").
func (e *BuiltinWorkflowEngine) Name() string { return "builtin" }

// Run validates wf, persists a new run (and every step pre-registered as
// pending), and executes as far as the DAG currently allows — which may be
// the whole workflow, or may stop early with RunStatusWaiting if execution
// reaches an unresolved gate.
func (e *BuiltinWorkflowEngine) Run(ctx context.Context, wf agentworkflow.WorkflowDefinition, input agentworkflow.WorkflowInput, exec agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	if err := agentworkflow.Validate(wf); err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	if e.store == nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow builtin engine: no store configured")
	}

	inputJSON, err := json.Marshal(input.Params)
	if err != nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow: marshal workflow input: %w", err)
	}
	runID := ulid.Make().String()
	if err := e.store.CreateWorkflowRun(&store.WorkflowRunRow{
		ID: runID, DefinitionName: wf.Name, Status: "running", InputJSON: string(inputJSON),
	}); err != nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow: create run: %w", err)
	}
	for _, s := range wf.Steps {
		if err := e.store.UpsertWorkflowRunStep(&store.WorkflowRunStepRow{
			WorkflowRunID: runID, StepID: s.ID, Kind: string(s.Kind), Status: "pending",
		}); err != nil {
			return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow: pre-register step %q: %w", s.ID, err)
		}
	}

	return e.execute(ctx, runID, wf, input, exec, map[string]agentworkflow.StepResult{}, map[string]bool{})
}

// Resume continues a previously persisted run from its last completed step
// (design doc: "a crash/restart can resume a run from its last completed
// step"). Not part of the WorkflowEngine interface — external engines own
// their own resumption semantics; this is specific to the built-in
// engine's own persisted state. The caller supplies the same
// WorkflowDefinition the run was originally started with: only run/step
// state is persisted here, not the definition itself.
//
// A step found in "running" status is treated as interrupted mid-flight
// (there's no transactional link between calling StepExecutor and
// persisting its result) and is re-run rather than resumed in place —
// at-least-once, not exactly-once, semantics for the step that was
// actually executing at crash time.
func (e *BuiltinWorkflowEngine) Resume(ctx context.Context, runID string, wf agentworkflow.WorkflowDefinition, exec agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	if err := agentworkflow.Validate(wf); err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	if e.store == nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow builtin engine: no store configured")
	}

	runRow, err := e.store.GetWorkflowRun(runID)
	if err != nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow: resume %s: %w", runID, err)
	}
	var params map[string]any
	if runRow.InputJSON != "" {
		if err := json.Unmarshal([]byte(runRow.InputJSON), &params); err != nil {
			return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow: resume %s: unmarshal input: %w", runID, err)
		}
	}
	input := agentworkflow.WorkflowInput{Params: params}

	persisted, err := e.store.ListWorkflowRunSteps(runID)
	if err != nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow: resume %s: list steps: %w", runID, err)
	}

	results := make(map[string]agentworkflow.StepResult, len(persisted))
	waiting := make(map[string]bool)
	for _, row := range persisted {
		switch row.Status {
		case "pending", "running":
			continue
		case "waiting_on_gate", "waiting_on_flex", "waiting_on_loop":
			// All three bucket into the same in-memory `waiting` map here —
			// execute()'s per-level loop is what actually treats them
			// differently: a gate stays untouched until an external
			// caller resolves it (store.ResolveGate) before ever calling
			// Resume again, while a flex step gets a real, active
			// re-check of its exit trigger on every Resume-driven pass
			// through this loop (recheckFlexStep) — "something external
			// (the exit trigger firing) is what calls Resume" per the
			// design doc, but for a flex step the resolution condition
			// itself is computed here rather than supplied externally,
			// since (unlike a gate's human-authored approval) it's
			// entirely derivable from already-persisted session state.
			//
			// A loop step (TASKS/loops/09-stepkindloop-executor-and-
			// waiting-status.md) gets the same "real, active re-check on
			// every Resume-driven pass" treatment (recheckLoopStep) — but
			// unlike flex, this is NOT the lazy piggyback-on-some-
			// unrelated-caller's-Resume pattern the design doc's own
			// "Real trigger-fire mechanism" note explicitly rules out for
			// loop: the reason THIS run ever gets a fresh Resume call
			// while a loop step is waiting is the real push
			// (LoopEngine's OuterResumeNotifier calling this same
			// .Resume the moment the contained LoopRun goes terminal).
			// This per-level recheck is the correctness/idempotency layer
			// on top of that push (re-deriving the resolution from
			// loop_runs.status rather than trusting that Resume was only
			// ever called because of a genuine terminal transition) — it
			// still resolves to "still legitimately waiting" (no store
			// write, stays waiting_on_loop) if some OTHER, unrelated
			// caller resumes this same run while the loop is genuinely
			// still in flight.
			waiting[row.StepID] = true
			continue
		}
		sr, decodeErr := stepResultFromRow(row)
		if decodeErr != nil {
			return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow: resume %s: decode step %q: %w", runID, row.StepID, decodeErr)
		}
		results[row.StepID] = sr
	}

	return e.execute(ctx, runID, wf, input, exec, results, waiting)
}

// execute runs wf's steps level by level, mutating results/waiting in
// place as steps resolve. results and waiting may arrive pre-populated
// (Resume) or empty (a fresh Run).
func (e *BuiltinWorkflowEngine) execute(
	ctx context.Context,
	runID string,
	wf agentworkflow.WorkflowDefinition,
	input agentworkflow.WorkflowInput,
	exec agentworkflow.StepExecutor,
	results map[string]agentworkflow.StepResult,
	waiting map[string]bool,
) (agentworkflow.WorkflowResult, error) {
	levels, err := agentworkflow.Levels(wf.Steps)
	if err != nil {
		return agentworkflow.WorkflowResult{}, err // already validated at Run/Resume entry; defensive only
	}
	byID := make(map[string]agentworkflow.StepDefinition, len(wf.Steps))
	for _, s := range wf.Steps {
		byID[s.ID] = s
	}

	type levelOutcome struct {
		stepID  string
		blocked bool
		skipped bool
		outcome stepRunOutcome
		// resolvedFromWait marks an outcome produced by recheckFlexStep or
		// recheckLoopStep (task 09 renamed this from flexResolved when
		// loop steps became the second kind to reuse this same "already
		// waiting, re-check on this Resume pass" path) rather than a
		// fresh runStep dispatch — the post-processing loop below uses it
		// to also drop the step out of the `waiting` map (a flex/loop
		// step's own first entry sets waiting[stepID]=true; a llm/tool/gate
		// step's first-and-only outcome never needs this, since it was
		// never in `waiting` to begin with).
		resolvedFromWait bool
	}

	for _, level := range levels {
		outcomes := make([]levelOutcome, len(level))
		var wg sync.WaitGroup

		// Steps within a level share no dependency edges (that's what
		// makes them a level), so it's safe to read the shared results/
		// waiting maps here without a mutex — nothing writes to them
		// until every goroutine in this level has returned (wg.Wait()
		// below), and reads-only concurrent map access is safe in Go.
		for i, step := range level {
			if _, done := results[step.ID]; done {
				continue
			}
			if waiting[step.ID] {
				// A step already in `waiting` (loaded by Resume, or set
				// by this same execute() call for a sibling level
				// visited earlier — flex/loop steps have no dependents
				// that would reach a later level before this one
				// resolves, so in practice this is always the
				// Resume-reload case) stays blocked for every kind
				// except flex and loop: both have a resolution condition
				// fully re-derivable from already-persisted state (a
				// flex step's exit trigger; a loop step's contained
				// LoopRun's own persisted status), so every Resume-driven
				// pass through this loop gets a real chance to close it,
				// dispatched through the same goroutine+outcomes[i]
				// machinery as a fresh step so it can't race the
				// concurrent reads other same-level goroutines are
				// doing against results/waiting.
				switch step.Kind {
				case agentworkflow.StepKindFlex:
					i, step := i, step
					wg.Add(1)
					go func() {
						defer wg.Done()
						resolved, sr, ferr := e.recheckFlexStep(ctx, runID, step)
						if ferr != nil {
							outcomes[i] = levelOutcome{stepID: step.ID, outcome: stepRunOutcome{Err: ferr}}
							return
						}
						if !resolved {
							return // still legitimately waiting; outcomes[i] stays zero-value (stepID == "") and is ignored below.
						}
						outcomes[i] = levelOutcome{
							stepID: step.ID, resolvedFromWait: true,
							outcome: stepRunOutcome{Kind: outcomeCompleted, Result: sr},
						}
					}()
				case agentworkflow.StepKindLoop:
					// See this task's Context (TASKS/loops/
					// 09-stepkindloop-executor-and-waiting-status.md) on
					// why this is NOT the same "lazy re-check piggybacked
					// on an unrelated caller's Resume" pattern flex uses:
					// the reason this run gets a Resume call at all while
					// a loop step is waiting is the real push
					// (OuterResumeNotifier.NotifyLoopRunTerminal, called
					// directly by LoopEngine the moment the contained
					// LoopRun goes terminal — internal/loop/engine.go).
					// This recheck is the idempotent correctness layer on
					// top of that push, not the trigger itself.
					i, step := i, step
					wg.Add(1)
					go func() {
						defer wg.Done()
						resolved, sr, lerr := e.recheckLoopStep(ctx, runID, step)
						if lerr != nil {
							outcomes[i] = levelOutcome{stepID: step.ID, outcome: stepRunOutcome{Err: lerr}}
							return
						}
						if !resolved {
							return // still legitimately waiting; outcomes[i] stays zero-value (stepID == "") and is ignored below.
						}
						outcomes[i] = levelOutcome{
							stepID: step.ID, resolvedFromWait: true,
							outcome: stepRunOutcome{Kind: outcomeCompleted, Result: sr},
						}
					}()
				}
				continue
			}

			switch dependencyState(step.DependsOn, results, waiting) {
			case depFailed:
				outcomes[i] = levelOutcome{stepID: step.ID, skipped: true}
				continue
			case depBlocked:
				outcomes[i] = levelOutcome{stepID: step.ID, blocked: true}
				continue
			}

			i, step := i, step
			wg.Add(1)
			go func() {
				defer wg.Done()
				outcomes[i] = levelOutcome{stepID: step.ID, outcome: e.runStep(ctx, runID, step, results, input, exec)}
			}()
		}
		wg.Wait()

		for _, oc := range outcomes {
			if oc.stepID == "" {
				continue
			}
			switch {
			case oc.blocked:
				// Left exactly as it was (pending) — nothing to persist.
			case oc.skipped:
				sr := agentworkflow.StepResult{
					StepID: oc.stepID, Kind: byID[oc.stepID].Kind, IsError: true,
					Output: "skipped: an upstream dependency failed or was skipped",
				}
				results[oc.stepID] = sr
				if err := e.store.UpsertWorkflowRunStep(&store.WorkflowRunStepRow{
					WorkflowRunID: runID, StepID: oc.stepID, Kind: string(byID[oc.stepID].Kind),
					Status: "skipped", Output: sr.Output, IsError: true, CompletedAt: time.Now().UTC(),
				}); err != nil {
					return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow: persist skip for %q: %w", oc.stepID, err)
				}
			case oc.outcome.Err != nil:
				return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow: step %q: %w", oc.stepID, oc.outcome.Err)
			case oc.outcome.Kind == outcomeWaiting:
				waiting[oc.stepID] = true
			default:
				results[oc.stepID] = oc.outcome.Result
				if oc.resolvedFromWait {
					delete(waiting, oc.stepID)
				}
			}
		}
	}

	return e.finishRun(runID, byID, results, waiting)
}

func (e *BuiltinWorkflowEngine) finishRun(
	runID string,
	byID map[string]agentworkflow.StepDefinition,
	results map[string]agentworkflow.StepResult,
	waiting map[string]bool,
) (agentworkflow.WorkflowResult, error) {
	status := agentworkflow.RunStatusCompleted
	errMsg := ""
	for _, sr := range results {
		if sr.IsError {
			status = agentworkflow.RunStatusFailed
			if errMsg == "" {
				errMsg = fmt.Sprintf("step %q failed: %s", sr.StepID, sr.Output)
			}
		}
	}
	if status == agentworkflow.RunStatusCompleted && len(waiting) > 0 {
		status = waitingRunStatus(byID, waiting)
	}

	completedAt := time.Time{}
	dbStatus := string(status)
	if status == agentworkflow.RunStatusCompleted || status == agentworkflow.RunStatusFailed {
		completedAt = time.Now().UTC()
	}
	if err := e.store.SetWorkflowRunStatus(runID, dbStatus, errMsg, completedAt); err != nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow: finalize run %s: %w", runID, err)
	}

	stepResults := make(map[string]agentworkflow.StepResult, len(results))
	for id, sr := range results {
		stepResults[id] = sr
	}
	return agentworkflow.WorkflowResult{RunID: runID, Status: status, StepResults: stepResults, Error: errMsg}, nil
}

// waitingRunStatus picks the run-level RunStatus for a run with at least
// one step still waiting — gate > flex > loop precedence
// (docs/engineering/architecture/21-loops.md Decision 1, quoted directly in
// TASKS/loops/09-stepkindloop-executor-and-waiting-status.md's Context: "If
// a run is blocked by more than one kind at once, gate still takes
// priority..., then flex, then loop — the least human-attention-demanding
// kind loses the tie-break"). A gate step's presence takes priority over
// both flex and loop (RunStatusWaiting == "waiting_on_gate") since a gate
// genuinely needs a human; RunStatusWaitingOnFlex is reported when every
// waiting step is flex-or-loop but at least one is flex; RunStatusWaitingOnLoop
// is reported only when every waiting step is a loop step. See
// agentworkflow.RunStatusWaitingOnFlex/RunStatusWaitingOnLoop's own doc
// comments and migrations 133/144 for why this has to be a real, distinct,
// DB-persisted status rather than an in-memory-only distinction.
//
// Renamed from the original two-way flexOrGateWaitingStatus by task 09,
// which extended (not replaced) this precedence function per its own "What
// to do" instruction — gate and flex keep their exact prior behavior; loop
// is the new, lowest-priority tier.
func waitingRunStatus(byID map[string]agentworkflow.StepDefinition, waiting map[string]bool) agentworkflow.RunStatus {
	sawFlex := false
	sawLoop := false
	for stepID := range waiting {
		switch byID[stepID].Kind {
		case agentworkflow.StepKindFlex:
			sawFlex = true
		case agentworkflow.StepKindLoop:
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
	// Unreachable in practice — finishRun only calls this when
	// len(waiting) > 0, so at least one of the two flags above is always
	// set by the loop — but a defensive fallback that mirrors gate's own
	// "least surprising default" is safer than a nonsensical zero-value
	// RunStatus.
	return agentworkflow.RunStatusWaiting
}

// stepOutcomeKind distinguishes a step that reached a terminal result from
// one that's now paused waiting on a gate.
type stepOutcomeKind int

const (
	outcomeCompleted stepOutcomeKind = iota
	outcomeWaiting
)

// stepRunOutcome is runStep's return value. Err is set only for infra-level
// failures (e.g. the store rejecting a write) that should abort the whole
// run — distinct from Result.IsError, which is the step's own semantic
// failure and lets sibling/downstream steps proceed or skip normally.
type stepRunOutcome struct {
	Kind   stepOutcomeKind
	Result agentworkflow.StepResult
	Err    error
}

// runStep executes one step: llm/tool steps resolve their templated config
// and dispatch to the matching StepExecutor method (never for gate, flex,
// or loop steps — all three are engine-native pausing, StepExecutor has no
// gate/flex/loop method); gate steps mark themselves waiting_on_gate and
// stop there. Every transition (running, then terminal) is persisted
// immediately.
//
// flex steps (StepKindFlex, docs/engineering/architecture/15-teams.md
// Decision 2) reuse that identical pause shape on first entry — marking
// waiting_on_flex and returning outcomeWaiting, same as a gate — per the
// design doc's own framing: "the pause/resume plumbing itself is not new."
// The real difference from a gate is entirely on the resolution side, not
// here: see execute()'s per-level loop and recheckFlexStep/evaluateFlexExit
// in workflow_engine_flex.go for what makes a flex step's wait actually
// end.
//
// loop steps (StepKindLoop, docs/engineering/architecture/21-loops.md
// Decision 1) are the third reuse of the same pause shape, but with real
// work on first entry (unlike gate/flex, which just mark themselves
// waiting): startLoopStep (workflow_engine_loop.go) resolves the step's
// config and actually launches the contained LoopRun before deciding
// whether to resolve immediately or park as waiting_on_loop.
func (e *BuiltinWorkflowEngine) runStep(
	ctx context.Context,
	runID string,
	step agentworkflow.StepDefinition,
	results map[string]agentworkflow.StepResult,
	input agentworkflow.WorkflowInput,
	exec agentworkflow.StepExecutor,
) stepRunOutcome {
	if err := e.store.UpsertWorkflowRunStep(&store.WorkflowRunStepRow{
		WorkflowRunID: runID, StepID: step.ID, Kind: string(step.Kind), Status: "running", StartedAt: time.Now().UTC(),
	}); err != nil {
		return stepRunOutcome{Err: fmt.Errorf("mark running: %w", err)}
	}

	if step.Kind == agentworkflow.StepKindGate {
		if err := e.store.UpsertWorkflowRunStep(&store.WorkflowRunStepRow{
			WorkflowRunID: runID, StepID: step.ID, Kind: string(step.Kind), Status: "waiting_on_gate",
		}); err != nil {
			return stepRunOutcome{Err: fmt.Errorf("mark waiting_on_gate: %w", err)}
		}
		return stepRunOutcome{Kind: outcomeWaiting, Result: agentworkflow.StepResult{StepID: step.ID, Kind: step.Kind}}
	}

	if step.Kind == agentworkflow.StepKindFlex {
		// First entry only — a step already waiting_on_flex never reaches
		// runStep again (Resume buckets it into `waiting` and execute()
		// dispatches recheckFlexStep instead; see that function's own
		// idempotency note). This branch itself never spawns or resolves
		// any team_run_members row, so re-entering it (the engine's own
		// at-least-once "running" step is re-run, not resumed in place"
		// semantics — a crash between the "running" write above and this
		// write) is trivially idempotent: it only ever writes the same
		// waiting_on_flex status again.
		if err := e.store.UpsertWorkflowRunStep(&store.WorkflowRunStepRow{
			WorkflowRunID: runID, StepID: step.ID, Kind: string(step.Kind), Status: "waiting_on_flex",
		}); err != nil {
			return stepRunOutcome{Err: fmt.Errorf("mark waiting_on_flex: %w", err)}
		}
		return stepRunOutcome{Kind: outcomeWaiting, Result: agentworkflow.StepResult{StepID: step.ID, Kind: step.Kind}}
	}

	if step.Kind == agentworkflow.StepKindLoop {
		// First entry only — a step already waiting_on_loop never reaches
		// runStep again (Resume buckets it into `waiting` and execute()
		// dispatches recheckLoopStep instead; see startLoopStep's own doc
		// comment in workflow_engine_loop.go for the full behavior:
		// resolve config, launch the contained LoopRun, and either resolve
		// immediately (the LoopRun already reached a terminal state
		// synchronously within this one launch call) or mark
		// waiting_on_loop and return.
		return e.startLoopStep(ctx, runID, step, input)
	}

	sr, verify := e.executeLLMOrTool(ctx, runID, step, results, input, exec)

	status := "completed"
	if sr.IsError {
		status = "failed"
	}
	toolCallsJSON, _ := json.Marshal(sr.ToolCalls)
	verifyJSON := ""
	if verify != nil {
		if b, err := json.Marshal(verify); err == nil {
			verifyJSON = string(b)
		}
	}
	errMsg := ""
	if sr.IsError {
		errMsg = sr.Output
	}
	if err := e.store.UpsertWorkflowRunStep(&store.WorkflowRunStepRow{
		WorkflowRunID: runID, StepID: step.ID, Kind: string(step.Kind), Status: status,
		Output: sr.Output, IsError: sr.IsError, ToolCallsJSON: string(toolCallsJSON), VerifyJSON: verifyJSON,
		Error: errMsg, CompletedAt: time.Now().UTC(),
	}); err != nil {
		return stepRunOutcome{Err: fmt.Errorf("persist result: %w", err)}
	}

	return stepRunOutcome{Kind: outcomeCompleted, Result: sr}
}

// executeLLMOrTool resolves the step's templated config, dispatches to the
// matching StepExecutor method, and — if the step has a verify modifier —
// checks the literal result before returning. Verify runs even when the
// underlying execution itself errored: mode=engine's "no_error" check
// exists precisely to catch that (design doc: don't trust what a step says
// it did; that includes not skipping the check just because the step
// already looks like it failed).
func (e *BuiltinWorkflowEngine) executeLLMOrTool(
	ctx context.Context,
	runID string,
	step agentworkflow.StepDefinition,
	results map[string]agentworkflow.StepResult,
	input agentworkflow.WorkflowInput,
	exec agentworkflow.StepExecutor,
) (agentworkflow.StepResult, *agentworkflow.VerifyResult) {
	cfg, err := resolveStepConfig(step.Config, dependencyResults(step.DependsOn, results), input)
	if err != nil {
		return agentworkflow.StepResult{StepID: step.ID, Kind: step.Kind, IsError: true, Output: fmt.Sprintf("config resolution: %v", err)}, nil
	}

	var sr agentworkflow.StepResult
	switch step.Kind {
	case agentworkflow.StepKindLLM:
		req, buildErr := buildLLMStepRequest(runID, step, cfg)
		if buildErr != nil {
			return agentworkflow.StepResult{StepID: step.ID, Kind: step.Kind, IsError: true, Output: buildErr.Error()}, nil
		}
		res, execErr := exec.ExecuteLLMStep(ctx, req)
		if execErr != nil {
			sr = agentworkflow.StepResult{StepID: step.ID, Kind: step.Kind, IsError: true, Output: execErr.Error()}
		} else {
			sr = agentworkflow.StepResult{StepID: step.ID, Kind: step.Kind, Output: res.Text, ToolCalls: res.ToolCalls}
		}

	case agentworkflow.StepKindTool:
		req, buildErr := buildToolStepRequest(runID, step, cfg)
		if buildErr != nil {
			return agentworkflow.StepResult{StepID: step.ID, Kind: step.Kind, IsError: true, Output: buildErr.Error()}, nil
		}
		res, execErr := exec.ExecuteToolStep(ctx, req)
		if execErr != nil {
			sr = agentworkflow.StepResult{StepID: step.ID, Kind: step.Kind, IsError: true, Output: execErr.Error()}
		} else {
			sr = agentworkflow.StepResult{StepID: step.ID, Kind: step.Kind, Output: res.Output, IsError: res.IsError}
		}

	}
	// No case for StepKindGate, StepKindFlex, or StepKindLoop here,
	// deliberately — all three are engine-native pausing intercepted
	// earlier in runStep, before executeLLMOrTool is ever called
	// (TASKS/teams/06-stepkindflex-executor.md: task 03's placeholder case
	// here, which used to fail clearly rather than silently no-op now that
	// migration 130 makes kind='flex' a DB-valid row, is superseded by
	// that earlier interception — a flex or loop step now genuinely never
	// reaches this switch on the happy path, so there is nothing for this
	// switch to guard against here anymore; StepDefinition.Kind isn't
	// attacker-controlled input, and agentworkflow.Validate already
	// rejects any kind outside {llm,tool,gate,flex,loop} before a run is
	// ever created).

	if step.Verify == nil {
		return sr, nil
	}

	vres, verr := exec.Verify(ctx, agentworkflow.VerifyRequest{
		WorkflowRunID: runID,
		StepID:        step.ID + ":verify",
		SubjectStepID: step.ID,
		VerifySpec:    *step.Verify,
		Subject: agentworkflow.VerifySubject{
			StepKind: step.Kind, Output: sr.Output, IsError: sr.IsError, ToolCalls: sr.ToolCalls,
		},
	})
	if verr != nil {
		sr.IsError = true
		sr.Output += "\n\nverify error: " + verr.Error()
		return sr, nil
	}
	sr.VerifyResult = &vres
	if !vres.Passed {
		sr.IsError = true
		sr.Output += "\n\nverify failed: " + vres.Reason
	}
	return sr, &vres
}

// dependencyResults returns the subset of results the caller is entitled
// to see for template resolution: exactly its declared DependsOn, not
// every step that happens to have completed so far in the run. Without
// this restriction a step could reference a step that completed earlier
// due to topology but was never listed as a dependency — undeclared
// coupling that contradicts resolveTemplateRef's own "must be listed in
// depends_on" error message.
func dependencyResults(dependsOn []string, results map[string]agentworkflow.StepResult) map[string]agentworkflow.StepResult {
	scoped := make(map[string]agentworkflow.StepResult, len(dependsOn))
	for _, id := range dependsOn {
		if r, ok := results[id]; ok {
			scoped[id] = r
		}
	}
	return scoped
}

// depState classifies a step's readiness against its DependsOn.
type depState int

const (
	depReady depState = iota
	depBlocked
	depFailed
)

// dependencyState reports whether step's dependencies are all resolved
// successfully (depReady), still waiting on a gate or not yet resolved
// (depBlocked — level ordering means "not yet resolved" shouldn't happen
// in practice, but it's the safe default rather than depFailed), or
// include a failure/skip (depFailed, which propagates as a skip).
func dependencyState(deps []string, results map[string]agentworkflow.StepResult, waiting map[string]bool) depState {
	for _, id := range deps {
		if waiting[id] {
			return depBlocked
		}
		r, ok := results[id]
		if !ok {
			return depBlocked
		}
		if r.IsError {
			return depFailed
		}
	}
	return depReady
}

// stepResultFromRow reconstructs a StepResult from persisted state — the
// typed inter-step data flow a later step's template resolution reads
// from directly, and what Resume rebuilds its in-memory results map from.
func stepResultFromRow(row *store.WorkflowRunStepRow) (agentworkflow.StepResult, error) {
	var toolCalls []agentworkflow.ToolCallRecord
	if row.ToolCallsJSON != "" && row.ToolCallsJSON != "[]" {
		if err := json.Unmarshal([]byte(row.ToolCallsJSON), &toolCalls); err != nil {
			return agentworkflow.StepResult{}, fmt.Errorf("unmarshal tool_calls_json: %w", err)
		}
	}
	var verifyResult *agentworkflow.VerifyResult
	if row.VerifyJSON != "" {
		var vr agentworkflow.VerifyResult
		if err := json.Unmarshal([]byte(row.VerifyJSON), &vr); err != nil {
			return agentworkflow.StepResult{}, fmt.Errorf("unmarshal verify_json: %w", err)
		}
		verifyResult = &vr
	}
	return agentworkflow.StepResult{
		StepID:       row.StepID,
		Kind:         agentworkflow.StepKind(row.Kind),
		Output:       row.Output,
		IsError:      row.IsError,
		ToolCalls:    toolCalls,
		VerifyResult: verifyResult,
	}, nil
}

// --- step config: templating + typed request builders ---

var templateRefPattern = regexp.MustCompile(`\{\{\s*([^}]+?)\s*\}\}`)

// resolveStepConfig returns a deep copy of cfg with every {{ ... }} string
// reference resolved against completed prior-step results and the run's
// input params — the typed inter-step data flow the design doc calls for
// (StepDefinition.Config's doc comment: "the built-in engine ... should
// design that shape against real workflow definitions").
//
// Unlike apps/hadron's resolveTemplate (which silently drops an
// unresolvable reference), an unresolvable reference here is a hard error:
// a step author who mistypes a step id, or references one outside
// depends_on, gets a clear failure rather than a step silently running
// against an empty string.
func resolveStepConfig(cfg map[string]any, results map[string]agentworkflow.StepResult, input agentworkflow.WorkflowInput) (map[string]any, error) {
	if cfg == nil {
		return nil, nil
	}
	resolved, err := resolveTemplateValue(cfg, results, input)
	if err != nil {
		return nil, err
	}
	m, _ := resolved.(map[string]any)
	return m, nil
}

func resolveTemplateValue(v any, results map[string]agentworkflow.StepResult, input agentworkflow.WorkflowInput) (any, error) {
	switch val := v.(type) {
	case string:
		return resolveTemplateString(val, results, input)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, sub := range val {
			rv, err := resolveTemplateValue(sub, results, input)
			if err != nil {
				return nil, err
			}
			out[k] = rv
		}
		return out, nil
	case []any:
		out := make([]any, len(val))
		for i, sub := range val {
			rv, err := resolveTemplateValue(sub, results, input)
			if err != nil {
				return nil, err
			}
			out[i] = rv
		}
		return out, nil
	default:
		return v, nil
	}
}

func resolveTemplateString(s string, results map[string]agentworkflow.StepResult, input agentworkflow.WorkflowInput) (string, error) {
	var resolveErr error
	out := templateRefPattern.ReplaceAllStringFunc(s, func(match string) string {
		if resolveErr != nil {
			return match
		}
		sub := templateRefPattern.FindStringSubmatch(match)
		val, err := resolveTemplateRef(sub[1], results, input)
		if err != nil {
			resolveErr = err
			return match
		}
		return val
	})
	if resolveErr != nil {
		return "", resolveErr
	}
	return out, nil
}

func resolveTemplateRef(ref string, results map[string]agentworkflow.StepResult, input agentworkflow.WorkflowInput) (string, error) {
	switch {
	case strings.HasPrefix(ref, "steps."):
		parts := strings.SplitN(strings.TrimPrefix(ref, "steps."), ".", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("agentworkflow: malformed step reference %q", ref)
		}
		stepID, field := parts[0], parts[1]
		sr, ok := results[stepID]
		if !ok {
			return "", fmt.Errorf("agentworkflow: reference to step %q, which has not completed (must be listed in depends_on)", stepID)
		}
		switch field {
		case "output":
			return sr.Output, nil
		case "is_error":
			return fmt.Sprintf("%t", sr.IsError), nil
		default:
			return "", fmt.Errorf("agentworkflow: unknown step field %q in reference %q", field, ref)
		}
	case strings.HasPrefix(ref, "input."):
		key := strings.TrimPrefix(ref, "input.")
		val, ok := input.Params[key]
		if !ok {
			// CW-20260815-0022: workflow_run's pre-launch check
			// (agentworkflow.RequiredInputs, internal/mcp/self_tools_workflow_run.go)
			// catches this for the common case, but that check is
			// best-effort and skipped when WorkflowRegistry isn't wired —
			// this is the backstop, so name what WAS supplied to make the
			// gap between "what params has" and "what this step needs"
			// concrete rather than a bare unknown-key message.
			return "", fmt.Errorf("agentworkflow: step references {{input.%s}}, but params has no %q key (params supplied: %v) — pass it via workflow_run(..., params: {%q: \"...\"})",
				key, key, paramKeys(input.Params), key)
		}
		if s, ok := val.(string); ok {
			return s, nil
		}
		return fmt.Sprintf("%v", val), nil
	default:
		return "", fmt.Errorf("agentworkflow: unrecognized template reference %q (expected steps.<id>.<field> or input.<key>)", ref)
	}
}

// paramKeys returns m's keys, sorted, for use in an error message — a
// deterministic "here's what you actually supplied" listing.
func paramKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func configString(cfg map[string]any, key string) string {
	if v, ok := cfg[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func configStringSlice(cfg map[string]any, key string) []string {
	switch vv := cfg[key].(type) {
	case []string:
		return vv
	case []any:
		out := make([]string, 0, len(vv))
		for _, item := range vv {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func configInt(cfg map[string]any, key string) int {
	switch vv := cfg[key].(type) {
	case int:
		return vv
	case int64:
		return int(vv)
	case float64:
		return int(vv)
	default:
		return 0
	}
}

func configMap(cfg map[string]any, key string) map[string]any {
	if m, ok := cfg[key].(map[string]any); ok {
		return m
	}
	return nil
}

func buildLLMStepRequest(runID string, step agentworkflow.StepDefinition, cfg map[string]any) (agentworkflow.LLMStepRequest, error) {
	provider := configString(cfg, "provider")
	if provider == "" {
		return agentworkflow.LLMStepRequest{}, fmt.Errorf("agentworkflow: llm step %q config requires \"provider\"", step.ID)
	}
	prompt := configString(cfg, "prompt")
	if prompt == "" {
		return agentworkflow.LLMStepRequest{}, fmt.Errorf("agentworkflow: llm step %q config requires \"prompt\"", step.ID)
	}
	return agentworkflow.LLMStepRequest{
		WorkflowRunID:     runID,
		StepID:            step.ID,
		SessionID:         configString(cfg, "session_id"),
		AgentID:           configString(cfg, "agent_id"),
		Provider:          provider,
		Model:             configString(cfg, "model"),
		SystemPrompt:      configString(cfg, "system_prompt"),
		Messages:          []llmtypes.ChatMessage{{Role: "user", Content: prompt}},
		Tools:             configStringSlice(cfg, "tools"),
		MaxToolIterations: configInt(cfg, "max_tool_iterations"),
	}, nil
}

func buildToolStepRequest(runID string, step agentworkflow.StepDefinition, cfg map[string]any) (agentworkflow.ToolStepRequest, error) {
	tool := configString(cfg, "tool")
	if tool == "" {
		return agentworkflow.ToolStepRequest{}, fmt.Errorf("agentworkflow: tool step %q config requires \"tool\"", step.ID)
	}
	return agentworkflow.ToolStepRequest{
		WorkflowRunID: runID,
		StepID:        step.ID,
		AgentID:       configString(cfg, "agent_id"),
		Tool:          tool,
		Args:          configMap(cfg, "args"),
	}, nil
}
