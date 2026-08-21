// This file implements TASKS/teams/10-team-crud-api.md: standard REST CRUD
// over task 01's teams store (internal/store/teams.go), following this
// codebase's existing precedent -- structurally modeled on
// internal/api/schedules.go (TASKS/scheduling/09-operator-http-api.md), and
// reusing internal/api/reflexes.go's validateReflexDefinition pattern
// (reject malformed sub-structure JSON at the write boundary, not
// downstream at compile/launch time -- task 07/08's problem to not have).
//
// --- Auth/permission model (this task's own documented call, same posture
// as TASKS/scheduling/09's) ---------------------------------------------
//
// Standard operator auth, no additional gating. Every /api/* route
// (registered on the same mux via RegisterRoutes) already passes through
// the server's fixed middleware chain -- recover -> logging -> CORS ->
// basicAuthMiddleware -> callerIdentityMiddleware -> bodyLimitMiddleware --
// applied uniformly at the mux level, confirmed directly against
// internal/server/server.go before deciding, not assumed. There is no
// narrower "operator-only" tier in use by any comparable resource-CRUD
// endpoint in this codebase (agents, reflexes, durable agents, schedules,
// workflows all rely on the same uniform chain) and Teams has no
// plugin-registered producer today that would need a provenance-tier-style
// boundary. This makes a concrete, documented call; it is not a locked
// security decision -- same explicit framing TASKS/scheduling/09's own
// Context section applies to itself, and the same one this task's own
// Context cites verbatim as the posture to reuse here.
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/hollis-labs/nanite/internal/store"
)

// --- Request/response shapes ---------------------------------------------
//
// No OpenAPI/schema doc is asked for by this task -- shapes are documented
// here, next to the handlers that produce/consume them. slots_json/
// authority_json/routing_json/phases_json are plain strings carrying
// caller-supplied JSON text (a JSON string containing JSON), the same
// double-encoding convention internal/api/reflexes.go's trigger_spec/
// action_spec fields already use for this codebase's other free-form JSON
// sub-structure columns -- not raw embedded JSON objects.

// teamCreateRequest is POST /api/teams' request body. name is required;
// every other field is optional -- store.CreateTeam defaults each JSON
// sub-structure column to "[]" when left empty, matching every other JSON
// column producer in this codebase.
type teamCreateRequest struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	SlotsJSON     string `json:"slots_json,omitempty"`
	AuthorityJSON string `json:"authority_json,omitempty"`
	RoutingJSON   string `json:"routing_json,omitempty"`
	PhasesJSON    string `json:"phases_json,omitempty"`
}

// teamPatchRequest is PATCH /api/teams/{id}'s request body. Every field is
// a pointer so an absent JSON key leaves the corresponding column
// untouched, matching schedulePatchRequest's own convention
// (internal/api/schedules.go). Unlike schedules' PATCH, no field here is
// permanently non-patchable -- store.UpdateTeam itself updates every
// mutable column (name, description, and all four JSON sub-structure
// columns) in one call; only id/created_at/created_by are immutable, and
// none of those are represented in this struct at all.
type teamPatchRequest struct {
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	SlotsJSON     *string `json:"slots_json"`
	AuthorityJSON *string `json:"authority_json"`
	RoutingJSON   *string `json:"routing_json"`
	PhasesJSON    *string `json:"phases_json"`
}

