package service

// TASKS/teams/09-team-routing.md — Phase 3 (routing & messaging) of the
// Teams batch (docs/engineering/architecture/15-teams.md). Implements the
// design doc's "Routing: real reuse, and one real gap" section for a
// launched TeamRun (task 08's TeamRunLauncher, already merged/reviewed —
// this file does not modify team_run_launcher.go): explicit @slot
// addressing, semantic/coordinator-fallback routing via run-scoped
// dispatch_to_agent reflex rows (task 05), and the routing-target
// resolution-failure stress test 15-teams.md's "Validating this design"
// section calls out by name.
//
// # Wiring boundary (this task's own documented call)
//
// This task's own item 2 leaves the exact install-time wiring boundary to
// the worker: "called from task 08's launcher, or from this task's own
// function that 08 would call." Task 08 (team_run_launcher.go) is already
// merged and reviewed — this file does NOT retrofit a call into
// LaunchTeamRun itself. Instead, InstallTeamRunRouting (below) is a
// standalone, independently-callable function, intended to be invoked by
// whatever caller assembles a full "launch a TeamRun" operation AFTER
// TeamRunLauncher.LaunchTeamRun returns a real workflow_runs.id — most
// concretely, TASKS/teams/11-team-run-launch-api.md's future HTTP launch
// handler (not yet built as of this task), which calls LaunchTeamRun then
// InstallTeamRunRouting in sequence, the same two-step composition this
// file's own tests use. This is a real, documented follow-up need (task 08
// itself is not changed to call this automatically) — flagged explicitly
// per this task's own Context instruction, not a silent retrofit.
//
// # Explicit @slot addressing — multi-member default (provisional, v1)
//
// 15-teams.md's own "What this session did not decide" list leaves
// multi-member slot addressing open on purpose: "route-to-one, broadcast,
// or address-a-specific-member are all plausible; no syntax or default is
// chosen here, deliberately, since real usage should inform it." SendToSlot
// (below) picks BROADCAST TO EVERY active-status member of the resolved
// Team Slot as its working default — a Team can't ship with `@engineer`
// simply undefined, but this is explicitly a provisional v1 default, not a
// closed answer to the design doc's own open question. A future revision
// may add route-to-one / address-a-specific-member syntax once real usage
// informs which default (if any) should change.
//
// # Routing-target resolution failure — chosen policy: (a) fail loudly
//
// 15-teams.md's second required stress test: "a message routes to a slot
// whose durable member is unavailable or whose fresh member fails to
// instantiate." This task's own file offers two options and recommends
// (a): the send fails loudly as a real tool-result-shaped error
// (ErrTeamRoutingTargetUnavailable, below), matching message_send's own
// per-call validation-error style, rather than (b) silently falling
// through to the coordinator-fallback routing rule. Chosen for the reason
// the task file itself gives: matches this codebase's "hints not control,
// but real gates stay real gates" posture, and avoids silently masking a
// real failure behind an automatic reroute a human/agent sender never
// asked for. resolveActiveMembers (below) is where this is implemented and
// tested (TestSendToSlot_TargetUnavailable_FailsLoudly) — a target whose
// team_run_members rows all carry status != 'active' returns
// ErrTeamRoutingTargetUnavailable directly; a target with zero rows at all
// (a genuinely dormant lazy slot) attempts ONE real lazy-resolution
// attempt (ResolveLazySlot, below) before applying the same failure
// policy, so "durable member unavailable" and "fresh member fails to
// instantiate" both funnel through the same, single tested failure path.
//
// Note this policy applies to explicit @slot addressing only — semantic/
// coordinator-fallback routing (dispatch_to_agent reflex rows,
// InstallTeamRunRouting below) does not resolve against team_run_members
// at firing time at all: task_execute's own existing dispatch pipeline
// (internal/service/chat_reflex_dispatch.go's attemptReflexDispatch) spawns
// a FRESH subagent session bound to whatever agent_slug the fired reflex's
// ActionSpec carries, independent of whether that Team Slot currently has
// an active team_run_members row. The routing-target-resolution-failure
// question is therefore structurally specific to explicit addressing
// (message_send to an existing, already-resolved tuple), not to semantic
// routing's own fresh-dispatch mechanism — see resolveAgentSlugForSlot's
// own doc comment for how a dormant slot still gets a valid agent_slug at
// install time.
//
// # ResolveLazySlot concurrency — real locking added at this call site
//
// Task 08's own fresh reviewer flagged ResolveLazySlot's check-then-act
// idempotency as a real race under true concurrency (two goroutines both
// seeing zero active members for the same dormant slot, both proceeding to
// resolve). This file's own call pattern IS genuinely concurrent — two
// SendToSlot calls addressing the same dormant Team Slot from two
// different in-flight chat turns (different goroutines) can plausibly race
// — so this task's own Context instruction applies: real locking is added
// HERE, at this call site, not assumed to already exist inside
// ResolveLazySlot itself. lockSlotResolution (below) is a real, in-process
// per-(workflow_run_id, slot_name) mutex (this whole service runs inside
// one nanite-api-service process — no cross-process race to protect
// against here); resolveActiveMembers re-checks team_run_members under the
// lock before ever calling ResolveLazySlot, so two racing callers can never
// both win the "zero active members" check and both proceed to resolve.
// See TestResolveActiveMembers_ConcurrentLazyResolution_OnlyResolvesOnce
// (team_routing_test.go), run with -race, for the regression coverage.
//
// # to_slot='self' sharp edge — how this file avoids it
//
// AuthorizedForVerb's to_slot='self' sentinel only correctly detects
// self-targeting when the caller passes the REAL resolved slot name as the
// toSlot argument, never the literal string "self" (internal/store/
// team_authority.go's own doc comment, flagged by Phase 1's reviewer).
// authorizedForMessage (below), this file's one real AuthorizedForVerb call
// site, always passes req.ToSlot — the real Team Slot name the caller is
// addressing — verbatim, never a sentinel string. This file never
// constructs or passes the literal "self" value anywhere.
//
// # Provenance tier for installed run-scoped reflex rows
//
// teamRoutingProvenanceTier below is "operator" — task 05's own documented
// choice (TASKS/teams/05-agent-reflexes-run-scoping.md's Work Log),
// confirmed here directly against ActionKindAllowsProvenanceTier for
// dispatch_to_agent (TestInsertTeamRoutingReflex_ProvenanceTierIsValid).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/hollis-labs/nanite/internal/messaging"
	"github.com/hollis-labs/nanite/internal/store"
)

