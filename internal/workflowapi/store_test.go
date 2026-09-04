package workflowapi

import (
	"testing"
	"time"
)

func testRunState(pipelineID, runID string, status RunStatus) *RunState {
	return &RunState{
		PipelineID: pipelineID,
		RunID:      runID,
		Status:     status,
		StepStates: map[string]*StepState{},
		StartedAt:  time.Now(),
	}
}

func testPipelineInfo(id string) PipelineInfo {
	return PipelineInfo{ID: id, Name: "Pipeline " + id, StepCount: 3}
}

func TestRunStoreFiltersEvictsAndUpdatesCompatibilityRecords(t *testing.T) {
	store := NewRunStore(3)
	store.Add(testPipelineInfo("pipe-1"), testRunState("pipe-1", "run-1", RunCompleted))
	store.Add(testPipelineInfo("pipe-1"), testRunState("pipe-1", "run-2", RunFailed))
	store.Add(testPipelineInfo("pipe-2"), testRunState("pipe-2", "run-3", RunCompleted))
	store.Add(testPipelineInfo("pipe-2"), testRunState("pipe-2", "run-4", RunRunning))

	if _, ok := store.Get("run-1"); ok {
		t.Fatal("oldest run was not evicted")
	}
	if got := store.List("pipe-1", string(RunFailed)); len(got) != 1 || got[0].Run.RunID != "run-2" {
		t.Fatalf("filtered runs = %+v, want run-2", got)
	}
	if !store.SetStatus("run-4", RunCanceled) {
		t.Fatal("SetStatus(run-4) = false")
	}
	record, ok := store.Get("run-4")
	if !ok || record.Run.Status != RunCanceled {
		t.Fatalf("updated record = %+v, present=%t", record, ok)
	}
	if store.SetStatus("missing", RunCanceled) {
		t.Fatal("SetStatus(missing) = true")
	}

	event := Event{Type: "step.completed", RunID: "run-4", StepID: "step-a", Timestamp: time.Now()}
	store.AppendEvent("run-4", event)
	record, _ = store.Get("run-4")
	if len(record.Events) != 1 || record.Events[0].Type != event.Type {
		t.Fatalf("events = %+v, want one %q event", record.Events, event.Type)
	}
}

func TestNewRunStoreUsesHistoricalDefaultCapacity(t *testing.T) {
	store := NewRunStore(0)
	for i := 0; i < 51; i++ {
		runID := time.Unix(int64(i), 0).Format(time.RFC3339)
		store.Add(testPipelineInfo("pipeline"), testRunState("pipeline", runID, RunCompleted))
	}
	if got := len(store.List("", "")); got != 50 {
		t.Fatalf("default capacity retained %d records, want 50", got)
	}
}
