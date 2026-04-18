package api

import (
	"log/slog"
	"net/http"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// WorkDiff mirrors the frontend WorkDiff type — a batched set of user-driven
// changes to todos and plans collected between syncs.
type WorkDiff struct {
	TodosChecked   []string             `json:"todos_checked"`
	TodosUnchecked []TodoUncheckedEntry `json:"todos_unchecked"`
	TodosAdded     []string             `json:"todos_added"`
	TodosReordered bool                 `json:"todos_reordered"`

	PlanStepsChecked   []PlanStepRef `json:"plan_steps_checked"`
	PlanStepsUnchecked []PlanStepRef `json:"plan_steps_unchecked"`
	PlansApproved      []string      `json:"plans_approved"`
	PlansRejected      []string      `json:"plans_rejected"`
}

type TodoUncheckedEntry struct {
	ID     string `json:"id"`
	Reason string `json:"reason,omitempty"`
}

type PlanStepRef struct {
	PlanID string `json:"plan_id"`
	StepID string `json:"step_id"`
	Reason string `json:"reason,omitempty"`
}

func (a *API) handleSyncWork(w http.ResponseWriter, r *http.Request) {
	var diff WorkDiff
	if err := a.decode(r, &diff); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	ctx := r.Context()
	svc := a.Services.Todos

	// Apply todo checks (mark done).
	done := "done"
	for _, id := range diff.TodosChecked {
		if _, err := svc.UpdateTodo(ctx, id, service.TodoUpdates{Status: &done}); err != nil {
			slog.Warn("work/sync: check todo failed", "id", id, "err", err)
		}
	}

	// Apply todo unchecks (reopen to pending).
	pending := "pending"
	for _, entry := range diff.TodosUnchecked {
		if _, err := svc.UpdateTodo(ctx, entry.ID, service.TodoUpdates{Status: &pending}); err != nil {
			slog.Warn("work/sync: uncheck todo failed", "id", entry.ID, "err", err)
		}
	}

	// Apply plan step checks (mark done).
	for _, ref := range diff.PlanStepsChecked {
		if err := svc.UpdatePlanStep(ctx, ref.PlanID, ref.StepID, store.PlanStep{Status: "done"}); err != nil {
			slog.Warn("work/sync: check plan step failed", "plan_id", ref.PlanID, "step_id", ref.StepID, "err", err)
		}
	}

	// Apply plan step unchecks (reopen to pending).
	for _, ref := range diff.PlanStepsUnchecked {
		if err := svc.UpdatePlanStep(ctx, ref.PlanID, ref.StepID, store.PlanStep{Status: "pending"}); err != nil {
			slog.Warn("work/sync: uncheck plan step failed", "plan_id", ref.PlanID, "step_id", ref.StepID, "err", err)
		}
	}

	// Plan approval/rejection is handled by dedicated endpoints
	// (/api/plans/{id}/approve, updatePlan with status=abandoned).
	// The sync endpoint only processes todo/step changes and broadcasts.

	// Notify other UI tabs that work items changed.
	a.Services.Streams.BroadcastWorkChanged()

	a.jsonResp(w, http.StatusOK, map[string]bool{"ok": true})
}
