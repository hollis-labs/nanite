package subagent

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/store/mailboxadapter"
	"github.com/hollis-labs/nanite/internal/storetest"
	core "github.com/hollis-labs/substrate/agent/subagent"
	messaging "github.com/hollis-labs/substrate/mesh/messaging/mailbox"
)

func newTestDB(t *testing.T) (*sql.DB, *store.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := storetest.New(t, context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()); _ = os.Remove(dbPath) })
	return s.DB, s
}

// stubPoster captures the last SendMessage input without actually
// going through messaging. Lets subagent tests assert reply shape
// without spinning up a full messaging.Service.
type stubPoster struct {
	mu   sync.Mutex
	last *messaging.SendInput
}

func (p *stubPoster) SendMessage(_ context.Context, in messaging.SendInput) (*messaging.Message, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	capturedInput := in
	p.last = &capturedInput
	return &messaging.Message{ID: "stub-" + in.FromAgentID, Body: in.Body}, nil
}

// stubEmitter records emit calls for assertions.
type stubEmitter struct {
	mu    sync.Mutex
	calls []emitCall
}
type emitCall struct {
	sessionID string
	typ       string
	payload   []byte
}

func (e *stubEmitter) Emit(_ context.Context, s, t string, p []byte) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, emitCall{s, t, append([]byte(nil), p...)})
	return "env-" + strconv.Itoa(len(e.calls)), nil
}
func (e *stubEmitter) Count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}
func (e *stubEmitter) Last() emitCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls[len(e.calls)-1]
}

// stubSettings returns fixed UserSettings.
type stubSettings struct{ us store.UserSettings }

func (s stubSettings) GetUserSettings(ctx context.Context) (*store.UserSettings, error) {
	return &s.us, nil
}

// notCalledRunner fails the test if core.Run is invoked.
type notCalledRunner struct{ t *testing.T }

func (r *notCalledRunner) Run(_ context.Context, _ *core.Run) (*core.Result, error) {
	r.t.Fatal("runner should not be invoked during gated Spawn")
	return nil, nil
}

// stubEventLogger captures LogEvent calls.
type stubEventLogger struct {
	mu    sync.Mutex
	calls []eventLogCall
}

type eventLogCall struct {
	sessionID string
	eventType string
	category  string
	detail    string
	metadata  string
}

func (l *stubEventLogger) LogEvent(ctx context.Context, sessionID, eventType, category, detail, metadata string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, eventLogCall{sessionID, eventType, category, detail, metadata})
}

