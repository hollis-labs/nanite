package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/hollis-labs/nanite/internal/store"
)

func (a *API) handleListPendingReflexes(w http.ResponseWriter, r *http.Request) {
	rows, err := a.Services.Store.ListPendingReflexes(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, rows)
}

func (a *API) handleApprovePendingReflex(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReviewedBy string `json:"reviewed_by"`
	}
	_ = a.decode(r, &req)
	reflex, err := a.Services.Store.ApprovePendingReflex(r.Context(), r.PathValue("id"), req.ReviewedBy)
	if err != nil {
		if errors.Is(err, store.ErrPendingReflexNotFound) {
			a.errorResp(w, http.StatusNotFound, "pending reflex not found")
			return
		}
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, reflex)
}

func (a *API) handleRejectPendingReflex(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		ReviewedBy string `json:"reviewed_by"`
		Reason     string `json:"reason"`
	}
	_ = a.decode(r, &req)
	if err := a.Services.Store.RejectPendingReflex(r.Context(), id, req.ReviewedBy, req.Reason); err != nil {
		if errors.Is(err, store.ErrPendingReflexNotFound) {
			a.errorResp(w, http.StatusNotFound, "pending reflex not found or already reviewed")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{"id": id, "status": store.PendingReflexStatusRejected})
}

func (a *API) handleListAgentReflexes(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	rows, err := a.Services.Store.ListAgentReflexesForAgent(r.Context(), agent.ID, agent.Class)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, rows)
}

