// This file implements TASKS/loops/10-loop-launcher-and-api.md: Goal CRUD
// plus the Loop trigger-surface REST routes docs/engineering/architecture/
// 21-loops.md's "Trigger surface" section names -- "Manual/API launch"
// (LoopLauncher.Launch) and "Human resolution of waiting_on_escalation"
// (LoopLauncher.ResolveEscalation).
//
// Structural template: internal/api/schedules.go / internal/api/teams.go /
// internal/api/team_runs.go -- this task's own instruction to check those
// files for the current house style (request decode, validation, store
// call, response encode, error handling) before inventing anything new.
// Goal CRUD below mirrors teams.go's own CRUD handlers almost verbatim
// (thin wrappers over task 01's store.Goal CRUD, internal/store/goals.go);
// the loop-launch/cancel/resolve handlers mirror team_runs.go's
// handleLaunchTeam shape (decode a request DTO, call the real launcher,
// translate a launcher error to 400/404/409, encode the result).
//
// --- Auth/permission model (this task's own documented call) -----------
//
// Standard operator auth, not provenance-tier-style gating -- per this
// task's own instruction to check Scheduling's already-landed
// TASKS/scheduling/09-operator-http-api.md call and mirror it, not
// reinvent it. That task's own Work Log confirms (re-verified directly
// against internal/server/server.go before writing this file, not just
// copied from that Work Log's prose): every /api/* route, this file's
// handlers included once registered via RegisterRoutes on the same mux,
// already passes through the server's fixed middleware chain -- recover ->
// logging -> CORS -> basicAuthMiddleware -> callerIdentityMiddleware ->
// bodyLimitMiddleware -- applied uniformly at the mux level, not opted
// into per-route. No narrower "operator-only" tier exists below that
// already in use by any comparable endpoint (schedules/teams/durable-agents
// all rely on the same uniform chain), and 21-loops.md's own "What this
// session did not decide" list explicitly leaves "auth/permission model for
// launching, canceling, or force-resolving a LoopRun escalation" open --
// so nothing narrower is added here, matching both precedents' own
// "acceptable to leave open and let real usage inform the answer" call.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/hollis-labs/nanite/internal/loop"
	"github.com/hollis-labs/nanite/internal/store"
)

// --- Request/response shapes ---------------------------------------------
//
// No OpenAPI/schema doc is asked for by this task -- shapes are documented
// here, next to the handlers that produce/consume them.

// goalCreateRequest is POST /api/goals' request body. Intent is required;
// every other field is optional and mirrors a store.Goal authoring field
// 1:1 (see that type's own doc comments, internal/store/goals.go). Status
// defaults to store.GoalStatusDraft when omitted (CreateGoal's own
// default), matching 21-loops.md's Decision 2 framing of draft/defined as
// the pre-launch authoring states a Goal can sit in before any Loop
// launches against it.
type goalCreateRequest struct {
	ParentGoalID       string   `json:"parent_goal_id,omitempty"`
	Intent             string   `json:"intent"`
	DesiredState       []string `json:"desired_state,omitempty"`
	Constraints        []string `json:"constraints,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	Invariants         []string `json:"invariants,omitempty"`
	Priority           string   `json:"priority,omitempty"`
	Scope              string   `json:"scope,omitempty"`
	Owner              string   `json:"owner,omitempty"`
	Source             string   `json:"source,omitempty"`
	Status             string   `json:"status,omitempty"`
}

// goalPatchRequest is PATCH /api/goals/{id}'s request body. Every field is
// optional (a pointer -- unset means "leave this column alone"), mirroring
// schedules.go's schedulePatchRequest convention. status is intentionally
// routed through store.UpdateGoalStatus (the narrow lifecycle updater)
// rather than store.UpdateGoal (the full "definition" column replacer) --
// the same split those two store methods themselves document; this handler
// preserves that split rather than collapsing it. id, created_at,
// activated_at, and completed_at are engine/bookkeeping-owned and not
// exposed for direct PATCH at all -- simply absent from this struct,
// matching this codebase's "an unknown JSON field is silently ignored by
// json.Decode, not rejected" precedent for an immutable field
// (TASKS/reflex-taxonomy/05's provenance_tier call, cited by schedules.go's
// own identical convention).
type goalPatchRequest struct {
	ParentGoalID       *string   `json:"parent_goal_id"`
	Intent             *string   `json:"intent"`
	DesiredState       *[]string `json:"desired_state"`
	Constraints        *[]string `json:"constraints"`
	AcceptanceCriteria *[]string `json:"acceptance_criteria"`
	Invariants         *[]string `json:"invariants"`
	Priority           *string   `json:"priority"`
	Scope              *string   `json:"scope"`
	Owner              *string   `json:"owner"`
	Source             *string   `json:"source"`
	Status             *string   `json:"status"`
}

// loopGoalSpecRequest is a JSON-friendly mirror of loop.GoalSpec (=
// loop.LoopGoalSpec, types.go), which carries no json tags of its own --
// it's an internal engine-input type, not meant for direct
// (de)serialization. Every field maps 1:1 onto loop.GoalSpec's own fields.
type loopGoalSpecRequest struct {
	ParentGoalID       string   `json:"parent_goal_id,omitempty"`
	Intent             string   `json:"intent"`
	DesiredState       []string `json:"desired_state,omitempty"`
	Constraints        []string `json:"constraints,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	Invariants         []string `json:"invariants,omitempty"`
	Priority           string   `json:"priority,omitempty"`
	Scope              string   `json:"scope,omitempty"`
	Owner              string   `json:"owner,omitempty"`
	Source             string   `json:"source,omitempty"`
}

