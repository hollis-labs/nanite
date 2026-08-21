package service

// TASKS/teams/09-team-routing.md's own regression coverage.
//
// Live-verification safety (EXECUTION-PROCESS.md): every test below builds
// its own t.TempDir()-rooted SQLite store via newTeamRunLauncherTestFixtures
// (already shared across this whole batch's test files, team_run_launcher_
// test.go) — no real tracked file, no real production database, is ever
// touched by anything in this file.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/messaging"
	"github.com/hollis-labs/nanite/internal/store"
)

// --- fixtures ---

// storeAgentResolver adapts *store.Store's non-ctx GetAgent to
// messaging.AgentResolver's ctx-taking Get — the same adapter shape
// container.go's own AgentService provides in production; minimal here
// since these tests need no other AgentService behavior.
type storeAgentResolver struct{ st *store.Store }

func (r storeAgentResolver) Get(_ context.Context, id string) (*store.AgentProfile, error) {
	return r.st.GetAgent(id)
}

// newTestMessagingService builds a real *messaging.Service against st's
// own underlying *sql.DB — mirrors container.go's own construction
// (messaging.NewSQLiteStore + messaging.NewService), using st itself as
// both AgentResolver (via storeAgentResolver) and AgentRegistrar (st.
// CreateAgent already matches messaging.AgentRegistrar's signature
// directly).
func newTestMessagingService(t *testing.T, st *store.Store) *messaging.Service {
	t.Helper()
	msgStore := messaging.NewSQLiteStore(st.DB)
	return messaging.NewService(msgStore, st.DB, storeAgentResolver{st}, st)
}

// newTeamRoutingTestFixtures wires a full stack: store, TeamRunLauncher
// (task 08, reused verbatim), messaging.Service, and the TeamRoutingService
// under test — all sharing the SAME store.
func newTeamRoutingTestFixtures(t *testing.T) (*store.Store, *TeamRunLauncher, *TeamRoutingService) {
	t.Helper()
	st, _, trl := newTeamRunLauncherTestFixtures(t)
	msg := newTestMessagingService(t, st)
	rt := NewTeamRoutingService(st, msg, trl)
	return st, trl, rt
}

// launchSMETeamRun launches buildSMETeam's own fixture against st/trl and
// returns the real workflow_run_id — the shared starting point for most
// tests below.
func launchSMETeamRun(t *testing.T, st *store.Store, trl *TeamRunLauncher, overrides TeamRunOverrides) (*store.Team, string) {
	t.Helper()
	ctx := context.Background()
	createTestRoleBoundAgent(t, st, "orchestrator")
	createTestRoleBoundAgent(t, st, "engineer")
	createTestRoleBoundAgent(t, st, "code-reviewer")
	architect := createTestDurableCandidateAgent(t, st, "nanite-architect")
	team := buildSMETeam(t, st, architect.ID)

	result, err := trl.LaunchTeamRun(ctx, team.ID, overrides)
	if err != nil {
		t.Fatalf("LaunchTeamRun: %v", err)
	}
	return team, result.RunID
}

// --- explicit @slot addressing tests ---

func TestSendToSlot_BroadcastsToEveryActiveMember(t *testing.T) {
	st, trl, rt := newTeamRoutingTestFixtures(t)
	ctx := context.Background()
	team, runID := launchSMETeamRun(t, st, trl, TeamRunOverrides{SlotMemberCounts: map[string]int{"engineer": 3}})

	orch, err := st.ListTeamRunMembersBySlot(ctx, runID, "orchestrator")
	if err != nil || len(orch) != 1 {
		t.Fatalf("orchestrator members: %v, err=%v", orch, err)
	}

	result, err := rt.SendToSlot(ctx, SendToSlotRequest{
		WorkflowRunID: runID,
		TeamID:        team.ID,
		FromSlot:      "orchestrator",
		FromSessionID: orch[0].SessionID,
		FromAgentID:   orch[0].AgentID,
		ToSlot:        "engineer",
		Body:          "status check",
	})
	if err != nil {
		t.Fatalf("SendToSlot: %v", err)
	}
	if len(result.Recipients) != 3 {
		t.Fatalf("recipients = %d, want 3 (broadcast to every active engineer member)", len(result.Recipients))
	}
	seenSessions := map[string]bool{}
	for _, r := range result.Recipients {
		if r.Err != nil {
			t.Fatalf("recipient send error: %v", r.Err)
		}
		if r.Message == nil {
			t.Fatal("recipient message is nil with no error")
		}
		if r.Message.ToSessionID != r.Member.SessionID || r.Message.ToAgentID != r.Member.AgentID {
			t.Fatalf("message tuple mismatch: message=(%s,%s) member=(%s,%s)",
				r.Message.ToSessionID, r.Message.ToAgentID, r.Member.SessionID, r.Member.AgentID)
		}
		if seenSessions[r.Message.ToSessionID] {
			t.Fatalf("duplicate recipient session %q", r.Message.ToSessionID)
		}
		seenSessions[r.Message.ToSessionID] = true
	}
}

