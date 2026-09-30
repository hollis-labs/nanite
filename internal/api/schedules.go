// This file implements TASKS/scheduling/09-operator-http-api.md:
// /api/schedules, the operator/UI-driven CRUD surface for agent_schedules
// rows, plus a coarse liveness endpoint over the go-scheduler engine's own
// Status(). See docs/engineering/architecture/12-scheduling.md's
// "Producers" section, item 4.
//
// Structural template: internal/api/workflows.go
// (handleListWorkflowRuns/handleGetWorkflowRun/handleCancelWorkflowRun/
// handleRunWorkflow) -- a resource-CRUD-plus-one-action surface already
// living in this codebase, named explicitly by this task's own Context.
//
// --- Auth/permission model (this task's own documented call) -----------
//
// Standard operator auth, not provenance-tier-style gating. Confirmed by
// reading internal/server/server.go before deciding: every /api/* route
// (this file's handlers included, once registered on the same mux via
// RegisterRoutes) already passes through the server's fixed middleware
// chain -- recover -> logging -> CORS -> basicAuthMiddleware ->
// callerIdentityMiddleware -> bodyLimitMiddleware -- applied uniformly at
// the mux level, not opted into per-route. There is no narrower
// "operator-only" tier below that already in use by any other
// operator-facing endpoint in this codebase (agents, reflexes, durable
// agents, workflows all rely on that same uniform chain), and no
// plugin-registered schedule producer exists today that would need a
// provenance-tier boundary the way reflexes' Facet 3 does
// (10-reflex-action-taxonomy.md). So: no additional gating is added here.
// This matches the task file's own recommended default and the identical
// precedent it cites (TASKS/reflex-taxonomy/05's provenance-tier call) for
// "acceptable to leave open and let real usage inform the answer."
package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// --- Request/response shapes ---------------------------------------------
//
// No OpenAPI/schema doc is asked for by this task -- shapes are documented
// here, next to the handlers that produce/consume them.

