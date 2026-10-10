package subagent

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

// Missing issuer cannot be replaced by a role slug, host UUID, retained trust
// tier, prior actor or an interactive approval. The real mailbox stays untouched.
func TestSpawnCannotEnrollOrDeliverWithoutIssuer(t *testing.T) {
	db, st := newTestDB(t)
	ctx := t.Context()
	prior := &store.AgentProfile{Name: "Private prior parent", Slug: "hint-selector"}
	if err := storetest.PriorAuthorizedActor(ctx, st, prior); err != nil {
		t.Fatal(err)
	}
	historical := &store.AgentProfile{ID: "historical-parent", Slug: "old-parent", Name: "Retained parent"}
	if err := storetest.HistoricalProfile(ctx, st, historical); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx, `UPDATE agent_profiles SET default_trust_tier='trusted' WHERE id=?`, historical.ID); err != nil {
		t.Fatal(err)
	}
	host, err := st.GetAgentBySlug(ctx, prior.Slug)
	if err != nil {
		t.Fatal(err)
	}
	poster := mailboxadapter.New(st).Service
	emitter := &stubEmitter{}
	svc := NewService(db, &notCalledRunner{t: t}, poster, emitter, st)
	svc.SetSpawnAuthorizer(Authorizer{st})
	svc.SetProfileResolver(ProfileAdapter{st})
	queries := []string{"SELECT * FROM agent_profiles ORDER BY id", "SELECT * FROM agent_actor_bindings ORDER BY actor_uri", "SELECT * FROM subagent_runs ORDER BY id", "SELECT * FROM messages ORDER BY id"}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = subagentSnapshot(t, st, q)
	}
	for _, id := range []string{"", prior.ID, prior.Slug, host.ID, historical.ID} {
		for _, mode := range []string{core.ModeInteractive, core.ModeAsync, core.ModeSync} {
			runID, err := svc.Spawn(ctx, core.SpawnRequest{ParentSessionID: "private-parent", ParentAgentID: id, AgentProfileID: id, Role: prior.Slug, Prompt: "claimed child", Mode: mode})
			if runID != "" || !errors.Is(err, store.ErrVerifiedActorRequired) {
				t.Fatalf("id=%q mode=%q run=%q err=%v", id, mode, runID, err)
			}
			for i, q := range queries {
				if after := subagentSnapshot(t, st, q); !reflect.DeepEqual(before[i], after) {
					t.Fatalf("%s changed", q)
				}
			}
		}
	}
	if emitter.Count() != 0 {
		t.Fatalf("approval emissions=%d", emitter.Count())
	}
}

func subagentSnapshot(t *testing.T, st *store.Store, query string) [][]any {
	t.Helper()
	rows, err := st.DB.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		out = append(out, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
