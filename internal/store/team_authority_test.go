package store

// TASKS/teams/04-team-authority-schema.md's own test coverage
// requirements ("Done means"):
//   - Round-trip test for TeamAuthorityGrant CRUD.
//   - AuthorizedForVerb regression tests covering: a granted verb returns
//     true; an ungranted verb/slot pair returns false; to_slot='self'
//     correctly excludes self-targeting for may_not_review; an unknown
//     team ID or slot name returns false (explicit fail-closed test, not
//     incidental).
//
// The fail-closed tests below are written explicitly, not left as an
// assumption the implementation happens to satisfy -- per this task
// file's own "Critical correctness requirement": this codebase has a
// documented, real history of fail-open bugs in exactly this shape
// (TASKS/reflex-taxonomy/08-fix-resolve-fail-open-visibility.md).

import (
	"context"
	"errors"
	"testing"
)

// makeTestTeam inserts a minimal teams row (the FK target for
// team_authority_grants.team_id) and returns its ID.
func makeTestTeam(t *testing.T, s *Store, name string) string {
	t.Helper()
	team := &Team{Name: name}
	if err := s.CreateTeam(context.Background(), team); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	return team.ID
}

// TestTeamAuthorityGrant_RoundTrip exercises Create/List/Delete -- the
// full CRUD surface this task's "Done means" requires, modeled on the SME
// scenario's own authority block (15-teams.md's "Illustrative shape":
// orchestrator.may_spawn: [engineer, reviewer]).
func TestTeamAuthorityGrant_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	teamID := makeTestTeam(t, s, "Feature Development")

	spawnEngineer, err := s.CreateTeamAuthorityGrant(ctx, TeamAuthorityGrant{
		TeamID:   teamID,
		FromSlot: "orchestrator",
		Verb:     TeamAuthorityVerbMaySpawn,
		ToSlot:   "engineer",
	})
	if err != nil {
		t.Fatalf("CreateTeamAuthorityGrant (spawn engineer): %v", err)
	}
	if spawnEngineer.ID == "" {
		t.Fatal("CreateTeamAuthorityGrant: expected generated ID")
	}
	if spawnEngineer.CreatedAt == "" {
		t.Fatal("CreateTeamAuthorityGrant: expected created_at to be populated")
	}

	spawnReviewer, err := s.CreateTeamAuthorityGrant(ctx, TeamAuthorityGrant{
		TeamID:   teamID,
		FromSlot: "orchestrator",
		Verb:     TeamAuthorityVerbMaySpawn,
		ToSlot:   "reviewer",
	})
	if err != nil {
		t.Fatalf("CreateTeamAuthorityGrant (spawn reviewer): %v", err)
	}

	notReviewSelf, err := s.CreateTeamAuthorityGrant(ctx, TeamAuthorityGrant{
		TeamID:   teamID,
		FromSlot: "reviewer",
		Verb:     TeamAuthorityVerbMayNotReview,
		ToSlot:   TeamAuthoritySelfSlot,
	})
	if err != nil {
		t.Fatalf("CreateTeamAuthorityGrant (may_not_review self): %v", err)
	}

	grants, err := s.ListTeamAuthorityGrants(ctx, teamID)
	if err != nil {
		t.Fatalf("ListTeamAuthorityGrants: %v", err)
	}
	if len(grants) != 3 {
		t.Fatalf("ListTeamAuthorityGrants: got %d rows, want 3: %+v", len(grants), grants)
	}

	gotIDs := map[string]bool{}
	for _, g := range grants {
		gotIDs[g.ID] = true
		if g.TeamID != teamID {
			t.Errorf("grant %+v has wrong team_id", g)
		}
	}
	for _, want := range []string{spawnEngineer.ID, spawnReviewer.ID, notReviewSelf.ID} {
		if !gotIDs[want] {
			t.Errorf("ListTeamAuthorityGrants: missing expected grant id %s in %+v", want, grants)
		}
	}

	// A different team's grants must not leak into this team's list.
	otherTeamID := makeTestTeam(t, s, "Other Team")
	if _, err := s.CreateTeamAuthorityGrant(ctx, TeamAuthorityGrant{
		TeamID: otherTeamID, FromSlot: "orchestrator", Verb: TeamAuthorityVerbMayMessage, ToSlot: "engineer",
	}); err != nil {
		t.Fatalf("CreateTeamAuthorityGrant (other team): %v", err)
	}
	grantsAfter, err := s.ListTeamAuthorityGrants(ctx, teamID)
	if err != nil {
		t.Fatalf("ListTeamAuthorityGrants after other-team insert: %v", err)
	}
	if len(grantsAfter) != 3 {
		t.Fatalf("ListTeamAuthorityGrants leaked another team's grant: got %d, want 3: %+v", len(grantsAfter), grantsAfter)
	}

	// Delete.
	if err := s.DeleteTeamAuthorityGrant(ctx, spawnEngineer.ID); err != nil {
		t.Fatalf("DeleteTeamAuthorityGrant: %v", err)
	}
	grantsAfterDelete, err := s.ListTeamAuthorityGrants(ctx, teamID)
	if err != nil {
		t.Fatalf("ListTeamAuthorityGrants after delete: %v", err)
	}
	if len(grantsAfterDelete) != 2 {
		t.Fatalf("ListTeamAuthorityGrants after delete: got %d, want 2: %+v", len(grantsAfterDelete), grantsAfterDelete)
	}
	if err := s.DeleteTeamAuthorityGrant(ctx, spawnEngineer.ID); !errors.Is(err, ErrTeamAuthorityGrantNotFound) {
		t.Errorf("DeleteTeamAuthorityGrant (already deleted): got %v, want ErrTeamAuthorityGrantNotFound", err)
	}
	if err := s.DeleteTeamAuthorityGrant(ctx, "does-not-exist"); !errors.Is(err, ErrTeamAuthorityGrantNotFound) {
		t.Errorf("DeleteTeamAuthorityGrant (unknown id): got %v, want ErrTeamAuthorityGrantNotFound", err)
	}
}

