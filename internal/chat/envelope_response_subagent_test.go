package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	subagenthost "github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/substrate/agent/subagent"
)

// newTestSubagentSvc builds a real subagent.Service backed by an in-memory
// store + EchoRunner, matching the pattern subagent tests use. Settings have
// SubagentApprovalRequired=true so Spawn() returns the run in 'requested'.
func newTestSubagentSvc(t *testing.T) *subagent.Service {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "chat_test.db")
	s, err := storetest.New(t, context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	_ = s.Seed(context. // idempotent
				Background())

	emitter := &stubApprovalEmitter{} // local fake
	settings := subagentSettingsStub{required: true}

	return subagenthost.NewService(s.DB, subagent.EchoRunner{}, nil, emitter, settings)
}

// stubApprovalEmitter: satisfies subagent.ApprovalEmitter without persisting.
type stubApprovalEmitter struct{}

func (stubApprovalEmitter) Emit(_ context.Context, sessionID, envelopeType string, _ []byte) (string, error) {
	return "env-stub-" + sessionID + "-" + envelopeType, nil
}

// subagentSettingsStub: satisfies subagent.SettingsReader.
type subagentSettingsStub struct{ required bool }

func (s subagentSettingsStub) GetUserSettings(ctx context.Context) (*store.UserSettings, error) {
	return &store.UserSettings{SubagentApprovalRequired: s.required}, nil
}

func TestSubagentApprovalHandler_Approve(t *testing.T) {
	assertChatApprovalRequiresVerifiedOwner(t, StatusSubmitted)
}

func TestSubagentApprovalHandler_Reject(t *testing.T) {
	assertChatApprovalRequiresVerifiedOwner(t, StatusCanceled)
}

