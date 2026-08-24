package service

// TASKS/teams/08-team-run-launcher.md — the last Phase 2 (runtime engine)
// task in the Teams batch: stitches together everything Phase 1 and tasks
// 06/07 built into one real, callable "launch a saved Team by name" entry
// point. Per docs/engineering/architecture/15-teams.md's own Guardrail
// diagram, this file IS the last three arrows:
//
//	Team definition
//	    ↓ resolve slots                                  <- this file
//	    ↓ install run-scoped routing/reflex policy        <- task 09, not here
//	    ↓ compile phase/gate sequence into a WorkflowDefinition  (task 07, called from here)
//	    ↓ launch an ordinary WorkflowRun                   <- this file, via WorkflowLauncher
//	    ↓ existing agents + messaging + lifecycle do the actual work
//
// "install run-scoped routing/reflex policy" is deliberately NOT done here
// — that is task 09's own scope (docs/engineering/architecture/
// 15-teams.md's routing section, and TASKS/teams/09-team-routing.md, not
// yet implemented as of this task). This file resolves slots, enforces
// may_spawn at resolution time, compiles, and launches — nothing more.
//
// # resolution: durable|fresh — the corrected semantics (this task's own
// central de-risking finding, repeated here per this task file's own
// "Done means" instruction not to bury it in code comments alone)
//
// docs/engineering/architecture/15-teams.md's illustrative slot config
// claims `resolution: durable|fresh` is "not new vocabulary — they're
// agents.durable/agents.activation_mode as they exist today." That is
// HALF right. activation_mode really is the same three-value enum
// (singleton|fresh-per-wake|concurrent), referenced not redefined. But
// agent_profiles.durable (migration 073) is an unrelated static flag: it
// exempts a profile row from migration 061's reingest/eject predicate — a
// data-retention property of the profile record, not a runtime signal
// about whether a durable_agent_instances row exists to wake. Treating
// `resolution: durable` as "read agent_profiles.durable" would be a
// category error. This file treats Resolution as new Team-Slot-level
// launch-time configuration (task 01's TeamSlotDefinition.Resolution)
// deciding whether to call DurableAgentService.Start/Resume against a
// named instance ("durable") or construct fresh via the ordinary Agent
// Construction cascade ("fresh") — never a read of agent_profiles.durable.
//
// # agent_id-to-instance mapping (this task's other required documented
// finding)
//
// TeamSlotDefinition.AgentID's own doc comment (task 01, already reviewed
// and merged) settles this: "the concrete agent_profiles.id to wake" — a
// profile ID, not a durable_agent_instances.slug. DurableAgentService.
// Start/Resume, however, take a durable_agent_instances.id, not a profile
// ID — there is no single "wake by profile" method. resolveDurableMember
// (below) bridges that gap: GetDurableAgentInstanceByProfileID (new,
// internal/store/durable_agents.go) looks up (or, on first use for a
// profile with no instance row yet, creates via the same
// durableAgentInstanceFromProfile shape internal/service/durable_agents.go
// already uses for its own profile-reconcile sweep) the
// durable_agent_instances row bound to that profile, then calls Start (new
// instance) or Resume (falling back to Start on
// ErrDurableAgentNoResumableSession) against that row's own id. Explicitly
// NOT gated on agent_profiles.durable or agent_profiles.tags containing
// "durable-agent" — reconcileProfileBackedInstances' own auto-reconcile
// sweep gates on exactly that flag, and depending on it here would
// silently reintroduce the very category error this task exists to avoid.

