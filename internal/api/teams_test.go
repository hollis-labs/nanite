package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestTeamsAPI_CreateGetListPatchDeleteLifecycle exercises all five CRUD
// endpoints against a real store, per this task's "Done means": create,
// get, list, patch, delete.
func TestTeamsAPI_CreateGetListPatchDeleteLifecycle(t *testing.T) {
	_, mux := newTestAPI(t)

	createBody := `{
		"name": "Feature Development",
		"description": "SME scenario from 15-teams.md",
		"slots_json": "[{\"name\":\"architect\",\"role_slug\":\"architecture-sme\",\"resolution\":\"durable\",\"agent_id\":\"nanite-architect\",\"activation_mode\":\"singleton\",\"required\":false,\"min\":0,\"max\":1},{\"name\":\"orchestrator\",\"role_slug\":\"orchestrator\",\"resolution\":\"fresh\",\"activation_mode\":\"singleton\",\"required\":true,\"min\":1,\"max\":1}]",
		"authority_json": "[{\"from_slot\":\"orchestrator\",\"verb\":\"may_spawn\",\"to_slot\":\"engineer\"}]",
		"routing_json": "{\"rules\":[{\"name\":\"arch-question\",\"phrases\":[\"architecture\"],\"target_slot\":\"architect\"}],\"coordinator_slot\":\"orchestrator\"}",
		"phases_json": "[{\"id\":\"scope_work\",\"kind\":\"flex\",\"active_slots\":[\"orchestrator\"],\"exit_trigger\":{\"self_tool\":\"mark_ready_for_review\"}},{\"id\":\"review_gate\",\"kind\":\"gate\",\"approver_slot\":\"reviewer\"}]"
	}`

	req := httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewBufferString(createBody))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create team = %d body=%s", w.Code, w.Body.String())
	}
	var created store.Team
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("created team has empty id")
	}
	if created.Name != "Feature Development" {
		t.Fatalf("created.Name = %q, want %q", created.Name, "Feature Development")
	}
	if created.CreatedAt == "" || created.UpdatedAt == "" {
		t.Fatalf("created team missing timestamps: %+v", created)
	}

	slots, err := created.Slots()
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}
	if len(slots) != 2 {
		t.Fatalf("created team has %d slots, want 2", len(slots))
	}
	if slots[0].Resolution != "durable" || slots[1].ActivationMode != "singleton" {
		t.Fatalf("slot fields did not round-trip: %+v", slots)
	}

	routing, err := created.Routing()
	if err != nil {
		t.Fatalf("Routing: %v", err)
	}
	if len(routing.Rules) != 1 || routing.Rules[0].TargetSlot != "architect" {
		t.Fatalf("routing did not round-trip: %+v", routing)
	}

	phases, err := created.Phases()
	if err != nil {
		t.Fatalf("Phases: %v", err)
	}
	if len(phases) != 2 || phases[1].ApproverSlot != "reviewer" {
		t.Fatalf("phases did not round-trip: %+v", phases)
	}

	// GET
	req = httptest.NewRequest(http.MethodGet, "/api/teams/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get team = %d body=%s", w.Code, w.Body.String())
	}
	var fetched store.Team
	if err := json.NewDecoder(w.Body).Decode(&fetched); err != nil {
		t.Fatalf("decode fetched: %v", err)
	}
	if fetched.ID != created.ID {
		t.Fatalf("fetched.ID = %q, want %q", fetched.ID, created.ID)
	}

	// LIST
	req = httptest.NewRequest(http.MethodGet, "/api/teams", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list teams = %d body=%s", w.Code, w.Body.String())
	}
	var list []store.Team
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list teams = %d rows, want 1", len(list))
	}

	// PATCH: rename + shrink slots.
	patchBody := `{
		"name": "Feature Development v2",
		"slots_json": "[{\"name\":\"orchestrator\",\"role_slug\":\"orchestrator\",\"resolution\":\"fresh\",\"activation_mode\":\"singleton\",\"required\":true,\"min\":1,\"max\":1}]"
	}`
	req = httptest.NewRequest(http.MethodPatch, "/api/teams/"+created.ID, bytes.NewBufferString(patchBody))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("patch team = %d body=%s", w.Code, w.Body.String())
	}
	var patched store.Team
	if err := json.NewDecoder(w.Body).Decode(&patched); err != nil {
		t.Fatalf("decode patched: %v", err)
	}
	if patched.Name != "Feature Development v2" {
		t.Fatalf("patched.Name = %q, want %q", patched.Name, "Feature Development v2")
	}
	// Untouched field (description) must survive the patch unchanged.
	if patched.Description != "SME scenario from 15-teams.md" {
		t.Fatalf("patched.Description = %q, want unchanged", patched.Description)
	}
	patchedSlots, err := patched.Slots()
	if err != nil {
		t.Fatalf("Slots (patched): %v", err)
	}
	if len(patchedSlots) != 1 {
		t.Fatalf("patched team has %d slots, want 1", len(patchedSlots))
	}

	// DELETE
	req = httptest.NewRequest(http.MethodDelete, "/api/teams/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete team = %d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/teams/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", w.Code)
	}
}