func TestSendToSlot_DormantDurableSlot_LazilyResolvesThenSends(t *testing.T) {
	st, trl, rt := newTeamRoutingTestFixtures(t)
	ctx := context.Background()
	team, runID := launchSMETeamRun(t, st, trl, TeamRunOverrides{})

	// architect is required:false, never resolved at launch (per task 08's
	// own SME test — see TestLaunchTeamRun_SMEExample_ReachesFirstFlexStepWaiting).
	before, err := st.ListTeamRunMembersBySlot(ctx, runID, "architect")
	if err != nil || len(before) != 0 {
		t.Fatalf("architect members before send = %v (err=%v), want 0", before, err)
	}

	// buildSMETeam's own authority grants never name "architect" for
	// may_spawn — this test needs its own grant so ResolveLazySlot's
	// authorization check passes and we reach the real lazy-resolution
	// mechanism (not an authorization rejection).
	if _, err := st.CreateTeamAuthorityGrant(ctx, store.TeamAuthorityGrant{
		TeamID: team.ID, FromSlot: "orchestrator", Verb: store.TeamAuthorityVerbMaySpawn, ToSlot: "architect",
	}); err != nil {
		t.Fatalf("CreateTeamAuthorityGrant: %v", err)
	}

	orch, _ := st.ListTeamRunMembersBySlot(ctx, runID, "orchestrator")
	result, err := rt.SendToSlot(ctx, SendToSlotRequest{
		WorkflowRunID: runID,
		TeamID:        team.ID,
		FromSlot:      "orchestrator",
		FromSessionID: orch[0].SessionID,
		FromAgentID:   orch[0].AgentID,
		ToSlot:        "architect",
		Body:          "quick design question",
	})
	if err != nil {
		t.Fatalf("SendToSlot: %v", err)
	}
	if len(result.Recipients) != 1 {
		t.Fatalf("recipients = %d, want 1", len(result.Recipients))
	}

	after, err := st.ListTeamRunMembersBySlot(ctx, runID, "architect")
	if err != nil || len(after) != 1 {
		t.Fatalf("architect members after send = %v (err=%v), want exactly 1 (lazily resolved)", after, err)
	}
	if after[0].Status != store.TeamRunMemberStatusActive {
		t.Fatalf("architect member status = %q, want active", after[0].Status)
	}
}

// TestSendToSlot_TargetUnavailable_NoActiveMember_FailsLoudly is this
// task's required routing-target-resolution-failure regression: a target
// whose team_run_members rows exist but none carry status='active'.
func TestSendToSlot_TargetUnavailable_NoActiveMember_FailsLoudly(t *testing.T) {
	st, trl, rt := newTeamRoutingTestFixtures(t)
	ctx := context.Background()
	team, runID := launchSMETeamRun(t, st, trl, TeamRunOverrides{})

	reviewer, err := st.ListTeamRunMembersBySlot(ctx, runID, "reviewer")
	if err != nil || len(reviewer) != 1 {
		t.Fatalf("reviewer members: %v, err=%v", reviewer, err)
	}
	// Simulate "durable member is unavailable" — the reviewer's only
	// resolved member is marked failed, matching this task's Done-means
	// instruction ("a target whose team_run_members.status != 'active'").
	if err := st.UpdateTeamRunMemberStatus(ctx, reviewer[0].ID, store.TeamRunMemberStatusFailed); err != nil {
		t.Fatalf("UpdateTeamRunMemberStatus: %v", err)
	}

	orch, _ := st.ListTeamRunMembersBySlot(ctx, runID, "orchestrator")
	_, err = rt.SendToSlot(ctx, SendToSlotRequest{
		WorkflowRunID: runID,
		TeamID:        team.ID,
		FromSlot:      "orchestrator",
		FromSessionID: orch[0].SessionID,
		FromAgentID:   orch[0].AgentID,
		ToSlot:        "reviewer",
		Body:          "please review",
	})
	if err == nil {
		t.Fatal("SendToSlot succeeded, want ErrTeamRoutingTargetUnavailable")
	}
	if !errors.Is(err, ErrTeamRoutingTargetUnavailable) {
		t.Fatalf("err = %v, want wrapping ErrTeamRoutingTargetUnavailable", err)
	}

	// No silent reroute: still exactly one (failed) reviewer row, no new
	// member conjured up to route around the failure.
	after, err := st.ListTeamRunMembersBySlot(ctx, runID, "reviewer")
	if err != nil || len(after) != 1 {
		t.Fatalf("reviewer members after failed send = %v (err=%v), want exactly 1 (still failed, no silent recovery)", after, err)
	}
}