func TestSpawn_ReplyDelivery_ExistingRoleSlug_NoAutoRegisterCollision(t *testing.T) {
	db, st := newTestDB(t)

	// newTestDB runs the full migration set, which already seeds the
	// real internal "worker" profile at id="blt-worker-001" (migration
	// 060) — the exact real-world shape this bug depends on: a role
	// whose agent_profiles.id is never equal to its slug. No manual seed
	// needed/possible here (it would collide with the migration's row).
	if err := st.CreateAgent(context.Background(), &store.AgentProfile{
		ID:     "parent-1",
		Slug:   "parent-1",
		Name:   "Parent",
		Kind:   "internal",
		Status: "active",
	}); err != nil {
		t.Fatalf("seed parent profile: %v", err)
	}

	messagingSvc := mailboxadapter.New(st).Service

	svc := NewService(db, core.EchoRunner{}, messagingSvc, nil, stubSettings{})
	svc.SetProfileResolver(ProfileAdapter{st})

	for i := 0; i < 2; i++ {
		id, err := svc.Spawn(context.Background(), core.SpawnRequest{
			ParentSessionID: "sess-1",
			ParentAgentID:   "parent-1",
			Role:            "worker",
			Prompt:          "do work",
			Mode:            core.ModeSync,
		})
		if err != nil {
			t.Fatalf("Spawn #%d: %v", i, err)
		}
		run, err := svc.Status(context.Background(), id)
		if err != nil {
			t.Fatalf("Status #%d: %v", i, err)
		}
		if run.Status != core.StatusCompleted {
			t.Fatalf("run #%d Status = %q, want %q (Error=%q)", i, run.Status, core.StatusCompleted, run.Error)
		}

		// The run completing does NOT prove the parent learned about it —
		// reply-delivery failures are only slog.Warn'd, never surfaced back
		// onto run.Status (that's the actual failure mode this ticket is
		// about: "parent may never learn a dispatched child completed").
		// So assert directly on delivery: the parent session must have
		// exactly i+1 reply messages by now.
		msgs, err := messagingSvc.RecentForSession(context.Background(), "sess-1", 10)
		if err != nil {
			t.Fatalf("RecentForSession #%d: %v", i, err)
		}
		// CW-20260512-0019: completion replies now carry
		// Kind=subagent_result, not the generic KindReply.
		replies := 0
		for _, m := range msgs {
			if m.Kind == core.ResultMessageKind {
				replies++
			}
		}
		if replies != i+1 {
			t.Fatalf("reply #%d: parent has %d subagent_result message(s) in sess-1, want %d — reply delivery failed (likely the agent_profiles.slug constraint)", i, replies, i+1)
		}
	}

	// No phantom row with id="worker" should ever have been created by a
	// (would-be) failed auto-register attempt.
	if _, err := st.GetAgent(context.Background(), "worker"); err == nil {
		t.Error(`a spurious agent_profiles row with id="worker" was created — auto-register should never have been attempted for an already-known role`)
	}
	profile, err := st.GetAgent(context.Background(), "blt-worker-001")
	if err != nil {
		t.Fatalf("real worker profile missing: %v", err)
	}
	if profile.Slug != "worker" {
		t.Errorf("real worker profile slug = %q, want %q", profile.Slug, "worker")
	}
}
func TestSpawn_ReplyDelivery_ParentSlug_ToAgentIDResolution(t *testing.T) {
	db, st := newTestDB(t)

	// Create an "operator" profile where ID != slug. This mirrors the
	// real-world pattern where agent_profiles.ID is a generated UUID
	// or prefixed ID (e.g. "agt-operator-001") but slug is the bare
	// role name ("operator").
	if err := st.CreateAgent(context.Background(), &store.AgentProfile{
		ID:     "agt-operator-001",
		Slug:   "operator",
		Name:   "Operator",
		Kind:   "internal",
		Status: "active",
	}); err != nil {
		t.Fatalf("seed operator profile: %v", err)
	}

	// newTestDB already seeds the "worker" profile at id="blt-worker-001"
	// (migration 060) for the child role.

	messagingSvc := mailboxadapter.New(st).Service

	svc := NewService(db, core.EchoRunner{}, messagingSvc, nil, stubSettings{})
	svc.SetProfileResolver(ProfileAdapter{st})

	// Spawn with ParentAgentID="operator" (the slug) instead of
	// "agt-operator-001" (the real ID). Before CW-20260815-0027 this
	// caused ValidateAgentID to fail on ToAgentID, silently dropping
	// the reply.
	id, err := svc.Spawn(context.Background(), core.SpawnRequest{
		ParentSessionID: "sess-parent",
		ParentAgentID:   "operator", // slug, not the real ID
		Role:            "worker",
		Prompt:          "do work",
		Mode:            core.ModeSync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	run, err := svc.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if run.Status != core.StatusCompleted {
		t.Fatalf("Status = %q, want %q (Error=%q)", run.Status, core.StatusCompleted, run.Error)
	}

	// Assert that the reply was delivered successfully to the parent
	// session. Before the fix, ValidateAgentID would reject
	// ToAgentID="operator" (no row with ID=operator), causing
	// SendMessage to fail and the parent to never see the result.
	msgs, err := messagingSvc.RecentForSession(context.Background(), "sess-parent", 10)
	if err != nil {
		t.Fatalf("RecentForSession: %v", err)
	}

	replies := 0
	for _, m := range msgs {
		if m.Kind == core.ResultMessageKind {
			// Additional verification: the message should be TO the
			// resolved ID, not the slug.
			if m.ToAgentID != "agt-operator-001" {
				t.Errorf("reply ToAgentID = %q, want %q (should be resolved ID, not slug)",
					m.ToAgentID, "agt-operator-001")
			}
			replies++
		}
	}

	if replies != 1 {
		t.Fatalf("parent session has %d subagent_result message(s), want 1 — reply delivery failed (likely ToAgentID validation failed on slug)", replies)
	}

}
