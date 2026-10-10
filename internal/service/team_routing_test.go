package service

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/store/mailboxadapter"
)

func newTeamRoutingTestFixtures(t *testing.T) (*store.Store, *TeamRunLauncher, *TeamRoutingService) {
	t.Helper()
	st, _, launcher := newTeamRunLauncherTestFixtures(t)
	return st, launcher, NewTeamRoutingService(st, mailboxadapter.New(st).Service, launcher)
}

func TestHeldTeamRoutingRefusesBeforeMessagingOrReflexEffects(t *testing.T) {
	st, _, routing := newTeamRoutingTestFixtures(t)
	f := newHeldTeamFixture(t, st)
	before := heldTeamSnapshot(t, st)
	for _, tc := range []struct{ name, run, team, from, to string }{
		{"prior binding", f.runID, f.team.ID, f.actor.ID, "lead"},
		{"historical identity", f.runID, f.team.ID, f.historical.ID, "worker"},
		{"host settings UUID", f.runID, f.team.ID, f.hostID, "worker"},
		{"unknown target", f.runID, f.team.ID, f.actor.ID, "missing"},
		{"missing run", "missing-run", f.team.ID, f.actor.ID, "worker"},
		{"empty request", "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := routing.SendToSlot(t.Context(), SendToSlotRequest{WorkflowRunID: tc.run, TeamID: tc.team, FromSlot: "lead", FromSessionID: "prior-team-session", FromAgentID: tc.from, ToSlot: tc.to, Body: "Private review request"})
			if result != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
				t.Fatalf("send=%+v,%v", result, err)
			}
			reflexIDs, err := routing.InstallTeamRunRouting(t.Context(), tc.run, tc.team)
			if reflexIDs != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
				t.Fatalf("install=%v,%v", reflexIDs, err)
			}
			assertHeldTeamUnchanged(t, st, before)
		})
	}
	empty := NewTeamRoutingService(nil, nil, nil)
	if result, err := empty.SendToSlot(t.Context(), SendToSlotRequest{}); result != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("unconfigured send=%+v,%v", result, err)
	}
	if ids, err := empty.InstallTeamRunRouting(t.Context(), "", ""); ids != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("unconfigured install=%v,%v", ids, err)
	}
}

func TestHeldTeamRoutingReadsPriorBindingWithoutHistoricalFallback(t *testing.T) {
	st, launcher, routing := newTeamRoutingTestFixtures(t)
	f := newHeldTeamFixture(t, st)
	before := heldTeamSnapshot(t, st)
	members, err := launcher.ListMembers(t.Context(), f.runID)
	if err != nil || len(members) != 1 || members[0].AgentID != f.actor.ID || members[0].ID != "prior-team-member" {
		t.Fatalf("fresh-only members=%+v,%v", members, err)
	}
	// Active member resolution reads the actual actor binding even when a
	// retained profile UUID is present in the declarative slot document.
	slot := store.TeamSlotDefinition{Name: "lead", Resolution: "durable", AgentID: &f.historical.ID}
	slug, err := routing.resolveAgentSlugForSlot(t.Context(), f.runID, slot)
	if err != nil || slug != f.actor.Slug {
		t.Fatalf("bound member slug=%q,%v", slug, err)
	}
	slot.Name = "dormant"
	slot.AgentID = &f.actor.ID
	if slug, err := routing.resolveAgentSlugForSlot(t.Context(), f.runID, slot); err != nil || slug != f.actor.Slug {
		t.Fatalf("prior dormant identity=%q,%v", slug, err)
	}
	for _, id := range []string{f.historical.ID, f.hostID, f.actor.Slug, "missing"} {
		slot.AgentID = &id
		if slug, err := routing.resolveAgentSlugForSlot(t.Context(), f.runID, slot); slug != "" || !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatalf("unverified dormant %q=%q,%v", id, slug, err)
		}
	}
	slot = store.TeamSlotDefinition{Name: "worker", Resolution: "fresh", RoleSlug: "retained-team-role"}
	if slug, err := routing.resolveAgentSlugForSlot(t.Context(), f.runID, slot); slug != "" || !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatalf("historical role slug=%q,%v", slug, err)
	}
	assertHeldTeamUnchanged(t, st, before)
	// Each read fence applies to both active-member and dormant identity paths.
	for _, tc := range []struct{ name, sql string }{
		{"disabled binding", `UPDATE agent_actor_bindings SET enabled=0 WHERE actor_uri=?`},
		{"missing receipt", `UPDATE agent_actor_bindings SET enabled=1,binding_receipt='   ' WHERE actor_uri=?`},
		{"disabled host", `UPDATE agent_host_settings SET enabled=0 WHERE id=(SELECT host_settings_id FROM agent_actor_bindings WHERE actor_uri=?)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := st.DB.ExecContext(t.Context(), `UPDATE agent_actor_bindings SET enabled=1,binding_receipt='private-existing-host-binding' WHERE actor_uri=?`, f.actor.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := st.DB.ExecContext(t.Context(), tc.sql, f.actor.ID); err != nil {
				t.Fatal(err)
			}
			before := heldTeamSnapshot(t, st)
			for _, name := range []string{"lead", "dormant"} {
				slot := store.TeamSlotDefinition{Name: name, Resolution: "durable", AgentID: &f.actor.ID}
				if slug, err := routing.resolveAgentSlugForSlot(t.Context(), f.runID, slot); slug != "" || !errors.Is(err, store.ErrVerifiedActorRequired) {
					t.Fatalf("fenced %s slug=%q,%v", name, slug, err)
				}
				if id, err := launcher.resolveSlotAgentIdentity(t.Context(), slot); id != "" || !errors.Is(err, store.ErrVerifiedActorRequired) {
					t.Fatalf("fenced %s identity=%q,%v", name, id, err)
				}
			}
			assertHeldTeamUnchanged(t, st, before)
		})
	}

}

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

func TestHeldTeamRoutingClassificationPreservesActiveAndLiteralRules(t *testing.T) {
	members := []store.TeamRunMember{
		{AgentID: "actor-b", Status: store.TeamRunMemberStatusActive},
		{AgentID: "actor-a", Status: store.TeamRunMemberStatusActive},
		{AgentID: "actor-b", Status: store.TeamRunMemberStatusActive},
		{AgentID: "retained-stopped", Status: store.TeamRunMemberStatusStopped},
	}
	if got := dedupeActiveAgentIDs(members); !reflect.DeepEqual(got, []string{"actor-a", "actor-b"}) {
		t.Fatalf("active identities=%v", got)
	}
	if got := filterActiveMembers(members); !reflect.DeepEqual(got, members[:3]) {
		t.Fatalf("active members=%+v", got)
	}
	pattern := regexp.MustCompile(anyPhraseRegex([]string{"a+b", "review [private]"}))
	for _, text := range []string{"Need A+B now", "REVIEW [PRIVATE]"} {
		if !pattern.MatchString(text) {
			t.Fatalf("literal phrase did not match %q", text)
		}
	}
	for _, text := range []string{"aaab", "review private"} {
		if pattern.MatchString(text) {
			t.Fatalf("literal phrase treated as regex: %q", text)
		}
	}
}