// ErrTeamRoutingTargetUnavailable is returned by SendToSlot (via
// resolveActiveMembers) when the addressed Team Slot has no member that is
// currently active and no lazy-resolution attempt succeeded in producing
// one — this task's chosen answer to the routing-target-resolution-failure
// stress test: fail loudly, never silently reroute.
var ErrTeamRoutingTargetUnavailable = errors.New("team routing: target team slot has no available member")

// ErrTeamRoutingNotAuthorized is returned by SendToSlot when the Team has
// declared at least one may_message grant for the sending slot and the
// addressed target slot is not among the grant's allow-listed targets.
var ErrTeamRoutingNotAuthorized = errors.New("team routing: sender team slot is not authorized to message target team slot")

const (
	// teamRoutingProvenanceTier is applied to every run-scoped
	// agent_reflexes row InstallTeamRunRouting inserts — task 05's own
	// documented choice (see this file's package doc comment).
	teamRoutingProvenanceTier = "operator"

	// defaultCoordinatorSlotName is used when a Team's TeamRouting leaves
	// CoordinatorSlot empty — matches 15-teams.md's own SME example slot
	// name, overridable per-Team via TeamRouting.CoordinatorSlot.
	defaultCoordinatorSlotName = "orchestrator"

	// semanticRulePriorityBase/Step derive a run-scoped semantic routing
	// rule's agent_reflexes.priority from its declaration order in
	// TeamRouting.Rules (first rule = highest priority) when the rule
	// itself leaves TeamRoutingRule.Priority unset. Chosen well above the
	// highest priority any global (non-run-scoped) advisor-class
	// dispatch_to_agent seed uses today (internal/agent/reflexes/seeds.go's
	// migrated-promptrouter seeds top out at 60) so a Team's own
	// explicitly-declared routing reliably wins over a coincidental global
	// phrase match once a session is inside a TeamRun — matching task 05's
	// own documented "author a higher priority value" guidance
	// (internal/service/chat_reflex_dispatch.go's merge-ordering comment).
	semanticRulePriorityBase = 500
	semanticRulePriorityStep = 10

	// coordinatorFallbackPriority is the lowest-priority row in the same
	// scoped set (15-teams.md's "Coordinator fallback... the lowest-
	// priority row"), but still well above every global advisor-class
	// seed's own priority ceiling (60) — once inside a TeamRun, the Team's
	// own three-tier routing (explicit / semantic / coordinator fallback)
	// is meant to govern, not a coincidental global class-bound rule.
	coordinatorFallbackPriority = 100
)