// TestTeamAuthorityGrant_ListEmpty confirms a team with no grants returns
// an empty, non-nil slice -- a Team with no authority rules declared is a
// legitimate starting shape, not an error (mirrors 15-teams.md's "a fully
// fluid team is a legitimate, gate-free shape" reasoning applied to
// authority).
func TestTeamAuthorityGrant_ListEmpty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	teamID := makeTestTeam(t, s, "Ungoverned Team")

	grants, err := s.ListTeamAuthorityGrants(ctx, teamID)
	if err != nil {
		t.Fatalf("ListTeamAuthorityGrants: %v", err)
	}
	if grants == nil {
		t.Error("ListTeamAuthorityGrants: expected non-nil empty slice")
	}
	if len(grants) != 0 {
		t.Errorf("ListTeamAuthorityGrants: got %d rows, want 0", len(grants))
	}
}

// TestTeamAuthorityGrant_Validation confirms CreateTeamAuthorityGrant
// rejects missing required fields and an invalid verb before ever hitting
// the DB.
func TestTeamAuthorityGrant_Validation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	teamID := makeTestTeam(t, s, "Validation Team")

	cases := []struct {
		name  string
		grant TeamAuthorityGrant
	}{
		{"missing team_id", TeamAuthorityGrant{FromSlot: "orchestrator", Verb: TeamAuthorityVerbMaySpawn, ToSlot: "engineer"}},
		{"missing from_slot", TeamAuthorityGrant{TeamID: teamID, Verb: TeamAuthorityVerbMaySpawn, ToSlot: "engineer"}},
		{"missing to_slot", TeamAuthorityGrant{TeamID: teamID, FromSlot: "orchestrator", Verb: TeamAuthorityVerbMaySpawn}},
		{"missing verb", TeamAuthorityGrant{TeamID: teamID, FromSlot: "orchestrator", ToSlot: "engineer"}},
		{"invalid verb", TeamAuthorityGrant{TeamID: teamID, FromSlot: "orchestrator", Verb: "may_delegate", ToSlot: "engineer"}},
	}
	for _, c := range cases {
		if _, err := s.CreateTeamAuthorityGrant(ctx, c.grant); err == nil {
			t.Errorf("CreateTeamAuthorityGrant (%s): expected error, got nil (row: %+v)", c.name, c.grant)
		}
	}
}