func (a *API) handleCreateAgentReflex(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	var req struct {
		Name                      string `json:"name"`
		TriggerKind               string `json:"trigger_kind"`
		TriggerSpec               string `json:"trigger_spec"`
		ActionKind                string `json:"action_kind"`
		ActionSpec                string `json:"action_spec"`
		Priority                  int64  `json:"priority"`
		OptOutAllowed             *bool  `json:"opt_out_allowed"`
		RecurrenceOverrideSeconds *int64 `json:"recurrence_override_seconds"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// opt_out_allowed defaults to true (permissive) when omitted — same
	// "default-on, agent may opt out" default the DB column and every
	// pre-existing row carry (Phase 1 item 07,
	// TASKS/phase-1/07-add-reflex-opt-out-field.md). Agent-created
	// reflexes through this endpoint are never the hand-picked
	// safety-critical base seeds, so permissive-by-default is correct
	// here; an operator who genuinely needs a non-opt-outable
	// agent-specific reflex can still pass opt_out_allowed:false
	// explicitly.
	optOutAllowed := true
	if req.OptOutAllowed != nil {
		optOutAllowed = *req.OptOutAllowed
	}
	row := store.AgentReflex{
		AgentID:                   agent.ID,
		Name:                      req.Name,
		TriggerKind:               req.TriggerKind,
		TriggerSpec:               req.TriggerSpec,
		ActionKind:                req.ActionKind,
		ActionSpec:                req.ActionSpec,
		Priority:                  req.Priority,
		CreatedBy:                 "operator",
		OptOutAllowed:             optOutAllowed,
		RecurrenceOverrideSeconds: req.RecurrenceOverrideSeconds,
	}
	if errs := a.validateReflexDefinition(r.Context(), row); len(errs) > 0 {
		a.jsonResp(w, http.StatusBadRequest, map[string]any{"valid": false, "errors": errs})
		return
	}
	id, err := a.Services.Store.InsertAgentReflex(r.Context(), row)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := a.Services.Store.GetAgentReflex(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, created)
}

func (a *API) handlePatchAgentReflex(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	reflexID := r.PathValue("reflexId")
	current, err := a.Services.Store.GetAgentReflex(r.Context(), reflexID)
	if err != nil {
		if errors.Is(err, store.ErrAgentReflexNotFound) {
			a.errorResp(w, http.StatusNotFound, "reflex not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if current.AgentID != agent.ID {
		a.errorResp(w, http.StatusBadRequest, "cannot patch inherited or different-agent reflex through this endpoint")
		return
	}
	var req struct {
		Name          *string `json:"name"`
		TriggerKind   *string `json:"trigger_kind"`
		TriggerSpec   *string `json:"trigger_spec"`
		ActionKind    *string `json:"action_kind"`
		ActionSpec    *string `json:"action_spec"`
		Status        *string `json:"status"`
		Priority      *int64  `json:"priority"`
		FiredCount    *int64  `json:"fired_count"`
		LastFiredAt   *string `json:"last_fired_at"`
		OptOutAllowed *bool   `json:"opt_out_allowed"`
		// RecurrenceOverrideSeconds: nil (field omitted) leaves the
		// existing override untouched; 0 clears it back to "inherit the
		// kind/system default"; a positive value sets an explicit
		// override. Mirrors the ttl_seconds=0-means-unset convention
		// already in use for agent_known_skills/agent_known_tools rows —
		// a recurrence of exactly zero seconds is never a meaningful
		// override, so it's free to serve as the "clear" sentinel instead
		// of needing a second field to disambiguate "not sent" from
		// "explicitly nulled."
		RecurrenceOverrideSeconds *int64 `json:"recurrence_override_seconds"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	updated := *current
	if req.Name != nil {
		updated.Name = *req.Name
	}
	if req.TriggerKind != nil {
		updated.TriggerKind = *req.TriggerKind
	}
	if req.TriggerSpec != nil {
		updated.TriggerSpec = *req.TriggerSpec
	}
	if req.ActionKind != nil {
		updated.ActionKind = *req.ActionKind
	}
	if req.ActionSpec != nil {
		updated.ActionSpec = *req.ActionSpec
	}
	if req.Status != nil {
		updated.Status = *req.Status
	}
	if req.Priority != nil {
		updated.Priority = *req.Priority
	}
	if req.FiredCount != nil {
		updated.FiredCount = *req.FiredCount
	}
	if req.LastFiredAt != nil {
		updated.LastFiredAt = *req.LastFiredAt
	}
	if req.OptOutAllowed != nil {
		updated.OptOutAllowed = *req.OptOutAllowed
	}
	if req.RecurrenceOverrideSeconds != nil {
		if *req.RecurrenceOverrideSeconds == 0 {
			updated.RecurrenceOverrideSeconds = nil
		} else {
			updated.RecurrenceOverrideSeconds = req.RecurrenceOverrideSeconds
		}
	}
	if errs := a.validateReflexDefinition(r.Context(), updated); len(errs) > 0 {
		a.jsonResp(w, http.StatusBadRequest, map[string]any{"valid": false, "errors": errs})
		return
	}
	if err := a.Services.Store.UpdateAgentReflex(r.Context(), updated); err != nil {
		if errors.Is(err, store.ErrAgentReflexNotFound) {
			a.errorResp(w, http.StatusNotFound, "reflex not found")
			return
		}
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	reflex, err := a.Services.Store.GetAgentReflex(r.Context(), reflexID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, reflex)
}

func (a *API) handleDeleteAgentReflex(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	reflexID := r.PathValue("reflexId")
	reflex, err := a.Services.Store.GetAgentReflex(r.Context(), reflexID)
	if err != nil {
		if errors.Is(err, store.ErrAgentReflexNotFound) {
			a.errorResp(w, http.StatusNotFound, "reflex not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if reflex.AgentID != agent.ID {
		a.errorResp(w, http.StatusBadRequest, "cannot delete inherited or different-agent reflex through this endpoint")
		return
	}
	if err := a.Services.Store.DeleteAgentReflex(r.Context(), reflexID); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{"id": reflexID, "status": "deleted"})
}

// handleSetAgentReflexOptOut detaches agent from a class-wide reflex it
// would otherwise inherit. Rejects reflexes with opt_out_allowed=false, the
// same way Store.ListAgentReflexesForAgent's own subquery ignores this
// table entirely for those rows (CW-20260918-0023).
// POST /api/agents/{id}/reflexes/{reflexId}/opt-out
func (a *API) handleSetAgentReflexOptOut(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	reflexID := r.PathValue("reflexId")
	reflex, err := a.Services.Store.GetAgentReflex(r.Context(), reflexID)
	if err != nil {
		if errors.Is(err, store.ErrAgentReflexNotFound) {
			a.errorResp(w, http.StatusNotFound, "reflex not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !reflex.OptOutAllowed {
		a.errorResp(w, http.StatusBadRequest, "reflex does not allow opt-out")
		return
	}
	if err := a.Services.Store.SetAgentReflexOptOut(r.Context(), agent.ID, reflexID); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{"agent_id": agent.ID, "reflex_id": reflexID, "opted_out": true})
}

// handleClearAgentReflexOptOut re-enables a class-wide reflex previously
// opted out of via handleSetAgentReflexOptOut.
// DELETE /api/agents/{id}/reflexes/{reflexId}/opt-out
func (a *API) handleClearAgentReflexOptOut(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	reflexID := r.PathValue("reflexId")
	if _, err := a.Services.Store.GetAgentReflex(r.Context(), reflexID); err != nil {
		if errors.Is(err, store.ErrAgentReflexNotFound) {
			a.errorResp(w, http.StatusNotFound, "reflex not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := a.Services.Store.ClearAgentReflexOptOut(r.Context(), agent.ID, reflexID); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{"agent_id": agent.ID, "reflex_id": reflexID, "opted_out": false})
}

func (a *API) handleValidateReflex(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TriggerKind string         `json:"trigger_kind"`
		TriggerSpec string         `json:"trigger_spec"`
		ActionKind  string         `json:"action_kind"`
		ActionSpec  string         `json:"action_spec"`
		State       map[string]any `json:"state"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	row := store.AgentReflex{
		Name:        "validation",
		TriggerKind: req.TriggerKind,
		TriggerSpec: req.TriggerSpec,
		ActionKind:  req.ActionKind,
		ActionSpec:  req.ActionSpec,
		Status:      store.ReflexStatusActive,
	}
	errs := a.validateReflexDefinition(r.Context(), row)
	a.jsonResp(w, http.StatusOK, map[string]any{
		"valid":        len(errs) == 0,
		"errors":       errs,
		"fired":        len(errs) == 0 && evaluatesSimpleReflex(req.TriggerSpec, req.State),
		"state_source": stateSource(req.State),
		"state_summary": map[string]any{
			"messages": messageCount(req.State),
		},
	})
}

// validateReflexDefinition validates row's shape (trigger/action kind and
// spec JSON) and, per TASKS/reflex-taxonomy/05-provenance-tier-enforcement.md,
// enforces the provenance-tier declare allow-list (Facet 3,
// docs/engineering/architecture/10-reflex-action-taxonomy.md): whether the
// resolved provenance tier for this write may even declare row's
// action_kind. Called from all three real touch points that decide what an
// agent_reflexes row would look like — handleCreateAgentReflex,
// handlePatchAgentReflex, and the dry-run handleValidateReflex preview —
// so the same gate applies whether the definition is about to be written
// or merely previewed.
//
// The provenance tier used for the gate check is resolved the same way
// created_by is resolved at each real call site (mostly already fixed by
// construction, per this task's own Context): row.ProvenanceTier is used
// directly when the caller has already set it (handlePatchAgentReflex
// carries the existing row's real tier forward; a synthetic test row can
// set it explicitly to exercise a tier with no live insert path, e.g.
// "plugin"); otherwise it falls back to the same rule
// store.InsertAgentReflex already applies when a caller leaves
// ProvenanceTier unset: CreatedBy == "system" resolves to "system",
// anything else (including handleCreateAgentReflex's hardcoded "operator"
// and handleValidateReflex's unset CreatedBy) resolves to "operator". No
// live call site resolves to "plugin" today — no concrete plugin insert
// path exists (task's own step 5) — so this fallback never invents a
// "plugin" resolution; it only ever mirrors the "operator"/"system" split
// InsertAgentReflex already encodes.
func (a *API) validateReflexDefinition(ctx context.Context, row store.AgentReflex) []string {
	var errs []string
	if row.Name == "" {
		errs = append(errs, "name is required")
	}
	switch row.TriggerKind {
	case store.ReflexTriggerPredicate, store.ReflexTriggerEvent, store.ReflexTriggerInterval:
	default:
		errs = append(errs, fmt.Sprintf("invalid trigger_kind %q", row.TriggerKind))
	}
	if row.TriggerSpec == "" {
		errs = append(errs, "trigger_spec is required")
	} else {
		var spec map[string]any
		if err := json.Unmarshal([]byte(row.TriggerSpec), &spec); err != nil {
			errs = append(errs, "trigger_spec: invalid JSON: "+err.Error())
		}
	}
	validActionKind := true
	switch row.ActionKind {
	case store.ReflexActionInjectReminder, store.ReflexActionForceToolChoice,
		store.ReflexActionSendMessage, store.ReflexActionHaltSession, store.ReflexActionAddSchedule,
		store.ReflexActionDispatchToAgent, store.ReflexActionResumeLoopRun:
	default:
		validActionKind = false
		errs = append(errs, fmt.Sprintf("invalid action_kind %q", row.ActionKind))
	}
	if validActionKind {
		tier := row.ProvenanceTier
		if tier == "" {
			// Same default InsertAgentReflex already applies when a caller
			// leaves ProvenanceTier unset — see this function's doc comment.
			if row.CreatedBy == "system" {
				tier = "system"
			} else {
				tier = "operator"
			}
		}
		allowed, err := a.Services.Store.ActionKindAllowsProvenanceTier(ctx, row.ActionKind, tier)
		if err != nil {
			errs = append(errs, fmt.Sprintf("provenance tier check failed: %v", err))
		} else if !allowed {
			errs = append(errs, fmt.Sprintf("provenance tier %q may not declare action_kind %q", tier, row.ActionKind))
		}
	}
	if row.ActionSpec == "" {
		errs = append(errs, "action_spec is required")
	} else {
		var spec map[string]any
		if err := json.Unmarshal([]byte(row.ActionSpec), &spec); err != nil {
			errs = append(errs, "action_spec: invalid JSON: "+err.Error())
		} else if row.ActionKind == store.ReflexActionDispatchToAgent {
			// dispatch_to_agent's config shape (Phase 4 item 02,
			// TASKS/phase-4/02-dispatch-to-agent-reflex-action-kind-and-broker-migration.md):
			// agent_slug is the one required field — it names the target
			// agent profile's slug/role for event_log capture and (for
			// class-bound reflexes migrated from the retired agent
			// broker) for the reflex's own self-documentation. confidence
			// and reason are optional (reason defaults to "reflex:"+name
			// at the executor call site).
			slug, _ := spec["agent_slug"].(string)
			if slug == "" {
				errs = append(errs, "action_spec: dispatch_to_agent requires a non-empty agent_slug")
			}
		} else if row.ActionKind == store.ReflexActionResumeLoopRun {
			// resume_loop_run's config shape (TASKS/loops/
			// 11-loop-event-predicate-trigger.md, store.
			// ReflexActionResumeLoopRun's own doc comment): loop_run_id is
			// the one required field — Store.ListAgentReflexesForLoopRun
			// reads it back via json_extract against exactly this key.
			loopRunID, _ := spec["loop_run_id"].(string)
			if loopRunID == "" {
				errs = append(errs, "action_spec: resume_loop_run requires a non-empty loop_run_id")
			}
		}
	}
	switch row.Status {
	case "", store.ReflexStatusActive, store.ReflexStatusPaused, store.ReflexStatusExpired:
	default:
		errs = append(errs, fmt.Sprintf("invalid status %q", row.Status))
	}
	return errs
}

func evaluatesSimpleReflex(triggerSpec string, state map[string]any) bool {
	var spec struct {
		Kind   string  `json:"kind"`
		Window int     `json:"window"`
		Op     string  `json:"op"`
		Value  float64 `json:"value"`
	}
	if err := json.Unmarshal([]byte(triggerSpec), &spec); err != nil {
		return false
	}
	if spec.Kind != "tool_calls_window" || spec.Op != "=" || spec.Window <= 0 {
		return false
	}
	messages, ok := state["messages"].([]any)
	if !ok || len(messages) < spec.Window {
		return false
	}
	start := len(messages) - spec.Window
	for _, raw := range messages[start:] {
		msg, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		if msg["tool_calls"] != spec.Value {
			return false
		}
	}
	return true
}

func stateSource(state map[string]any) string {
	if len(state) > 0 {
		return "request"
	}
	return "empty"
}

func messageCount(state map[string]any) int {
	messages, ok := state["messages"].([]any)
	if !ok {
		return 0
	}
	return len(messages)
}