// TeamRoutingService owns the real runtime pieces of TASKS/teams/
// 09-team-routing.md: explicit @slot addressing (SendToSlot) and
// semantic/coordinator-fallback routing installation (InstallTeamRunRouting).
// Mirrors TeamRunLauncher's own "struct + method, several real
// collaborators" shape (internal/service/team_run_launcher.go).
type TeamRoutingService struct {
	store     *store.Store
	messaging *messaging.Service
	// launcher is used to lazily resolve a dormant Team Slot the first
	// time SendToSlot addresses it and finds no active member yet — see
	// this file's own package doc comment on the ResolveLazySlot
	// concurrency fix. May be nil ONLY for a caller that never expects to
	// address a lazy/dormant slot (SendToSlot returns a clear error rather
	// than panicking if a lazy resolution is actually needed and launcher
	// is nil).
	launcher *TeamRunLauncher

	// slotResolveMu serializes the ResolveLazySlot check-then-act sequence
	// per (workflow_run_id, slot_name) — see this file's own package doc
	// comment. Keyed by runID+"\x00"+slotName; entries are never removed
	// (bounded by the number of distinct (run, slot) pairs this process
	// ever addresses in its lifetime — the same trade-off sync.Map's own
	// documentation describes as its intended use case).
	slotResolveMu sync.Map
}

// NewTeamRoutingService constructs a TeamRoutingService. launcher may be
// nil if this service will only ever address Team Slots that are already
// eagerly resolved (SendToSlot surfaces a clear error, not a panic, if a
// lazy resolution is attempted with no launcher configured).
func NewTeamRoutingService(st *store.Store, msg *messaging.Service, launcher *TeamRunLauncher) *TeamRoutingService {
	return &TeamRoutingService{store: st, messaging: msg, launcher: launcher}
}

// ---- Explicit @slot addressing ----

// SendToSlotRequest is SendToSlot's input — the resolved caller identity
// (FromSlot/FromSessionID/FromAgentID, already known to the calling turn,
// the same way message_send's own from_session_id/from_agent_id args are
// already known to its caller) plus the target Team Slot name and message
// body. Overrides is forwarded to ResolveLazySlot only when ToSlot needs a
// lazy resolution (see resolveActiveMembers).
type SendToSlotRequest struct {
	WorkflowRunID string
	TeamID        string

	FromSlot      string
	FromSessionID string
	FromAgentID   string

	ToSlot string

	Subject     string
	Body        string
	Type        string
	Kind        string
	Channel     string
	PayloadJSON string
	ReplyTo     string

	Overrides TeamRunOverrides
}

// SentMessage is one recipient's outcome within a SendToSlotResult — one
// entry per active member of the resolved target Team Slot (the broadcast
// default, see this file's package doc comment). Err is set (Message nil)
// when this specific recipient's own messaging.SendMessage call failed;
// SendToSlot itself only returns a top-level error when EVERY recipient
// failed.
type SentMessage struct {
	Member  store.TeamRunMember
	Message *messaging.Message
	Err     error
}

// SendToSlotResult is SendToSlot's return value.
type SendToSlotResult struct {
	ToSlot     string
	Recipients []SentMessage
}