// TestSendToSlot_TargetUnavailable_LazyResolutionFails covers the other
// half of the same stress test: "fresh member fails to instantiate" — a
// dormant slot whose own configuration cannot actually be resolved.
func TestSendToSlot_TargetUnavailable_LazyResolutionFails(t *testing.T) {
	st, trl, rt := newTeamRoutingTestFixtures(t)
	ctx := context.Background()

	createTestRoleBoundAgent(t, st, "lead")
	slots := []store.TeamSlotDefinition{
		{Name: "lead", RoleSlug: "lead", Resolution: "fresh", ActivationMode: "singleton", Required: true},
		// "ghost" names a role_slug with NO agent_profiles row bound to it
		// at all — resolveFreshMember (and therefore ResolveLazySlot)
		// fails to instantiate a member for it.
		{Name: "ghost", RoleSlug: "ghost-role-does-not-exist", Resolution: "fresh", ActivationMode: "singleton", Required: false},
	}
	phases := []store.TeamPhase{
		{ID: "work", Kind: "flex", ActiveSlots: []string{"lead"}, ExitTrigger: map[string]any{"event": "done"}},
	}
	team := &store.Team{Name: "Ghost Slot Team"}
	if err := team.SetSlots(slots); err != nil {
		t.Fatalf("SetSlots: %v", err)
	}
	if err := team.SetPhases(phases); err != nil {
		t.Fatalf("SetPhases: %v", err)
	}
	if err := st.CreateTeam(ctx, team); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if _, err := st.CreateTeamAuthorityGrant(ctx, store.TeamAuthorityGrant{
		TeamID: team.ID, FromSlot: "lead", Verb: store.TeamAuthorityVerbMaySpawn, ToSlot: "ghost",
	}); err != nil {
		t.Fatalf("CreateTeamAuthorityGrant: %v", err)
	}

	result, err := trl.LaunchTeamRun(ctx, team.ID, TeamRunOverrides{})
	if err != nil {
		t.Fatalf("LaunchTeamRun: %v", err)
	}

	lead, err := st.ListTeamRunMembersBySlot(ctx, result.RunID, "lead")
	if err != nil || len(lead) != 1 {
		t.Fatalf("lead members: %v, err=%v", lead, err)
	}

	_, err = rt.SendToSlot(ctx, SendToSlotRequest{
		WorkflowRunID: result.RunID,
		TeamID:        team.ID,
		FromSlot:      "lead",
		FromSessionID: lead[0].SessionID,
		FromAgentID:   lead[0].AgentID,
		ToSlot:        "ghost",
		Body:          "hello?",
	})
	if err == nil {
		t.Fatal("SendToSlot succeeded, want ErrTeamRoutingTargetUnavailable (lazy resolution should fail)")
	}
	if !errors.Is(err, ErrTeamRoutingTargetUnavailable) {
		t.Fatalf("err = %v, want wrapping ErrTeamRoutingTargetUnavailable", err)
	}
}

// --- may_message authority tests ---

func TestAuthorizedForMessage_UngovernedSlot_Allowed(t *testing.T) {
	st, _, rt := newTeamRoutingTestFixtures(t)
	ctx := context.Background()
	team := &store.Team{Name: "Ungoverned"}
	if err := st.CreateTeam(ctx, team); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	ok, err := rt.authorizedForMessage(ctx, team.ID, "engineer", "architect")
	if err != nil {
		t.Fatalf("authorizedForMessage: %v", err)
	}
	if !ok {
		t.Fatal("expected true for a Team with no may_message grants at all — transport stays generic")
	}
}

func TestAuthorizedForMessage_GovernedSlot_DeniesUnlistedTarget(t *testing.T) {
	st, _, rt := newTeamRoutingTestFixtures(t)
	ctx := context.Background()
	team := &store.Team{Name: "Governed"}
	if err := st.CreateTeam(ctx, team); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if _, err := st.CreateTeamAuthorityGrant(ctx, store.TeamAuthorityGrant{
		TeamID: team.ID, FromSlot: "engineer", Verb: store.TeamAuthorityVerbMayMessage, ToSlot: "architect",
	}); err != nil {
		t.Fatalf("CreateTeamAuthorityGrant: %v", err)
	}

	ok, err := rt.authorizedForMessage(ctx, team.ID, "engineer", "architect")
	if err != nil || !ok {
		t.Fatalf("authorizedForMessage(architect) = %v, %v; want true, nil", ok, err)
	}

	ok, err = rt.authorizedForMessage(ctx, team.ID, "engineer", "reviewer")
	if err != nil {
		t.Fatalf("authorizedForMessage(reviewer): %v", err)
	}
	if ok {
		t.Fatal("expected false: engineer's declared may_message grants don't name reviewer")
	}
}

// TestSendToSlot_NotAuthorized_RejectsSend confirms SendToSlot itself
// (not just the authorizedForMessage helper in isolation) surfaces the
// authorization denial as a real error and never sends anything.
func TestSendToSlot_NotAuthorized_RejectsSend(t *testing.T) {
	st, trl, rt := newTeamRoutingTestFixtures(t)
	ctx := context.Background()
	team, runID := launchSMETeamRun(t, st, trl, TeamRunOverrides{})

	if _, err := st.CreateTeamAuthorityGrant(ctx, store.TeamAuthorityGrant{
		TeamID: team.ID, FromSlot: "engineer", Verb: store.TeamAuthorityVerbMayMessage, ToSlot: "orchestrator",
	}); err != nil {
		t.Fatalf("CreateTeamAuthorityGrant: %v", err)
	}

	eng, err := st.ListTeamRunMembersBySlot(ctx, runID, "engineer")
	if err != nil || len(eng) != 1 {
		t.Fatalf("engineer members: %v, err=%v", eng, err)
	}

	_, err = rt.SendToSlot(ctx, SendToSlotRequest{
		WorkflowRunID: runID,
		TeamID:        team.ID,
		FromSlot:      "engineer",
		FromSessionID: eng[0].SessionID,
		FromAgentID:   eng[0].AgentID,
		ToSlot:        "reviewer", // NOT in engineer's declared may_message grant list
		Body:          "hey reviewer",
	})
	if err == nil || !errors.Is(err, ErrTeamRoutingNotAuthorized) {
		t.Fatalf("err = %v, want wrapping ErrTeamRoutingNotAuthorized", err)
	}
}

