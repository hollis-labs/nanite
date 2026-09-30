package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

var teamViewKeys = []string{"id", "name", "description", "slots_json", "authority_json", "routing_json", "phases_json", "created_at", "updated_at", "created_by"}

var teamRunMemberViewKeys = []string{"id", "workflow_run_id", "slot_name", "agent_id", "session_id", "resolved_at", "status"}

func TestTeamViewJSON(t *testing.T) {
	var tm store.Team
	populate(t, &tm)
	assertKeys(t, "TeamView", mustJSON(t, teamToView(&tm)), teamViewKeys)
	assertSameJSON(t, "populated", teamToView(&tm), tm)
	assertSameJSON(t, "empty list", teamsToView([]store.Team{}), []store.Team{})
	assertSameJSON(t, "nil list", teamsToView(nil), []store.Team(nil))
}

func TestTeamRunMemberViewJSON(t *testing.T) {
	var m store.TeamRunMember
	populate(t, &m)
	got := teamRunMembersToView([]store.TeamRunMember{m, {}})
	assertKeys(t, "TeamRunMemberView", mustJSON(t, got[0]), teamRunMemberViewKeys)
	assertSameJSON(t, "list", got, []store.TeamRunMember{m, {}})
	assertSameJSON(t, "empty list", teamRunMembersToView([]store.TeamRunMember{}), []store.TeamRunMember{})
	assertSameJSON(t, "nil list", teamRunMembersToView(nil), []store.TeamRunMember(nil))
}

// Every problem in a definition is reported at once, in the special
// {"valid":false,"errors":[...]} body.
func TestTeams_ValidationCollectsAllErrors(t *testing.T) {
	_, mux := newTestAPI(t)
	w := mcpDo(mux, "POST", "/api/teams", `{"slots_json":"not json","authority_json":"{}"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Valid  bool     `json:"valid"`
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Valid || len(body.Errors) != 3 {
		t.Fatalf("body %s: %v", w.Body.String(), err)
	}
	if body.Errors[0] != "name is required" || !strings.HasPrefix(body.Errors[1], "slots_json: invalid JSON: ") || !strings.HasPrefix(body.Errors[2], "authority_json: must be a well-formed JSON array: ") {
		t.Fatalf("errors = %q", body.Errors)
	}
}

// A PATCH that fails validation writes nothing.
func TestTeams_RejectedPatchWritesNothing(t *testing.T) {
	_, mux := newTestAPI(t)
	w := mcpDo(mux, "POST", "/api/teams", `{"name":"Keep","description":"d"}`)
	var created TeamView
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if rec := mcpDo(mux, "PATCH", "/api/teams/"+created.ID, `{"description":"changed","authority_json":"{}"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	if rec := mcpDo(mux, "GET", "/api/teams/"+created.ID, ""); !strings.Contains(rec.Body.String(), `"description":"d"`) {
		t.Fatalf("rejected patch was written: %s", rec.Body.String())
	}
	rec := mcpDo(mux, "PATCH", "/api/teams/"+created.ID, `{"description":"changed"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"description":"changed"`) || !strings.Contains(rec.Body.String(), `"name":"Keep"`) {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
}

// vanishingTeams serves one team but reports it gone on update, as when it
// is deleted between the PATCH's read and its write.
type vanishingTeams struct{}

func (vanishingTeams) ListTeams(context.Context) ([]store.Team, error) { return nil, nil }
func (vanishingTeams) GetTeam(context.Context, string) (*store.Team, error) {
	return &store.Team{ID: "t", Name: "T"}, nil
}
func (vanishingTeams) CreateTeam(context.Context, *store.Team) error { return nil }
func (vanishingTeams) UpdateTeam(context.Context, *store.Team) error { return store.ErrTeamNotFound }
func (vanishingTeams) DeleteTeam(context.Context, string) error      { return nil }

func TestTeams_PatchOfTeamDeletedMidwayIs404(t *testing.T) {
	a := &API{Services: &service.Container{Teams: service.NewTeamService(vanishingTeams{})}}
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/teams/{id}", a.handlePatchTeam)
	w := mcpDo(mux, "PATCH", "/api/teams/t", `{"description":"x"}`)
	if w.Code != http.StatusNotFound || errorBody(t, w) != "team not found" {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
}