// TestTeamAuthorityGrant_UnknownTeamID_ForeignKeyRejects confirms the
// team_id REFERENCES teams(id) foreign key (migration 132) actually
// rejects an insert against a nonexistent team -- this codebase runs with
// PRAGMA foreign_keys=1 (see internal/store/consumers_test.go's own note
// on this), so a bogus team_id should fail at the DB layer even though
// CreateTeamAuthorityGrant's own Go-side validation has no way to check
// team existence without an extra round-trip.
func TestTeamAuthorityGrant_UnknownTeamID_ForeignKeyRejects(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.CreateTeamAuthorityGrant(ctx, TeamAuthorityGrant{
		TeamID: "does-not-exist", FromSlot: "orchestrator", Verb: TeamAuthorityVerbMaySpawn, ToSlot: "engineer",
	}); err == nil {
		t.Error("CreateTeamAuthorityGrant: expected foreign key error for unknown team_id, got nil")
	}
}

// --- AuthorizedForVerb -------------------------------------------------

// TestAuthorizedForVerb_GrantedReturnsTrue is the positive case: a real
// grant row matching (teamID, fromSlot, verb, toSlot) returns true.
func TestAuthorizedForVerb_GrantedReturnsTrue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	teamID := makeTestTeam(t, s, "Granted Team")
	if _, err := s.CreateTeamAuthorityGrant(ctx, TeamAuthorityGrant{
		TeamID: teamID, FromSlot: "orchestrator", Verb: TeamAuthorityVerbMaySpawn, ToSlot: "engineer",
	}); err != nil {
		t.Fatalf("CreateTeamAuthorityGrant: %v", err)
	}

	ok, err := s.AuthorizedForVerb(ctx, teamID, "orchestrator", TeamAuthorityVerbMaySpawn, "engineer")
	if err != nil {
		t.Fatalf("AuthorizedForVerb: %v", err)
	}
	if !ok {
		t.Error("AuthorizedForVerb: got false, want true for a granted verb/slot pair")
	}
}

// TestAuthorizedForVerb_UngrantedReturnsFalse confirms a verb/slot pair
// with no matching grant row returns false -- both when the verb matches
// but the target slot doesn't, and when the target slot matches but the
// verb doesn't.
func TestAuthorizedForVerb_UngrantedReturnsFalse(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	teamID := makeTestTeam(t, s, "Ungranted Team")
	if _, err := s.CreateTeamAuthorityGrant(ctx, TeamAuthorityGrant{
		TeamID: teamID, FromSlot: "orchestrator", Verb: TeamAuthorityVerbMaySpawn, ToSlot: "engineer",
	}); err != nil {
		t.Fatalf("CreateTeamAuthorityGrant: %v", err)
	}

	// Same fromSlot/verb, different (ungranted) toSlot.
	ok, err := s.AuthorizedForVerb(ctx, teamID, "orchestrator", TeamAuthorityVerbMaySpawn, "architect")
	if err != nil {
		t.Fatalf("AuthorizedForVerb (wrong toSlot): %v", err)
	}
	if ok {
		t.Error("AuthorizedForVerb: got true, want false for an ungranted toSlot")
	}

	// Same fromSlot/toSlot, different (ungranted) verb.
	ok, err = s.AuthorizedForVerb(ctx, teamID, "orchestrator", TeamAuthorityVerbMayMessage, "engineer")
	if err != nil {
		t.Fatalf("AuthorizedForVerb (wrong verb): %v", err)
	}
	if ok {
		t.Error("AuthorizedForVerb: got true, want false for an ungranted verb")
	}

	// Different fromSlot entirely.
	ok, err = s.AuthorizedForVerb(ctx, teamID, "engineer", TeamAuthorityVerbMaySpawn, "engineer")
	if err != nil {
		t.Fatalf("AuthorizedForVerb (wrong fromSlot): %v", err)
	}
	if ok {
		t.Error("AuthorizedForVerb: got true, want false for an ungranted fromSlot")
	}
}

