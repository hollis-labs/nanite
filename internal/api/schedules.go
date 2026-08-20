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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/robfig/cron/v3"

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
// this file's "Patchable fields" doc comment on handlePatchSchedule for
// which fields are (and are deliberately not) represented here.
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

// --- Validation ------------------------------------------------------------
//
// Shared-validator call (this task's step 3): 07 (the add_schedule reflex
// hook) and 08 (the agent self-tool) are both in flight in parallel
// worktrees as this file is written -- neither has landed here, so there
// is no existing shared validation function to call into, and no way to
// coordinate a shared one live. The functions below are this endpoint's
// own independent implementation, deliberately factored into small
// single-field validators (validateScheduleKind/validateCronSpec/
// validateJobType/validateJobPayload/validateOnFailPolicy/
// validateScheduleStatus) rather than one monolithic block, specifically
// so that consolidating them behind a single cross-producer validator
// later (once 07/08 land and all three can be diffed side by side) is a
// mechanical extraction, not a rewrite. Flagged here as a real follow-up
// candidate, not done in this task: the three producers validate the same
// conceptual fields (schedule kind/spec, job type/payload, retry policy)
// but at different call sites (an HTTP handler here, a reflex hook, a
// self-tool handler) with different error-surfacing conventions (JSON
// error body vs. a logged hook-failure warning vs. a tool-result error) --
// picking the right shared shape needs to see all three, not guess ahead
// of them.
func validateScheduleKind(kind string) error {
	switch kind {
	case store.ScheduleKindCron, store.ScheduleKindOneShot:
		return nil
	default:
		return fmt.Errorf("schedule_kind must be %q or %q (got %q)", store.ScheduleKindCron, store.ScheduleKindOneShot, kind)
	}
}

// validateCronSpec is only called for schedule_kind=cron. It calls
// robfig/cron/v3's own ParseStandard directly -- the exact same parser
// store.ComputeAgentScheduleNextRun wraps -- rather than hand-rolling a
// second cron-validity check, matching this batch's standing "don't
// hand-roll cron-parsing a second time" finding (already flagged once on
// task 05's own Work Log). ComputeAgentScheduleNextRun itself can't be
// reused for validation alone: on a malformed spec it deliberately falls
// back to "due now" rather than surfacing the parse error (see that
// function's own doc comment on why: a NULL/zero next_run is a worse
// failure mode for an already-inserted row than one off-schedule fire) --
// exactly the swallowed signal this handler needs to surface to a caller
// who hasn't inserted anything yet and should get a clear rejection
// instead.
func validateCronSpec(spec string) error {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return fmt.Errorf("schedule_spec is required when schedule_kind=%q", store.ScheduleKindCron)
	}
	if _, err := cron.ParseStandard(trimmed); err != nil {
		return fmt.Errorf("schedule_spec is not a valid cron expression: %w", err)
	}
	return nil
}

func validateJobType(jobType string) error {
	switch jobType {
	case store.ScheduleJobTypeDurableAgentWake, store.ScheduleJobTypeAgentWorkflowRun,
		store.ScheduleJobTypeCommandRun, store.ScheduleJobTypeReflexDispatch:
		return nil
	default:
		return fmt.Errorf("job_type must be one of %q, %q, %q, %q (got %q)",
			store.ScheduleJobTypeDurableAgentWake, store.ScheduleJobTypeAgentWorkflowRun,
			store.ScheduleJobTypeCommandRun, store.ScheduleJobTypeReflexDispatch, jobType)
	}
}

// validateJobPayload only checks that a non-empty payload is well-formed
// JSON. It deliberately does not validate per-job-type field shape (e.g.
// that a durable_agent_wake payload carries instance_id) -- that decode
// happens at dispatch time in internal/scheduler.RunnerAdapter.Enqueue's
// per-job-type enqueueX functions, which already produce a clear,
// specific error if the shape is wrong. Duplicating that per-type field
// validation here would be a second, driftable copy of the same contract
// runner_adapter.go already owns.
func validateJobPayload(payload string) error {
	if payload == "" {
		return nil
	}
	if !json.Valid([]byte(payload)) {
		return fmt.Errorf("job_payload must be valid JSON")
	}
	return nil
}

func validateOnFailPolicy(onFail string) error {
	switch onFail {
	case store.ScheduleOnFailRetry, store.ScheduleOnFailDisable, store.ScheduleOnFailNotify:
		return nil
	default:
		return fmt.Errorf("on_fail must be one of %q, %q, %q (got %q)",
			store.ScheduleOnFailRetry, store.ScheduleOnFailDisable, store.ScheduleOnFailNotify, onFail)
	}
}