import (
	"context"
	"errors"
	"fmt"

	"github.com/oklog/ulid/v2"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

// ErrTeamSlotNotFound is returned when a named Team Slot doesn't exist on
// the resolved Team's own slot definitions.
var ErrTeamSlotNotFound = errors.New("team slot not found")

// ErrTeamRunElasticResolutionUnauthorized is returned when a Team Slot
// resolved beyond the Team's own required:true baseline (an elastic
// concurrent slot's eager min, or a later lazy-slot wake — see
// ResolveLazySlot) has no team_authority_grants may_spawn row authorizing
// it from any of the Team's required:true slots. Fails the whole launch
// (or the whole lazy-resolve call) rather than silently skipping the slot
// — an unauthorized elastic resolution attempt is rejected, not degraded.
var ErrTeamRunElasticResolutionUnauthorized = errors.New("team run: elastic slot resolution is not authorized by any required team slot's may_spawn grant")

// TeamRunOverrides is the invocation-time layer of the "Team defaults →
// saved config → invocation overrides" closest-wins cascade
// (15-teams.md's "Runtime overrides follow the existing cascade" section,
// this task's own item 5). In this codebase's Team schema (task 01) there
// is genuinely only one saved-config layer — teams has no separate
// "default_params" sub-structure distinct from the Team row itself, unlike
// role→agent→task's real three tiers — so the practical cascade here is
// two layers, not three: a small, informational base derived from the
// Team row itself (see buildBaseParams), with TeamRunOverrides.Params
// layered on top, closest wins. Documented here rather than assumed,
// since the task's own Context describes this as "the same shape already
// standardized elsewhere" without itself confirming Team has three real
// tiers to cascade across — it doesn't.
type TeamRunOverrides struct {
	// SlotMemberCounts overrides a concurrent-activation-mode Team Slot's
	// eagerly-resolved member count for this one launch, keyed by Team
	// Slot name. Only consulted for a slot whose ActivationMode ==
	// "concurrent" — singleton/fresh-per-wake slots always resolve to
	// exactly one member regardless of any entry here. Must fall within
	// the slot's own [Min, Max] range (Min defaults to 1, Max <= 0 means
	// unbounded) — a value outside that range is a launch-time error, not
	// silently clamped.
	SlotMemberCounts map[string]int

	// Params is layered onto a small Team-derived base (buildBaseParams)
	// and forwarded as WorkflowLaunchRequest.Params — this call's own
	// closest-wins override of whatever the compiled TeamRun's steps read
	// out of input.Params at runtime.
	Params map[string]any

	// ProjectID / ParentSessionID / TimeoutSeconds forward straight
	// through to WorkflowLaunchRequest — see that struct's own doc
	// comments (internal/service/workflow_launch.go).
	ProjectID       string
	ParentSessionID string
	TimeoutSeconds  int

	// AgentProfileID is the calling identity WorkflowLaunchRequest.
	// AgentProfileID requires (durable_agent_instances.profile_id is a
	// required FK on the template-class instance WorkflowLauncher.Launch
	// creates to own this TeamRun's own audit trail — a bookkeeping
	// instance distinct from every Team Slot member's own resolved
	// session). When left empty, LaunchTeamRun defaults it to the first
	// eagerly-resolved required:true Team Slot's own resolved agent_id
	// (the SME example's "orchestrator") — the Team's own primary/
	// organizing identity is the natural owner of this TeamRun's audit
	// trail when no caller-specific identity is supplied.
	AgentProfileID string
}

// resolvedTeamMember is LaunchTeamRun's provisional, local record of one
// slot resolution BEFORE the real workflow_runs.id exists — see this
// file's package-level doc comment and LaunchTeamRun's own doc comment for
// the full ordering this type exists to support.
type resolvedTeamMember struct {
	SlotName  string
	AgentID   string
	SessionID string
}

// TeamRunLauncher owns the real "launch a saved Team by name" call —
// TASKS/teams/08-team-run-launcher.md. A struct (not a bare package-level
// function, despite the task file's own illustrative signature) because
// slot resolution genuinely needs four independent collaborators (store
// reads/writes, the shared workflow definition registry, the existing
// WorkflowLauncher, and DurableAgentService for durable Team Slot
// resolution) — the same "struct + method" shape WorkflowLauncher itself
// already uses for an analogous "assemble several dependencies, expose one
// Launch-shaped entry point" job, not a stylistic deviation.
type TeamRunLauncher struct {
	store    *store.Store
	registry *agentworkflow.Registry
	launcher *WorkflowLauncher
	durable  DurableAgentService
}

// NewTeamRunLauncher constructs a TeamRunLauncher. All four arguments are
// required — LaunchTeamRun returns a clear error rather than panicking if
// any is nil.
func NewTeamRunLauncher(st *store.Store, registry *agentworkflow.Registry, launcher *WorkflowLauncher, durable DurableAgentService) *TeamRunLauncher {
	return &TeamRunLauncher{store: st, registry: registry, launcher: launcher, durable: durable}
}

// LaunchTeamRun loads teamID, resolves its Team Slots into concrete
// (agent_id, session_id) tuples, compiles its phase sequence (task 07's
// CompileTeam), and launches the result as an ordinary WorkflowRun (the
// existing WorkflowLauncher — TeamRun IS a WorkflowRun, 15-teams.md
// Decision 2). Returns the underlying agentworkflow.WorkflowResult once
// the run has made all the progress it currently can — which, for a Team
// whose first phase is a flex step (every illustrative example in
// 15-teams.md), means RunStatusWaitingOnFlex, not RunStatusCompleted: the
// run is genuinely still in progress, waiting on its first phase's exit
// trigger, exactly like a gate-fronted workflow already waits on operator
// input today.
//
// # Resolved ordering — team_run_members can only be inserted AFTER the
// real workflow_runs.id exists
//
// task 02's team_run_members.workflow_run_id is NOT NULL, but
// WorkflowLauncher.Launch generates the run's real id internally (the
// built-in engine's own ulid.Make() call, internal/service/
// workflow_engine.go) and only exposes it on the WorkflowResult it
// returns once Run() has made all the progress it currently can — there
// is no dedicated FK linking a durable_agent_instances row to its
// workflow_runs row either (WorkflowLauncher's own doc comment: the link
// is metadata_json-based, stamped AFTER Run() returns). So the real id
// this launch's team_run_members rows need does not exist until after
// WorkflowLauncher.Launch has already returned. This function's resolved
// order, concretely:
//
//  1. Resolve every eagerly-resolved Team Slot member (durable
//     Start/Resume or fresh session construction — see resolveSlotMember)
//     into a purely local, provisional []resolvedTeamMember slice. No
//     team_run_members row exists yet at this point.
//  2. Compile the Team's phase sequence (CompileTeam) — this needs no
//     workflow_run_id either; a flex step's Config carries Team Slot
//     *names*, re-resolved against team_run_members live at flex-step
//     entry by task 06's already-merged executor, never a baked-in id
//     (see CompileTeam's own doc comment for why this is correct, not
//     just convenient).
//  3. Register the compiled definition (agentworkflow.Registry.Register,
//     this task's own small addition) under a fresh, per-launch unique
//     name, then call WorkflowLauncher.Launch — this is the one call that
//     actually produces a real workflow_runs.id, and it runs the engine
//     to completion (or its first genuine wait) synchronously, in-process,
//     before returning.
//  4. Only now, with a real WorkflowLaunchResult.RunID in hand, backfill
//     one InsertTeamRunMember row per provisionally-resolved member.
//
// This ordering is safe specifically because task 06's own merged
// implementation confirms a flex step's FIRST entry never reads
// team_run_members at all (workflow_engine_flex.go's runStep flex branch
// only marks the step/run waiting) — team_run_members is only ever
// consulted later, when something external calls Resume (an exit trigger
// firing), by which point this function has already returned and
// backfilled every row. A Team whose very first phase is a flex step
// therefore reaches RunStatusWaitingOnFlex with zero team_run_members
// rows existing yet mid-Launch-call — and gains them the moment Launch
// returns, before this function itself returns to its own caller.
func (l *TeamRunLauncher) LaunchTeamRun(ctx context.Context, teamID string, overrides TeamRunOverrides) (*agentworkflow.WorkflowResult, error) {
	if l == nil || l.store == nil || l.registry == nil || l.launcher == nil || l.durable == nil {
		return nil, fmt.Errorf("team run launch: launcher not fully configured")
	}
	if teamID == "" {
		return nil, fmt.Errorf("team run launch: team id is required")
	}

	team, err := l.store.GetTeam(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("team run launch: load team %s: %w", teamID, err)
	}
	slots, err := team.Slots()
	if err != nil {
		return nil, fmt.Errorf("team run launch: decode team %q slots: %w", team.Name, err)
	}
	phases, err := team.Phases()
	if err != nil {
		return nil, fmt.Errorf("team run launch: decode team %q phases: %w", team.Name, err)
	}
	if len(phases) == 0 {
		return nil, fmt.Errorf("team run launch: team %q has an empty phase sequence", team.Name)
	}

	plan, err := l.planEagerResolution(ctx, team.ID, slots, overrides)
	if err != nil {
		return nil, err
	}
	if len(plan) == 0 {
		return nil, fmt.Errorf("team run launch: team %q has no required or eagerly-resolvable team slots", team.Name)
	}

	// Step 1 — resolve every planned member into a purely local slice. No
	// team_run_members row exists yet (see doc comment above).
	resolved := make([]resolvedTeamMember, 0, len(plan))
	for _, item := range plan {
		for i := 0; i < item.count; i++ {
			agentID, sessionID, err := l.resolveSlotMember(ctx, item.slot, overrides)
			if err != nil {
				return nil, fmt.Errorf("team run launch: resolve team slot %q (member %d/%d): %w", item.slot.Name, i+1, item.count, err)
			}
			resolved = append(resolved, resolvedTeamMember{SlotName: item.slot.Name, AgentID: agentID, SessionID: sessionID})
		}
	}

	agentProfileID := overrides.AgentProfileID
	if agentProfileID == "" {
		agentProfileID = defaultLaunchAgentProfileID(plan, resolved)
	}
	if agentProfileID == "" {
		return nil, fmt.Errorf("team run launch: team %q: no AgentProfileID supplied and no required team slot resolved to default to", team.Name)
	}

	// Step 2 — compile. No workflow_run_id needed (see doc comment above).
	wfName := fmt.Sprintf("%s%s:%s", agentworkflow.TeamRunDefinitionNamePrefix, team.Name, ulid.Make().String())
	wf, err := CompileTeam(wfName, phases, nil)
	if err != nil {
		return nil, fmt.Errorf("team run launch: compile team %q: %w", team.Name, err)
	}

	// Step 3 — register + launch. This is the one call that produces a
	// real workflow_runs.id.
	if err := l.registry.Register(wf); err != nil {
		return nil, fmt.Errorf("team run launch: register compiled team %q: %w", team.Name, err)
	}
	launchResult, err := l.launcher.Launch(ctx, WorkflowLaunchRequest{
		WorkflowName:    wfName,
		Params:          mergeTeamRunParams(team, overrides),
		ProjectID:       overrides.ProjectID,
		AgentProfileID:  agentProfileID,
		ParentSessionID: overrides.ParentSessionID,
		TimeoutSeconds:  overrides.TimeoutSeconds,
	})
	if err != nil {
		return nil, fmt.Errorf("team run launch: launch compiled team %q: %w", team.Name, err)
	}
	if launchResult == nil || launchResult.RunID == "" {
		return nil, fmt.Errorf("team run launch: launch compiled team %q: launcher returned no run id", team.Name)
	}

	// Registry-growth mitigation (TASKS/teams/11-team-run-launch-api.md's
	// required prerequisite; see agentworkflow.Registry.Unregister's own
	// doc comment for the full reasoning). A run that came back terminal
	// (completed/failed/canceled) will never be Resume()d — nothing will
	// ever call l.registry.Get(wfName) again — so its one-off compiled
	// definition can be evicted immediately. A run still waiting on a gate
	// or a flex step's exit trigger is deliberately left registered: Resume
	// re-fetches the definition by name later
	// (a2a_task_manager.go's resumeWorkflowRun), and evicting it here would
	// break that resume path. This does not, by itself, bound registry
	// growth for the common case (every 15-teams.md illustrative Team
	// opens on a flex step) — AgentCardGenerator's own
	// IsTeamRunDefinitionName filter is what unconditionally keeps a
	// still-registered, still-waiting TeamRun definition out of the public
	// A2A skill-discovery response regardless of this eviction's own
	// narrower reach.
	if isTerminalRunStatus(launchResult.Status) {
		l.registry.Unregister(wfName)
	}

	// Step 4 — backfill team_run_members, now that the real
	// workflow_runs.id exists.
	for _, m := range resolved {
		if _, err := l.store.InsertTeamRunMember(ctx, store.TeamRunMember{
			WorkflowRunID: launchResult.RunID,
			SlotName:      m.SlotName,
			AgentID:       m.AgentID,
			SessionID:     m.SessionID,
		}); err != nil {
			return nil, fmt.Errorf("team run launch: record resolved member for team slot %q (run %s): %w", m.SlotName, launchResult.RunID, err)
		}
	}

	return &agentworkflow.WorkflowResult{
		RunID:       launchResult.RunID,
		Status:      launchResult.Status,
		StepResults: launchResult.StepResults,
		Error:       launchResult.Error,
	}, nil
}

// isTerminalRunStatus reports whether status is one of the built-in
// engine's real terminal states (agentworkflow.RunStatusCompleted/Failed/
// Canceled) — as opposed to RunStatusWaiting/RunStatusWaitingOnFlex/
// RunStatusWaitingOnLoop, which mean the run made all the progress it
// currently can but is not done: a later external Resume call will still
// need to look its compiled definition up by name. See this file's own
// Unregister call site, above.
func isTerminalRunStatus(status agentworkflow.RunStatus) bool {
	switch status {
	case agentworkflow.RunStatusCompleted, agentworkflow.RunStatusFailed, agentworkflow.RunStatusCanceled:
		return true
	default:
		return false
	}
}

// eagerResolutionItem is one planEagerResolution result entry: a Team Slot
// this launch resolves eagerly, and how many members to resolve for it.
type eagerResolutionItem struct {
	slot  store.TeamSlotDefinition
	count int
}

// planEagerResolution decides which Team Slots resolve eagerly at launch
// (required:true baseline slots, plus any concurrent-activation slot with
// Min > 0 or an explicit invocation-time member-count override), checks
// may_spawn authorization for every slot beyond the required:true
// baseline BEFORE resolving anything (a two-pass design specifically so an
// unauthorized Team definition never creates a single session/durable
// wake for any slot — see this function's own authorization sub-pass,
// which runs to completion before Step 1 in LaunchTeamRun ever resolves a
// single member), and computes each planned slot's member count. A Team
// Slot that is neither required nor eagerly min/override-resolved is
// deliberately left off this plan entirely — that is the lazy-resolution
// case (15-teams.md's "architect, normally dormant" example); see
// ResolveLazySlot for how that slot resolves later, on demand.
func (l *TeamRunLauncher) planEagerResolution(ctx context.Context, teamID string, slots []store.TeamSlotDefinition, overrides TeamRunOverrides) ([]eagerResolutionItem, error) {
	var plan []eagerResolutionItem
	for _, slot := range slots {
		eager := slot.Required || (slot.ActivationMode == "concurrent" && slot.Min > 0)
		if !eager {
			continue // lazy — ResolveLazySlot's job, not launch's.
		}
		if !slot.Required {
			ok, err := l.authorizedForElasticResolution(ctx, teamID, slots, slot.Name)
			if err != nil {
				return nil, fmt.Errorf("team run launch: check may_spawn authorization for team slot %q: %w", slot.Name, err)
			}
			if !ok {
				return nil, fmt.Errorf("%w: team slot %q", ErrTeamRunElasticResolutionUnauthorized, slot.Name)
			}
		}
		count, err := resolveMemberCount(slot, overrides)
		if err != nil {
			return nil, err
		}
		plan = append(plan, eagerResolutionItem{slot: slot, count: count})
	}
	return plan, nil
}

// authorizedForElasticResolution reports whether ANY of this Team's own
// required:true Team Slots holds a may_spawn grant naming targetSlot —
// task 04's AuthorizedForVerb (internal/store/team_authority.go) is the
// real enforcement primitive, called here as a real hard if-gate at the
// actual resolution call site (planEagerResolution, above; ResolveLazySlot
// reuses this same helper), not merely documented as a future integration
// point.
//
// Why "any required slot," not one fixed slot name: 15-teams.md's SME
// example grants may_spawn from "orchestrator" specifically
// (orchestrator.may_spawn: [engineer, reviewer]), but nothing in the
// design or task 04's schema hardcodes which slot name is allowed to hold
// a may_spawn grant — a Team's required:true baseline IS the set of
// identities guaranteed to exist at launch time to have granted such
// permission in the first place (an elastic slot cannot be authorized by
// another slot that might not exist yet). Checking every required slot
// (not just one) is the generalization that doesn't hardcode "orchestrator"
// as a magic name anywhere in this file.
//
// Fails closed: an error from AuthorizedForVerb aborts immediately (not
// treated as "no grant"); zero required slots holding a matching grant
// returns false, nil — never true by default. See
// TestLaunchTeamRun_ElasticSlotWithoutAuthorityGrant_Rejected and
// TestAuthorizedForElasticResolution_FailsClosed for the regression
// coverage.
func (l *TeamRunLauncher) authorizedForElasticResolution(ctx context.Context, teamID string, slots []store.TeamSlotDefinition, targetSlot string) (bool, error) {
	for _, s := range slots {
		if !s.Required {
			continue
		}
		ok, err := l.store.AuthorizedForVerb(ctx, teamID, s.Name, store.TeamAuthorityVerbMaySpawn, targetSlot)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// resolveMemberCount computes how many members to eagerly resolve for
// slot. Singleton/fresh-per-wake slots (ActivationMode != "concurrent")
// always resolve to exactly 1, ignoring any SlotMemberCounts override —
// "concurrent" is the only activation mode 15-teams.md's own text ever
// describes as multi-member (min/max). A concurrent slot resolves its own
// Min (defaulting to 1 when Min <= 0), or an invocation-time override
// between Min and Max when overrides.SlotMemberCounts names it — no
// mid-run elastic growth beyond this launch-time count, per this task's
// own Context section (task_execute's real recursion-depth-0 cap blocks
// the design doc's own speculated reuse path for a dispatched, non-root
// orchestrator slot to grow a slot mid-run; deliberately out of this
// batch's scope, not an oversight).
func resolveMemberCount(slot store.TeamSlotDefinition, overrides TeamRunOverrides) (int, error) {
	if slot.ActivationMode != "concurrent" {
		return 1, nil
	}
	minCount := slot.Min
	if minCount <= 0 {
		minCount = 1
	}
	count := minCount
	if overrides.SlotMemberCounts != nil {
		if v, ok := overrides.SlotMemberCounts[slot.Name]; ok {
			count = v
		}
	}
	if count < minCount {
		return 0, fmt.Errorf("team slot %q: requested member count %d is below the slot's own min %d", slot.Name, count, minCount)
	}
	if slot.Max > 0 && count > slot.Max {
		return 0, fmt.Errorf("team slot %q: requested member count %d exceeds the slot's own max %d", slot.Name, count, slot.Max)
	}
	return count, nil
}

// defaultLaunchAgentProfileID picks the first required:true slot's own
// resolved agent_id (in plan/resolved order) as the default
// WorkflowLaunchRequest.AgentProfileID when overrides.AgentProfileID is
// left empty — see TeamRunOverrides.AgentProfileID's own doc comment.
// Returns "" if no required slot was resolved (defensive; LaunchTeamRun
// treats that as a launch-time error, not a silent skip).
func defaultLaunchAgentProfileID(plan []eagerResolutionItem, resolved []resolvedTeamMember) string {
	requiredSlots := make(map[string]bool, len(plan))
	for _, item := range plan {
		if item.slot.Required {
			requiredSlots[item.slot.Name] = true
		}
	}
	for _, m := range resolved {
		if requiredSlots[m.SlotName] {
			return m.AgentID
		}
	}
	return ""
}

// mergeTeamRunParams builds WorkflowLaunchRequest.Params — a small,
// informational base derived from the Team row itself (team_id/team_name,
// the only "Team default" this schema actually has to cascade from — see
// TeamRunOverrides' own doc comment), with overrides.Params layered on
// top, closest wins, per key. A caller-supplied "team_id"/"team_name"
// entry in overrides.Params intentionally overrides the derived value —
// closest wins per key, no field is protected from being overridden.
func mergeTeamRunParams(team *store.Team, overrides TeamRunOverrides) map[string]any {
	out := map[string]any{
		"team_id":   team.ID,
		"team_name": team.Name,
	}
	for k, v := range overrides.Params {
		out[k] = v
	}
	return out
}

// resolveSlotMember resolves ONE concrete (agent_id, session_id) member
// for slot, per Resolution's corrected semantics (see this file's own
// package-level doc comment).
func (l *TeamRunLauncher) resolveSlotMember(ctx context.Context, slot store.TeamSlotDefinition, overrides TeamRunOverrides) (agentID, sessionID string, err error) {
	switch slot.Resolution {
	case "durable":
		return l.resolveDurableMember(ctx, slot)
	case "fresh":
		return l.resolveFreshMember(ctx, slot, overrides)
	default:
		return "", "", fmt.Errorf("team slot %q: unknown resolution %q (want \"durable\" or \"fresh\")", slot.Name, slot.Resolution)
	}
}

// resolveDurableMember implements `resolution: durable` — wake an existing
// identity via DurableAgentService, never a read of agent_profiles.durable
// (see this file's own package-level doc comment for the full corrected-
// semantics finding).
//
// Concurrent multi-member durable resolution (ActivationMode ==
// "concurrent" with more than one member requested for a durable slot) is
// explicitly NOT supported by this function — a durable slot wakes ONE
// existing identity; DurableAgentService.Start/Resume's own
// selectOrCreateLaunchSession/latestAttachedSession logic would hand back
// the SAME already-attached session on a second Resume call for most
// LifecycleClass session policies, silently collapsing what should be N
// distinct concurrent members onto one shared session. 15-teams.md's own
// SME example never asks for this (its one durable slot, "architect," is
// singleton), so this function fails closed with a clear error rather
// than produce that silently-wrong behavior — a real, documented scope
// boundary, not an oversight. resolveSlotMember's caller
// (LaunchTeamRun/ResolveLazySlot) invokes this once per requested member,
// so the guard lives here rather than duplicated at every call site.
func (l *TeamRunLauncher) resolveDurableMember(ctx context.Context, slot store.TeamSlotDefinition) (string, string, error) {
	if slot.ActivationMode == "concurrent" && slot.Max > 1 {
		return "", "", fmt.Errorf("team slot %q: resolution=durable does not support concurrent multi-member resolution (max=%d) — a durable slot wakes a single existing identity", slot.Name, slot.Max)
	}
	if slot.AgentID == nil || *slot.AgentID == "" {
		return "", "", fmt.Errorf("team slot %q: resolution=durable requires agent_id (the agent_profiles.id to wake)", slot.Name)
	}
	profileID := *slot.AgentID

	profile, err := l.store.GetAgent(ctx, profileID)
	if err != nil {
		return "", "", fmt.Errorf("team slot %q: durable agent_id %q: %w", slot.Name, profileID, err)
	}

	inst, err := l.store.GetDurableAgentInstanceByProfileID(ctx, profileID)
	isNewInstance := false
	if err != nil {
		if !errors.Is(err, store.ErrDurableAgentInstanceNotFound) {
			return "", "", fmt.Errorf("team slot %q: look up durable instance for profile %q: %w", slot.Name, profileID, err)
		}
		// No durable_agent_instances row bound to this profile yet —
		// bootstrap one, reusing the same shape
		// reconcileProfileBackedInstances already builds elsewhere in this
		// package (internal/service/durable_agents.go), but NOT gated on
		// profileIsDurableCandidate — see this function's own doc comment
		// and this file's package-level doc comment for why that gate
		// would be the exact category error this task exists to avoid.
		newInst := durableAgentInstanceFromProfile(*profile)
		if err := l.durable.Create(ctx, newInst); err != nil {
			return "", "", fmt.Errorf("team slot %q: create durable instance for profile %q: %w", slot.Name, profileID, err)
		}
		inst = newInst
		isNewInstance = true
	}

	wake := DurableAgentWakePayload{Reason: "team_run_slot_resolution"}
	var result *DurableAgentLaunchResult
	if isNewInstance {
		result, err = l.durable.Start(ctx, inst.ID, DurableAgentStartRequest{WakePayload: wake})
	} else {
		result, err = l.durable.Resume(ctx, inst.ID, DurableAgentStartRequest{WakePayload: wake})
		if err != nil && errors.Is(err, ErrDurableAgentNoResumableSession) {
			// An existing instance row with no attached session yet (e.g.
			// created but never actually launched) — Start, not a fatal
			// error. Confirms which of Start/Resume applies per the
			// target instance's own current state, as this task's Context
			// requires, rather than guessing one unconditionally.
			result, err = l.durable.Start(ctx, inst.ID, DurableAgentStartRequest{WakePayload: wake})
		}
	}
	if err != nil {
		return "", "", fmt.Errorf("team slot %q: wake durable instance %q (profile %q): %w", slot.Name, inst.ID, profileID, err)
	}
	if result == nil || result.Session == nil {
		return "", "", fmt.Errorf("team slot %q: durable wake for instance %q returned no session", slot.Name, inst.ID)
	}
	return profileID, result.Session.ID, nil
}

// resolveFreshMember implements `resolution: fresh` — construct through
// the ordinary Agent Construction cascade (internal/service/
// role_cascade.go's RoleOverrideConfig/AgentOverrideConfig/
// ResolveAgentCascade), NOT by materializing a brand-new agent_profiles
// row per launch. Concretely: slot.RoleSlug resolves to a store.Role
// (task 01's own TeamSlotDefinition.RoleSlug doc comment: "references
// roles.slug"); ListAgentsByRoleID (this task's own small store addition)
// finds the concrete Agent (an already-composed agent_profiles row bound
// to that Role via role_id — GLOSSARY.md's Agent entry: "a role... bound
// to a scope... Not a flat, standalone definition") to attach a fresh
// session to. The cascade's own scalar merge (system_prompt/class/model/
// provider) is used to seed the new session's Provider/Model; the SAME
// merge additionally happens automatically, per-turn, once this session's
// primary agent is set (internal/service/agent.go's applyScalarCascade,
// via profile.RoleID) — this function does not need to re-apply or persist
// system_prompt/tools/skills itself, only pick which concrete Agent this
// Team Slot member's session is bound to.
//
// A "fresh" Team Slot therefore means "spawn a new session/turn identity
// through the ordinary construction cascade," as distinct from "durable"
// waking an EXISTING instance's own session — not "create a brand-new,
// one-off agent_profiles row." Multiple concurrent members of the same
// slot (ActivationMode == "concurrent") share the SAME resolved
// agent_profiles.id, each with its own distinct session — mirroring how
// this codebase already lets one durable_agent_instances row own more
// than one attached session.
func (l *TeamRunLauncher) resolveFreshMember(ctx context.Context, slot store.TeamSlotDefinition, overrides TeamRunOverrides) (string, string, error) {
	if slot.RoleSlug == "" {
		return "", "", fmt.Errorf("team slot %q: resolution=fresh requires role_slug", slot.Name)
	}
	role, err := l.store.GetRoleBySlug(ctx, slot.RoleSlug)
	if err != nil {
		return "", "", fmt.Errorf("team slot %q: look up role %q: %w", slot.Name, slot.RoleSlug, err)
	}
	if role == nil {
		return "", "", fmt.Errorf("team slot %q: role_slug %q does not exist", slot.Name, slot.RoleSlug)
	}

	profiles, err := l.store.ListAgentsByRoleID(ctx, role.ID)
	if err != nil {
		return "", "", fmt.Errorf("team slot %q: list agents for role %q: %w", slot.Name, role.Slug, err)
	}
	if len(profiles) == 0 {
		return "", "", fmt.Errorf("team slot %q: resolution=fresh requires at least one agent_profiles row bound to role %q (role_id) — none found", slot.Name, role.Slug)
	}
	profile := profiles[0]

	cascade := ResolveAgentCascade(role, &profile, nil)

	sess := &store.Session{
		ProjectID:   overrides.ProjectID,
		Provider:    firstNonEmpty(cascade.Provider, profile.DefaultProvider),
		Model:       firstNonEmpty(cascade.Model, profile.DefaultModel),
		Title:       fmt.Sprintf("team slot: %s", slot.Name),
		ContextType: "team_slot",
		ContextID:   slot.Name,
	}
	if err := l.store.CreateSession(ctx, sess); err != nil {
		return "", "", fmt.Errorf("team slot %q: create session: %w", slot.Name, err)
	}
	if err := l.store.EnsureSessionAgent(ctx, sess.ID, profile.ID, "team_slot", true); err != nil {
		return "", "", fmt.Errorf("team slot %q: attach agent %q to session %s: %w", slot.Name, profile.ID, sess.ID, err)
	}
	return profile.ID, sess.ID, nil
}

// ResolveLazySlot is the lazy-resolution mechanism for a Team Slot that
// was neither required:true nor eagerly min/override-resolved at launch
// (15-teams.md's "architect, normally dormant" example) — 15-teams.md's
// own text: "required: false slots... resolve lazily — not at launch, but
// the first time a flex step's active_slots actually names them."
//
// # Mechanism: a check-and-resolve-on-read callback, not a flex-executor
// hook
//
// This task's own "Touches" scope is this file alone — task 06's already-
// merged, already-reviewed flex executor (internal/service/
// workflow_engine_flex.go) is deliberately NOT modified here. Wiring a
// live call to this method into evaluateFlexExit/activeMembersForSlots
// (so a flex step whose active_slots names a still-dormant lazy slot
// resolves it automatically the moment that step is entered) is real,
// but out of this task's own scope — task 06's own evaluateFlexExit
// already degrades correctly without it (a lazy slot with zero
// team_run_members rows simply contributes zero active members to that
// step, "legitimately still waiting," not an error). ResolveLazySlot is
// this task's documented answer to "how is the lazy trigger implemented":
// an explicit, idempotent, authorization-checked resolve-on-demand
// function, intended to be invoked by whichever real event actually
// names a dormant slot — most concretely, task 09's routing layer
// (15-teams.md's "Explicit addressing" bullet: "a thin resolver from slot
// name to the team_run_members tuple") the first time something
// addresses/messages that slot and finds no active member resolved for
// it yet, or a future revision of task 06's flex executor calling this
// before activeMembersForSlots if immediate mid-flex-step wake-on-name is
// ever wanted. Both are legitimate "the first time [something] actually
// names them" triggers per the design doc's own phrasing; this function
// is the shared resolution body either caller needs.
//
// # Idempotency
//
// Calling this twice for the same (runID, teamID, slotName) with an
// already-active resolution is a no-op: existing TeamRunMemberStatusActive
// rows for the slot are returned as-is, no new session/durable wake is
// created. This matters because more than one caller could plausibly race
// to lazily resolve the same dormant slot (e.g. two messages addressed to
// the architect in close succession, or a future flex-executor hook and a
// routing-layer caller both firing) — resolving twice would otherwise
// silently double a singleton/fresh-per-wake slot's member count.
//
// # Authorization
//
// Same may_spawn enforcement as launch-time elastic resolution
// (authorizedForElasticResolution) — a lazy slot is, by definition,
// "beyond the Team's required:true baseline," so this task's own item 4
// instruction applies here exactly as much as it does to
// planEagerResolution.
func (l *TeamRunLauncher) ResolveLazySlot(ctx context.Context, workflowRunID, teamID, slotName string, overrides TeamRunOverrides) ([]store.TeamRunMember, error) {
	if l == nil || l.store == nil {
		return nil, fmt.Errorf("team run launch: launcher not fully configured")
	}
	if workflowRunID == "" || teamID == "" || slotName == "" {
		return nil, fmt.Errorf("team run launch: resolve lazy slot: workflow_run_id, team_id, and slot_name are all required")
	}

	existing, err := l.store.ListTeamRunMembersBySlot(ctx, workflowRunID, slotName)
	if err != nil {
		return nil, fmt.Errorf("team run launch: resolve lazy slot %q: list existing members: %w", slotName, err)
	}
	active := make([]store.TeamRunMember, 0, len(existing))
	for _, m := range existing {
		if m.Status == store.TeamRunMemberStatusActive {
			active = append(active, m)
		}
	}
	if len(active) > 0 {
		return active, nil
	}

	team, err := l.store.GetTeam(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("team run launch: resolve lazy slot %q: load team %s: %w", slotName, teamID, err)
	}
	slots, err := team.Slots()
	if err != nil {
		return nil, fmt.Errorf("team run launch: resolve lazy slot %q: decode team %q slots: %w", slotName, team.Name, err)
	}
	slot, ok := findSlot(slots, slotName)
	if !ok {
		return nil, fmt.Errorf("%w: team %q has no team slot named %q", ErrTeamSlotNotFound, team.Name, slotName)
	}

	if !slot.Required {
		ok, err := l.authorizedForElasticResolution(ctx, teamID, slots, slotName)
		if err != nil {
			return nil, fmt.Errorf("team run launch: resolve lazy slot %q: check may_spawn authorization: %w", slotName, err)
		}
		if !ok {
			return nil, fmt.Errorf("%w: team slot %q", ErrTeamRunElasticResolutionUnauthorized, slotName)
		}
	}

	count, err := resolveMemberCount(slot, overrides)
	if err != nil {
		return nil, err
	}

	out := make([]store.TeamRunMember, 0, count)
	for i := 0; i < count; i++ {
		agentID, sessionID, err := l.resolveSlotMember(ctx, slot, overrides)
		if err != nil {
			return nil, fmt.Errorf("team run launch: resolve lazy slot %q (member %d/%d): %w", slotName, i+1, count, err)
		}
		m, err := l.store.InsertTeamRunMember(ctx, store.TeamRunMember{
			WorkflowRunID: workflowRunID,
			SlotName:      slotName,
			AgentID:       agentID,
			SessionID:     sessionID,
		})
		if err != nil {
			return nil, fmt.Errorf("team run launch: resolve lazy slot %q: record resolved member: %w", slotName, err)
		}
		out = append(out, *m)
	}
	return out, nil
}

// findSlot returns the TeamSlotDefinition named name, if present.
func findSlot(slots []store.TeamSlotDefinition, name string) (store.TeamSlotDefinition, bool) {
	for _, s := range slots {
		if s.Name == name {
			return s, true
		}
	}
	return store.TeamSlotDefinition{}, false
}
