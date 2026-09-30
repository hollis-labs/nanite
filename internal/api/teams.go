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
	"errors"
	"net/http"

	"github.com/hollis-labs/nanite/internal/service"
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

// --- Handlers --------------------------------------------------------------

// handleListTeams lists every team, ordered by name (store.ListTeams' own
// ordering).
//
// GET /api/teams
func (a *API) handleListTeams(w http.ResponseWriter, r *http.Request) {
	rows, err := a.Services.Teams.List(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, teamsToView(rows))
}

// handleGetTeam fetches one teams row by id.
//
// GET /api/teams/{id}
func (a *API) handleGetTeam(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	row, err := a.Services.Teams.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrTeamNotFound) {
			a.errorResp(w, http.StatusNotFound, "team not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, teamToView(row))
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
	if err := a.Services.Teams.Create(r.Context(), &team); err != nil {
		a.teamWriteError(w, err)
		return
	}
	a.jsonResp(w, http.StatusCreated, teamToView(&team))
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
	current, err := a.Services.Teams.Get(r.Context(), id)
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

	saved, err := a.Services.Teams.Update(r.Context(), current, service.TeamPatch{
		Name:          req.Name,
		Description:   req.Description,
		SlotsJSON:     req.SlotsJSON,
		AuthorityJSON: req.AuthorityJSON,
		RoutingJSON:   req.RoutingJSON,
		PhasesJSON:    req.PhasesJSON,
	})
	if err != nil {
		a.teamWriteError(w, err)
		return
	}
	a.jsonResp(w, http.StatusOK, teamToView(saved))
}

// handleDeleteTeam removes a teams row. Storage-only, per
// store.DeleteTeam's own doc comment: this does not check for or cascade
// into any TeamRun (workflow_runs) launched against this Team --
// TASKS/teams/08-team-run-launcher.md's concern, not this task's.
//
// DELETE /api/teams/{id}
func (a *API) handleDeleteTeam(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Services.Teams.Delete(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrTeamNotFound) {
			a.errorResp(w, http.StatusNotFound, "team not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"id": id, "status": "deleted"})
}

// teamWriteError maps a TeamService create/update error: an invalid
// definition is a 400 listing every problem, a missing team a 404, a
// rejected write a 400 with the store's message, anything else a 500.
func (a *API) teamWriteError(w http.ResponseWriter, err error) {
	var ve *service.TeamValidationError
	var we *service.TeamWriteError
	switch {
	case errors.As(err, &ve):
		a.jsonResp(w, http.StatusBadRequest, map[string]any{"valid": false, "errors": ve.Errs})
	case errors.Is(err, store.ErrTeamNotFound):
		a.errorResp(w, http.StatusNotFound, "team not found")
	case errors.As(err, &we):
		a.errorResp(w, http.StatusBadRequest, we.Error())
	default:
		a.errorResp(w, http.StatusInternalServerError, err.Error())
	}
}
