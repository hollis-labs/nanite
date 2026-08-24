package service

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// fakeSubagentSpawner satisfies SubagentSpawner. Records every Spawn
// invocation and returns canned Status responses so the dispatchSpawner
// adapter can be exercised without a real subagent.Service.
type fakeSubagentSpawner struct {
	gotSpawn subagent.SpawnRequest
	runID    string
	spawnErr error

	// runByID is consulted by Status. Tests that want to vary the
	// returned run between calls populate this map.
	runByID    map[string]*subagent.Run
	statusErr  error
	statusCall int
}

func (f *fakeSubagentSpawner) Spawn(_ context.Context, req subagent.SpawnRequest) (string, error) {
	f.gotSpawn = req
	if f.spawnErr != nil {
		return "", f.spawnErr
	}
	id := f.runID
	if id == "" {
		id = "run-test-1"
	}
	return id, nil
}

func (f *fakeSubagentSpawner) Status(_ context.Context, runID string) (*subagent.Run, error) {
	f.statusCall++
	if f.statusErr != nil {
		return nil, f.statusErr
	}
	if f.runByID == nil {
		return nil, errors.New("no run configured")
	}
	r, ok := f.runByID[runID]
	if !ok {
		return nil, errors.New("run not found")
	}
	return r, nil
}

// fakeMessageReader satisfies MessageReader for prose recovery.
type fakeMessageReader struct {
	bySession map[string][]store.Message
	err       error
}

func (f *fakeMessageReader) ListMessages(ctx context.Context, sessionID string, _ int) ([]store.Message, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.bySession[sessionID], nil
}

// TestDispatchSpawner_HappyPath exercises the full sync-spawn flow:
// the adapter submits a SpawnRequest, reads the terminal Run row, and
// recovers the assistant text from the child session.
func TestDispatchSpawner_HappyPath(t *testing.T) {
	svc := &fakeSubagentSpawner{
		runID: "run-1",
		runByID: map[string]*subagent.Run{
			"run-1": {
				ID:              "run-1",
				ParentSessionID: "parent-s",
				ChildSessionID:  "child-s",
				Status:          subagent.StatusCompleted,
				ResultJSON:      `{"kind":"envelope","version":1,"type":"report-card","title":"Done"}`,
			},
		},
	}
	msgs := &fakeMessageReader{
		bySession: map[string][]store.Message{
			"child-s": {
				// Older system message — must be skipped.
				{Role: "system", Content: `{"text":"system note"}`},
				// Latest assistant message — should be the recovered prose.
				{Role: "assistant", Content: `{"text":"worker did the thing"}`},
			},
		},
	}

	adapter := NewDispatchSpawner(svc, msgs)

	got, err := adapter.Spawn(context.Background(), dispatch.SpawnRequest{
		ParentSessionID: "parent-s",
		ParentAgentID:   "file-default",
		Role:            dispatch.WorkerRoleSlug,
		Prompt:          "fix the typo",
		Mode:            "sync",
	})
	if err != nil {
		t.Fatalf("Spawn err = %v", err)
	}

	// Spawn forwarded the slug + sync mode unchanged.
	if svc.gotSpawn.Role != dispatch.WorkerRoleSlug {
		t.Errorf("spawn Role = %q, want %q", svc.gotSpawn.Role, dispatch.WorkerRoleSlug)
	}
	if svc.gotSpawn.Mode != subagent.ModeSync {
		t.Errorf("spawn Mode = %q, want %q", svc.gotSpawn.Mode, subagent.ModeSync)
	}

	// EnvelopeJSON round-tripped from the run row.
	if got.EnvelopeJSON == "" {
		t.Error("EnvelopeJSON missing — run.ResultJSON should round-trip")
	}
	// Prose summary recovered from the latest assistant message.
	if got.Summary != "worker did the thing" {
		t.Errorf("Summary = %q, want %q (latest assistant text)", got.Summary, "worker did the thing")
	}
}

