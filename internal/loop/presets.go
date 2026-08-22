package loop

// TASKS/loops/13-loop-presets.md -- the Presets phase (Phase 4, the final
// task of the `TASKS/loops` batch): docs/engineering/architecture/21-loops.md's
// closing claim, realized directly: "Ralph falls out of this for free: it's
// max_iterations runs of a trivial one-llm-step (optionally + tool + verify)
// WorkflowDefinition, no REPLAN, no REARCHITECT... Ralph support can
// therefore be a preset over a more general loop abstraction rather than a
// special subsystem." And from that doc's own ledger: "Presets (ralph,
// test-fix, review-fix, ...) -- New, but pure configuration -- named
// (budget, continuation_policy, iteration-definition template) bundles, no
// per-preset engine."
//
// # Go-coded constants, not a DB table -- this planning session's own call
//
// 21-loops.md's own "What this session did not decide" leaves this open:
// "Whether presets ... are DB rows (like reflex seeds) or Go-coded constants
// for v1." This task's own README settles it for v1: "Go-coded constants ...
// not a DB table -- a map[string]LoopPreset registry, matching this repo's
// own 'start narrow' bias." Presets are pure config with no independent
// lifecycle (no per-preset status, no per-preset CRUD, no per-preset audit
// trail) -- nothing a DB row would earn its keep for, unlike Goal (Decision
// 2's deliberate divergence, justified by an independent lifecycle a Loop
// preset simply doesn't have).
//
// # Init-vs-explicit-registration convention -- checked against
// internal/store/seed.go per this task's own instruction, but the closer
// real precedent turned out to be one level up
//
// This task's own "What to do" #1 points at internal/store/seed.go's
// builtin-seed pattern as "conceptually similar: compiled-in defaults, not
// DB rows" and asks this file to match whatever init-vs-explicit-
// registration convention it establishes. Read directly: seed.go's own
// Seed()/SeedProviders() are DB-row seeders -- they INSERT OR IGNORE compiled
// Go literals (seededProviders, pkg/models.AllSeeded()) into real tables at
// boot/first-run. That's a different shape than what a Go-coded, in-memory-
// only preset catalog needs (presets are never persisted to a table at all
// -- LoopRun.DefinitionName just stores the preset's *name* as a plain
// string, identical to any other WorkflowDefinition name).
//
// The actual matching precedent is one level further down the call chain
// seed.go itself depends on: pkg/models' own catalog (pkg/models/registry.go)
// -- a plain package-level var (allModels, ProviderDefaults) built directly
// by a composite literal, with plain accessor functions (AllSeeded, ByModelID)
// over it. No func init(), no explicit RegisterBuiltinModels() call -- Go's
// own package-level var initialization IS the "registration," and every
// caller (including seed.go) treats the catalog as always-already-populated.
// presets (below) follows that exact shape: a package-level `presets` map
// literal, plus GetPreset/PresetNames accessors -- no func init(), no
// explicit RegisterBuiltinPresets() call, for the identical reason pkg/models
// has neither: the catalog has no construction-order dependency on anything
// else in the program, so there is nothing an init-time hook would buy over
// a plain var.
//
// # Ralph's deterministic-only guarantee -- how, concretely, and why "0", not
// "generously large"
//
// This task's own Context section leaves the mechanism to the implementer:
// "either because the iteration WorkflowDefinition never produces a
// no_progress streak signal that would trigger the reasoning fallback, or
// because Ralph's own continuation-policy config short-circuits straight to
// RETRY/COMPLETE/FAIL -- pick one and document it." This file uses BOTH,
// deliberately redundant with each other rather than relying on either
// alone:
//
//  1. ralphPreset's Budget.MaxNoProgressIterations is 0. Decide's own branch-3
//     guard (decide.go: "budget.MaxNoProgressIterations > 0 && streak >=
//     budget.MaxNoProgressIterations") is false whenever the threshold itself
//     is 0 -- not "a generously large number a real run is unlikely to
//     reach," but a hard, structural "this check never fires for this
//     preset, full stop," regardless of how many consecutive NO_PROGRESS
//     iterations ever accumulate. A "generously large" threshold (e.g. 1000)
//     is still, in principle, reachable; 0 is not reachable by construction
//     (decide.go's own condition is strictly "> 0").
//  2. ralphDefinitionTemplate's one llm step carries no Verify modifier.
//     classifyIterationProgress's own doc comment (engine.go) names this
//     exact shape by name: "a completed run with zero [pass/fail] data
//     points (including a run with no Verify configured at all -- the common
//     Ralph one-llm-step-no-verify case) is optimistically PROGRESS, since
//     there is no declared failure signal to the contrary." A step that
//     errors still classifies REGRESSION (budget.MaxFailures' own
//     accounting, not branch 3's), but nothing about Ralph's default shape
//     can ever produce a NO_PROGRESS classification for noProgressStreak to
//     accumulate in the first place.
//
// Point 1 alone would already be sufficient (Decide's own guard is
// structural, not probabilistic) -- point 2 is kept as a second, independent
// belt-and-suspenders reason Ralph's own WorkflowRun shape never even
// produces the signal branch 3 looks for, so the guarantee doesn't rest on
// remembering to keep MaxNoProgressIterations at 0 correctly forever. Because
// of this, ralphPreset's own ContinuationPolicy is left at its zero value
// (empty Provider/Model) rather than populated with a placeholder LLM
// backend that would never actually be called -- decideByReasoning's own
// hard-error ("reasoning fallback requires a ContinuationPolicy with
// Provider and Model set") is the correct, loud failure mode for the one way
// this guarantee could be defeated: a caller overriding Budget at launch
// time (LoopLaunchRequest.BudgetOverrides) to raise
// MaxNoProgressIterations above 0 without also supplying a real
// ContinuationPolicy of their own. Baking in a specific model string here
// instead (e.g. a literal "claude-sonnet-4-5-...") would risk exactly the
// staleness pkg/models' own registry exists to centralize against (see that
// package's own doc comments on model deprecation) for a code path this
// preset's whole design goal is to make unreachable in the first place.
//
// # Ralph's WorkflowDefinition template -- per-launch content via
// {{input.*}} templating, not a template-builder function
//
// This task's own "What to do" #1 anticipates needing "a template-builder
// function if the WorkflowDefinition needs per-launch parameterization --
// e.g. Ralph's own prompt/task content varies per launch even though its
// shape doesn't." Confirmed directly against internal/service/
// workflow_engine.go's resolveStepConfig/resolveTemplateString/
// resolveTemplateRef: a StepDefinition.Config string value containing
// "{{input.<key>}}" is resolved, at each iteration's launch time, against
// that iteration's own agentworkflow.WorkflowInput.Params -- which is
// exactly LoopInput.WorkflowParams, forwarded unchanged to every iteration
// (engine.go's loopRunPersistentConfig.WorkflowParams). This existing
// mechanism already does everything a per-launch template-builder function
// would otherwise need to hand-roll, so ralphDefinitionTemplate is a single,
// static agentworkflow.WorkflowDefinition value (registered once, reused by
// every "ralph" launch) whose one llm step's prompt/provider/model/agent_id
// fields are all "{{input.*}}" references -- no Go-level templating
// function needed.
//
// Required LoopLaunchRequest.WorkflowParams keys for "ralph" (each is a hard
// launch-time error via resolveTemplateRef's own "params has no %q key"
// message if omitted, since buildLLMStepRequest requires a non-empty
// provider and prompt, and a real agent_id is required for the LLM step's
// permission checks to mean anything against a real agent_profiles row):
//
//	task      -- becomes the llm step's prompt (buildLLMStepRequest requires
//	             a non-empty "prompt").
//	provider  -- the LLM backend this iteration's step runs against
//	             (buildLLMStepRequest requires a non-empty "provider").
//	model     -- the specific model for that provider.
//	agent_id  -- the calling identity for permission checks -- deliberately
//	             not hardcoded into the preset (a literal agent_id baked into
//	             Go source would only work against whichever DB happened to
//	             have that exact agent_profiles row, which is never
//	             guaranteed across deployments).
//
// system_prompt/session_id/tools are deliberately left OUT of the template
// entirely for v1, not templated-but-optional -- resolveTemplateRef errors
// on ANY missing {{input.*}} key unconditionally (there is no "template this
// if present, else leave blank" mode), so including them as template
// references would silently make them required inputs too. A future task
// that wants a richer, still-single-llm-step Ralph shape (a system prompt,
// tool access) can extend ralphDefinitionTemplate then; v1 ships the
// narrowest shape the design doc's own closing line actually describes ("a
// trivial one-llm-step ... WorkflowDefinition").
//
// # Optional tool+verify steps -- opt-in via a different real
// WorkflowDefinition, not a v1 preset knob (disclosed follow-up)
//
// 21-loops.md's own parenthetical ("optionally + tool + verify") and this
// task's own "What to do" #2 ask this file to "document whether/how the
// optional tool+verify steps are included by default or opt-in." Decision:
// NOT included by default, and v1 has no opt-in mechanism to add them to
// the "ralph" preset itself -- LoopPreset (below) is a single static
// DefinitionTemplate value per preset name, not a template-builder function
// parameterized by "which optional steps to include" (see the section
// above: the one per-launch parameterization Ralph actually needs --
// prompt/provider/model/agent_id content -- is already fully served by
// {{input.*}} templating against a FIXED step shape; varying the DAG SHAPE
// itself per launch is a materially different, unbuilt capability). A caller
// that wants Ralph-with-verify today authors and registers their own
// WorkflowDefinition (llm step + tool step + verify modifier, exactly the
// shape 21-loops.md's parenthetical describes) directly against the shared
// *agentworkflow.Registry and launches LoopLaunchRequest.DefinitionName
// against THAT name instead of "ralph" -- LoopLauncher.Launch's existing
// fallback path (this file's own resolvePresetDefaults, below: "not a
// preset name -- treat it as an ordinary, already-registered
// WorkflowDefinition name") already supports this without any further
// change. This is a real, disclosed gap for whoever picks up the next
// preset task, not a silently-dropped one: a genuine "ralph, but with a
// verify step" preset variant (or a second named preset, e.g. a hypothetical
// "ralph-verify") needs a real per-launch DAG-shape decision this task does
// not make.
//
// # Stubbed presets (test-fix, review-fix, plan-execute, queue-drain,
// durable, self-improve) -- "not yet implemented" error, not a placeholder
// template
//
// This task's own "What to do" #3 authorizes either choice ("deliberately
// minimal/placeholder ... or returns a clear 'not yet implemented' error if
// actually launched -- your call which, document it"). This file picks the
// error: every stub preset's DefinitionTemplate is the WorkflowDefinition
// zero value plus just a Name (Steps left nil) -- deliberately NOT a
// minimal placeholder llm step, because a placeholder step that actually
// runs (even trivially) would let a caller believe launching "test-fix"
// today does something meaningful toward that preset's own real, unbuilt
// job, when it would really just be running Ralph's own generic one-step
// shape under a different name. That's a worse failure mode than a loud,
// explicit error: it fails to look like a stub. (LoopPreset).implemented()
// (below) treats "zero Steps" as the one bit distinguishing a real preset
// from a stub; resolvePresetDefaults (launcher.go) checks this BEFORE ever
// attempting to register a stub's DefinitionTemplate into the shared
// registry, returning ErrPresetNotImplemented with the preset's own name
// rather than surfacing agentworkflow.Validate's own generic "has no steps"
// message (which would be technically accurate but wouldn't name WHICH
// preset, or that this is an expected, disclosed gap rather than an
// authoring bug). GetPreset itself is unaffected either way: every one of
// these seven names still resolves via GetPreset with ok=true, per this
// task's own "Done means" -- only attempting to LAUNCH a stub is where the
// clear error appears, exactly the split this task's own "What to do" #3
// calls for ("GetPreset still succeeds ... even if launching against it
// fails cleanly").
//
// Every stub preset's Budget/ContinuationPolicy are the same reasonable
// defaults (MaxIterations 20, OnExhausted escalate, MaxNoProgressIterations
// 3 -- unlike Ralph, a stub preset's own real iteration-definition shape is
// unknown, so there is no basis yet for asserting its no-progress detection
// can be structurally disabled the way Ralph's can; a future task that
// builds a stub's real DefinitionTemplate should revisit whether its own
// shape can make the same deterministic-only claim Ralph's does, not assume
// it inherits Ralph's specific 0 value by default). ContinuationPolicy is
// left at its zero value for every stub for the identical reason: nothing
// about a stub preset should ever reach decideByReasoning, since nothing
// about it can be launched at all yet.
//
// # Wiring into LoopLaunchRequest -- see launcher.go's own resolvePresetDefaults
//
// This file only defines the preset catalog itself (LoopPreset, the presets
// map, GetPreset/PresetNames, ralphPreset/stubPreset). The actual "resolve
// DefinitionName against GetPreset before falling back to the registry"
// wiring this task's own "What to do" #4 describes lives in launcher.go,
// which is a package-internal caller of GetPreset exactly like any other
// consumer would be -- this file exposes no launcher-specific surface.