// SendToSlot resolves req.ToSlot to its team_run_members tuple(s) and
// sends req's message to every ACTIVE member found — the broadcast-to-all
// multi-member default (this file's package doc comment). Enforces
// may_message when the Team has declared any may_message grant for
// req.FromSlot (see authorizedForMessage); otherwise transport stays
// generic, per 15-teams.md's own "the transport can and should stay
// generic" framing. Returns ErrTeamRoutingTargetUnavailable (never a
// silent coordinator-fallback reroute) when the target slot cannot be
// resolved to at least one active member — this task's chosen answer to
// the routing-target-resolution-failure stress test.
func (svc *TeamRoutingService) SendToSlot(ctx context.Context, req SendToSlotRequest) (*SendToSlotResult, error) {
	if svc == nil || svc.store == nil || svc.messaging == nil {
		return nil, fmt.Errorf("team routing: service not fully configured")
	}
	if req.WorkflowRunID == "" || req.TeamID == "" || req.FromSlot == "" || req.ToSlot == "" {
		return nil, fmt.Errorf("team routing: workflow_run_id, team_id, from_slot, and to_slot are all required")
	}
	if req.FromSessionID == "" || req.FromAgentID == "" {
		return nil, fmt.Errorf("team routing: from_session_id and from_agent_id are required")
	}
	if req.Body == "" {
		return nil, fmt.Errorf("team routing: body is required")
	}

	authorized, err := svc.authorizedForMessage(ctx, req.TeamID, req.FromSlot, req.ToSlot)
	if err != nil {
		return nil, fmt.Errorf("team routing: check may_message authorization: %w", err)
	}
	if !authorized {
		return nil, fmt.Errorf("%w: team slot %q -> team slot %q", ErrTeamRoutingNotAuthorized, req.FromSlot, req.ToSlot)
	}

	members, err := svc.resolveActiveMembers(ctx, req.WorkflowRunID, req.TeamID, req.ToSlot, req.Overrides)
	if err != nil {
		return nil, err
	}

	result := &SendToSlotResult{ToSlot: req.ToSlot, Recipients: make([]SentMessage, 0, len(members))}
	sentAny := false
	var lastErr error
	for _, m := range members {
		msg, sendErr := svc.messaging.SendMessage(ctx, messaging.SendInput{
			FromSessionID: req.FromSessionID,
			FromAgentID:   req.FromAgentID,
			ToSessionID:   m.SessionID,
			ToAgentID:     m.AgentID,
			Channel:       req.Channel,
			Kind:          req.Kind,
			PayloadJSON:   req.PayloadJSON,
			Subject:       req.Subject,
			Body:          req.Body,
			Type:          req.Type,
			ReplyTo:       req.ReplyTo,
		})
		if sendErr != nil {
			lastErr = sendErr
			result.Recipients = append(result.Recipients, SentMessage{Member: m, Err: sendErr})
			continue
		}
		sentAny = true
		result.Recipients = append(result.Recipients, SentMessage{Member: m, Message: msg})
	}
	if !sentAny {
		return result, fmt.Errorf("team routing: message send failed for every active member of team slot %q: %w", req.ToSlot, lastErr)
	}
	return result, nil
}

// authorizedForMessage reports whether fromSlot may message toSlot within
// teamID. Opt-in allow-listing, mirroring team_run_launcher.go's own
// authorizedForElasticResolution posture for may_spawn: presence of ANY
// may_message grant declared FOR fromSlot means messaging from that slot
// is now restricted to exactly its grant-listed targets (a real Team-
// authoring governance choice); absence of any such declared grant means
// unrestricted — 15-teams.md's own "may_message is one illustrative verb,
// not the root authority primitive... the transport can and should stay
// generic" instruction, applied literally: an ungoverned Team's transport
// stays generic by default, the same way an ungoverned Team has no gates
// at all and that's a legitimate shape.
//
// Always passes toSlot as the REAL resolved target Team Slot name, never
// the literal sentinel string "self" — see this file's package doc comment
// on AuthorizedForVerb's to_slot='self' sharp edge.
func (svc *TeamRoutingService) authorizedForMessage(ctx context.Context, teamID, fromSlot, toSlot string) (bool, error) {
	grants, err := svc.store.ListTeamAuthorityGrants(ctx, teamID)
	if err != nil {
		return false, fmt.Errorf("list team authority grants: %w", err)
	}
	governed := false
	for _, g := range grants {
		if g.Verb == store.TeamAuthorityVerbMayMessage && g.FromSlot == fromSlot {
			governed = true
			break
		}
	}
	if !governed {
		return true, nil
	}
	return svc.store.AuthorizedForVerb(ctx, teamID, fromSlot, store.TeamAuthorityVerbMayMessage, toSlot)
}