// --- Validation ------------------------------------------------------------
//
// validateTeamDefinition mirrors internal/api/reflexes.go's
// validateReflexDefinition: collect every problem with team's current field
// values into a []string (rather than stopping at the first), so a caller
// gets one clear 400 response listing everything wrong, not a
// fix-one-resubmit-find-the-next loop.
//
// Per this task's own instruction, each of the three free-form JSON
// sub-structure columns gets exactly the validation its current typed
// coverage in internal/store/teams.go supports as of this task's dispatch
// -- documented explicitly here so a later task tightening one doesn't have
// to rediscover which is which:
//
//   - slots_json -- FULL typed validation. task 01's TeamSlotDefinition is
//     fully defined; team.SetSlots (which this function calls) runs
//     validateTeamSlots, which is the actual enforcement point for this
//     task's step 3 requirement -- Resolution/ActivationMode are checked
//     against their real enum values ("durable"|"fresh" and
//     "singleton"|"fresh-per-wake"|"concurrent") here, at the write
//     boundary, not left to fail silently at launch (task 08).
//   - phases_json -- FULL typed validation. task 07's TeamPhase is fully
//     defined; team.SetPhases (called here) runs validateTeamPhases. Note
//     SetPhases' own doc comment: it is deliberately NOT wired into
//     store.CreateTeam/UpdateTeam themselves (an already-merged pre-typed
//     test fixture would otherwise retroactively fail), so this API layer
//     calling it explicitly is what actually gives phases_json write-time
//     validation at all -- exactly the "future Team-authoring API" caller
//     that doc comment names by this task's own file path.
//   - routing_json -- FULL typed validation. task 09's TeamRouting/
//     TeamRoutingRule are fully defined; team.SetRouting (called here) runs
//     validateTeamRouting. Same "not wired into CreateTeam/UpdateTeam,
//     validates via this explicit call instead" shape as phases_json above,
//     per SetRouting's own doc comment. "" and the column's own "[]"
//     DEFAULT are both treated as "no routing configured yet" (matching
//     Team.Routing()'s identical special-case for those two literal
//     values) and are not run through TeamRouting's struct decode, which
//     would otherwise fail on an array literal.
//   - authority_json -- JSON-SHAPE-ONLY validation, not full typed
//     validation. Confirmed directly against internal/store/teams.go and
//     internal/store/team_authority.go before writing this: task 04 built
//     the real, enforced authority-grant shape as its own normalized table
//     (team_authority_grants, internal/store/team_authority.go's
//     TeamAuthorityGrant/AuthorizedForVerb) rather than a typed shape for
//     this column -- Team.AuthorityJSON itself remains exactly what task
//     01's own doc comment called it, "a storage placeholder", with no
//     TeamAuthority Go type anywhere in this codebase to validate against.
//     Per this task's own explicit instruction for exactly this
//     no-typed-shape case, this validates only that a non-empty value is
//     well-formed JSON of the column's own expected top-level shape -- an
//     array, matching store.CreateTeam/UpdateTeam's own "[]" DEFAULT for
//     this column. A later task giving this column a real typed shape (or
//     retiring it now that team_authority_grants exists) can tighten this
//     without rediscovering the gap.
func validateTeamDefinition(team *store.Team) []string {
	var errs []string
	if team.Name == "" {
		errs = append(errs, "name is required")
	}

	if team.SlotsJSON != "" {
		var slots []store.TeamSlotDefinition
		if err := json.Unmarshal([]byte(team.SlotsJSON), &slots); err != nil {
			errs = append(errs, "slots_json: invalid JSON: "+err.Error())
		} else if err := team.SetSlots(slots); err != nil {
			// SetSlots' own validateTeamSlots call is the real Resolution/
			// ActivationMode enum + Min/Max-consistency enforcement point.
			errs = append(errs, "slots_json: "+err.Error())
		}
	}

	if team.RoutingJSON != "" && team.RoutingJSON != "[]" {
		var routing store.TeamRouting
		if err := json.Unmarshal([]byte(team.RoutingJSON), &routing); err != nil {
			errs = append(errs, "routing_json: invalid JSON: "+err.Error())
		} else if err := team.SetRouting(routing); err != nil {
			errs = append(errs, "routing_json: "+err.Error())
		}
	}

	if team.PhasesJSON != "" {
		var phases []store.TeamPhase
		if err := json.Unmarshal([]byte(team.PhasesJSON), &phases); err != nil {
			errs = append(errs, "phases_json: invalid JSON: "+err.Error())
		} else if err := team.SetPhases(phases); err != nil {
			errs = append(errs, "phases_json: "+err.Error())
		}
	}

	if team.AuthorityJSON != "" {
		var arr []json.RawMessage
		if err := json.Unmarshal([]byte(team.AuthorityJSON), &arr); err != nil {
			errs = append(errs, "authority_json: must be a well-formed JSON array: "+err.Error())
		}
	}

	return errs
}

