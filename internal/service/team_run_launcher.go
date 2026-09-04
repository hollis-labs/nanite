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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/hollis-labs/nanite/internal/agent/override"
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
	// IdempotencyKey binds caller retries and crash recovery to one TeamRun.
	// When omitted, LaunchTeamRun generates a fresh key for this invocation.
	IdempotencyKey string

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
	ID        string
	SlotName  string
	AgentID   string
	SessionID string
}

// TeamRunLaunchError preserves the durable run identity when work after the
// shared host commit needs reconciliation.
type TeamRunLaunchError struct {
	RunID string
	Err   error
}

func (e *TeamRunLaunchError) Error() string { return e.Err.Error() }
func (e *TeamRunLaunchError) Unwrap() error { return e.Err }

type TeamRunReconcileReport struct {
	PendingInspected int
	RoutingInspected int
	SignalsInspected int
	SignalsCompleted int
	MembersStopped   int
	Recovered        int
	Failures         []error
}

type TeamRunRoutingInstaller interface {
	InstallTeamRunRouting(context.Context, string, string) ([]string, error)
}

// TeamRunLauncher owns the real "launch a saved Team by name" call —
// TASKS/teams/08-team-run-launcher.md. A struct (not a bare package-level
// function, despite the task file's own illustrative signature) because
// slot resolution genuinely needs three independent collaborators (store
// reads/writes, the existing WorkflowLauncher, and DurableAgentService for
// durable Team Slot resolution) — the same "struct + method" shape WorkflowLauncher itself
// already uses for an analogous "assemble several dependencies, expose one
// Launch-shaped entry point" job, not a stylistic deviation.
type TeamRunLauncher struct {
	store    *store.Store
	launcher *WorkflowLauncher
	durable  DurableAgentService
	routing  TeamRunRoutingInstaller

	launchMu      sync.Mutex
	recoveryMu    sync.Mutex
	launchCursor  string
	routingCursor string
	signalCursor  string
}

// NewTeamRunLauncher constructs a TeamRunLauncher. All three arguments are
// required — LaunchTeamRun returns a clear error rather than panicking if
// any is nil.
func NewTeamRunLauncher(st *store.Store, launcher *WorkflowLauncher, durable DurableAgentService) *TeamRunLauncher {
	return &TeamRunLauncher{store: st, launcher: launcher, durable: durable}
}

// WithRoutingInstaller attaches the production routing repair dependency after
// TeamRoutingService is constructed (it in turn uses this launcher for lazy
// slots). Startup and cadence reconciliation can then close the
// members-ready -> routing-ready crash window.
func (l *TeamRunLauncher) WithRoutingInstaller(routing TeamRunRoutingInstaller) *TeamRunLauncher {
	if l != nil {
		l.routing = routing
	}
	return l
}