import (
	"errors"
	"sort"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

// ErrPresetNotImplemented is returned (wrapped with the preset's own name)
// when a caller tries to actually launch a registered-but-stubbed preset --
// see this file's own package-level doc comment, "Stubbed presets" section,
// for why this is a hard error rather than a silently-running placeholder.
var ErrPresetNotImplemented = errors.New("loop: preset is not yet implemented")

// Preset name constants -- the seven names 21-loops.md's own ledger and
// "What this session did not decide" section name verbatim: "ralph,
// test-fix, review-fix, plan-execute, queue-drain, durable, self-improve."
// Exported as typed constants (rather than bare string literals scattered
// across the presets map, this file's tests, and any future caller) so a
// typo in a preset name is a compile-time-checkable identifier mismatch
// wherever these constants are used, the same discipline decide.go's own
// DecisionKind constants already apply to loop_run_iterations.decision's
// literal vocabulary.
const (
	PresetRalph       = "ralph"
	PresetTestFix     = "test-fix"
	PresetReviewFix   = "review-fix"
	PresetPlanExecute = "plan-execute"
	PresetQueueDrain  = "queue-drain"
	PresetDurable     = "durable"
	PresetSelfImprove = "self-improve"
)

// LoopPreset bundles a Budget, a ContinuationPolicy, and an iteration-
// definition template -- 21-loops.md's own ledger entry, quoted verbatim in
// this task's Context section: "named (budget, continuation_policy,
// iteration-definition template) bundles, no per-preset engine." Nothing
// about LoopPreset itself executes anything -- it is pure data, consumed by
// LoopLauncher.Launch's resolvePresetDefaults (launcher.go), exactly the
// same way LoopDefinition/LoopInput (types.go) are pure data LoopEngine.Run
// consumes.
type LoopPreset struct {
	// Name is this preset's own registry key, duplicated onto the struct
	// itself (rather than only living as the presets map's own key) so a
	// LoopPreset value remains self-describing if ever passed around
	// independent of a map lookup (e.g. in a future admin/introspection
	// endpoint that lists preset details, not just names).
	Name string

	// Budget is this preset's default loop_runs.budget_json -- applied by
	// resolvePresetDefaults (launcher.go) only when the caller's own
	// LoopLaunchRequest.BudgetOverrides is nil; an explicit caller-supplied
	// Budget always wins over a preset's own default, exactly the same
	// "explicit input beats a computed/default one" precedence Launch's own
	// existing BudgetOverrides field already documents.
	Budget store.Budget

	// ContinuationPolicy is this preset's default reasoning-fallback
	// backend (decide.go's own ContinuationPolicy type) -- applied by
	// resolvePresetDefaults only when the caller's own
	// LoopLaunchRequest.ContinuationPolicy is the zero value. See this
	// file's own "Ralph's deterministic-only guarantee" doc-comment section
	// for why ralphPreset deliberately leaves this at its zero value rather
	// than populating a placeholder backend.
	ContinuationPolicy ContinuationPolicy

	// DefinitionTemplate is the WorkflowDefinition every iteration of this
	// preset launches under, registered into the shared
	// *agentworkflow.Registry (idempotently, under Name) the first time this
	// preset is actually launched -- see launcher.go's own
	// resolvePresetDefaults. Zero Steps means "stub, not yet implemented" --
	// see implemented(), below, and this file's own "Stubbed presets"
	// doc-comment section.
	DefinitionTemplate agentworkflow.WorkflowDefinition
}

// implemented reports whether p has a real, launchable DefinitionTemplate
// (non-empty Steps) rather than a named stub. agentworkflow.Validate's own
// "workflow ... has no steps" check would eventually catch an attempt to
// register a stub's empty template anyway, but resolvePresetDefaults checks
// this first so a caller gets ErrPresetNotImplemented (naming the preset)
// rather than that more generic validation error.
func (p LoopPreset) implemented() bool {
	return len(p.DefinitionTemplate.Steps) > 0
}

// Ralph's own WorkflowParams keys -- see this file's own package-level doc
// comment, "Ralph's WorkflowDefinition template" section, for the full
// reasoning behind exactly these four and no others.
const (
	RalphParamTask     = "task"
	RalphParamProvider = "provider"
	RalphParamModel    = "model"
	RalphParamAgentID  = "agent_id"
)

// ralphDefaultMaxIterations is Ralph's own Budget.MaxIterations default --
// this task's own "What to do" #2: "a real default, e.g. 20, matching the
// design doc's own illustrative loop_run example" (21-loops.md's
// "Illustrative shape" section literally uses budget: { max_iterations: 20,
// ... }).
const ralphDefaultMaxIterations = 20

// ralphDefinitionTemplate is Ralph's one-llm-step WorkflowDefinition
// template -- 21-loops.md's own closing line: "a trivial one-llm-step
// ... WorkflowDefinition, no REPLAN, no REARCHITECT." No Verify modifier by
// design -- see this file's own package-level doc comment, "Ralph's
// deterministic-only guarantee" section, point 2.
func ralphDefinitionTemplate() agentworkflow.WorkflowDefinition {
	return agentworkflow.WorkflowDefinition{
		Name: PresetRalph,
		Steps: []agentworkflow.StepDefinition{
			{
				ID:   "work",
				Kind: agentworkflow.StepKindLLM,
				Config: map[string]any{
					"provider": "{{input." + RalphParamProvider + "}}",
					"model":    "{{input." + RalphParamModel + "}}",
					"agent_id": "{{input." + RalphParamAgentID + "}}",
					"prompt":   "{{input." + RalphParamTask + "}}",
				},
			},
		},
	}
}

// ralphPreset is the one preset this batch builds and tests fully
// end-to-end -- see this file's own package-level doc comment for the full
// design reasoning behind every field below.
func ralphPreset() LoopPreset {
	return LoopPreset{
		Name: PresetRalph,
		Budget: store.Budget{
			MaxIterations:           ralphDefaultMaxIterations,
			OnExhausted:             store.LoopRunOnExhaustedEscalate,
			MaxNoProgressIterations: 0,
		},
		// Deliberately the zero value -- see "Ralph's deterministic-only
		// guarantee," above.
		ContinuationPolicy: ContinuationPolicy{},
		DefinitionTemplate: ralphDefinitionTemplate(),
	}
}

// stubPresetDefaultMaxIterations/stubPresetDefaultMaxNoProgress are the
// "reasonable defaults" this task's own "What to do" #3 asks for on every
// registered-but-unbuilt preset. Not tied to ralphDefaultMaxIterations (kept
// as a separate constant, even though it happens to share Ralph's own
// value today) because a stub's real default budget is a decision that
// belongs to whichever future task actually builds its DefinitionTemplate,
// not an accidental inheritance from Ralph's own tuning.
const (
	stubPresetDefaultMaxIterations = 20
	stubPresetDefaultMaxNoProgress = 3
)

// stubPreset builds a named-but-unimplemented LoopPreset -- see this file's
// own package-level doc comment, "Stubbed presets" section, for why this is
// an empty-Steps DefinitionTemplate (checked by (LoopPreset).implemented(),
// enforced by launcher.go's resolvePresetDefaults) rather than a minimal
// placeholder step that would actually run something.
func stubPreset(name string) LoopPreset {
	return LoopPreset{
		Name: name,
		Budget: store.Budget{
			MaxIterations:           stubPresetDefaultMaxIterations,
			OnExhausted:             store.LoopRunOnExhaustedEscalate,
			MaxNoProgressIterations: stubPresetDefaultMaxNoProgress,
		},
		ContinuationPolicy: ContinuationPolicy{},
		DefinitionTemplate: agentworkflow.WorkflowDefinition{
			Name: name,
			// Steps deliberately nil -- see implemented() and this file's
			// own "Stubbed presets" doc-comment section.
		},
	}
}

// presets is the compiled-in preset catalog -- a plain package-level var
// built by composite literal, matching pkg/models' own catalog convention
// (allModels/ProviderDefaults). See this file's own package-level doc
// comment, "Init-vs-explicit-registration convention" section, for why this
// shape (not func init(), not an explicit RegisterBuiltinPresets() call) is
// the right match for this codebase's existing precedent.
var presets = map[string]LoopPreset{
	PresetRalph:       ralphPreset(),
	PresetTestFix:     stubPreset(PresetTestFix),
	PresetReviewFix:   stubPreset(PresetReviewFix),
	PresetPlanExecute: stubPreset(PresetPlanExecute),
	PresetQueueDrain:  stubPreset(PresetQueueDrain),
	PresetDurable:     stubPreset(PresetDurable),
	PresetSelfImprove: stubPreset(PresetSelfImprove),
}

// GetPreset returns the named preset and whether it exists. Existence alone
// says nothing about whether the preset can actually be launched yet --
// see (LoopPreset).implemented() and this file's own "Stubbed presets"
// doc-comment section. Every one of the seven names 21-loops.md's own ledger
// lists returns ok=true, per this task's own "Done means."
func GetPreset(name string) (LoopPreset, bool) {
	p, ok := presets[name]
	return p, ok
}

// PresetNames returns every registered preset name, sorted -- mirrors
// (*agentworkflow.Registry).Names' own sorted-listing convention.
func PresetNames() []string {
	names := make([]string, 0, len(presets))
	for name := range presets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