// --- Handlers --------------------------------------------------------------

// handleListTeams lists every team, ordered by name (store.ListTeams' own
// ordering).
//
// GET /api/teams
func (a *API) handleListTeams(w http.ResponseWriter, r *http.Request) {
	rows, err := a.Services.Store.ListTeams(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, rows)
}

// handleGetTeam fetches one teams row by id.
//
// GET /api/teams/{id}
func (a *API) handleGetTeam(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	row, err := a.Services.Store.GetTeam(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrTeamNotFound) {
			a.errorResp(w, http.StatusNotFound, "team not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, row)
}

// handleCreateTeam creates a new teams row.
//
// POST /api/teams
// Request: teamCreateRequest. Response: the created store.Team (201
// Created). id/created_at/updated_at are always store-generated
// (store.CreateTeam mints a uuid.New() id when unset, exactly like every
// other real caller of this store method) -- not accepted from the
// request body at all.
func (a *API) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	var req teamCreateRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	team := store.Team{
		Name:          req.Name,
		Description:   req.Description,
		SlotsJSON:     req.SlotsJSON,
		AuthorityJSON: req.AuthorityJSON,
		RoutingJSON:   req.RoutingJSON,
		PhasesJSON:    req.PhasesJSON,
		CreatedBy:     "operator",
	}
	if errs := validateTeamDefinition(&team); len(errs) > 0 {
		a.jsonResp(w, http.StatusBadRequest, map[string]any{"valid": false, "errors": errs})
		return
	}
	if err := a.Services.Store.CreateTeam(r.Context(), &team); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, &team)
}

// handlePatchTeam updates an existing teams row. Every field is optional
// (teamPatchRequest's pointer fields) -- only fields present in the
// request body are applied on top of the current row, matching
// handlePatchSchedule's "load current, overlay only patched fields, save
// the full merged row" shape.
//
// PATCH /api/teams/{id}
// Request: teamPatchRequest. Response: the updated store.Team.
func (a *API) handlePatchTeam(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	current, err := a.Services.Store.GetTeam(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrTeamNotFound) {
			a.errorResp(w, http.StatusNotFound, "team not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	var req teamPatchRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	updated := *current
	if req.Name != nil {
		updated.Name = *req.Name
	}
	if req.Description != nil {
		updated.Description = *req.Description
	}
	if req.SlotsJSON != nil {
		updated.SlotsJSON = *req.SlotsJSON
	}
	if req.AuthorityJSON != nil {
		updated.AuthorityJSON = *req.AuthorityJSON
	}
	if req.RoutingJSON != nil {
		updated.RoutingJSON = *req.RoutingJSON
	}
	if req.PhasesJSON != nil {
		updated.PhasesJSON = *req.PhasesJSON
	}

	if errs := validateTeamDefinition(&updated); len(errs) > 0 {
		a.jsonResp(w, http.StatusBadRequest, map[string]any{"valid": false, "errors": errs})
		return
	}
	if err := a.Services.Store.UpdateTeam(r.Context(), &updated); err != nil {
		if errors.Is(err, store.ErrTeamNotFound) {
			a.errorResp(w, http.StatusNotFound, "team not found")
			return
		}
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := a.Services.Store.GetTeam(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, saved)
}

// handleDeleteTeam removes a teams row. Storage-only, per
// store.DeleteTeam's own doc comment: this does not check for or cascade
// into any TeamRun (workflow_runs) launched against this Team --
// TASKS/teams/08-team-run-launcher.md's concern, not this task's.
//
// DELETE /api/teams/{id}
func (a *API) handleDeleteTeam(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Services.Store.DeleteTeam(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrTeamNotFound) {
			a.errorResp(w, http.StatusNotFound, "team not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"id": id, "status": "deleted"})
}