func (r loopGoalSpecRequest) toGoalSpec() *loop.GoalSpec {
	return &loop.GoalSpec{
		ParentGoalID:       r.ParentGoalID,
		Intent:             r.Intent,
		DesiredState:       r.DesiredState,
		Constraints:        r.Constraints,
		AcceptanceCriteria: r.AcceptanceCriteria,
		Invariants:         r.Invariants,
		Priority:           r.Priority,
		Scope:              r.Scope,
		Owner:              r.Owner,
		Source:             r.Source,
	}
}

// loopLaunchRequest is POST /api/loops' request body -- a JSON-friendly
// mirror of loop.LoopLaunchRequest (launcher.go). ContinuationPolicy and
// Budget reuse loop.ContinuationPolicy/store.Budget directly (both already
// carry their own snake_case json tags -- no separate DTO needed for
// either). Exactly one of GoalID/InlineGoal must be set; that union check
// is left to LoopEngine.Run's own resolveGoal (via LoopLauncher.Launch,
// which forwards both straight through) rather than duplicated here -- see
// internal/loop/launcher.go's own package doc comment.
type loopLaunchRequest struct {
	GoalID             *string                  `json:"goal_id,omitempty"`
	InlineGoal         *loopGoalSpecRequest     `json:"inline_goal,omitempty"`
	DefinitionName     string                   `json:"definition_name"`
	Budget             *store.Budget            `json:"budget,omitempty"`
	ContinuationPolicy *loop.ContinuationPolicy `json:"continuation_policy,omitempty"`
	WorkflowParams     map[string]any           `json:"workflow_params,omitempty"`
	AgentProfileID     string                   `json:"agent_profile_id"`
	ProjectID          string                   `json:"project_id,omitempty"`
	ParentSessionID    string                   `json:"parent_session_id,omitempty"`
	TimeoutSeconds     int                      `json:"timeout_seconds,omitempty"`
}

// loopDecisionResponse is loopResultResponse's LastDecision field shape --
// a JSON-friendly mirror of loop.Decision (decide.go), which also carries
// no json tags (an internal engine-output type).
type loopDecisionResponse struct {
	Kind     string                    `json:"kind"`
	Reason   string                    `json:"reason,omitempty"`
	Revision *loopGoalRevisionResponse `json:"revision,omitempty"`
}