func validateScheduleStatus(status string) error {
	switch status {
	case store.ScheduleStatusActive, store.ScheduleStatusPaused, store.ScheduleStatusExpired:
		return nil
	default:
		return fmt.Errorf("status must be one of %q, %q, %q (got %q)",
			store.ScheduleStatusActive, store.ScheduleStatusPaused, store.ScheduleStatusExpired, status)
	}
}

func validateExpiresAt(v string) error {
	if v == "" {
		return nil
	}
	if _, err := time.Parse(time.RFC3339, v); err != nil {
		return fmt.Errorf("expires_at must be RFC3339 (got %q): %w", v, err)
	}
	return nil
}

// --- Handlers --------------------------------------------------------------

// handleListSchedules lists agent_schedules rows, optionally scoped to one
// agent -- mirroring store.ListAgentSchedules' own agent_id scoping.
//
// GET /api/schedules
// GET /api/schedules?agent_id={agentID}
func (a *API) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	var (
		rows []store.AgentSchedule
		err  error
	)
	if agentID != "" {
		rows, err = a.Services.Store.ListAgentSchedules(r.Context(), agentID)
	} else {
		rows, err = a.Services.Store.ListAllAgentSchedules(r.Context())
	}
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, rows)
}

// handleGetSchedule fetches one agent_schedules row by id.
//
// GET /api/schedules/{id}
func (a *API) handleGetSchedule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	row, err := a.Services.Store.GetAgentSchedule(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrAgentScheduleNotFound) {
			a.errorResp(w, http.StatusNotFound, "schedule not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, row)
}

// handleCreateSchedule creates a new agent_schedules row.
//
// POST /api/schedules
// Request: scheduleCreateRequest. Response: the created store.AgentSchedule
// (201 Created).
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
	if req.Name == "" {
		a.errorResp(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Body == "" {
		a.errorResp(w, http.StatusBadRequest, "body is required")
		return
	}
	if err := validateScheduleKind(req.ScheduleKind); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ScheduleKind == store.ScheduleKindCron {
		if err := validateCronSpec(req.ScheduleSpec); err != nil {
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.Status != "" {
		if err := validateScheduleStatus(req.Status); err != nil {
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.ExpiresAt != "" {
		if err := validateExpiresAt(req.ExpiresAt); err != nil {
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.MaxRetries != nil && *req.MaxRetries < 0 {
		a.errorResp(w, http.StatusBadRequest, "max_retries must not be negative")
		return
	}
	if req.OnFail != "" {
		if err := validateOnFailPolicy(req.OnFail); err != nil {
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.JobType != "" {
		if err := validateJobType(req.JobType); err != nil {
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := validateJobPayload(req.JobPayload); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}

	row := store.AgentSchedule{
		ID:           "sched-" + ulid.Make().String(),
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

	// next_run must be computed at insert time, not left NULL -- an
	// inserted row with no next_run would never be picked up by
	// go-scheduler's ListDueSchedules, silently defeating the whole point
	// of this endpoint (the same finding TASKS/scheduling/
	// 05-engine-wiring-and-full-replace.md's Work Log made about
	// managed_durable_configs.go's own insert path, which is why
	// ComputeAgentScheduleNextRun was factored out for exactly this kind
	// of new-row-mid-process caller).
	next := store.ComputeAgentScheduleNextRun(row.ScheduleKind, row.ScheduleSpec, time.Now())
	if !next.IsZero() {
		row.NextRun = next.UTC().Format(time.RFC3339)
	}

	if err := a.Services.Store.InsertAgentSchedule(r.Context(), row); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := a.Services.Store.GetAgentSchedule(r.Context(), row.ID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, created)
}

// handlePatchSchedule updates an existing agent_schedules row.
//
// PATCH /api/schedules/{id}
// Request: schedulePatchRequest. Response: the updated store.AgentSchedule.
//
// Patchable-fields call (this task's step 4): name, session_id,
// schedule_spec, body, priority, status, expires_at, max_retries, on_fail,
// job_payload are patchable in place. agent_id, schedule_kind, and
// job_type are deliberately NOT represented in schedulePatchRequest at
// all -- the same "immutable field simply isn't in the patch struct"
// convention TASKS/reflex-taxonomy/05-provenance-tier-enforcement.md set
// for agent_reflexes.provenance_tier (an unknown JSON field is silently
// ignored by json.Decode, not rejected, matching that precedent exactly).
// Rationale per field:
//   - agent_id: changing ownership of a schedule is a delete-and-recreate
//     operation, not an edit, matching handlePatchAgentReflex/
//     handleDeleteAgentReflex's own "cannot patch a different-agent
//     resource through this endpoint" boundary for the adjacent resource.
//   - schedule_kind: cron vs. one_shot changes next_run's entire
//     interpretation (an empty CronExpr vs. a real one --
//     internal/scheduler/store_adapter.go's toSchedule) and would need to
//     be paired with a schedule_spec change atomically to stay coherent;
//     safer to require delete+recreate than to let the two drift out of
//     sync mid-PATCH.
//   - job_type: job_payload's valid shape is entirely determined by
//     job_type (runner_adapter.go's four Payload structs are not
//     interchangeable). Allowing job_type to change without also
//     replacing job_payload in the same request would produce a row that
//     passes this handler's validation but fails only later, at dispatch
//     time inside RunnerAdapter.Enqueue -- a worse failure mode (silent
//     until the next firing) than rejecting the whole "change what kind
//     of job this is" operation up front and requiring delete+recreate.
//
// job_payload IS patchable on its own (unlike job_type) since a same-type
// payload update (e.g. tweaking a command_run's args) is a legitimate
// in-place edit that doesn't change the row's dispatch contract.
//
// next_run, id, fired_count, last_fired_at, created_at, created_by are
// engine/bookkeeping-owned and not exposed for direct PATCH at all --
// next_run is instead recomputed as a side effect (see below) whenever a
// patch could invalidate the previously-computed value, rather than being
// directly settable (a caller-supplied next_run could violate the CAS
// claim's own invariants if it doesn't match what ComputeAgentScheduleNextRun
// would produce for the row's actual kind/spec).
//
// next_run recomputation: triggered when schedule_spec changes (a cron
// row's next-fire time must reflect the new expression), or when status
// transitions into "active" from a non-active state (reactivating a
// paused/expired row with a stale or NULL next_run would otherwise either
// never fire again or misfire immediately on whatever next_run happened to
// be left over).
func (a *API) handlePatchSchedule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	current, err := a.Services.Store.GetAgentSchedule(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrAgentScheduleNotFound) {
			a.errorResp(w, http.StatusNotFound, "schedule not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	var req schedulePatchRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	updated := *current
	recomputeNextRun := false

	if req.Name != nil {
		if *req.Name == "" {
			a.errorResp(w, http.StatusBadRequest, "name must not be empty")
			return
		}
		updated.Name = *req.Name
	}
	if req.SessionID != nil {
		updated.SessionID = *req.SessionID
	}
	if req.ScheduleSpec != nil {
		if updated.ScheduleKind == store.ScheduleKindCron {
			if err := validateCronSpec(*req.ScheduleSpec); err != nil {
				a.errorResp(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		updated.ScheduleSpec = *req.ScheduleSpec
		recomputeNextRun = true
	}
	if req.Body != nil {
		if *req.Body == "" {
			a.errorResp(w, http.StatusBadRequest, "body must not be empty")
			return
		}
		updated.Body = *req.Body
	}
	if req.Priority != nil {
		updated.Priority = *req.Priority
	}
	if req.Status != nil {
		if err := validateScheduleStatus(*req.Status); err != nil {
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
		if *req.Status == store.ScheduleStatusActive && current.Status != store.ScheduleStatusActive {
			recomputeNextRun = true
		}
		updated.Status = *req.Status
	}
	if req.ExpiresAt != nil {
		if err := validateExpiresAt(*req.ExpiresAt); err != nil {
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
		updated.ExpiresAt = *req.ExpiresAt
	}
	if req.MaxRetries != nil {
		if *req.MaxRetries < 0 {
			a.errorResp(w, http.StatusBadRequest, "max_retries must not be negative")
			return
		}
		updated.MaxRetries = *req.MaxRetries
	}
	if req.OnFail != nil {
		if err := validateOnFailPolicy(*req.OnFail); err != nil {
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
		updated.OnFail = *req.OnFail
	}
	if req.JobPayload != nil {
		if err := validateJobPayload(*req.JobPayload); err != nil {
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
		updated.JobPayload = *req.JobPayload
	}

	if recomputeNextRun {
		next := store.ComputeAgentScheduleNextRun(updated.ScheduleKind, updated.ScheduleSpec, time.Now())
		if !next.IsZero() {
			updated.NextRun = next.UTC().Format(time.RFC3339)
		}
	}

	// InsertAgentSchedule is an INSERT OR REPLACE upsert keyed on id --
	// the same call managed_durable_configs.go already uses to update an
	// existing row on resync, so reusing it here for PATCH is an
	// already-exercised path, not a new upsert-via-replace pattern. Since
	// `updated` starts as a full copy of `current` and only the patched
	// fields are overridden above, every untouched column round-trips
	// unchanged.
	if err := a.Services.Store.InsertAgentSchedule(r.Context(), updated); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := a.Services.Store.GetAgentSchedule(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, saved)
}

// handleDeleteSchedule removes an agent_schedules row.
//
// DELETE /api/schedules/{id}
func (a *API) handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Services.Store.DeleteAgentSchedule(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrAgentScheduleNotFound) {
			a.errorResp(w, http.StatusNotFound, "schedule not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
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