// TestDispatchSpawner_AsyncCoercedToSync defends the capture contract
// at the wiring layer (defense-in-depth — the dispatch primitive
// already coerces, but the adapter does too in case a future caller
// bypasses ExecuteTask).
func TestDispatchSpawner_AsyncCoercedToSync(t *testing.T) {
	svc := &fakeSubagentSpawner{
		runID: "run-1",
		runByID: map[string]*subagent.Run{
			"run-1": {Status: subagent.StatusCompleted, ResultJSON: "{}"},
		},
	}
	adapter := NewDispatchSpawner(svc, nil)

	if _, err := adapter.Spawn(context.Background(), dispatch.SpawnRequest{
		ParentSessionID: "p",
		Role:            "worker",
		Prompt:          "do",
		Mode:            "async",
	}); err != nil {
		t.Fatalf("Spawn err = %v", err)
	}
	if svc.gotSpawn.Mode != subagent.ModeSync {
		t.Errorf("Mode = %q, want sync (async must be coerced for capture)", svc.gotSpawn.Mode)
	}
}

// TestDispatchSpawner_FailedRunSurfacesError covers the failure cases.
func TestDispatchSpawner_TerminalStateMapping(t *testing.T) {
	cases := []struct {
		name    string
		run     *subagent.Run
		wantErr string
	}{
		{
			name:    "failed",
			run:     &subagent.Run{Status: subagent.StatusFailed, Error: "tool exploded"},
			wantErr: "subagent failed: tool exploded",
		},
		{
			name:    "canceled",
			run:     &subagent.Run{Status: subagent.StatusCanceled},
			wantErr: "subagent canceled",
		},
		{
			name:    "rejected",
			run:     &subagent.Run{Status: subagent.StatusRejected, RejectionReason: "policy"},
			wantErr: "subagent rejected: policy",
		},
		{
			name:    "unexpected non-terminal",
			run:     &subagent.Run{Status: subagent.StatusRunning},
			wantErr: "non-terminal state",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeSubagentSpawner{runID: "r", runByID: map[string]*subagent.Run{"r": tc.run}}
			adapter := NewDispatchSpawner(svc, nil)
			_, err := adapter.Spawn(context.Background(), dispatch.SpawnRequest{
				ParentSessionID: "p", Role: "worker", Prompt: "x",
			})
			if err == nil {
				t.Fatalf("expected err, got nil")
			}
			if !contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %q, want containing %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestDispatchSpawner_SpawnError surfaces upstream Spawn failures.
func TestDispatchSpawner_SpawnError(t *testing.T) {
	svc := &fakeSubagentSpawner{spawnErr: errors.New("subagent svc down")}
	adapter := NewDispatchSpawner(svc, nil)
	_, err := adapter.Spawn(context.Background(), dispatch.SpawnRequest{
		ParentSessionID: "p", Role: "worker", Prompt: "x",
	})
	if err == nil || !contains(err.Error(), "subagent svc down") {
		t.Errorf("err = %v, want spawn-error wrapped", err)
	}
}

// TestDispatchSpawner_NilMessageReader keeps the adapter functional when
// the message reader is not wired — Summary is empty but no panic.
func TestDispatchSpawner_NilMessageReader(t *testing.T) {
	svc := &fakeSubagentSpawner{
		runID: "r",
		runByID: map[string]*subagent.Run{
			"r": {Status: subagent.StatusCompleted, ResultJSON: "{}", ChildSessionID: "child"},
		},
	}
	adapter := NewDispatchSpawner(svc, nil)
	got, err := adapter.Spawn(context.Background(), dispatch.SpawnRequest{
		ParentSessionID: "p", Role: "worker", Prompt: "x",
	})
	if err != nil {
		t.Fatalf("Spawn err = %v", err)
	}
	if got.Summary != "" {
		t.Errorf("Summary = %q, want empty (nil messages reader)", got.Summary)
	}
}