// scheduleCreateRequest is POST /api/schedules' request body.
//
//   - agent_id, name, schedule_kind, body are required.
//   - schedule_spec is required (and must be a valid robfig/cron/v3
//     standard-form expression) when schedule_kind="cron"; ignored for
//     "one_shot" (agent_schedules has no independent one-shot target-time
//     encoding today -- see store.AgentSchedule's own doc comment).
//   - status, priority, expires_at, max_retries, on_fail, job_type,
//     job_payload are optional; store.InsertAgentSchedule applies the same
//     defaults it applies to every other producer (status=active,
//     max_retries=3, on_fail=retry, job_type=durable_agent_wake,
//     job_payload="{}") when omitted.
type scheduleCreateRequest struct {
	AgentID      string `json:"agent_id"`
	SessionID    string `json:"session_id,omitempty"`
	Name         string `json:"name"`
	ScheduleKind string `json:"schedule_kind"`
	ScheduleSpec string `json:"schedule_spec,omitempty"`
	Body         string `json:"body"`
	Priority     int64  `json:"priority,omitempty"`
	Status       string `json:"status,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
	MaxRetries   *int64 `json:"max_retries,omitempty"`
	OnFail       string `json:"on_fail,omitempty"`
	JobType      string `json:"job_type,omitempty"`
	JobPayload   string `json:"job_payload,omitempty"`
}

// schedulePatchRequest is PATCH /api/schedules/{id}'s request body -- see
// service.SchedulePatch for which fields are (and are deliberately not)
// represented here.
type schedulePatchRequest struct {
	Name         *string `json:"name"`
	SessionID    *string `json:"session_id"`
	ScheduleSpec *string `json:"schedule_spec"`
	Body         *string `json:"body"`
	Priority     *int64  `json:"priority"`
	Status       *string `json:"status"`
	ExpiresAt    *string `json:"expires_at"`
	MaxRetries   *int64  `json:"max_retries"`
	OnFail       *string `json:"on_fail"`
	JobPayload   *string `json:"job_payload"`
}

// scheduleEngineStatusResponse is GET /api/schedules/status' response --
// a coarse liveness signal wrapping gosched.Engine.Status()'s own four
// fields verbatim (Running/LastTickAt/Dispatches/WorkerErrors,
// libs/go-scheduler/engine.go:25-30), plus a "configured" flag for the
// (real, nil-checked, matching AgentCardGenerator/TaskManager's own
// container-field convention) case where main.go hasn't wired an Engine
// into this process at all -- e.g. every handler test in this package,
// which builds a service.Container directly via service.NewContainer
// without the main.go wiring that constructs and Start()s the engine.
//
// Supplementary to, not a replacement for, TASKS/scheduling/
// 06-schedule-fire-telemetry.md's per-firing event rows -- this endpoint
// only ever reads the engine's own in-memory counters, no dependency on
// 06's telemetry table.
type scheduleEngineStatusResponse struct {
	Configured   bool   `json:"configured"`
	Running      bool   `json:"running"`
	LastTickAt   string `json:"last_tick_at,omitempty"`
	Dispatches   int64  `json:"dispatches"`
	WorkerErrors int64  `json:"worker_errors"`
}

// --- Handlers --------------------------------------------------------------

// scheduleError writes the response for a ScheduleService error: not found
// is 404, a rejected row or write is 400, anything else 500.
func (a *API) scheduleError(w http.ResponseWriter, err error) {
	var writeErr *service.ScheduleWriteError
	switch {
	case errors.Is(err, store.ErrAgentScheduleNotFound):
		a.errorResp(w, http.StatusNotFound, "schedule not found")
	case errors.As(err, &writeErr):
		a.errorResp(w, http.StatusBadRequest, err.Error())
	default:
		a.errorResp(w, http.StatusInternalServerError, err.Error())
	}
}

// handleListSchedules lists agent_schedules rows, optionally scoped to one
// agent.
//
// GET /api/schedules
// GET /api/schedules?agent_id={agentID}
func (a *API) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	rows, err := a.Services.Schedules.List(r.Context(), r.URL.Query().Get("agent_id"))
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, agentSchedulesToView(rows))
}

// handleGetSchedule fetches one agent_schedules row by id.
//
// GET /api/schedules/{id}
func (a *API) handleGetSchedule(w http.ResponseWriter, r *http.Request) {
	row, err := a.Services.Schedules.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		a.scheduleError(w, err)
		return
	}
	a.jsonResp(w, http.StatusOK, agentScheduleToView(row))
}

// handleCreateSchedule creates a new agent_schedules row; the service
// assigns its id and first next_run.
//
// POST /api/schedules
// Request: scheduleCreateRequest. Response: the created schedule (201).
func (a *API) handleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	var req scheduleCreateRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.AgentID == "" {
		a.errorResp(w, http.StatusBadRequest, "agent_id is required")
		return
	}
	if _, err := a.Services.Agents.Get(r.Context(), req.AgentID); err != nil {
		a.errorResp(w, http.StatusBadRequest, fmt.Sprintf("agent_id %q does not resolve to a known agent: %v", req.AgentID, err))
		return
	}
	row := store.AgentSchedule{
		AgentID:      req.AgentID,
		SessionID:    req.SessionID,
		Name:         req.Name,
		ScheduleKind: req.ScheduleKind,
		ScheduleSpec: req.ScheduleSpec,
		Body:         req.Body,
		Priority:     req.Priority,
		Status:       req.Status,
		ExpiresAt:    req.ExpiresAt,
		OnFail:       req.OnFail,
		JobType:      req.JobType,
		JobPayload:   req.JobPayload,
	}
	if req.MaxRetries != nil {
		row.MaxRetries = *req.MaxRetries
	}

	created, err := a.Services.Schedules.Create(r.Context(), row)
	if err != nil {
		a.scheduleError(w, err)
		return
	}
	a.jsonResp(w, http.StatusCreated, agentScheduleToView(created))
}

// handlePatchSchedule updates an existing agent_schedules row. Which fields
// are patchable, and when next_run is recomputed, is service.SchedulePatch's
// and ScheduleService.Patch's to say.
//
// PATCH /api/schedules/{id}
// Request: schedulePatchRequest. Response: the updated schedule.
func (a *API) handlePatchSchedule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// A missing schedule is reported before the body is read.
	if _, err := a.Services.Schedules.Get(r.Context(), id); err != nil {
		a.scheduleError(w, err)
		return
	}

	var req schedulePatchRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	saved, err := a.Services.Schedules.Patch(r.Context(), id, service.SchedulePatch{
		Name:         req.Name,
		SessionID:    req.SessionID,
		ScheduleSpec: req.ScheduleSpec,
		Body:         req.Body,
		Priority:     req.Priority,
		Status:       req.Status,
		ExpiresAt:    req.ExpiresAt,
		MaxRetries:   req.MaxRetries,
		OnFail:       req.OnFail,
		JobPayload:   req.JobPayload,
	})
	if err != nil {
		a.scheduleError(w, err)
		return
	}
	a.jsonResp(w, http.StatusOK, agentScheduleToView(saved))
}

// handleDeleteSchedule removes an agent_schedules row.
//
// DELETE /api/schedules/{id}
func (a *API) handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Services.Schedules.Delete(r.Context(), id); err != nil {
		a.scheduleError(w, err)
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"id": id, "status": "deleted"})
}

// handleScheduleEngineStatus exposes gosched.Engine.Status() as a coarse
// liveness signal, per docs/engineering/architecture/12-scheduling.md's
// Observability section ("Engine.Status() is still worth exposing... as a
// coarse liveness signal, but it supplements per-firing event rows, it
// doesn't replace them" -- TASKS/scheduling/06-schedule-fire-telemetry.md
// owns those per-firing rows, independently, in parallel with this task).
//
// GET /api/schedules/status
//
// Services.Engine is nil in every test container built via
// service.NewContainer directly (main.go-only wiring, per that field's own
// doc comment in container.go) and in any real process where the engine
// hasn't been constructed yet -- "configured": false with every other
// field at its zero value is the response in that case, not a 404/503,
// since "the engine isn't wired in this process" is itself a meaningful,
// successfully-reported liveness fact, not an error.
func (a *API) handleScheduleEngineStatus(w http.ResponseWriter, r *http.Request) {
	if a.Services.Engine == nil {
		a.jsonResp(w, http.StatusOK, scheduleEngineStatusResponse{Configured: false})
		return
	}
	st := a.Services.Engine.Status()
	resp := scheduleEngineStatusResponse{
		Configured:   true,
		Running:      st.Running,
		Dispatches:   st.Dispatches,
		WorkerErrors: st.WorkerErrors,
	}
	if !st.LastTickAt.IsZero() {
		resp.LastTickAt = st.LastTickAt.UTC().Format(time.RFC3339)
	}
	a.jsonResp(w, http.StatusOK, resp)
}