// TestAuthorizedForMessage_SelfSentinel_ResolvesCorrectly is this task's
// required regression for AuthorizedForVerb's to_slot='self' sharp edge:
// a stored to_slot='self' grant must only match true self-targeting
// (fromSlot == the real toSlot passed), never every call regardless of
// target, and never fail to match genuine self-targeting either.
func TestAuthorizedForMessage_SelfSentinel_ResolvesCorrectly(t *testing.T) {
	st, _, rt := newTeamRoutingTestFixtures(t)
	ctx := context.Background()
	team := &store.Team{Name: "Self Sentinel"}
	if err := st.CreateTeam(ctx, team); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if _, err := st.CreateTeamAuthorityGrant(ctx, store.TeamAuthorityGrant{
		TeamID: team.ID, FromSlot: "reviewer", Verb: store.TeamAuthorityVerbMayMessage, ToSlot: store.TeamAuthoritySelfSlot,
	}); err != nil {
		t.Fatalf("CreateTeamAuthorityGrant: %v", err)
	}

	// reviewer -> reviewer (real self-targeting): the self-sentinel grant
	// should match.
	ok, err := rt.authorizedForMessage(ctx, team.ID, "reviewer", "reviewer")
	if err != nil || !ok {
		t.Fatalf("authorizedForMessage(reviewer->reviewer) = %v, %v; want true, nil", ok, err)
	}

	// reviewer -> engineer (NOT self-targeting): this call site always
	// passes the real resolved target slot name ("engineer"), never the
	// literal string "self" — the self-sentinel grant must NOT match.
	ok, err = rt.authorizedForMessage(ctx, team.ID, "reviewer", "engineer")
	if err != nil {
		t.Fatalf("authorizedForMessage(reviewer->engineer): %v", err)
	}
	if ok {
		t.Fatal("a to_slot='self' grant must not authorize messaging a DIFFERENT slot — this is the to_slot='self' sharp edge this call site must avoid")
	}
}

// --- ResolveLazySlot concurrency fix ---

// TestResolveActiveMembers_ConcurrentLazyResolution_OnlyResolvesOnce is
// this task's required regression for the ResolveLazySlot check-then-act
// race task 08's own reviewer flagged: N goroutines racing to address the
// same dormant Team Slot must produce exactly ONE resolved active member,
// never N. Run with `go test -race` to also confirm no data race.
func TestResolveActiveMembers_ConcurrentLazyResolution_OnlyResolvesOnce(t *testing.T) {
	st, trl, rt := newTeamRoutingTestFixtures(t)
	ctx := context.Background()
	team, runID := launchSMETeamRun(t, st, trl, TeamRunOverrides{})

	if _, err := st.CreateTeamAuthorityGrant(ctx, store.TeamAuthorityGrant{
		TeamID: team.ID, FromSlot: "orchestrator", Verb: store.TeamAuthorityVerbMaySpawn, ToSlot: "architect",
	}); err != nil {
		t.Fatalf("CreateTeamAuthorityGrant: %v", err)
	}

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := rt.resolveActiveMembers(ctx, runID, team.ID, "architect", TeamRunOverrides{})
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: resolveActiveMembers: %v", i, err)
		}
	}

	after, err := st.ListTeamRunMembersBySlot(ctx, runID, "architect")
	if err != nil {
		t.Fatalf("ListTeamRunMembersBySlot: %v", err)
	}
	active := filterActiveMembers(after)
	if len(after) != 1 || len(active) != 1 {
		t.Fatalf("architect members after %d concurrent resolveActiveMembers calls = %d (active=%d), want exactly 1 — the check-then-act race must not double-resolve a dormant slot",
			n, len(after), len(active))
	}
}

// --- semantic / coordinator-fallback routing installation ---

func addRoutingToTeam(t *testing.T, st *store.Store, team *store.Team, routing store.TeamRouting) {
	t.Helper()
	if err := team.SetRouting(routing); err != nil {
		t.Fatalf("SetRouting: %v", err)
	}
	if err := st.UpdateTeam(context.Background(), team); err != nil {
		t.Fatalf("UpdateTeam: %v", err)
	}
}

func TestInstallTeamRunRouting_UnknownTargetSlot_Errors(t *testing.T) {
	st, trl, rt := newTeamRoutingTestFixtures(t)
	ctx := context.Background()
	team, runID := launchSMETeamRun(t, st, trl, TeamRunOverrides{})
	addRoutingToTeam(t, st, team, store.TeamRouting{
		Rules: []store.TeamRoutingRule{
			{Name: "bogus_rule", Phrases: []string{"whatever"}, TargetSlot: "does-not-exist"},
		},
	})

	_, err := rt.InstallTeamRunRouting(ctx, runID, team.ID)
	if err == nil || !strings.Contains(err.Error(), "unknown team slot") {
		t.Fatalf("err = %v, want unknown-target-slot rejection", err)
	}
}