// resolveActiveMembers resolves slotName to its currently-active
// team_run_members row(s), attempting exactly one real lazy-resolution
// pass (via TeamRunLauncher.ResolveLazySlot, real-locked against the
// documented concurrency race) when the slot has never been resolved at
// all. Implements this task's chosen routing-target-resolution-failure
// policy — see this file's package doc comment for the full reasoning.
func (svc *TeamRoutingService) resolveActiveMembers(ctx context.Context, runID, teamID, slotName string, overrides TeamRunOverrides) ([]store.TeamRunMember, error) {
	existing, err := svc.store.ListTeamRunMembersBySlot(ctx, runID, slotName)
	if err != nil {
		return nil, fmt.Errorf("team routing: list members for team slot %q: %w", slotName, err)
	}
	if active := filterActiveMembers(existing); len(active) > 0 {
		return active, nil
	}
	if len(existing) > 0 {
		// At least one member was resolved for this slot at some point,
		// but none is currently active: a durable member that's no longer
		// resumable, or a fresh member whose prior resolution attempt was
		// marked failed/replaced/stopped. Fail loudly (chosen policy (a)),
		// no retry, no silent fallback.
		return nil, fmt.Errorf("%w: team slot %q has %d resolved member(s), none active",
			ErrTeamRoutingTargetUnavailable, slotName, len(existing))
	}

	// Zero rows at all — a genuinely dormant lazy slot, potentially its
	// first-ever touch. Real concurrent-capable call path into
	// ResolveLazySlot (see this file's package doc comment) — serialize
	// per (runID, slotName) and re-check under the lock before calling it,
	// so two callers racing to address the same dormant slot can never
	// both observe "zero rows" and both proceed to resolve.
	unlock := svc.lockSlotResolution(runID, slotName)
	defer unlock()

	existing, err = svc.store.ListTeamRunMembersBySlot(ctx, runID, slotName)
	if err != nil {
		return nil, fmt.Errorf("team routing: list members for team slot %q (post-lock re-check): %w", slotName, err)
	}
	if active := filterActiveMembers(existing); len(active) > 0 {
		return active, nil // another caller won the race while this one waited for the lock
	}
	if len(existing) > 0 {
		return nil, fmt.Errorf("%w: team slot %q has %d resolved member(s), none active",
			ErrTeamRoutingTargetUnavailable, slotName, len(existing))
	}

	if svc.launcher == nil {
		return nil, fmt.Errorf("team routing: team slot %q is dormant and no TeamRunLauncher is configured to lazily resolve it", slotName)
	}
	newly, err := svc.launcher.ResolveLazySlot(ctx, runID, teamID, slotName, overrides)
	if err != nil {
		// "fresh member fails to instantiate" — the lazy-resolution
		// attempt itself errored (e.g. no agent bound to the slot's role,
		// a durable wake failure, an unauthorized elastic resolution).
		return nil, fmt.Errorf("%w: lazy resolution of team slot %q failed: %v", ErrTeamRoutingTargetUnavailable, slotName, err)
	}
	active := filterActiveMembers(newly)
	if len(active) == 0 {
		return nil, fmt.Errorf("%w: lazy resolution of team slot %q produced no active member", ErrTeamRoutingTargetUnavailable, slotName)
	}
	return active, nil
}