// TestAuthorizedForVerb_SelfSlotExcludesSelfTargeting is this task's
// required "to_slot='self' correctly excludes self-targeting for
// may_not_review" regression test: 15-teams.md's `reviewer.may_not_review:
// self` example -- a to_slot='self' grant row matches only when the
// queried toSlot equals fromSlot (self-targeting), and does not match any
// other toSlot.
func TestAuthorizedForVerb_SelfSlotExcludesSelfTargeting(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	teamID := makeTestTeam(t, s, "Self Slot Team")
	if _, err := s.CreateTeamAuthorityGrant(ctx, TeamAuthorityGrant{
		TeamID: teamID, FromSlot: "reviewer", Verb: TeamAuthorityVerbMayNotReview, ToSlot: TeamAuthoritySelfSlot,
	}); err != nil {
		t.Fatalf("CreateTeamAuthorityGrant: %v", err)
	}

	// Self-targeting: fromSlot == toSlot == "reviewer" -- the 'self'
	// sentinel row matches.
	ok, err := s.AuthorizedForVerb(ctx, teamID, "reviewer", TeamAuthorityVerbMayNotReview, "reviewer")
	if err != nil {
		t.Fatalf("AuthorizedForVerb (self-targeting): %v", err)
	}
	if !ok {
		t.Error("AuthorizedForVerb: got false, want true for self-targeting against a to_slot='self' grant")
	}

	// Not self-targeting: fromSlot "reviewer" querying toSlot "engineer" --
	// the 'self' sentinel must NOT match a literal slot named "engineer",
	// and there is no literal grant to "engineer" either.
	ok, err = s.AuthorizedForVerb(ctx, teamID, "reviewer", TeamAuthorityVerbMayNotReview, "engineer")
	if err != nil {
		t.Fatalf("AuthorizedForVerb (non-self toSlot): %v", err)
	}
	if ok {
		t.Error("AuthorizedForVerb: got true, want false -- a to_slot='self' grant must not match a different concrete toSlot")
	}

	// A to_slot='self' grant for a DIFFERENT fromSlot must not leak into
	// this fromSlot's self-targeting check.
	ok, err = s.AuthorizedForVerb(ctx, teamID, "engineer", TeamAuthorityVerbMayNotReview, "engineer")
	if err != nil {
		t.Fatalf("AuthorizedForVerb (different fromSlot self-targeting): %v", err)
	}
	if ok {
		t.Error("AuthorizedForVerb: got true, want false -- reviewer's self-exclusion grant must not apply to engineer's self-targeting")
	}
}

// TestAuthorizedForVerb_FailClosed_UnknownTeamID is this task's explicit,
// non-incidental fail-closed regression test for an unknown team_id --
// required by this task's own "Critical correctness requirement" given
// this codebase's documented history of fail-open bugs in exactly this
// shape (TASKS/reflex-taxonomy/08-fix-resolve-fail-open-visibility.md).
func TestAuthorizedForVerb_FailClosed_UnknownTeamID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	ok, err := s.AuthorizedForVerb(ctx, "does-not-exist-team", "orchestrator", TeamAuthorityVerbMaySpawn, "engineer")
	if err != nil {
		t.Fatalf("AuthorizedForVerb (unknown team_id): unexpected error %v", err)
	}
	if ok {
		t.Error("AuthorizedForVerb: got true, want false (fail-closed) for an unknown team_id -- must never default to true")
	}
}