func TestInstallTeamRunRouting_SemanticRuleAndCoordinatorFallback_InstalledAndPrioritizedCorrectly(t *testing.T) {
	st, trl, rt := newTeamRoutingTestFixtures(t)
	ctx := context.Background()
	team, runID := launchSMETeamRun(t, st, trl, TeamRunOverrides{})
	addRoutingToTeam(t, st, team, store.TeamRouting{
		Rules: []store.TeamRoutingRule{
			{
				Name:       "architecture_question",
				Phrases:    []string{"architecture question", "design review"},
				TargetSlot: "architect",
			},
		},
	})

	installed, err := rt.InstallTeamRunRouting(ctx, runID, team.ID)
	if err != nil {
		t.Fatalf("InstallTeamRunRouting: %v", err)
	}
	// 3 distinct active resolved agent_ids at launch (orchestrator,
	// engineer, reviewer — architect is dormant) × 2 rows (semantic rule +
	// coordinator fallback) = 6.
	if len(installed) != 6 {
		t.Fatalf("installed = %d rows, want 6 (3 askers x (1 semantic rule + 1 coordinator fallback))", len(installed))
	}

	orch, err := st.ListTeamRunMembersBySlot(ctx, runID, "orchestrator")
	if err != nil || len(orch) != 1 {
		t.Fatalf("orchestrator members: %v, err=%v", orch, err)
	}

	candidates, err := st.ListAgentReflexesForWorkflowRun(ctx, runID, orch[0].AgentID, "advisor")
	if err != nil {
		t.Fatalf("ListAgentReflexesForWorkflowRun: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("run-scoped candidates for orchestrator's own agent_id = %d, want 2 (semantic + fallback)", len(candidates))
	}

	exec := &reflexes.Executor{Logger: slog.Default()}
	kindLookup := func(ctx context.Context, kind string) (*store.ReflexActionKind, error) {
		return st.GetReflexActionKind(ctx, kind)
	}

	t.Run("matching phrase fires the semantic rule, not the fallback", func(t *testing.T) {
		state := reflexes.State{
			SessionID:  orch[0].SessionID,
			AgentID:    orch[0].AgentID,
			AgentClass: "advisor",
			UserMessages: []reflexes.MessageSignal{
				{Content: "I have an architecture question about the schema"},
			},
		}
		applied, _, err := reflexes.Resolve(ctx, candidates, state, exec, nil, kindLookup)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if len(applied.FiredReflexes) != 1 {
			t.Fatalf("fired = %d, want 1", len(applied.FiredReflexes))
		}
		if !strings.Contains(applied.FiredReflexes[0].Name, "architecture_question") {
			t.Fatalf("winner = %q, want the semantic rule to win over the coordinator fallback", applied.FiredReflexes[0].Name)
		}
		targetSlot, _ := applied.Actions[0].Spec["team_target_slot"].(string)
		if targetSlot != "architect" {
			t.Fatalf("team_target_slot = %q, want %q", targetSlot, "architect")
		}
	})

	t.Run("non-matching phrase falls through to the coordinator fallback", func(t *testing.T) {
		state := reflexes.State{
			SessionID:  orch[0].SessionID,
			AgentID:    orch[0].AgentID,
			AgentClass: "advisor",
			UserMessages: []reflexes.MessageSignal{
				{Content: "just checking in on progress"},
			},
		}
		applied, _, err := reflexes.Resolve(ctx, candidates, state, exec, nil, kindLookup)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if len(applied.FiredReflexes) != 1 {
			t.Fatalf("fired = %d, want 1", len(applied.FiredReflexes))
		}
		if !strings.Contains(applied.FiredReflexes[0].Name, "coordinator_fallback") {
			t.Fatalf("winner = %q, want the coordinator fallback to win when nothing more specific matched", applied.FiredReflexes[0].Name)
		}
		targetSlot, _ := applied.Actions[0].Spec["team_target_slot"].(string)
		if targetSlot != "orchestrator" {
			t.Fatalf("team_target_slot = %q, want %q (coordinator fallback default)", targetSlot, "orchestrator")
		}
	})
}

// TestInstallTeamRunRouting_ExplicitPriorityAtOrBelowFloor_IsFloorClamped is
// the regression test for the bug a fresh reviewer of this task found and
// reported (documented in this task's own Work Log addendum,
// TASKS/teams/09-team-routing.md): InstallTeamRunRouting's floor guard
// (coordinatorFallbackPriority) used to run ONLY inside the derived-default
// (`rule.Priority <= 0`) branch, so a Team author setting
// TeamRoutingRule.Priority explicitly to a value at or below
// coordinatorFallbackPriority (100) sailed through unclamped and made that
// semantic rule permanently unreachable: first_applicable groups same-
// ActionKind candidates and picks the single highest-priority ELIGIBLE one,
// and the always-firing coordinator fallback (fixed at 100) would always
// outrank an explicit-priority rule set at or below 100. Mirrors
// TestInstallTeamRunRouting_SemanticRuleAndCoordinatorFallback_
// InstalledAndPrioritizedCorrectly's own install-then-Resolve approach
// exactly, but with an EXPLICIT, too-low TeamRoutingRule.Priority (50)
// instead of the derived-default path that test already covers — the real
// end-to-end proof the reviewer's own reproduction used: confirms the
// installed row's stored priority is now floor-clamped, AND that a matching
// user message correctly resolves to the semantic rule's target via the
// real reflexes.Resolve combining logic, not the coordinator fallback.
func TestInstallTeamRunRouting_ExplicitPriorityAtOrBelowFloor_IsFloorClamped(t *testing.T) {
	st, trl, rt := newTeamRoutingTestFixtures(t)
	ctx := context.Background()
	team, runID := launchSMETeamRun(t, st, trl, TeamRunOverrides{})
	addRoutingToTeam(t, st, team, store.TeamRouting{
		Rules: []store.TeamRoutingRule{
			{
				Name:       "architecture_question",
				Phrases:    []string{"architecture question", "design review"},
				TargetSlot: "architect",
				// Explicit, well below coordinatorFallbackPriority (100) —
				// the exact bug shape: before the fix, this sailed through
				// unclamped and made the rule permanently unreachable.
				Priority: 50,
			},
		},
	})

	installed, err := rt.InstallTeamRunRouting(ctx, runID, team.ID)
	if err != nil {
		t.Fatalf("InstallTeamRunRouting: %v", err)
	}
	if len(installed) != 6 {
		t.Fatalf("installed = %d rows, want 6 (3 askers x (1 semantic rule + 1 coordinator fallback))", len(installed))
	}

	orch, err := st.ListTeamRunMembersBySlot(ctx, runID, "orchestrator")
	if err != nil || len(orch) != 1 {
		t.Fatalf("orchestrator members: %v, err=%v", orch, err)
	}

	candidates, err := st.ListAgentReflexesForWorkflowRun(ctx, runID, orch[0].AgentID, "advisor")
	if err != nil {
		t.Fatalf("ListAgentReflexesForWorkflowRun: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("run-scoped candidates for orchestrator's own agent_id = %d, want 2 (semantic + fallback)", len(candidates))
	}

	// Confirm the stored priority is now correctly floor-clamped: strictly
	// greater than coordinatorFallbackPriority, not the raw explicit 50
	// that was set on the rule.
	var semanticPriority, fallbackPriority int64
	var foundSemantic, foundFallback bool
	for _, c := range candidates {
		switch {
		case strings.Contains(c.Name, "architecture_question"):
			semanticPriority = c.Priority
			foundSemantic = true
		case strings.Contains(c.Name, "coordinator_fallback"):
			fallbackPriority = c.Priority
			foundFallback = true
		}
	}
	if !foundSemantic || !foundFallback {
		t.Fatalf("expected both a semantic rule row and a coordinator fallback row, foundSemantic=%v foundFallback=%v", foundSemantic, foundFallback)
	}
	if semanticPriority <= coordinatorFallbackPriority {
		t.Fatalf("semantic rule priority = %d, want strictly greater than coordinatorFallbackPriority (%d) -- explicit rule.Priority=50 must be floor-clamped, not stored as-is", semanticPriority, coordinatorFallbackPriority)
	}
	if semanticPriority <= fallbackPriority {
		t.Fatalf("semantic rule priority (%d) must be greater than the coordinator fallback's own priority (%d) for first_applicable to ever pick it over the always-firing fallback", semanticPriority, fallbackPriority)
	}

	// The real end-to-end proof the reviewer's own reproduction used: a
	// matching user message must resolve to the semantic rule's target via
	// the REAL reflexes.Resolve combining logic, not silently lose to the
	// coordinator fallback the way it did before this fix.
	exec := &reflexes.Executor{Logger: slog.Default()}
	kindLookup := func(ctx context.Context, kind string) (*store.ReflexActionKind, error) {
		return st.GetReflexActionKind(ctx, kind)
	}
	state := reflexes.State{
		SessionID:  orch[0].SessionID,
		AgentID:    orch[0].AgentID,
		AgentClass: "advisor",
		UserMessages: []reflexes.MessageSignal{
			{Content: "I have an architecture question about the schema"},
		},
	}
	applied, _, err := reflexes.Resolve(ctx, candidates, state, exec, nil, kindLookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(applied.FiredReflexes) != 1 {
		t.Fatalf("fired = %d, want 1", len(applied.FiredReflexes))
	}
	if !strings.Contains(applied.FiredReflexes[0].Name, "architecture_question") {
		t.Fatalf("winner = %q, want the explicit-priority semantic rule to win over the coordinator fallback (this is exactly the bug: before the fix, the coordinator fallback always won here)", applied.FiredReflexes[0].Name)
	}
	targetSlot, _ := applied.Actions[0].Spec["team_target_slot"].(string)
	if targetSlot != "architect" {
		t.Fatalf("team_target_slot = %q, want %q", targetSlot, "architect")
	}
}

// TestInstallTeamRunRouting_DormantTargetSlot_StillResolvesAgentSlug
// confirms a semantic rule targeting a normally-dormant slot (architect)
// still gets installed with a valid, non-empty agent_slug even though the
// slot has zero team_run_members rows at install time — resolveAgentSlugForSlot's
// static-config fallback (durable AgentID's own profile), not a forced
// eager resolution.
func TestInstallTeamRunRouting_DormantTargetSlot_StillResolvesAgentSlug(t *testing.T) {
	st, trl, rt := newTeamRoutingTestFixtures(t)
	ctx := context.Background()
	team, runID := launchSMETeamRun(t, st, trl, TeamRunOverrides{})
	addRoutingToTeam(t, st, team, store.TeamRouting{
		Rules: []store.TeamRoutingRule{
			{Name: "architecture_question", Phrases: []string{"architecture"}, TargetSlot: "architect"},
		},
	})

	if _, err := rt.InstallTeamRunRouting(ctx, runID, team.ID); err != nil {
		t.Fatalf("InstallTeamRunRouting: %v", err)
	}

	// architect is STILL dormant — installing routing must not force an
	// eager resolution.
	archMembers, err := st.ListTeamRunMembersBySlot(ctx, runID, "architect")
	if err != nil || len(archMembers) != 0 {
		t.Fatalf("architect members after InstallTeamRunRouting = %v (err=%v), want 0 (still dormant)", archMembers, err)
	}

	orch, _ := st.ListTeamRunMembersBySlot(ctx, runID, "orchestrator")
	candidates, err := st.ListAgentReflexesForWorkflowRun(ctx, runID, orch[0].AgentID, "advisor")
	if err != nil {
		t.Fatalf("ListAgentReflexesForWorkflowRun: %v", err)
	}
	var found bool
	for _, c := range candidates {
		var spec map[string]any
		if err := json.Unmarshal([]byte(c.ActionSpec), &spec); err != nil {
			t.Fatalf("unmarshal action_spec: %v", err)
		}
		if slot, _ := spec["team_target_slot"].(string); slot == "architect" {
			found = true
			slug, _ := spec["agent_slug"].(string)
			if slug == "" {
				t.Fatal("architecture_question rule's agent_slug is empty")
			}
		}
	}
	if !found {
		t.Fatal("no installed reflex row targets team slot architect")
	}
}

func TestInstallTeamRunRouting_ProvenanceTierIsValid(t *testing.T) {
	st, trl, rt := newTeamRoutingTestFixtures(t)
	ctx := context.Background()
	team, runID := launchSMETeamRun(t, st, trl, TeamRunOverrides{})
	addRoutingToTeam(t, st, team, store.TeamRouting{
		Rules: []store.TeamRoutingRule{
			{Name: "architecture_question", Phrases: []string{"architecture"}, TargetSlot: "architect"},
		},
	})

	installed, err := rt.InstallTeamRunRouting(ctx, runID, team.ID)
	if err != nil {
		t.Fatalf("InstallTeamRunRouting: %v", err)
	}
	if len(installed) == 0 {
		t.Fatal("nothing installed")
	}

	ok, err := st.ActionKindAllowsProvenanceTier(ctx, store.ReflexActionDispatchToAgent, teamRoutingProvenanceTier)
	if err != nil {
		t.Fatalf("ActionKindAllowsProvenanceTier: %v", err)
	}
	if !ok {
		t.Fatalf("provenance tier %q is not allowed to declare a %q row — task 05's own allow-list rejects this task's chosen tier", teamRoutingProvenanceTier, store.ReflexActionDispatchToAgent)
	}

	for _, id := range installed {
		row, err := st.GetAgentReflex(ctx, id)
		if err != nil {
			t.Fatalf("GetAgentReflex(%s): %v", id, err)
		}
		if row.ProvenanceTier != teamRoutingProvenanceTier {
			t.Fatalf("row %s provenance_tier = %q, want %q", id, row.ProvenanceTier, teamRoutingProvenanceTier)
		}
		if row.WorkflowRunID != runID {
			t.Fatalf("row %s workflow_run_id = %q, want %q", id, row.WorkflowRunID, runID)
		}
	}
}

// --- provenance trace, end to end ---

// TestTeamRouting_ProvenanceTrace_EndToEnd is this task's required "why
// did this message go to Architect" walk: a delivered agent_messages row
// -> the event_log firing (via EmitFirings) that produced it -> the
// resolved slot -> the concrete team_run_members tuple, reconstructable
// end to end and filterable by workflow_run_id, using only existing
// tables (event_log, agent_messages via internal/messaging,
// team_run_members) — no new persistence layer.
func TestTeamRouting_ProvenanceTrace_EndToEnd(t *testing.T) {
	st, trl, rt := newTeamRoutingTestFixtures(t)
	ctx := context.Background()
	team, runID := launchSMETeamRun(t, st, trl, TeamRunOverrides{})
	addRoutingToTeam(t, st, team, store.TeamRouting{
		Rules: []store.TeamRoutingRule{
			{Name: "architecture_question", Phrases: []string{"architecture question"}, TargetSlot: "architect"},
		},
	})
	if _, err := st.CreateTeamAuthorityGrant(ctx, store.TeamAuthorityGrant{
		TeamID: team.ID, FromSlot: "orchestrator", Verb: store.TeamAuthorityVerbMaySpawn, ToSlot: "architect",
	}); err != nil {
		t.Fatalf("CreateTeamAuthorityGrant: %v", err)
	}
	if _, err := rt.InstallTeamRunRouting(ctx, runID, team.ID); err != nil {
		t.Fatalf("InstallTeamRunRouting: %v", err)
	}

	orch, err := st.ListTeamRunMembersBySlot(ctx, runID, "orchestrator")
	if err != nil || len(orch) != 1 {
		t.Fatalf("orchestrator members: %v, err=%v", orch, err)
	}

	// Step 1: evaluate the run-scoped candidate set for the orchestrator's
	// own turn — the routing DECISION (a runtime event, one reflex firing).
	candidates, err := st.ListAgentReflexesForWorkflowRun(ctx, runID, orch[0].AgentID, "advisor")
	if err != nil {
		t.Fatalf("ListAgentReflexesForWorkflowRun: %v", err)
	}
	state := reflexes.State{
		SessionID:  orch[0].SessionID,
		AgentID:    orch[0].AgentID,
		AgentClass: "advisor",
		UserMessages: []reflexes.MessageSignal{
			{Content: "I have an architecture question about the schema"},
		},
	}
	exec := &reflexes.Executor{Logger: slog.Default()}
	kindLookup := func(ctx context.Context, kind string) (*store.ReflexActionKind, error) {
		return st.GetReflexActionKind(ctx, kind)
	}
	applied, outcomes, err := reflexes.Resolve(ctx, candidates, state, exec, nil, kindLookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(applied.FiredReflexes) != 1 {
		t.Fatalf("fired = %d, want 1", len(applied.FiredReflexes))
	}

	// Step 2: emit the unified telemetry sink — the real EmitFirings every
	// production dispatch_to_agent call site goes through
	// (internal/service/chat_reflex_dispatch.go's attemptReflexDispatch,
	// internal/selftools/self_tools_dispatch.go's matchDispatchToAgentReflex).
	// This is the event_log write this trace walks.
	reflexes.EmitFirings(ctx, st, nil, applied, outcomes, state, reflexes.FiringContext{
		AgentID: orch[0].AgentID, AgentClass: "advisor",
	}, slog.Default())

	// Step 3: the delivered agent_messages row — explicit @slot addressing
	// from the orchestrator to the (still-dormant, first-touch) architect,
	// exercising SendToSlot's own lazy resolution in the same walk.
	sendResult, err := rt.SendToSlot(ctx, SendToSlotRequest{
		WorkflowRunID: runID,
		TeamID:        team.ID,
		FromSlot:      "orchestrator",
		FromSessionID: orch[0].SessionID,
		FromAgentID:   orch[0].AgentID,
		ToSlot:        "architect",
		Body:          "I have an architecture question about the schema",
	})
	if err != nil {
		t.Fatalf("SendToSlot: %v", err)
	}
	if len(sendResult.Recipients) != 1 || sendResult.Recipients[0].Message == nil {
		t.Fatalf("sendResult = %+v, want exactly one delivered message", sendResult)
	}
	deliveredMsg := sendResult.Recipients[0].Message

	// --- The trace walk itself, filtered by workflow_run_id ---

	runIDFromSession, found, err := st.ResolveWorkflowRunIDForSession(ctx, orch[0].SessionID)
	if err != nil || !found || runIDFromSession != runID {
		t.Fatalf("ResolveWorkflowRunIDForSession = (%q, %v, %v), want (%q, true, nil)", runIDFromSession, found, err, runID)
	}

	// 1) Walk event_log for the reflex firing that produced this decision,
	// scoped to the orchestrator's own session (which ResolveWorkflowRunIDForSession
	// above just confirmed belongs to runID).
	var detail, metadataJSON string
	row := st.DB.QueryRow(
		`SELECT detail, metadata FROM event_log
		  WHERE session_id = ? AND category = 'reflex' AND event_type = ?
		  ORDER BY id DESC LIMIT 1`,
		orch[0].SessionID, store.ReflexActionDispatchToAgent,
	)
	if err := row.Scan(&detail, &metadataJSON); err != nil {
		t.Fatalf("query event_log: %v", err)
	}
	if !strings.Contains(detail, "architecture_question") {
		t.Fatalf("event_log detail = %q, want the architecture_question reflex name", detail)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &rec); err != nil {
		t.Fatalf("unmarshal event_log metadata: %v", err)
	}
	spec, _ := rec["spec"].(map[string]any)
	if spec == nil {
		t.Fatal("event_log metadata has no spec")
	}
	targetSlotName, _ := spec["team_target_slot"].(string)
	if targetSlotName != "architect" {
		t.Fatalf("traced target slot = %q, want %q", targetSlotName, "architect")
	}
	agentSlug, _ := spec["agent_slug"].(string)
	if agentSlug == "" {
		t.Fatal("traced agent_slug is empty")
	}

	// 2) Walk the resolved slot -> the concrete team_run_members tuple.
	archMembers, err := st.ListTeamRunMembersBySlot(ctx, runID, targetSlotName)
	if err != nil || len(archMembers) != 1 {
		t.Fatalf("team_run_members for traced slot %q = %v (err=%v), want exactly 1", targetSlotName, archMembers, err)
	}
	archProfile, err := st.GetAgent(archMembers[0].AgentID)
	if err != nil {
		t.Fatalf("GetAgent(%s): %v", archMembers[0].AgentID, err)
	}
	if archProfile.Slug != agentSlug {
		t.Fatalf("traced agent_slug %q does not match the resolved team_run_members tuple's own profile slug %q", agentSlug, archProfile.Slug)
	}

	// 3) Confirm the delivered agent_messages row (Step 3, SendToSlot)
	// really does point at that SAME concrete tuple — the full walk
	// closes the loop: message -> firing -> slot -> tuple -> (back to)
	// message.
	if deliveredMsg.ToSessionID != archMembers[0].SessionID || deliveredMsg.ToAgentID != archMembers[0].AgentID {
		t.Fatalf("delivered message tuple (%s,%s) does not match traced team_run_members tuple (%s,%s)",
			deliveredMsg.ToSessionID, deliveredMsg.ToAgentID, archMembers[0].SessionID, archMembers[0].AgentID)
	}

	// Sanity: the delivered message itself is independently reachable by
	// workflow_run_id too, via the SAME session-scoping primitive.
	msgRunID, msgFound, err := st.ResolveWorkflowRunIDForSession(ctx, deliveredMsg.FromSessionID)
	if err != nil || !msgFound || msgRunID != runID {
		t.Fatalf("ResolveWorkflowRunIDForSession(delivered message's from_session_id) = (%q, %v, %v), want (%q, true, nil)", msgRunID, msgFound, err, runID)
	}
}