func TestSubagentApprovalHandler_MissingRunID(t *testing.T) {
	h := NewSubagentApprovalHandler(nil) // svc never reached
	env := store.EnvelopeInstance{
		EnvelopeType: "subagent-spawn-approval",
		EnvelopeJSON: `{}`,
	}
	_, err := h.HandleResponse(context.Background(), env, ResponseV1{
		V: 1, Status: StatusSubmitted,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestSubagentApprovalHandler_UnsupportedStatus(t *testing.T) {
	h := NewSubagentApprovalHandler(nil) // not reached
	env := store.EnvelopeInstance{
		EnvelopeType: "subagent-spawn-approval",
		EnvelopeJSON: `{"run_id":"r-1"}`,
	}
	_, err := h.HandleResponse(context.Background(), env, ResponseV1{
		V: 1, Status: "partial",
	})
	if err == nil {
		t.Fatal("expected error for unsupported status")
	}
}

// Real retained requested rows are not continuation authority. This fixture
// uses the app-owned handler fence and real SDK service, never an issuer or a
// fake authorization port that would revive spawning/approval.
func assertChatApprovalRequiresVerifiedOwner(t *testing.T, status ResponseStatus) {
	t.Helper()
	st := newTestStoreForChat(t)
	sess := &store.Session{Title: "Retained approval parent"}
	if err := st.CreateSession(t.Context(), sess); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ id, status, created string }{
		{"retained-request", "requested", time.Now().UTC().Format(time.RFC3339Nano)},
		{"retained-stale", "requested", "2000-01-01T00:00:00Z"},
		{"retained-completed", "completed", "2000-01-01T00:00:00Z"},
		{"client-request", "requested", time.Now().UTC().Format(time.RFC3339Nano)},
	} {
		if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO subagent_runs(id,parent_session_id,role,prompt,mode,status,created_at) VALUES(?,?,?,?,?,?,?)`, row.id, sess.ID, "claimed-role", "retained prompt", subagent.ModeInteractive, row.status, row.created); err != nil {
			t.Fatal(err)
		}
	}
	runner := &heldChatApprovalRunner{}
	emitter := &heldChatApprovalEmitter{}
	svc := subagenthost.NewService(st.DB, runner, nil, emitter, subagentSettingsStub{required: true})
	handler := NewSubagentApprovalHandler(svc)
	before := heldChatApprovalState(t, st)
	runID, err := svc.Spawn(t.Context(), subagent.SpawnRequest{ParentSessionID: sess.ID, ParentAgentID: "claimed-parent", Role: "claimed-role", Prompt: "hi", Mode: subagent.ModeInteractive})
	if runID != "" || !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("production Spawn=%q,%v want missing verified binding", runID, err)
	}
	assertHeldChatApprovalState(t, st, before)
	for _, serverID := range []string{"retained-request", "retained-stale", "retained-completed", "missing"} {
		wire, marshalErr := json.Marshal(map[string]any{"run_id": serverID})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		envelope := &store.EnvelopeInstance{SessionID: sess.ID, EnvelopeType: "subagent-spawn-approval", EnvelopeJSON: string(wire)}
		if createErr := st.CreateEnvelopeInstance(t.Context(), envelope); createErr != nil {
			t.Fatal(createErr)
		}
		persisted, readErr := st.GetEnvelopeInstance(t.Context(), envelope.ID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		before = heldChatApprovalState(t, st)
		response := ResponseV1{V: 1, Kind: "subagent-spawn-approval", Status: status, Data: map[string]any{"run_id": "client-request", "reason": "claimed reason"}}
		result, responseErr := handler.HandleResponse(t.Context(), *persisted, response)
		if !errors.Is(responseErr, store.ErrVerifiedActorRequired) || result.FollowUp != "" || len(result.TranscriptData) != 0 {
			t.Fatalf("held approval server=%q client=client-request: %+v,%v", serverID, result, responseErr)
		}
		assertHeldChatApprovalState(t, st, before)
		// Nil downstream proves the refusal occurs before SDK continuation.
		_, err = NewSubagentApprovalHandler(nil).HandleResponse(t.Context(), *persisted, response)
		if !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatal(err)
		}
	}
	for _, payload := range []string{`{`, `{}`, `{"run_id":""}`, `{"run_id":42}`} {
		_, inputErr := handler.HandleResponse(t.Context(), store.EnvelopeInstance{EnvelopeJSON: payload}, ResponseV1{V: 1, Status: status, Data: map[string]any{"run_id": "client-request"}})
		if inputErr == nil || errors.Is(inputErr, store.ErrVerifiedActorRequired) || !strings.Contains(inputErr.Error(), "missing run_id") {
			t.Fatalf("invalid persisted payload %s: %v", payload, inputErr)
		}
	}
	_, err = handler.HandleResponse(t.Context(), store.EnvelopeInstance{EnvelopeJSON: `{"run_id":"retained-request"}`}, ResponseV1{V: 1, Status: "partial"})
	if err == nil || errors.Is(err, store.ErrVerifiedActorRequired) || !strings.Contains(err.Error(), "unsupported response status") {
		t.Fatal(err)
	}
	assertHeldChatApprovalState(t, st, before)
	if runner.calls.Load() != 0 || emitter.calls.Load() != 0 {
		t.Fatalf("refusal effects: runner=%d emitter=%d", runner.calls.Load(), emitter.calls.Load())
	}
}

type heldChatApprovalRunner struct{ calls atomic.Int64 }

func (r *heldChatApprovalRunner) Run(context.Context, *subagent.Run) (*subagent.Result, error) {
	r.calls.Add(1)
	return &subagent.Result{}, nil
}

type heldChatApprovalEmitter struct{ calls atomic.Int64 }

func (e *heldChatApprovalEmitter) Emit(context.Context, string, string, []byte) (string, error) {
	e.calls.Add(1)
	return "unexpected-approval", nil
}

func heldChatApprovalState(t *testing.T, st *store.Store) map[string][][]any {
	t.Helper()
	tables, err := st.DB.QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='table' AND (name='subagent_runs' OR name='sessions' OR name='messages' OR name='envelope_instances' OR name='session_actor_bindings' OR name LIKE 'agent_%' OR name LIKE 'actor_%') ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var name string
		if err := tables.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := tables.Err(); err != nil {
		t.Fatal(err)
	}
	if err := tables.Close(); err != nil {
		t.Fatal(err)
	}
	state := make(map[string][][]any, len(names))
	for _, name := range names {
		rows, err := st.DB.QueryContext(t.Context(), fmt.Sprintf(`SELECT * FROM %q ORDER BY rowid`, name))
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		state[name] = make([][]any, 0)
		for rows.Next() {
			values := make([]any, len(columns))
			targets := make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatal(err)
			}
			for i, value := range values {
				if raw, ok := value.([]byte); ok {
					values[i] = string(raw)
				}
			}
			state[name] = append(state[name], values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return state
}
func assertHeldChatApprovalState(t *testing.T, st *store.Store, before map[string][][]any) {
	t.Helper()
	after := heldChatApprovalState(t, st)
	for table, rows := range before {
		if !reflect.DeepEqual(rows, after[table]) {
			t.Fatalf("refusal changed %s rows", table)
		}
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("refusal changed stored state")
	}
}