// TestAuthorizedForVerb_FailClosed_UnknownSlot is this task's explicit
// fail-closed regression test for an unknown from_slot/to_slot -- a real
// team exists with a real grant, but the queried slot names don't match
// anything stored.
func TestAuthorizedForVerb_FailClosed_UnknownSlot(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	teamID := makeTestTeam(t, s, "Unknown Slot Team")
	if _, err := s.CreateTeamAuthorityGrant(ctx, TeamAuthorityGrant{
		TeamID: teamID, FromSlot: "orchestrator", Verb: TeamAuthorityVerbMaySpawn, ToSlot: "engineer",
	}); err != nil {
		t.Fatalf("CreateTeamAuthorityGrant: %v", err)
	}

	if ok, err := s.AuthorizedForVerb(ctx, teamID, "unknown-slot", TeamAuthorityVerbMaySpawn, "engineer"); err != nil || ok {
		t.Errorf("AuthorizedForVerb (unknown from_slot): got ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	if ok, err := s.AuthorizedForVerb(ctx, teamID, "orchestrator", TeamAuthorityVerbMaySpawn, "unknown-slot"); err != nil || ok {
		t.Errorf("AuthorizedForVerb (unknown to_slot): got ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

// TestAuthorizedForVerb_FailClosed_NoGrantsAtAll confirms a team with zero
// authority grants (a legitimate, fully fluid team per 15-teams.md) is
// never accidentally authorized for anything -- absence of governance is
// not implicit permission.
func TestAuthorizedForVerb_FailClosed_NoGrantsAtAll(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	teamID := makeTestTeam(t, s, "No Grants Team")

	ok, err := s.AuthorizedForVerb(ctx, teamID, "orchestrator", TeamAuthorityVerbMaySpawn, "engineer")
	if err != nil {
		t.Fatalf("AuthorizedForVerb (no grants): unexpected error %v", err)
	}
	if ok {
		t.Error("AuthorizedForVerb: got true, want false (fail-closed) for a team with zero authority grants")
	}
}

// TestAuthorizedForVerb_FailClosed_EmptyArguments confirms an empty
// teamID/fromSlot/verb/toSlot argument is rejected before the query even
// runs, rather than accidentally matching a real row (defense in depth --
// this package's own writer never persists an empty required column, but
// a caller passing an empty string via its own resolution-logic bug must
// still get false).
func TestAuthorizedForVerb_FailClosed_EmptyArguments(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	teamID := makeTestTeam(t, s, "Empty Args Team")
	if _, err := s.CreateTeamAuthorityGrant(ctx, TeamAuthorityGrant{
		TeamID: teamID, FromSlot: "orchestrator", Verb: TeamAuthorityVerbMaySpawn, ToSlot: "engineer",
	}); err != nil {
		t.Fatalf("CreateTeamAuthorityGrant: %v", err)
	}

	cases := []struct {
		name                       string
		teamID, from, verb, toSlot string
	}{
		{"empty teamID", "", "orchestrator", TeamAuthorityVerbMaySpawn, "engineer"},
		{"empty fromSlot", teamID, "", TeamAuthorityVerbMaySpawn, "engineer"},
		{"empty verb", teamID, "orchestrator", "", "engineer"},
		{"empty toSlot", teamID, "orchestrator", TeamAuthorityVerbMaySpawn, ""},
	}
	for _, c := range cases {
		ok, err := s.AuthorizedForVerb(ctx, c.teamID, c.from, c.verb, c.toSlot)
		if err != nil {
			t.Errorf("AuthorizedForVerb (%s): unexpected error %v", c.name, err)
		}
		if ok {
			t.Errorf("AuthorizedForVerb (%s): got true, want false", c.name)
		}
	}
}
