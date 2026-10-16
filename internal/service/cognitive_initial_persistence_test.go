package service

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	permissionlib "github.com/hollis-labs/go-permission"
	"github.com/hollis-labs/nanite/internal/store"
)

// Admission uses the real transaction; only the post-admission snapshot save
// fails. A transient outage permits the failure terminal's fallback save.
type initialSnapshotFailureStore struct {
	*store.Store
	cause      error
	failAll    bool
	admissions atomic.Int64
	saves      atomic.Int64
}

func (s *initialSnapshotFailureStore) CreateCognitiveTurn(ctx context.Context, user *store.Message, id, initial string) error {
	err := s.Store.CreateCognitiveTurn(ctx, user, id, initial)
	if err == nil {
		s.admissions.Add(1)
	}
	return err
}
func (s *initialSnapshotFailureStore) SaveCognitiveTurnSnapshot(ctx context.Context, view, id, data string) error {
	n := s.saves.Add(1)
	if s.failAll || n == 1 {
		return s.cause
	}
	return s.Store.SaveCognitiveTurnSnapshot(ctx, view, id, data)
}

func TestCognitiveInitialSnapshotFailureSettlesBeforeDispatch(t *testing.T) {
	for _, outage := range []string{"transient", "persistent"} {
		t.Run(outage, func(t *testing.T) {
			f := newHandleMessageFixture(t, []characterizationProviderStep{{events: toolTurnEvents(llmtypes.ToolUseBlock{ID: "fixture-call", Name: "fixture_tool", Input: map[string]any{"value": "example"}})}, {events: doneEvents("normal answer")}})
			f.tools.definitions = []llmtypes.ToolDefinition{{Name: "fixture_tool", Description: "fixture tool"}}
			f.svc.permissions = permissionlib.NewEngine(permissionlib.ModeDefault, nil)
			view := createTestDefinedView(t, f)
			backing := &initialSnapshotFailureStore{Store: f.st, cause: errors.New("initial snapshot unavailable"), failAll: outage == "persistent"}
			f.svc.store = backing
			streams := NewStreamManager(f.st)
			streams.turnsOnce.Do(func() { streams.turns = newCognitiveTurns(backing, nil) })
			f.svc.streams = streams
			id, err := f.svc.SubmitCognitiveTurn(t.Context(), view.ID, "accepted input")
			if err != nil || id == "" {
				t.Fatalf("accepted failure lost its turn: %s %v", id, err)
			}
			if backing.admissions.Load() != 1 {
				t.Fatal("real atomic admission did not commit")
			}
			turns := streams.CognitiveTurns()
			snapshot, err := turns.Get(view.ID, id)
			if err != nil || snapshot.State != "failed" || snapshot.Message.Error == nil || snapshot.Message.Error.Code != "persistence_failed" {
				t.Fatalf("failed turn undiscoverable: %+v %v", snapshot, err)
			}
			events, ok := streams.GetStream(id)
			if !ok {
				t.Fatal("accepted producer missing")
			}
			select {
			case event, open := <-events:
				if open {
					t.Fatalf("producer dispatched behind failure: %+v", event)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("accepted failed producer did not close")
			}
			if f.provider.callCount() != 0 || len(f.tools.calls()) != 0 {
				t.Fatalf("failed turn executed: provider=%d tools=%v", f.provider.callCount(), f.tools.calls())
			}
			if _, err = f.st.GetMessage(t.Context(), id); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("failed turn wrote assistant output: %v", err)
			}
			rows, err := f.st.ListMessages(t.Context(), view.ID, 10)
			if err != nil || len(rows) != 1 || rows[0].Role != "user" || rows[0].Content != "accepted input" {
				t.Fatalf("admitted input lost or hidden assistant committed: %+v %v", rows, err)
			}
			f.svc.activeGenMu.Lock()
			active := f.svc.activeGen[view.ID]
			f.svc.activeGenMu.Unlock()
			if active != nil || turns.PendingView(view.ID) {
				t.Fatal("settled failure retained an executing/pending owner")
			}
			// The canonical subscription retains the accepted terminal even when both
			// saves failed. This makes no restart durability claim under that outage.
			subscription, err := turns.Subscribe(t.Context(), view.ID, id, 0)
			if err != nil {
				t.Fatal(err)
			}
			canonical := readCognitiveEvents(t, subscription)
			if len(canonical) != 1 || canonical[0].Code != "persistence_failed" || !canonical[0].IsTerminal() {
				t.Fatalf("failure replay changed: %+v", canonical)
			}
			if outage == "transient" {
				// A later healthy admission proves no orphan generation/queue owner or
				// disabled fixture hides dispatch. Its actual provider and tool both run.
				next, err := f.svc.SubmitCognitiveTurn(t.Context(), view.ID, "healthy input")
				if err != nil || next == id {
					t.Fatalf("healthy turn refused: %s %v", next, err)
				}
				stream, ok := streams.GetStream(next)
				if !ok {
					t.Fatal("healthy stream missing")
				}
				drainStream(stream)
				outcome, err := turns.Get(view.ID, next)
				if err != nil || outcome.State != "completed" || f.provider.callCount() != 2 || len(f.tools.calls()) != 1 {
					t.Fatalf("healthy path lost: %+v %v provider=%d tools=%v", outcome, err, f.provider.callCount(), f.tools.calls())
				}
			}
		})
	}
}