func (l *TeamRunLauncher) MarkRoutingReady(ctx context.Context, key, runID string) error {
	if l == nil || l.store == nil {
		return errors.New("team run launch: launcher not fully configured")
	}
	return l.store.MarkTeamRunLaunchRoutingReady(ctx, key, runID)
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
//  3. Hand the compiled definition directly to
//     WorkflowLauncher.LaunchDefinition under a fresh per-launch name.
//     The durable host records canonical compiled plan material before it
//     starts execution, so later Resume calls do not depend on a mutable
//     process-local registry entry. This is the call that produces the
//     real workflow_runs.id.
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
	if l == nil || l.store == nil || l.launcher == nil || l.durable == nil {
		return nil, fmt.Errorf("team run launch: launcher not fully configured")
	}
	l.launchMu.Lock()
	defer l.launchMu.Unlock()
	if teamID == "" {
		return nil, fmt.Errorf("team run launch: team id is required")
	}
	if strings.TrimSpace(overrides.IdempotencyKey) == "" {
		overrides.IdempotencyKey = ulid.Make().String()
	}
	digest, err := teamRunRequestDigest(teamID, overrides)
	if err != nil {
		return nil, err
	}
	if existing, loadErr := l.store.GetTeamRunLaunch(ctx, overrides.IdempotencyKey); loadErr == nil {
		if existing.TeamID != teamID || existing.RequestDigest != digest {
			return nil, fmt.Errorf("%w: key %q belongs to a different Team launch", store.ErrTeamRunLaunchConflict, overrides.IdempotencyKey)
		}
		if existing.Status == store.TeamRunLaunchPlanning {
			return l.continueTeamRunPlanning(ctx, *existing)
		}
		return l.launchPreparedTeamRun(ctx, *existing)
	} else if !errors.Is(loadErr, store.ErrTeamRunLaunchNotFound) {
		return nil, fmt.Errorf("team run launch: load idempotency record: %w", loadErr)
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
	planningJSON, err := json.Marshal(teamRunPlanningRecord{Team: *team, Plan: plan, Overrides: overrides})
	if err != nil {
		return nil, fmt.Errorf("team run launch: encode recovery plan: %w", err)
	}
	planning, err := l.store.BeginTeamRunLaunch(ctx, store.TeamRunLaunch{
		IdempotencyKey: overrides.IdempotencyKey, TeamID: teamID, RequestDigest: digest, PlanningJSON: string(planningJSON),
	})
	if err != nil {
		return nil, err
	}
	if planning.Status != store.TeamRunLaunchPlanning {
		return l.launchPreparedTeamRun(ctx, *planning)
	}
	return l.continueTeamRunPlanning(ctx, *planning)
}

type teamRunPlanningRecord struct {
	Team      store.Team            `json:"team"`
	Plan      []eagerResolutionItem `json:"plan"`
	Overrides TeamRunOverrides      `json:"overrides"`
}

func (l *TeamRunLauncher) continueTeamRunPlanning(ctx context.Context, launch store.TeamRunLaunch) (*agentworkflow.WorkflowResult, error) {
	var planning teamRunPlanningRecord
	if err := json.Unmarshal([]byte(launch.PlanningJSON), &planning); err != nil {
		return nil, fmt.Errorf("team run launch: decode recovery plan: %w", err)
	}
	if planning.Team.ID != launch.TeamID || planning.Overrides.IdempotencyKey != launch.IdempotencyKey || len(planning.Plan) == 0 {
		return nil, fmt.Errorf("%w: launch %q recovery plan identity differs", store.ErrTeamRunLaunchConflict, launch.IdempotencyKey)
	}
	phases, err := planning.Team.Phases()
	if err != nil {
		return nil, fmt.Errorf("team run launch: decode planned team %q phases: %w", planning.Team.Name, err)
	}
	team := &planning.Team
	plan := planning.Plan
	overrides := planning.Overrides

	// Step 1 — resolve every planned member into a purely local slice. No
	// team_run_members row exists yet (see doc comment above).
	resolved := make([]resolvedTeamMember, 0, len(plan))
	memberOrdinal := 0
	for _, item := range plan {
		for i := 0; i < item.Count; i++ {
			memberID := stableTeamRunMemberID(overrides.IdempotencyKey, memberOrdinal)
			agentID, sessionID, resolveErr := l.resolveSlotMemberKeyed(ctx, overrides.IdempotencyKey, memberOrdinal, memberID, team.ID, item.Slot, overrides)
			if resolveErr != nil {
				return nil, fmt.Errorf("team run launch: resolve team slot %q (member %d/%d): %w", item.Slot.Name, i+1, item.Count, resolveErr)
			}
			resolved = append(resolved, resolvedTeamMember{ID: memberID, SlotName: item.Slot.Name, AgentID: agentID, SessionID: sessionID})
			memberOrdinal++
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
	keyDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(overrides.IdempotencyKey)))
	wfName := fmt.Sprintf("%s%s:%s", agentworkflow.TeamRunDefinitionNamePrefix, team.Name, keyDigest[:20])
	wf, err := CompileTeam(wfName, phases, nil)
	if err != nil {
		return nil, fmt.Errorf("team run launch: compile team %q: %w", team.Name, err)
	}

	launchRequest := WorkflowLaunchRequest{
		WorkflowName:    wfName,
		Params:          mergeTeamRunParams(team, overrides),
		ProjectID:       overrides.ProjectID,
		AgentProfileID:  agentProfileID,
		ParentSessionID: overrides.ParentSessionID,
		TimeoutSeconds:  overrides.TimeoutSeconds,
		IdempotencyKey:  "team-run:" + overrides.IdempotencyKey,
	}
	members := make([]store.TeamRunMember, 0, len(resolved))
	for _, member := range resolved {
		members = append(members, store.TeamRunMember{ID: member.ID, SlotName: member.SlotName, AgentID: member.AgentID, SessionID: member.SessionID, Status: store.TeamRunMemberStatusActive})
	}
	definitionJSON, err := json.Marshal(wf)
	if err != nil {
		return nil, fmt.Errorf("team run launch: encode prepared definition: %w", err)
	}
	requestJSON, err := json.Marshal(launchRequest)
	if err != nil {
		return nil, fmt.Errorf("team run launch: encode prepared request: %w", err)
	}
	membersJSON, err := json.Marshal(members)
	if err != nil {
		return nil, fmt.Errorf("team run launch: encode prepared members: %w", err)
	}
	prepared, err := l.store.CreateTeamRunLaunch(ctx, store.TeamRunLaunch{
		IdempotencyKey: overrides.IdempotencyKey, TeamID: launch.TeamID, RequestDigest: launch.RequestDigest,
		PlanningJSON:   launch.PlanningJSON,
		DefinitionJSON: string(definitionJSON), LaunchRequestJSON: string(requestJSON), MembersJSON: string(membersJSON),
	})
	if err != nil {
		return nil, fmt.Errorf("team run launch: persist prepared launch: %w", err)
	}
	return l.launchPreparedTeamRun(ctx, *prepared)
}

func teamRunRequestDigest(teamID string, overrides TeamRunOverrides) (string, error) {
	overrides.IdempotencyKey = ""
	encoded, err := json.Marshal(struct {
		TeamID    string
		Overrides TeamRunOverrides
	}{TeamID: teamID, Overrides: overrides})
	if err != nil {
		return "", fmt.Errorf("team run launch: encode idempotency request: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}

func stableTeamRunMemberID(key string, index int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", key, index)))
	return fmt.Sprintf("team-member-%x", digest[:16])
}

func stableTeamRunSessionID(key string, index int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00session\x00%d", key, index)))
	return fmt.Sprintf("team-session-%x", digest[:16])
}

func (l *TeamRunLauncher) resolveSlotMemberKeyed(ctx context.Context, key string, ordinal int, memberID, teamID string, slot store.TeamSlotDefinition, overrides TeamRunOverrides) (string, string, error) {
	if existing, err := l.store.GetTeamRunMemberIntent(ctx, key, ordinal); err == nil {
		if existing.MemberID != memberID || existing.TeamID != teamID || existing.SlotName != slot.Name {
			return "", "", fmt.Errorf("%w: member intent %q/%d differs", store.ErrTeamRunLaunchConflict, key, ordinal)
		}
		if existing.Status == store.TeamRunMemberIntentProvisioned {
			if _, sessionErr := l.store.GetSession(ctx, existing.SessionID); sessionErr != nil {
				return "", "", fmt.Errorf("recover provisioned Team member session %s: %w", existing.SessionID, sessionErr)
			}
			return existing.AgentID, existing.SessionID, nil
		}
		return l.provisionTeamMemberIntent(ctx, *existing, slot, overrides)
	} else if !errors.Is(err, store.ErrTeamRunLaunchNotFound) {
		return "", "", err
	}

	agentID, err := l.resolveSlotAgentIdentity(ctx, slot)
	if err != nil {
		return "", "", err
	}
	intent, err := l.store.CreateTeamRunMemberIntent(ctx, store.TeamRunMemberIntent{
		IdempotencyKey: key, Ordinal: ordinal, MemberID: memberID, TeamID: teamID,
		SlotName: slot.Name, AgentID: agentID, SessionID: stableTeamRunSessionID(key, ordinal), ProvisioningKind: slot.Resolution,
	})
	if err != nil {
		return "", "", err
	}
	return l.provisionTeamMemberIntent(ctx, *intent, slot, overrides)
}

func (l *TeamRunLauncher) resolveSlotAgentIdentity(ctx context.Context, slot store.TeamSlotDefinition) (string, error) {
	switch slot.Resolution {
	case "durable":
		if slot.AgentID == nil || strings.TrimSpace(*slot.AgentID) == "" {
			return "", fmt.Errorf("team slot %q: resolution=durable requires agent_id", slot.Name)
		}
		if _, err := l.store.GetAgent(ctx, *slot.AgentID); err != nil {
			return "", fmt.Errorf("team slot %q: durable agent_id %q: %w", slot.Name, *slot.AgentID, err)
		}
		return *slot.AgentID, nil
	case "fresh":
		profile, _, err := l.resolveFreshProfile(ctx, slot)
		if err != nil {
			return "", err
		}
		return profile.ID, nil
	default:
		return "", fmt.Errorf("team slot %q: unknown resolution %q (want \"durable\" or \"fresh\")", slot.Name, slot.Resolution)
	}
}

func (l *TeamRunLauncher) provisionTeamMemberIntent(ctx context.Context, intent store.TeamRunMemberIntent, slot store.TeamSlotDefinition, overrides TeamRunOverrides) (string, string, error) {
	var sessionID string
	var err error
	switch intent.ProvisioningKind {
	case "fresh":
		sessionID, err = l.resolveFreshMemberWithIdentity(ctx, slot, overrides, intent.AgentID, intent.SessionID)
	case "durable":
		_, sessionID, err = l.resolveDurableMember(ctx, slot, intent.SessionID)
	default:
		err = fmt.Errorf("unsupported member provisioning kind %q", intent.ProvisioningKind)
	}
	if err != nil {
		return "", "", err
	}
	completed, err := l.store.CompleteTeamRunMemberIntent(context.WithoutCancel(ctx), intent.IdempotencyKey, intent.Ordinal, sessionID)
	if err != nil {
		return "", "", err
	}
	return completed.AgentID, completed.SessionID, nil
}

func (l *TeamRunLauncher) launchPreparedTeamRun(ctx context.Context, launch store.TeamRunLaunch) (*agentworkflow.WorkflowResult, error) {
	var definition agentworkflow.WorkflowDefinition
	var request WorkflowLaunchRequest
	var members []store.TeamRunMember
	if err := json.Unmarshal([]byte(launch.DefinitionJSON), &definition); err != nil {
		return nil, fmt.Errorf("team run launch: decode prepared definition: %w", err)
	}
	if err := json.Unmarshal([]byte(launch.LaunchRequestJSON), &request); err != nil {
		return nil, fmt.Errorf("team run launch: decode prepared request: %w", err)
	}
	if err := json.Unmarshal([]byte(launch.MembersJSON), &members); err != nil {
		return nil, fmt.Errorf("team run launch: decode prepared members: %w", err)
	}

	launchResult, launchErr := l.launcher.LaunchDefinition(ctx, definition, request)
	if launchResult == nil || launchResult.RunID == "" {
		if launchErr != nil {
			return nil, fmt.Errorf("team run launch: launch prepared Team: %w", launchErr)
		}
		return nil, errors.New("team run launch: prepared launcher returned no run id")
	}
	persistCtx := context.WithoutCancel(ctx)
	if err := l.store.RecordTeamRunLaunchRun(persistCtx, launch.IdempotencyKey, launchResult.RunID, string(launchResult.Status), launchErr); err != nil {
		return workflowResultFromLaunch(launchResult), &TeamRunLaunchError{RunID: launchResult.RunID, Err: err}
	}
	if err := l.store.CompleteTeamRunLaunch(persistCtx, launch.IdempotencyKey, launchResult.RunID, string(launchResult.Status), members); err != nil {
		return workflowResultFromLaunch(launchResult), &TeamRunLaunchError{RunID: launchResult.RunID, Err: err}
	}
	result := workflowResultFromLaunch(launchResult)
	if launchErr != nil {
		return result, &TeamRunLaunchError{RunID: launchResult.RunID, Err: fmt.Errorf("team run launch: launch prepared Team: %w", launchErr)}
	}
	return result, nil
}

func workflowResultFromLaunch(result *WorkflowLaunchResult) *agentworkflow.WorkflowResult {
	if result == nil {
		return nil
	}
	return &agentworkflow.WorkflowResult{RunID: result.RunID, Status: result.Status, StepResults: result.StepResults, Error: result.Error}
}

// ReconcileTeamRuns recovers prepared launches and evaluates every open Team
// signal through the ordinary durable host Resume surface.
func (l *TeamRunLauncher) ReconcileTeamRuns(ctx context.Context, limit int) TeamRunReconcileReport {
	l.recoveryMu.Lock()
	defer l.recoveryMu.Unlock()
	l.launchMu.Lock()
	defer l.launchMu.Unlock()
	report := TeamRunReconcileReport{}
	stopped, err := l.store.StopTerminalTeamRunMembers(ctx, limit)
	if err != nil {
		report.Failures = append(report.Failures, err)
		return report
	}
	report.MembersStopped = stopped
	completed, err := l.store.CompleteClosedTeamSignalResolutions(ctx, limit)
	if err != nil {
		report.Failures = append(report.Failures, err)
		return report
	}
	report.SignalsCompleted = completed
	launches, err := l.store.ListPendingTeamRunLaunchesAfter(ctx, l.launchCursor, limit)
	if err != nil {
		report.Failures = append(report.Failures, err)
		return report
	}
	for _, launch := range launches {
		l.launchCursor = launch.IdempotencyKey
		report.PendingInspected++
		var recoverErr error
		if launch.Status == store.TeamRunLaunchPlanning {
			_, recoverErr = l.continueTeamRunPlanning(ctx, launch)
		} else {
			_, recoverErr = l.launchPreparedTeamRun(ctx, launch)
		}
		if recoverErr != nil {
			report.Failures = append(report.Failures, fmt.Errorf("recover Team launch %q: %w", launch.IdempotencyKey, recoverErr))
		} else {
			report.Recovered++
		}
	}
	if l.routing != nil {
		routingLaunches, routingErr := l.store.ListTeamRunLaunchesPendingRoutingAfter(ctx, l.routingCursor, limit)
		if routingErr != nil {
			report.Failures = append(report.Failures, routingErr)
			return report
		}
		for _, launch := range routingLaunches {
			l.routingCursor = launch.IdempotencyKey
			report.RoutingInspected++
			if launch.WorkflowRunID == "" {
				report.Failures = append(report.Failures, fmt.Errorf("recover Team routing %q: workflow run is missing", launch.IdempotencyKey))
				continue
			}
			if _, installErr := l.routing.InstallTeamRunRouting(ctx, launch.WorkflowRunID, launch.TeamID); installErr != nil {
				report.Failures = append(report.Failures, fmt.Errorf("recover Team routing %q: %w", launch.IdempotencyKey, installErr))
				continue
			}
			if markErr := l.store.MarkTeamRunLaunchRoutingReady(context.WithoutCancel(ctx), launch.IdempotencyKey, launch.WorkflowRunID); markErr != nil {
				report.Failures = append(report.Failures, fmt.Errorf("complete Team routing %q: %w", launch.IdempotencyKey, markErr))
				continue
			}
			report.Recovered++
		}
	}
	runIDs, err := l.store.ListOpenTeamSignalRunIDsAfter(ctx, l.signalCursor, limit)
	if err != nil {
		report.Failures = append(report.Failures, err)
		return report
	}
	for _, runID := range runIDs {
		l.signalCursor = runID
		report.SignalsInspected++
		if _, resumeErr := l.launcher.Resume(ctx, runID); resumeErr != nil {
			report.Failures = append(report.Failures, fmt.Errorf("reconcile Team signal run %s: %w", runID, resumeErr))
		} else {
			report.Recovered++
		}
	}
	return report
}

// RunReconciler is the production cadence for Team launch recovery and signal
// evaluation. It runs once immediately, then until the lifecycle context ends.
func (l *TeamRunLauncher) RunReconciler(ctx context.Context, cadence time.Duration, limit int, observe func(TeamRunReconcileReport)) {
	if cadence <= 0 {
		cadence = 5 * time.Second
	}
	run := func() {
		report := l.ReconcileTeamRuns(ctx, limit)
		if observe != nil {
			observe(report)
		}
	}
	run()
	ticker := time.NewTicker(cadence)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// eagerResolutionItem is one planEagerResolution result entry: a Team Slot
// this launch resolves eagerly, and how many members to resolve for it.
type eagerResolutionItem struct {
	Slot  store.TeamSlotDefinition `json:"slot"`
	Count int                      `json:"count"`
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
		plan = append(plan, eagerResolutionItem{Slot: slot, Count: count})
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
		if item.Slot.Required {
			requiredSlots[item.Slot.Name] = true
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
func (l *TeamRunLauncher) resolveDurableMember(ctx context.Context, slot store.TeamSlotDefinition, stableSessionID ...string) (string, string, error) {
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
		stableInstanceDigest := sha256.Sum256([]byte(profileID))
		newInst.ID = fmt.Sprintf("durable-profile-%x", stableInstanceDigest[:16])
		if err := l.durable.Create(ctx, newInst); err != nil {
			inst, err = l.store.GetDurableAgentInstanceByProfileID(context.WithoutCancel(ctx), profileID)
			if err != nil {
				return "", "", fmt.Errorf("team slot %q: create durable instance for profile %q: %w", slot.Name, profileID, err)
			}
		} else {
			inst = newInst
			isNewInstance = true
		}
	}
	if len(stableSessionID) != 0 && inst.CurrentSessionID == stableSessionID[0] {
		session, sessionErr := l.store.GetSession(ctx, stableSessionID[0])
		if sessionErr != nil {
			return "", "", fmt.Errorf("team slot %q: recover stable durable session %q: %w", slot.Name, stableSessionID[0], sessionErr)
		}
		if session.ContextType != "durable_agent" || session.ContextID != inst.ID {
			return "", "", fmt.Errorf("team slot %q: stable durable session %q belongs to another launch", slot.Name, stableSessionID[0])
		}
		return profileID, session.ID, nil
	}

	wake := DurableAgentWakePayload{Reason: "team_run_slot_resolution"}
	startRequest := DurableAgentStartRequest{WakePayload: wake}
	if len(stableSessionID) != 0 {
		startRequest.SessionID = stableSessionID[0]
	}
	var result *DurableAgentLaunchResult
	if len(stableSessionID) != 0 {
		// A journaled member intent owns this exact session. Start is itself
		// idempotent for the requested ID and must not silently resume some older
		// session already attached to the durable identity.
		result, err = l.durable.Start(ctx, inst.ID, startRequest)
	} else if isNewInstance {
		result, err = l.durable.Start(ctx, inst.ID, startRequest)
	} else {
		result, err = l.durable.Resume(ctx, inst.ID, DurableAgentStartRequest{WakePayload: wake})
		if err != nil && errors.Is(err, ErrDurableAgentNoResumableSession) {
			// An existing instance row with no attached session yet (e.g.
			// created but never actually launched) — Start, not a fatal
			// error. Confirms which of Start/Resume applies per the
			// target instance's own current state, as this task's Context
			// requires, rather than guessing one unconditionally.
			result, err = l.durable.Start(ctx, inst.ID, startRequest)
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
	profile, cascade, err := l.resolveFreshProfile(ctx, slot)
	if err != nil {
		return "", "", err
	}
	return l.resolveFreshMemberWithProfile(ctx, slot, overrides, profile, cascade, "")
}

func (l *TeamRunLauncher) resolveFreshProfile(ctx context.Context, slot store.TeamSlotDefinition) (store.AgentProfile, override.OverrideConfig, error) {
	if slot.RoleSlug == "" {
		return store.AgentProfile{}, override.OverrideConfig{}, fmt.Errorf("team slot %q: resolution=fresh requires role_slug", slot.Name)
	}
	role, err := l.store.GetRoleBySlug(ctx, slot.RoleSlug)
	if err != nil {
		return store.AgentProfile{}, override.OverrideConfig{}, fmt.Errorf("team slot %q: look up role %q: %w", slot.Name, slot.RoleSlug, err)
	}
	if role == nil {
		return store.AgentProfile{}, override.OverrideConfig{}, fmt.Errorf("team slot %q: role_slug %q does not exist", slot.Name, slot.RoleSlug)
	}

	profiles, err := l.store.ListAgentsByRoleID(ctx, role.ID)
	if err != nil {
		return store.AgentProfile{}, override.OverrideConfig{}, fmt.Errorf("team slot %q: list agents for role %q: %w", slot.Name, role.Slug, err)
	}
	if len(profiles) == 0 {
		return store.AgentProfile{}, override.OverrideConfig{}, fmt.Errorf("team slot %q: resolution=fresh requires at least one agent_profiles row bound to role %q (role_id) — none found", slot.Name, role.Slug)
	}
	profile := profiles[0]
	cascade := ResolveAgentCascade(role, &profile, nil)
	return profile, cascade, nil
}

func (l *TeamRunLauncher) resolveFreshMemberWithIdentity(ctx context.Context, slot store.TeamSlotDefinition, overrides TeamRunOverrides, agentID, sessionID string) (string, error) {
	profile, cascade, err := l.resolveFreshProfile(ctx, slot)
	if err != nil {
		return "", err
	}
	if profile.ID != agentID {
		return "", fmt.Errorf("team slot %q: prepared agent %q no longer matches resolved profile %q", slot.Name, agentID, profile.ID)
	}
	_, resolvedSession, err := l.resolveFreshMemberWithProfile(ctx, slot, overrides, profile, cascade, sessionID)
	return resolvedSession, err
}

func (l *TeamRunLauncher) resolveFreshMemberWithProfile(ctx context.Context, slot store.TeamSlotDefinition, overrides TeamRunOverrides, profile store.AgentProfile, cascade override.OverrideConfig, sessionID string) (string, string, error) {
	if sessionID != "" {
		existing, err := l.store.GetSession(ctx, sessionID)
		if err == nil {
			if existing.ContextType != "team_slot" || existing.ContextID != slot.Name {
				return "", "", fmt.Errorf("team slot %q: stable session %s belongs to another launch", slot.Name, sessionID)
			}
			if err := l.store.EnsureSessionAgent(ctx, existing.ID, profile.ID, "team_slot", true); err != nil {
				return "", "", fmt.Errorf("team slot %q: recover agent attachment: %w", slot.Name, err)
			}
			return profile.ID, existing.ID, nil
		}
	}
	sess := &store.Session{
		ID:          sessionID,
		ProjectID:   overrides.ProjectID,
		Provider:    firstNonEmpty(cascade.Provider, profile.DefaultProvider),
		Model:       firstNonEmpty(cascade.Model, profile.DefaultModel),
		Title:       fmt.Sprintf("team slot: %s", slot.Name),
		ContextType: "team_slot",
		ContextID:   slot.Name,
	}
	if err := l.store.CreateSession(ctx, sess); err != nil {
		if sessionID != "" {
			existing, getErr := l.store.GetSession(context.WithoutCancel(ctx), sessionID)
			if getErr == nil && existing.ContextType == "team_slot" && existing.ContextID == slot.Name {
				if attachErr := l.store.EnsureSessionAgent(context.WithoutCancel(ctx), existing.ID, profile.ID, "team_slot", true); attachErr != nil {
					return "", "", fmt.Errorf("team slot %q: recover agent attachment: %w", slot.Name, attachErr)
				}
				return profile.ID, existing.ID, nil
			}
		}
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