// TestTeamsAPI_CreateDefaultsWithoutSubStructures confirms a minimal
// create (name only) round-trips with the store's own "[]" defaults for
// every JSON sub-structure column.
func TestTeamsAPI_CreateDefaultsWithoutSubStructures(t *testing.T) {
	_, mux := newTestAPI(t)

	req := httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewBufferString(`{"name":"Minimal Team"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create minimal team = %d body=%s", w.Code, w.Body.String())
	}
	var created store.Team
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.SlotsJSON != "[]" || created.AuthorityJSON != "[]" || created.RoutingJSON != "[]" || created.PhasesJSON != "[]" {
		t.Fatalf("unexpected JSON column defaults: %+v", created)
	}
}

// TestTeamsAPI_GetPatchDeleteUnknownID404 proves the not-found path for
// every id-addressed endpoint, per this task's "Done means".
func TestTeamsAPI_GetPatchDeleteUnknownID404(t *testing.T) {
	_, mux := newTestAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/teams/does-not-exist", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get unknown id = %d, want 404 body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/teams/does-not-exist", bytes.NewBufferString(`{"name":"x"}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("patch unknown id = %d, want 404 body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/teams/does-not-exist", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("delete unknown id = %d, want 404 body=%s", w.Code, w.Body.String())
	}
}

// TestTeamsAPI_CreateRejectsMissingName proves the plain required-field
// validation path (mirrors validateReflexDefinition's own "name is
// required" check).
func TestTeamsAPI_CreateRejectsMissingName(t *testing.T) {
	_, mux := newTestAPI(t)
	req := httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create without name = %d, want 400 body=%s", w.Code, w.Body.String())
	}
}

// TestTeamsAPI_CreateRejectsInvalidBodyJSON proves a genuinely malformed
// (non-JSON) request body is rejected with 400, not a 5xx.
func TestTeamsAPI_CreateRejectsInvalidBodyJSON(t *testing.T) {
	_, mux := newTestAPI(t)
	req := httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewBufferString(`{not json`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with malformed body = %d, want 400 body=%s", w.Code, w.Body.String())
	}
}

// TestTeamsAPI_CreateRejectsMalformedSlotsJSON is this task's required
// regression test (Done means bullet 2): malformed JSON inside slots_json
// is rejected with a clear 400 at write time, not left to surface later as
// a decode failure in the compiler/launcher.
func TestTeamsAPI_CreateRejectsMalformedSlotsJSON(t *testing.T) {
	_, mux := newTestAPI(t)
	body := `{"name":"Bad Slots","slots_json":"{not valid json"}`
	req := httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with malformed slots_json = %d, want 400 body=%s", w.Code, w.Body.String())
	}
	assertTeamValidationErrorMentions(t, w.Body.Bytes(), "slots_json")
}

