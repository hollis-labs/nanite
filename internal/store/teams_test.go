package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// TestTeam_RoundTrip exercises the basic CRUD path: create, get (by id and
// by name), list, update, delete -- including all four JSON sub-structure
// columns and a TeamSlotDefinition with every field populated, per
// TASKS/teams/01-team-definition-schema.md's "Done means".
func TestTeam_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	architectAgentID := "nanite-architect"
	slots := []TeamSlotDefinition{
		{
			Name:           "architect",
			RoleSlug:       "architecture-sme",
			Resolution:     "durable",
			AgentID:        &architectAgentID,
			ActivationMode: "singleton",
			Required:       false,
			Min:            0,
			Max:            1,
		},
		{
			Name:           "orchestrator",
			RoleSlug:       "orchestrator",
			Resolution:     "fresh",
			ActivationMode: "singleton",
			Required:       true,
			Min:            1,
			Max:            1,
		},
		{
			Name:           "engineer",
			RoleSlug:       "engineer",
			Resolution:     "fresh",
			ActivationMode: "concurrent",
			Required:       true,
			Min:            1,
			Max:            4,
		},
		{
			Name:           "reviewer",
			RoleSlug:       "code-reviewer",
			Resolution:     "fresh",
			ActivationMode: "singleton",
			Required:       true,
			Min:            1,
			Max:            1,
		},
	}

	authority := `{"orchestrator.may_spawn":["engineer","reviewer"],"engineer.may_message":["architect","orchestrator","engineer"],"reviewer.may_message":["engineer","architect"],"reviewer.may_not_review":"self"}`
	routing := `{"architecture_question":"architect","otherwise":"orchestrator"}`
	phases := `[{"id":"scope_work","kind":"flex"},{"id":"review_gate","kind":"gate","depends_on":["scope_work"]},{"id":"address_feedback","kind":"flex","depends_on":["review_gate"]},{"id":"merge_gate","kind":"gate","depends_on":["address_feedback"]}]`

	team := &Team{
		Name:          "Feature Development",
		Description:   "SME scenario from 15-teams.md",
		AuthorityJSON: authority,
		RoutingJSON:   routing,
		PhasesJSON:    phases,
		CreatedBy:     "operator",
	}
	if err := team.SetSlots(slots); err != nil {
		t.Fatalf("SetSlots: %v", err)
	}

	if err := s.CreateTeam(ctx, team); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if team.ID == "" {
		t.Fatal("CreateTeam did not populate ID")
	}
	if team.CreatedAt == "" || team.UpdatedAt == "" {
		t.Fatalf("CreateTeam did not populate timestamps: %+v", team)
	}

	// Get by id.
	got, err := s.GetTeam(ctx, team.ID)
	if err != nil {
		t.Fatalf("GetTeam: %v", err)
	}
	if got.Name != "Feature Development" || got.Description != team.Description {
		t.Errorf("unexpected row: %+v", got)
	}
	if got.AuthorityJSON != authority {
		t.Errorf("authority_json round-trip: got %q want %q", got.AuthorityJSON, authority)
	}
	if got.RoutingJSON != routing {
		t.Errorf("routing_json round-trip: got %q want %q", got.RoutingJSON, routing)
	}
	if got.PhasesJSON != phases {
		t.Errorf("phases_json round-trip: got %q want %q", got.PhasesJSON, phases)
	}

	gotSlots, err := got.Slots()
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}
	if len(gotSlots) != len(slots) {
		t.Fatalf("Slots round-trip: got %d, want %d", len(gotSlots), len(slots))
	}
	// architect slot: every field populated, including the nullable AgentID.
	arch := gotSlots[0]
	if arch.Name != "architect" || arch.RoleSlug != "architecture-sme" ||
		arch.Resolution != "durable" || arch.ActivationMode != "singleton" ||
		arch.Required != false || arch.Min != 0 || arch.Max != 1 {
		t.Errorf("architect slot round-trip mismatch: %+v", arch)
	}
	if arch.AgentID == nil || *arch.AgentID != architectAgentID {
		t.Errorf("architect slot AgentID round-trip: got %v, want %q", arch.AgentID, architectAgentID)
	}
	// engineer slot: elastic min/max, no AgentID (fresh resolution).
	eng := gotSlots[2]
	if eng.Name != "engineer" || eng.Resolution != "fresh" || eng.ActivationMode != "concurrent" ||
		eng.Required != true || eng.Min != 1 || eng.Max != 4 {
		t.Errorf("engineer slot round-trip mismatch: %+v", eng)
	}
	if eng.AgentID != nil {
		t.Errorf("engineer slot AgentID: got %v, want nil (fresh resolution)", eng.AgentID)
	}

	// Get by name.
	byName, err := s.GetTeamByName(ctx, "Feature Development")
	if err != nil {
		t.Fatalf("GetTeamByName: %v", err)
	}
	if byName.ID != team.ID {
		t.Errorf("GetTeamByName: got id %q, want %q", byName.ID, team.ID)
	}

	// List.
	list, err := s.ListTeams(ctx)
	if err != nil {
		t.Fatalf("ListTeams: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListTeams: got %d, want 1", len(list))
	}

	// Update: rename and shrink the slot list.
	updated := *got
	updated.Name = "Feature Development v2"
	updated.Description = "renamed"
	if err := updated.SetSlots(slots[:2]); err != nil {
		t.Fatalf("SetSlots (update): %v", err)
	}
	if err := s.UpdateTeam(ctx, &updated); err != nil {
		t.Fatalf("UpdateTeam: %v", err)
	}
	afterUpdate, err := s.GetTeam(ctx, team.ID)
	if err != nil {
		t.Fatalf("GetTeam after update: %v", err)
	}
	if afterUpdate.Name != "Feature Development v2" || afterUpdate.Description != "renamed" {
		t.Errorf("update did not apply: %+v", afterUpdate)
	}
	updatedSlots, err := afterUpdate.Slots()
	if err != nil {
		t.Fatalf("Slots after update: %v", err)
	}
	if len(updatedSlots) != 2 {
		t.Errorf("update did not shrink slots: got %d, want 2", len(updatedSlots))
	}
	if afterUpdate.UpdatedAt == afterUpdate.CreatedAt {
		// Not a hard failure (clock resolution), but worth flagging if it
		// silently never advances.
		t.Logf("updated_at did not change from created_at (may be same-second update): %+v", afterUpdate)
	}

	// Delete.
	if err := s.DeleteTeam(ctx, team.ID); err != nil {
		t.Fatalf("DeleteTeam: %v", err)
	}
	if _, err := s.GetTeam(ctx, team.ID); !errors.Is(err, ErrTeamNotFound) {
		t.Errorf("post-delete GetTeam: got %v, want ErrTeamNotFound", err)
	}
	if _, err := s.GetTeamByName(ctx, "Feature Development v2"); !errors.Is(err, ErrTeamNotFound) {
		t.Errorf("post-delete GetTeamByName: got %v, want ErrTeamNotFound", err)
	}
}

