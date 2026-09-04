package workflowcompat

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestLegacyDispositionDecisionJSONContract(t *testing.T) {
	var decisions []LegacyDispositionDecision
	if err := json.Unmarshal([]byte(`[{"run_id":"legacy-1","disposition":"failed","actor":"release-operator","reason":"approved cutover"}]`), &decisions); err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || decisions[0].RunID != "legacy-1" ||
		decisions[0].Disposition != store.WorkflowLegacyDispositionFailed ||
		decisions[0].Actor != "release-operator" || decisions[0].Reason != "approved cutover" {
		t.Fatalf("decoded legacy disposition plan = %+v", decisions)
	}
}

func TestCutoverCoordinatorActivatesSharedAndReplaysAfterRestart(t *testing.T) {
	product := newCutoverTestStore(t)
	coordinator, err := NewCutoverCoordinator(product)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 4, 23, 0, 0, 0, time.UTC)
	report, err := coordinator.PrepareSharedStartup(t.Context(), CutoverRequest{
		Owner: "deployment-a", Token: "boot-a", Now: now,
	})
	if err != nil || report.State.Phase != store.WorkflowCutoverSharedOnly ||
		len(report.PendingLegacy) != 0 || report.State.LeaseOwner != "" {
		t.Fatalf("PrepareSharedStartup = %+v, %v", report, err)
	}

	restarted, err := NewCutoverCoordinator(product)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := restarted.PrepareSharedStartup(t.Context(), CutoverRequest{
		Owner: "deployment-b", Token: "boot-b", Now: now.Add(time.Hour),
	})
	if err != nil || replayed.State.Generation != report.State.Generation ||
		replayed.State.Phase != store.WorkflowCutoverSharedOnly {
		t.Fatalf("restarted cutover replay = %+v, %v; want generation %d", replayed, err, report.State.Generation)
	}
}

func TestCutoverCoordinatorRequiresExplicitAuditedLegacyDisposition(t *testing.T) {
	product := newCutoverTestStore(t)
	if err := product.CreateWorkflowRun(t.Context(), &store.WorkflowRunRow{
		ID: "active-legacy-run", DefinitionName: "legacy definition", Status: "waiting_on_gate",
	}); err != nil {
		t.Fatal(err)
	}
	coordinator, err := NewCutoverCoordinator(product)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 4, 23, 30, 0, 0, time.UTC)
	report, err := coordinator.PrepareSharedStartup(t.Context(), CutoverRequest{
		Owner: "deployment-a", Token: "boot-a", Now: now,
	})
	if !errors.Is(err, ErrExplicitLegacyDispositionRequired) ||
		report.State.Phase != store.WorkflowCutoverQuiescing || len(report.PendingLegacy) != 1 ||
		report.PendingLegacy[0].RunID != "active-legacy-run" ||
		report.PendingLegacy[0].Disposition != store.WorkflowLegacyDispositionPending {
		t.Fatalf("blocked cutover = %+v, %v", report, err)
	}
	persisted, err := product.GetWorkflowRun(t.Context(), "active-legacy-run")
	if err != nil || persisted.Status != "waiting_on_gate" {
		t.Fatalf("cutover silently mutated active legacy run = %+v, %v", persisted, err)
	}

	disposed, err := coordinator.RecordLegacyDisposition(t.Context(), LegacyDispositionRequest{
		CutoverRequest: CutoverRequest{
			Owner: "operator", Token: "decision-1", Now: now.Add(time.Minute),
		},
		RunID: "active-legacy-run", Disposition: store.WorkflowLegacyDispositionCanceled,
		Actor: "operator@example.test", Reason: "approved retirement during shared-engine cutover",
	})
	if err != nil || disposed.FinalStatus != "canceled" || disposed.Actor != "operator@example.test" {
		t.Fatalf("RecordLegacyDisposition = %+v, %v", disposed, err)
	}
	completed, err := coordinator.PrepareSharedStartup(t.Context(), CutoverRequest{
		Owner: "deployment-b", Token: "boot-b", Now: now.Add(2 * time.Minute),
	})
	if err != nil || completed.State.Phase != store.WorkflowCutoverSharedOnly {
		t.Fatalf("complete cutover = %+v, %v", completed, err)
	}
	persisted, err = product.GetWorkflowRun(t.Context(), "active-legacy-run")
	if err != nil || persisted.Status != "canceled" || persisted.Error == "" {
		t.Fatalf("audited legacy terminal projection = %+v, %v", persisted, err)
	}
}

func TestCutoverCoordinatorAppliesOnlyAnExactOperatorDispositionPlan(t *testing.T) {
	product := newCutoverTestStore(t)
	for _, runID := range []string{"legacy-a", "legacy-b"} {
		if err := product.CreateWorkflowRun(t.Context(), &store.WorkflowRunRow{
			ID: runID, DefinitionName: "legacy definition", Status: "running",
		}); err != nil {
			t.Fatal(err)
		}
	}
	coordinator, err := NewCutoverCoordinator(product)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	request := CutoverRequest{Owner: "deployment", Token: "configured-plan", Now: now}
	report, err := coordinator.PrepareSharedStartupWithDecisions(t.Context(), request, []LegacyDispositionDecision{{
		RunID: "legacy-a", Disposition: store.WorkflowLegacyDispositionFailed,
		Actor: "release-operator", Reason: "explicitly approved cutover failure",
	}})
	if !errors.Is(err, ErrExplicitLegacyDispositionRequired) || len(report.PendingLegacy) != 2 {
		t.Fatalf("partial disposition plan = %+v, %v", report, err)
	}
	for _, runID := range []string{"legacy-a", "legacy-b"} {
		run, getErr := product.GetWorkflowRun(t.Context(), runID)
		if getErr != nil || run.Status != "running" {
			t.Fatalf("partial plan mutated %s: %+v, %v", runID, run, getErr)
		}
	}

	decisions := []LegacyDispositionDecision{
		{RunID: "legacy-a", Disposition: store.WorkflowLegacyDispositionFailed, Actor: "release-operator", Reason: "explicitly approved cutover failure"},
		{RunID: "legacy-b", Disposition: store.WorkflowLegacyDispositionCanceled, Actor: "release-operator", Reason: "explicitly approved cutover cancellation"},
	}
	completed, err := coordinator.PrepareSharedStartupWithDecisions(t.Context(), request, decisions)
	if err != nil || completed.State.Phase != store.WorkflowCutoverSharedOnly || len(completed.PendingLegacy) != 0 {
		t.Fatalf("exact disposition plan = %+v, %v", completed, err)
	}
}

func newCutoverTestStore(t *testing.T) *store.Store {
	t.Helper()
	product, err := store.New(t.Context(), filepath.Join(t.TempDir(), "cutover.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := product.Close(t.Context()); closeErr != nil {
			t.Errorf("close cutover store: %v", closeErr)
		}
	})
	return product
}