// TestTeamsAPI_CreateRejectsInvalidSlotResolution is this task's required
// enum-validation test (step 3): an out-of-range Resolution value is
// rejected at write time.
func TestTeamsAPI_CreateRejectsInvalidSlotResolution(t *testing.T) {
	_, mux := newTestAPI(t)
	body := `{"name":"Bad Resolution","slots_json":"[{\"name\":\"x\",\"resolution\":\"bogus\",\"activation_mode\":\"singleton\"}]"}`
	req := httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with invalid resolution = %d, want 400 body=%s", w.Code, w.Body.String())
	}
	assertTeamValidationErrorMentions(t, w.Body.Bytes(), "resolution")
}

// TestTeamsAPI_CreateRejectsInvalidActivationMode is this task's other
// required enum-validation test (step 3): an out-of-range ActivationMode
// value is rejected at write time.
func TestTeamsAPI_CreateRejectsInvalidActivationMode(t *testing.T) {
	_, mux := newTestAPI(t)
	body := `{"name":"Bad Activation Mode","slots_json":"[{\"name\":\"x\",\"resolution\":\"fresh\",\"activation_mode\":\"parallel\"}]"}`
	req := httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with invalid activation_mode = %d, want 400 body=%s", w.Code, w.Body.String())
	}
	assertTeamValidationErrorMentions(t, w.Body.Bytes(), "activation_mode")
}

// TestTeamsAPI_CreateRejectsMalformedRoutingJSON proves routing_json's full
// typed validation (task 09's TeamRouting) runs at the write boundary --
// both a non-JSON string and a structurally-invalid rule (missing
// target_slot) are rejected.
func TestTeamsAPI_CreateRejectsMalformedRoutingJSON(t *testing.T) {
	_, mux := newTestAPI(t)

	body := `{"name":"Bad Routing A","routing_json":"{not valid json"}`
	req := httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with malformed routing_json = %d, want 400 body=%s", w.Code, w.Body.String())
	}
	assertTeamValidationErrorMentions(t, w.Body.Bytes(), "routing_json")

	body = `{"name":"Bad Routing B","routing_json":"{\"rules\":[{\"name\":\"r1\",\"phrases\":[\"x\"]}]}"}`
	req = httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewBufferString(body))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with routing rule missing target_slot = %d, want 400 body=%s", w.Code, w.Body.String())
	}
	assertTeamValidationErrorMentions(t, w.Body.Bytes(), "routing_json")
}

// TestTeamsAPI_CreateRejectsMalformedPhasesJSON proves phases_json's full
// typed validation (task 07's TeamPhase) runs at the write boundary --
// both a non-JSON string and a structurally-invalid phase (a flex phase
// missing active_slots/exit_trigger) are rejected. This is the one place
// this API layer gives phases_json write-time validation at all, since
// store.CreateTeam/UpdateTeam deliberately do not call validateTeamPhases
// themselves (see SetPhases' own doc comment in internal/store/teams.go).
func TestTeamsAPI_CreateRejectsMalformedPhasesJSON(t *testing.T) {
	_, mux := newTestAPI(t)

	body := `{"name":"Bad Phases A","phases_json":"[not valid json"}`
	req := httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with malformed phases_json = %d, want 400 body=%s", w.Code, w.Body.String())
	}
	assertTeamValidationErrorMentions(t, w.Body.Bytes(), "phases_json")

	body = `{"name":"Bad Phases B","phases_json":"[{\"id\":\"scope_work\",\"kind\":\"flex\"}]"}`
	req = httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewBufferString(body))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with flex phase missing active_slots/exit_trigger = %d, want 400 body=%s", w.Code, w.Body.String())
	}
	assertTeamValidationErrorMentions(t, w.Body.Bytes(), "phases_json")
}