// TestTeam_UniqueName confirms the idx_teams_name UNIQUE index actually
// rejects a duplicate name at the DB layer.
func TestTeam_UniqueName(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	first := &Team{Name: "Duplicate Name"}
	if err := s.CreateTeam(ctx, first); err != nil {
		t.Fatalf("CreateTeam (first): %v", err)
	}
	second := &Team{Name: "Duplicate Name"}
	if err := s.CreateTeam(ctx, second); err == nil {
		t.Fatal("CreateTeam (second, duplicate name): expected error, got nil")
	}
}

// TestTeam_DefaultsAndNotFound confirms JSON sub-structure defaults apply
// when left unset, and that unknown-id lookups/mutations return
// ErrTeamNotFound rather than a generic error.
func TestTeam_DefaultsAndNotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	team := &Team{Name: "Defaults Only"}
	if err := s.CreateTeam(ctx, team); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if team.SlotsJSON != "[]" || team.AuthorityJSON != "[]" || team.RoutingJSON != "[]" || team.PhasesJSON != "[]" {
		t.Errorf("unexpected JSON column defaults: %+v", team)
	}
	slots, err := team.Slots()
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}
	if len(slots) != 0 {
		t.Errorf("Slots on default-empty team: got %d, want 0", len(slots))
	}

	if _, err := s.GetTeam(ctx, "does-not-exist"); !errors.Is(err, ErrTeamNotFound) {
		t.Errorf("GetTeam unknown id: got %v, want ErrTeamNotFound", err)
	}
	if _, err := s.GetTeamByName(ctx, "does-not-exist"); !errors.Is(err, ErrTeamNotFound) {
		t.Errorf("GetTeamByName unknown name: got %v, want ErrTeamNotFound", err)
	}
	if err := s.UpdateTeam(ctx, &Team{ID: "does-not-exist", Name: "x"}); !errors.Is(err, ErrTeamNotFound) {
		t.Errorf("UpdateTeam unknown id: got %v, want ErrTeamNotFound", err)
	}
	if err := s.DeleteTeam(ctx, "does-not-exist"); !errors.Is(err, ErrTeamNotFound) {
		t.Errorf("DeleteTeam unknown id: got %v, want ErrTeamNotFound", err)
	}
}

// TestTeam_SlotsValidation confirms SetSlots rejects an invalid
// Resolution/ActivationMode enum value or an inverted Min/Max range before
// the JSON ever reaches the DB.
func TestTeam_SlotsValidation(t *testing.T) {
	team := &Team{Name: "Invalid"}

	if err := team.SetSlots([]TeamSlotDefinition{{Name: "x", Resolution: "bogus"}}); err == nil {
		t.Error("SetSlots: expected error for invalid resolution, got nil")
	}
	if err := team.SetSlots([]TeamSlotDefinition{{Name: "x", ActivationMode: "bogus"}}); err == nil {
		t.Error("SetSlots: expected error for invalid activation_mode, got nil")
	}
	if err := team.SetSlots([]TeamSlotDefinition{{Name: "x", Min: 5, Max: 1}}); err == nil {
		t.Error("SetSlots: expected error for min > max, got nil")
	}
	if err := team.SetSlots([]TeamSlotDefinition{{Name: ""}}); err == nil {
		t.Error("SetSlots: expected error for empty slot name, got nil")
	}

	// A directly-assigned (bypassing SetSlots) invalid SlotsJSON string is
	// still caught by CreateTeam/UpdateTeam's own validateTeamSlots call --
	// exercised via CreateTeam here since that's the real insert path.
	s := newTestStore(t)
	ctx := context.Background()
	raw, err := json.Marshal([]TeamSlotDefinition{{Name: "y", Resolution: "not-a-real-value"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	bad := &Team{Name: "Invalid Raw", SlotsJSON: string(raw)}
	if err := s.CreateTeam(ctx, bad); err == nil {
		t.Error("CreateTeam: expected error for invalid raw slots_json, got nil")
	}
}
