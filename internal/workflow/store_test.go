package workflow

import (
	"testing"
	"time"
)

func makeRunState(pipelineID, runID string, status RunStatus) *RunState {
	return &RunState{
		PipelineID: pipelineID,
		RunID:      runID,
		Status:     status,
		StepStates: map[string]*StepState{},
		StartedAt:  time.Now(),
	}
}

func makePipelineInfo(id, name string) PipelineInfo {
	return PipelineInfo{
		ID:          id,
		Name:        name,
		Description: "Test pipeline " + id,
		StepCount:   3,
	}
}

func TestRunStore_AddAndGet(t *testing.T) {
	store := NewRunStore(10)

	info := makePipelineInfo("pipe-1", "Pipeline One")
	run := makeRunState("pipe-1", "run-abc", RunCompleted)

	store.Add(info, run)

	got, ok := store.Get("run-abc")
	if !ok {
		t.Fatal("expected to find run-abc, got not found")
	}
	if got.Run.RunID != "run-abc" {
		t.Errorf("expected RunID run-abc, got %s", got.Run.RunID)
	}
	if got.Pipeline.ID != "pipe-1" {
		t.Errorf("expected PipelineID pipe-1, got %s", got.Pipeline.ID)
	}
	if got.Pipeline.Name != "Pipeline One" {
		t.Errorf("expected pipeline name 'Pipeline One', got %s", got.Pipeline.Name)
	}
	if got.Run.Status != RunCompleted {
		t.Errorf("expected status completed, got %s", got.Run.Status)
	}
}

func TestRunStore_List(t *testing.T) {
	store := NewRunStore(10)

	store.Add(makePipelineInfo("pipe-1", "Pipeline One"), makeRunState("pipe-1", "run-1", RunCompleted))
	store.Add(makePipelineInfo("pipe-1", "Pipeline One"), makeRunState("pipe-1", "run-2", RunFailed))
	store.Add(makePipelineInfo("pipe-2", "Pipeline Two"), makeRunState("pipe-2", "run-3", RunCompleted))

	// List all — should return 3, newest first.
	all := store.List("", "")
	if len(all) != 3 {
		t.Fatalf("expected 3 runs, got %d", len(all))
	}
	// Newest first: run-3, run-2, run-1.
	if all[0].Run.RunID != "run-3" {
		t.Errorf("expected newest first (run-3), got %s", all[0].Run.RunID)
	}

	// Filter by pipeline ID.
	byPipe := store.List("pipe-1", "")
	if len(byPipe) != 2 {
		t.Fatalf("expected 2 runs for pipe-1, got %d", len(byPipe))
	}

	// Filter by status.
	completed := store.List("", string(RunCompleted))
	if len(completed) != 2 {
		t.Fatalf("expected 2 completed runs, got %d", len(completed))
	}

	// Filter by both.
	pipe1Failed := store.List("pipe-1", string(RunFailed))
	if len(pipe1Failed) != 1 {
		t.Fatalf("expected 1 failed run for pipe-1, got %d", len(pipe1Failed))
	}
	if pipe1Failed[0].Run.RunID != "run-2" {
		t.Errorf("expected run-2, got %s", pipe1Failed[0].Run.RunID)
	}
}

func TestRunStore_RingBuffer(t *testing.T) {
	store := NewRunStore(3)

	for i := 1; i <= 5; i++ {
		runID := "run-" + string(rune('0'+i))
		store.Add(makePipelineInfo("pipe-1", "Pipeline One"), makeRunState("pipe-1", runID, RunCompleted))
	}

	all := store.List("", "")
	if len(all) != 3 {
		t.Fatalf("expected 3 runs (ring buffer cap), got %d", len(all))
	}

	// Oldest (run-1, run-2) should have been evicted.
	_, ok1 := store.Get("run-1")
	_, ok2 := store.Get("run-2")
	if ok1 {
		t.Error("run-1 should have been evicted")
	}
	if ok2 {
		t.Error("run-2 should have been evicted")
	}

	// run-3, run-4, run-5 should remain.
	for _, id := range []string{"run-3", "run-4", "run-5"} {
		if _, ok := store.Get(id); !ok {
			t.Errorf("expected %s to be present", id)
		}
	}
}

func TestRunStore_AppendEvent(t *testing.T) {
	store := NewRunStore(10)

	info := makePipelineInfo("pipe-1", "Pipeline One")
	run := makeRunState("pipe-1", "run-evt", RunRunning)
	store.Add(info, run)

	event := Event{
		Type:       "step.started",
		PipelineID: "pipe-1",
		RunID:      "run-evt",
		StepID:     "step-a",
		Data:       map[string]any{"attempt": 1},
		Timestamp:  time.Now(),
	}

	store.AppendEvent("run-evt", event)

	got, ok := store.Get("run-evt")
	if !ok {
		t.Fatal("expected run-evt to exist")
	}
	if len(got.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got.Events))
	}
	if got.Events[0].Type != "step.started" {
		t.Errorf("expected event type step.started, got %s", got.Events[0].Type)
	}
	if got.Events[0].StepID != "step-a" {
		t.Errorf("expected step-a, got %s", got.Events[0].StepID)
	}
}