// TestTeamsAPI_CreateRejectsNonArrayAuthorityJSON is this task's required
// regression test for authority_json's JSON-shape-only validation: no
// typed TeamAuthority shape exists in internal/store/teams.go (task 04
// built a separate normalized table, team_authority_grants, instead), so
// this endpoint only confirms authority_json is well-formed JSON of the
// column's own expected top-level shape -- an array, matching
// store.CreateTeam/UpdateTeam's own "[]" DEFAULT. A JSON object (even a
// structurally reasonable one) at the top level is rejected, as is
// non-JSON text.
func TestTeamsAPI_CreateRejectsNonArrayAuthorityJSON(t *testing.T) {
	_, mux := newTestAPI(t)

	body := `{"name":"Bad Authority A","authority_json":"{not valid json"}`
	req := httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with malformed authority_json = %d, want 400 body=%s", w.Code, w.Body.String())
	}
	assertTeamValidationErrorMentions(t, w.Body.Bytes(), "authority_json")

	body = `{"name":"Bad Authority B","authority_json":"{\"orchestrator.may_spawn\":[\"engineer\"]}"}`
	req = httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewBufferString(body))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with object-shaped authority_json = %d, want 400 body=%s", w.Code, w.Body.String())
	}
	assertTeamValidationErrorMentions(t, w.Body.Bytes(), "authority_json")

	// A well-formed array, even with arbitrary element shape (no typed
	// sub-structure to check element-by-element), is accepted.
	body = `{"name":"Good Authority","authority_json":"[{\"from_slot\":\"orchestrator\",\"verb\":\"may_spawn\",\"to_slot\":\"engineer\"}]"}`
	req = httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewBufferString(body))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create with array-shaped authority_json = %d, want 201 body=%s", w.Code, w.Body.String())
	}
}

// TestTeamsAPI_PatchRejectsMalformedSlotsJSON confirms PATCH runs the same
// write-boundary validation as POST for a patched field.
func TestTeamsAPI_PatchRejectsMalformedSlotsJSON(t *testing.T) {
	_, mux := newTestAPI(t)

	req := httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewBufferString(`{"name":"Patch Target"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create team = %d body=%s", w.Code, w.Body.String())
	}
	var created store.Team
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created: %v", err)
	}

	patchBody := `{"slots_json":"[{\"name\":\"x\",\"resolution\":\"not-real\",\"activation_mode\":\"singleton\"}]"}`
	req = httptest.NewRequest(http.MethodPatch, "/api/teams/"+created.ID, bytes.NewBufferString(patchBody))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("patch with invalid resolution = %d, want 400 body=%s", w.Code, w.Body.String())
	}
	assertTeamValidationErrorMentions(t, w.Body.Bytes(), "resolution")

	// Confirm the row was NOT mutated by the rejected patch.
	req = httptest.NewRequest(http.MethodGet, "/api/teams/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var stillCurrent store.Team
	if err := json.NewDecoder(w.Body).Decode(&stillCurrent); err != nil {
		t.Fatalf("decode after rejected patch: %v", err)
	}
	if stillCurrent.SlotsJSON != "[]" {
		t.Fatalf("rejected patch mutated slots_json: %+v", stillCurrent)
	}
}

// assertTeamValidationErrorMentions decodes a {"valid":false,"errors":[...]}
// response body and fails the test unless at least one error string
// contains substr -- mirrors this task's own instruction that malformed
// JSON/invalid enums must be "covered by an explicit test, not just
// implied."
func assertTeamValidationErrorMentions(t *testing.T, body []byte, substr string) {
	t.Helper()
	var resp struct {
		Valid  bool     `json:"valid"`
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode validation error response: %v (body=%s)", err, body)
	}
	if resp.Valid {
		t.Fatalf("expected valid=false, got true: %s", body)
	}
	if len(resp.Errors) == 0 {
		t.Fatalf("expected a non-empty errors list, got: %s", body)
	}
	found := false
	for _, e := range resp.Errors {
		if bytes.Contains([]byte(e), []byte(substr)) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected an error mentioning %q, got: %+v", substr, resp.Errors)
	}
}