// lockSlotResolution returns an unlock function for the per-(runID,
// slotName) mutex, blocking until acquired. See this file's package doc
// comment on the ResolveLazySlot concurrency fix.
func (svc *TeamRoutingService) lockSlotResolution(runID, slotName string) func() {
	key := runID + "\x00" + slotName
	v, _ := svc.slotResolveMu.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func filterActiveMembers(members []store.TeamRunMember) []store.TeamRunMember {
	out := make([]store.TeamRunMember, 0, len(members))
	for _, m := range members {
		if m.Status == store.TeamRunMemberStatusActive {
			out = append(out, m)
		}
	}
	return out
}

// ---- Semantic / coordinator-fallback routing installation ----

// InstallTeamRunRouting installs teamID's Team-authored semantic routing
// rules (team.Routing().Rules) plus one structural coordinator-fallback
// row, as run-scoped (workflow_run_id = runID) agent_reflexes rows —
// 15-teams.md's "Routing: real reuse, and one real gap" section's
// semantic/coordinator-fallback tiers. Must be called AFTER
// TeamRunLauncher.LaunchTeamRun has returned a real runID (team_run_members
// rows for this run must already exist for eagerly-resolved slots) — see
// this file's package doc comment for the full wiring-boundary reasoning.
//
// One reflex row is inserted per (routing rule ∪ coordinator fallback) ×
// (distinct resolved agent_id currently active in this run) — see
// ListAgentReflexesForWorkflowRun's own doc comment (internal/store/
// agent_reflexes.go) for why: its WHERE clause matches a run-scoped row
// either by (agent_id IS NULL AND class_tag = caller's class) or by
// agent_id = the caller's own agent_id, and Team Slot members can span
// different classes, so an agent_id-scoped row per asking member is the
// only shape that reliably applies "whichever Team member asks" —
// class-tag scoping was considered and rejected for exactly this reason.
// Deduplicated by agent_id first (a concurrent slot's members share one
// agent_id across distinct sessions, per resolveFreshMember's own doc
// comment) so a concurrent slot doesn't produce redundant identical rows.
//
// Known limitation, documented rather than silently assumed away: a Team
// Slot resolved lazily AFTER this call (e.g. the architect SME, first
// addressed mid-run) does not retroactively get an agent_id-scoped routing
// row of its own — the architect's OWN future turns won't see this Team's
// routing rules as candidates until a future revision extends this
// mechanism to also install rows at ResolveLazySlot time. Out of this
// task's own scope (not required by "What to do"), and does not affect
// this run's ALREADY-eager members (orchestrator/engineer/reviewer in the
// SME example) or the ability to route TO a dormant slot (semantic
// routing's ActionSpec.agent_slug for a dormant target slot is resolved
// via resolveAgentSlugForSlot's static-config fallback, independent of
// whether the slot has ever been resolved as a team_run_members row).
func (svc *TeamRoutingService) InstallTeamRunRouting(ctx context.Context, runID, teamID string) ([]string, error) {
	if svc == nil || svc.store == nil {
		return nil, fmt.Errorf("team routing: service not fully configured")
	}
	if runID == "" || teamID == "" {
		return nil, fmt.Errorf("team routing: workflow_run_id and team_id are required")
	}

	team, err := svc.store.GetTeam(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("team routing: load team %s: %w", teamID, err)
	}
	slots, err := team.Slots()
	if err != nil {
		return nil, fmt.Errorf("team routing: decode team %q slots: %w", team.Name, err)
	}
	routing, err := team.Routing()
	if err != nil {
		return nil, fmt.Errorf("team routing: decode team %q routing: %w", team.Name, err)
	}

	runMembers, err := svc.store.ListTeamRunMembersByRun(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("team routing: list team_run_members for run %s: %w", runID, err)
	}
	askers := dedupeActiveAgentIDs(runMembers)
	if len(askers) == 0 {
		return nil, fmt.Errorf("team routing: run %s has no active team_run_members to install routing for", runID)
	}

	var installed []string

	for i, rule := range routing.Rules {
		targetSlot, ok := findSlot(slots, rule.TargetSlot)
		if !ok {
			return installed, fmt.Errorf("team routing: rule %q targets unknown team slot %q", rule.Name, rule.TargetSlot)
		}
		agentSlug, err := svc.resolveAgentSlugForSlot(ctx, runID, targetSlot)
		if err != nil {
			return installed, fmt.Errorf("team routing: resolve agent_slug for rule %q (target %q): %w", rule.Name, rule.TargetSlot, err)
		}
		priority := rule.Priority
		if priority <= 0 {
			priority = semanticRulePriorityBase - int64(i)*semanticRulePriorityStep
			if priority <= coordinatorFallbackPriority {
				priority = coordinatorFallbackPriority + 1
			}
		}
		triggerSpec := map[string]any{
			"kind":    "user_regex_window",
			"window":  1,
			"pattern": anyPhraseRegex(rule.Phrases),
		}
		reason := fmt.Sprintf("team routing rule %q -> %s (team %q)", rule.Name, targetSlot.Name, team.Name)
		for _, agentID := range askers {
			id, err := svc.insertTeamRoutingReflex(ctx, runID, team, agentID, rule.Name, triggerSpec, priority, targetSlot.Name, agentSlug, reason)
			if err != nil {
				return installed, err
			}
			installed = append(installed, id)
		}
	}

	// Coordinator fallback — structural, always attempted once per Team
	// (not authored per-rule in RoutingJSON), per 15-teams.md's "add one
	// additional lowest-priority row per Team for the coordinator
	// fallback." Skipped (non-fatal, logged) only when the coordinator
	// slot name doesn't resolve to a real Team Slot on this Team — a Team
	// author is not required to name its coordinator "orchestrator" or to
	// have a coordinator slot at all (a fully fluid Team, matching
	// 15-teams.md's own "a Team is not required to declare any gates at
	// all" framing extended to routing).
	coordinatorSlotName := routing.CoordinatorSlot
	if coordinatorSlotName == "" {
		coordinatorSlotName = defaultCoordinatorSlotName
	}
	coordinatorSlot, ok := findSlot(slots, coordinatorSlotName)
	if !ok {
		slog.Warn("team routing: coordinator slot not found on team, skipping coordinator fallback row",
			"team", team.Name, "coordinator_slot", coordinatorSlotName)
		return installed, nil
	}
	agentSlug, err := svc.resolveAgentSlugForSlot(ctx, runID, coordinatorSlot)
	if err != nil {
		slog.Warn("team routing: could not resolve coordinator fallback agent_slug, skipping fallback row",
			"team", team.Name, "coordinator_slot", coordinatorSlotName, "err", err)
		return installed, nil
	}
	triggerSpec := map[string]any{"kind": "user_regex_window", "window": 1, "pattern": ".*"}
	reason := fmt.Sprintf("team coordinator fallback -> %s (team %q)", coordinatorSlot.Name, team.Name)
	for _, agentID := range askers {
		id, err := svc.insertTeamRoutingReflex(ctx, runID, team, agentID, "coordinator_fallback", triggerSpec, coordinatorFallbackPriority, coordinatorSlot.Name, agentSlug, reason)
		if err != nil {
			return installed, err
		}
		installed = append(installed, id)
	}

	return installed, nil
}

// insertTeamRoutingReflex writes one run-scoped dispatch_to_agent
// agent_reflexes row. ActionSpec carries the standard {agent_slug,
// confidence, reason} shape (internal/store/agent_reflexes.go's documented
// ActionSpec doc comment, internal/agent/reflexes/seeds.go's
// dispatch_to_agent_open_subagent example) plus one additive field,
// team_target_slot, this task's own provenance-trace enrichment — the
// Team Slot name this firing resolves to, so a provenance walk (event_log
// metadata -> team_target_slot -> team_run_members) needs no second lookup
// by agent_slug. Extra ActionSpec keys are inert to every existing
// dispatch_to_agent consumer (chat_reflex_dispatch.go/self_tools_dispatch.go
// only ever read agent_slug/confidence/reason) and pass straight through
// into EmitFirings' own traceRecord.Spec.
func (svc *TeamRoutingService) insertTeamRoutingReflex(ctx context.Context, runID string, team *store.Team, agentID, ruleName string, triggerSpec map[string]any, priority int64, targetSlotName, agentSlug, reason string) (string, error) {
	triggerJSON, err := json.Marshal(triggerSpec)
	if err != nil {
		return "", fmt.Errorf("team routing: marshal trigger_spec: %w", err)
	}
	actionSpec := map[string]any{
		"agent_slug":       agentSlug,
		"confidence":       1.0,
		"reason":           reason,
		"team_target_slot": targetSlotName,
	}
	actionJSON, err := json.Marshal(actionSpec)
	if err != nil {
		return "", fmt.Errorf("team routing: marshal action_spec: %w", err)
	}
	id, err := svc.store.InsertAgentReflex(ctx, store.AgentReflex{
		AgentID:        agentID,
		Name:           fmt.Sprintf("team-routing:%s:%s:%s", team.Name, ruleName, agentID),
		TriggerKind:    store.ReflexTriggerPredicate,
		TriggerSpec:    string(triggerJSON),
		ActionKind:     store.ReflexActionDispatchToAgent,
		ActionSpec:     string(actionJSON),
		Priority:       priority,
		CreatedBy:      "operator",
		ProvenanceTier: teamRoutingProvenanceTier,
		WorkflowRunID:  runID,
	})
	if err != nil {
		return "", fmt.Errorf("team routing: insert run-scoped reflex for rule %q (agent %s): %w", ruleName, agentID, err)
	}
	return id, nil
}

// resolveAgentSlugForSlot resolves the agent_profiles.slug a run-scoped
// dispatch_to_agent reflex's ActionSpec.agent_slug should carry for slot,
// preferring an actually-resolved, active team_run_members row ("the
// target slot's currently-resolved member", this task's own item 2
// wording) and falling back to the Team Slot's own static configuration
// (durable AgentID's profile, or the first agent bound to a fresh slot's
// RoleSlug — the same lookups resolveDurableMember/resolveFreshMember make
// in team_run_launcher.go, read-only here, no session/durable-wake side
// effect) when the slot has no active member yet. This is what makes a
// semantic routing rule targeting a normally-dormant slot (the architect
// SME) installable and immediately actionable at launch time, without
// forcing an eager resolution that would defeat the slot's own
// required:false/"normally dormant" configuration.
func (svc *TeamRoutingService) resolveAgentSlugForSlot(ctx context.Context, runID string, slot store.TeamSlotDefinition) (string, error) {
	members, err := svc.store.ListTeamRunMembersBySlot(ctx, runID, slot.Name)
	if err != nil {
		return "", fmt.Errorf("list members for team slot %q: %w", slot.Name, err)
	}
	for _, m := range members {
		if m.Status == store.TeamRunMemberStatusActive {
			profile, err := svc.store.GetAgent(m.AgentID)
			if err != nil {
				return "", fmt.Errorf("look up resolved agent %s for team slot %q: %w", m.AgentID, slot.Name, err)
			}
			return profile.Slug, nil
		}
	}

	switch slot.Resolution {
	case "durable":
		if slot.AgentID == nil || *slot.AgentID == "" {
			return "", fmt.Errorf("team slot %q: resolution=durable requires agent_id", slot.Name)
		}
		profile, err := svc.store.GetAgent(*slot.AgentID)
		if err != nil {
			return "", fmt.Errorf("team slot %q: durable agent_id %q: %w", slot.Name, *slot.AgentID, err)
		}
		return profile.Slug, nil
	case "fresh":
		if slot.RoleSlug == "" {
			return "", fmt.Errorf("team slot %q: resolution=fresh requires role_slug", slot.Name)
		}
		role, err := svc.store.GetRoleBySlug(slot.RoleSlug)
		if err != nil {
			return "", fmt.Errorf("team slot %q: look up role %q: %w", slot.Name, slot.RoleSlug, err)
		}
		if role == nil {
			return "", fmt.Errorf("team slot %q: role_slug %q does not exist", slot.Name, slot.RoleSlug)
		}
		profiles, err := svc.store.ListAgentsByRoleID(role.ID)
		if err != nil {
			return "", fmt.Errorf("team slot %q: list agents for role %q: %w", slot.Name, role.Slug, err)
		}
		if len(profiles) == 0 {
			return "", fmt.Errorf("team slot %q: no agent_profiles row bound to role %q", slot.Name, role.Slug)
		}
		return profiles[0].Slug, nil
	default:
		return "", fmt.Errorf("team slot %q: unknown resolution %q", slot.Name, slot.Resolution)
	}
}

// dedupeActiveAgentIDs returns the distinct, sorted (deterministic order)
// set of agent_id values among members' ACTIVE rows — a concurrent Team
// Slot's members share one resolved agent_id across distinct sessions
// (team_run_launcher.go's resolveFreshMember doc comment), so this collapses
// them to one routing-reflex install per real asking identity.
func dedupeActiveAgentIDs(members []store.TeamRunMember) []string {
	seen := make(map[string]bool, len(members))
	out := make([]string, 0, len(members))
	for _, m := range members {
		if m.Status != store.TeamRunMemberStatusActive {
			continue
		}
		if seen[m.AgentID] {
			continue
		}
		seen[m.AgentID] = true
		out = append(out, m.AgentID)
	}
	sort.Strings(out)
	return out
}

// anyPhraseRegex builds a case-insensitive alternation regex matching any
// of phrases — a local, deliberate copy of internal/agent/reflexes/
// seeds.go's own unexported helper of the same shape (that package's own
// version is unexported and this file has no other reason to import
// internal/agent/reflexes beyond the small State/Executor/Resolve surface
// its own tests already use — duplicating one four-line helper is cheaper
// than exporting it across a package boundary for one caller).
func anyPhraseRegex(phrases []string) string {
	quoted := make([]string, len(phrases))
	for i, p := range phrases {
		quoted[i] = regexp.QuoteMeta(p)
	}
	return "(?i)(" + strings.Join(quoted, "|") + ")"
}