type loopGoalRevisionResponse struct {
	DesiredState       []string `json:"desired_state,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
}

// loopResultResponse is the response shape for every Loop action endpoint
// (POST /api/loops, /cancel, /resolve) -- a JSON-friendly mirror of
// loop.LoopResult (types.go), which is exactly what Launch/Cancel/
// ResolveEscalation return: "the LoopRun's status once this call has made
// all the progress it currently can" (that type's own doc comment).
// LastDecision is omitted when the call returned before any iteration's
// evaluation ran this call (e.g. a force_complete/force_cancel override, or
// LoopResult's own zero-value Kind case documented on that type).
type loopResultResponse struct {
	LoopRunID        string                `json:"loop_run_id"`
	Status           string                `json:"status"`
	CurrentIteration int                   `json:"current_iteration"`
	LastDecision     *loopDecisionResponse `json:"last_decision,omitempty"`
}

func toLoopResultResponse(result loop.LoopResult) loopResultResponse {
	resp := loopResultResponse{
		LoopRunID:        result.LoopRunID,
		Status:           result.Status,
		CurrentIteration: result.CurrentIteration,
	}
	if result.LastDecision.Kind != "" {
		dec := &loopDecisionResponse{
			Kind:   string(result.LastDecision.Kind),
			Reason: result.LastDecision.Reason,
		}
		if result.LastDecision.Revision != nil {
			dec.Revision = &loopGoalRevisionResponse{
				DesiredState:       result.LastDecision.Revision.DesiredState,
				AcceptanceCriteria: result.LastDecision.Revision.AcceptanceCriteria,
			}
		}
		resp.LastDecision = dec
	}
	return resp
}

// --- Goal CRUD handlers ----------------------------------------------------

// handleCreateGoal creates a new goals row -- task 01's store.CreateGoal,
// thinly wrapped. Field-level validation (intent required, status enum
// membership) is left to CreateGoal itself; any error it returns is
// caller-correctable request-shape input, translated to 400, matching
// team_runs.go's own "every launcher/store failure is a 400" convention
// for this kind of thin wrapper.
//
// POST /api/goals
func (a *API) handleCreateGoal(w http.ResponseWriter, r *http.Request) {
	var req goalCreateRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	g := &store.Goal{
		ParentGoalID: req.ParentGoalID,
		Intent:       req.Intent,
		Priority:     req.Priority,
		Scope:        req.Scope,
		Owner:        req.Owner,
		Source:       req.Source,
		Status:       req.Status,
	}
	if err := g.SetDesiredState(req.DesiredState); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := g.SetConstraints(req.Constraints); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := g.SetAcceptanceCriteria(req.AcceptanceCriteria); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := g.SetInvariants(req.Invariants); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := a.Services.Store.CreateGoal(r.Context(), g); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, g)
}

// handleGetGoal fetches one goals row by id.
//
// GET /api/goals/{id}
func (a *API) handleGetGoal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g, err := a.Services.Store.GetGoal(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrGoalNotFound) {
			a.errorResp(w, http.StatusNotFound, "goal not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, g)
}

// handleListGoals lists goals rows, optionally filtered by status and/or
// parent_goal_id -- task 01's store.GoalFilter's own two documented filter
// fields.
//
// GET /api/goals
// GET /api/goals?status={status}
// GET /api/goals?parent_goal_id={parentGoalID}
func (a *API) handleListGoals(w http.ResponseWriter, r *http.Request) {
	filter := store.GoalFilter{
		Status:       r.URL.Query().Get("status"),
		ParentGoalID: r.URL.Query().Get("parent_goal_id"),
	}
	rows, err := a.Services.Store.ListGoals(r.Context(), filter)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, rows)
}

// handlePatchGoal updates an existing goals row -- "definition" columns via
// store.UpdateGoal, status via store.UpdateGoalStatus. See
// goalPatchRequest's own doc comment for the status/UpdateGoal split and
// which fields are deliberately not patchable at all.
//
// PATCH /api/goals/{id}
func (a *API) handlePatchGoal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	current, err := a.Services.Store.GetGoal(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrGoalNotFound) {
			a.errorResp(w, http.StatusNotFound, "goal not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	var req goalPatchRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	updated := *current
	touchedDefinition := false

	if req.ParentGoalID != nil {
		updated.ParentGoalID = *req.ParentGoalID
		touchedDefinition = true
	}
	if req.Intent != nil {
		updated.Intent = *req.Intent
		touchedDefinition = true
	}
	if req.DesiredState != nil {
		if err := updated.SetDesiredState(*req.DesiredState); err != nil {
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
		touchedDefinition = true
	}
	if req.Constraints != nil {
		if err := updated.SetConstraints(*req.Constraints); err != nil {
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
		touchedDefinition = true
	}
	if req.AcceptanceCriteria != nil {
		if err := updated.SetAcceptanceCriteria(*req.AcceptanceCriteria); err != nil {
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
		touchedDefinition = true
	}
	if req.Invariants != nil {
		if err := updated.SetInvariants(*req.Invariants); err != nil {
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
		touchedDefinition = true
	}
	if req.Priority != nil {
		updated.Priority = *req.Priority
		touchedDefinition = true
	}
	if req.Scope != nil {
		updated.Scope = *req.Scope
		touchedDefinition = true
	}
	if req.Owner != nil {
		updated.Owner = *req.Owner
		touchedDefinition = true
	}
	if req.Source != nil {
		updated.Source = *req.Source
		touchedDefinition = true
	}

	if touchedDefinition {
		if err := a.Services.Store.UpdateGoal(r.Context(), &updated); err != nil {
			if errors.Is(err, store.ErrGoalNotFound) {
				a.errorResp(w, http.StatusNotFound, "goal not found")
				return
			}
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if req.Status != nil {
		if err := a.Services.Store.UpdateGoalStatus(r.Context(), id, *req.Status); err != nil {
			if errors.Is(err, store.ErrGoalNotFound) {
				a.errorResp(w, http.StatusNotFound, "goal not found")
				return
			}
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	saved, err := a.Services.Store.GetGoal(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, saved)
}

// handleDeleteGoal removes a goals row.
//
// DELETE /api/goals/{id}
func (a *API) handleDeleteGoal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Services.Store.DeleteGoal(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrGoalNotFound) {
			a.errorResp(w, http.StatusNotFound, "goal not found")
			return
		}
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"id": id, "status": "deleted"})
}

// handleListGoalEvidence lists goal_evidence rows for a goal -- task 02's
// store.ListGoalEvidence, thinly wrapped. Confirms the goal itself exists
// first (a plain GetGoal call) so a typo'd id 404s clearly rather than
// silently returning an empty list -- ListGoalEvidence itself has no way to
// distinguish "goal exists, no evidence yet" from "no such goal."
//
// GET /api/goals/{id}/evidence
// GET /api/goals/{id}/evidence?loop_run_id={loopRunID}
// GET /api/goals/{id}/evidence?evidence_type={evidenceType}
func (a *API) handleListGoalEvidence(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := a.Services.Store.GetGoal(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrGoalNotFound) {
			a.errorResp(w, http.StatusNotFound, "goal not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	filter := store.GoalEvidenceFilter{
		LoopRunID:    r.URL.Query().Get("loop_run_id"),
		EvidenceType: r.URL.Query().Get("evidence_type"),
	}
	rows, err := a.Services.Store.ListGoalEvidence(r.Context(), id, filter)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, rows)
}

// --- Loop trigger-surface handlers -----------------------------------------

// handleLaunchLoop is the "Manual/API launch" trigger surface
// (21-loops.md) -- LoopLauncher.Launch, thinly wrapped.
//
// POST /api/loops
func (a *API) handleLaunchLoop(w http.ResponseWriter, r *http.Request) {
	if a.loopLauncher == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "loop launcher not available")
		return
	}

	var req loopLaunchRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	launchReq := loop.LoopLaunchRequest{
		GoalID:          req.GoalID,
		DefinitionName:  req.DefinitionName,
		BudgetOverrides: req.Budget,
		WorkflowParams:  req.WorkflowParams,
		AgentProfileID:  req.AgentProfileID,
		ProjectID:       req.ProjectID,
		ParentSessionID: req.ParentSessionID,
		TimeoutSeconds:  req.TimeoutSeconds,
	}
	if req.InlineGoal != nil {
		launchReq.InlineGoal = req.InlineGoal.toGoalSpec()
	}
	if req.ContinuationPolicy != nil {
		launchReq.ContinuationPolicy = *req.ContinuationPolicy
	}

	result, err := a.loopLauncher.Launch(r.Context(), launchReq)
	if err != nil {
		if errors.Is(err, loop.ErrLoopRunAlreadyActive) {
			a.errorResp(w, http.StatusConflict, err.Error())
			return
		}
		// Every other Launch failure this batch's own tests exercise
		// (missing definition_name/agent_profile_id, an invalid
		// GoalID/InlineGoal union, an unknown workflow name) is
		// caller-correctable request-shape input, not an unexpected server
		// fault -- a 400, matching team_runs.go's own handleLaunchTeam
		// posture for the identical kind of launcher-error surface.
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, toLoopResultResponse(result))
}

// handleGetLoop fetches one loop_runs row by id.
//
// GET /api/loops/{id}
func (a *API) handleGetLoop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	lr, err := a.Services.Store.GetLoopRun(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrLoopRunNotFound) {
			a.errorResp(w, http.StatusNotFound, "loop run not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, lr)
}

// handleListLoops lists loop_runs rows, optionally filtered by goal_id
// and/or status -- task 03's store.LoopRunFilter.
//
// GET /api/loops
// GET /api/loops?goal_id={goalID}
// GET /api/loops?status={status}
func (a *API) handleListLoops(w http.ResponseWriter, r *http.Request) {
	filter := store.LoopRunFilter{
		GoalID: r.URL.Query().Get("goal_id"),
	}
	if status := r.URL.Query().Get("status"); status != "" {
		filter.Statuses = []string{status}
	}
	rows, err := a.Services.Store.ListLoopRuns(r.Context(), filter)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, rows)
}

// handleCancelLoop is an operator-initiated hard stop -- LoopLauncher.Cancel,
// thinly wrapped.
//
// POST /api/loops/{id}/cancel
func (a *API) handleCancelLoop(w http.ResponseWriter, r *http.Request) {
	if a.loopLauncher == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "loop launcher not available")
		return
	}
	id := r.PathValue("id")
	if err := a.loopLauncher.Cancel(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrLoopRunNotFound) {
			a.errorResp(w, http.StatusNotFound, "loop run not found")
			return
		}
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	lr, err := a.Services.Store.GetLoopRun(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, lr)
}

// handleResolveLoopEscalation is the "Human resolution of
// waiting_on_escalation" trigger surface (21-loops.md) -- LoopLauncher.
// ResolveEscalation, thinly wrapped. The request body is entirely
// optional -- an empty body means "resume normally" (loop.EscalationOverride
// nil); decodeOptionalEscalationOverride (below) is what makes an empty
// body a legitimate, non-error input rather than a JSON-decode failure.
//
// POST /api/loops/{id}/resolve
// Request body (optional): loop.EscalationOverride.
func (a *API) handleResolveLoopEscalation(w http.ResponseWriter, r *http.Request) {
	if a.loopLauncher == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "loop launcher not available")
		return
	}
	id := r.PathValue("id")

	override, err := decodeOptionalEscalationOverride(r)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	result, err := a.loopLauncher.ResolveEscalation(r.Context(), id, override)
	if err != nil {
		if errors.Is(err, store.ErrLoopRunNotFound) {
			a.errorResp(w, http.StatusNotFound, "loop run not found")
			return
		}
		if errors.Is(err, loop.ErrLoopRunNotWaitingOnEscalation) {
			a.errorResp(w, http.StatusConflict, err.Error())
			return
		}
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, toLoopResultResponse(result))
}

// decodeOptionalEscalationOverride decodes r's body into a
// *loop.EscalationOverride, treating a genuinely empty body (io.EOF --
// no bytes at all, not merely `{}`) as "no override" (nil, nil) rather than
// a decode error. a.decode's own bare json.Decode has no such allowance --
// every other POST handler in this package requires a real body, but this
// endpoint's body is documented as optional (this task's own "What to do"
// #2: "body: optional EscalationOverride").
func decodeOptionalEscalationOverride(r *http.Request) (*loop.EscalationOverride, error) {
	defer func() {
		_ = r.Body.Close() // The server owns request-body cleanup; decode/read errors are handled separately.
	}()
	var body loop.EscalationOverride
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, err
	}
	return &body, nil
}

// handleListLoopIterations lists loop_run_iterations rows for a LoopRun --
// task 04's store.ListLoopRunIterations, thinly wrapped. Confirms the
// LoopRun itself exists first, mirroring handleListGoalEvidence's own
// existence-check convention for the identical reason (ListLoopRunIterations
// can't itself distinguish "no iterations yet" from "no such loop run").
//
// GET /api/loops/{id}/iterations
func (a *API) handleListLoopIterations(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := a.Services.Store.GetLoopRun(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrLoopRunNotFound) {
			a.errorResp(w, http.StatusNotFound, "loop run not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	rows, err := a.Services.Store.ListLoopRunIterations(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, rows)
}
