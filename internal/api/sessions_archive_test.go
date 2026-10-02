package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// activitySink records the events an ActivityEmitter posts to it.
type activitySink struct {
	mu     sync.Mutex
	events []map[string]any
}

func (s *activitySink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var ev map[string]any
	_ = json.NewDecoder(r.Body).Decode(&ev)
	s.mu.Lock()
	s.events = append(s.events, ev)
	s.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}

func (s *activitySink) count(eventType, entityID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, ev := range s.events {
		if ev["event_type"] == eventType && ev["entity_id"] == entityID {
			n++
		}
	}
	return n
}

// newArchiveTestAPI serves DELETE /api/sessions/{id} from a hand-built
// container holding just what the handler touches: a real store, a
// SessionService whose events go to an activity emitter posting to sink,
// and a stream manager. It deliberately avoids service.NewContainer, whose
// background model-catalog refresher can outlive the test and race its
// temp-dir cleanup.
func newArchiveTestAPI(t *testing.T) (*testAPI, *http.ServeMux, *activitySink) {
	t.Helper()
	sink := &activitySink{}
	srv := httptest.NewServer(sink)
	t.Cleanup(srv.Close)
	activity := chat.NewActivityEmitter(srv.URL)

	st, err := storetest.New(t, context.Background(), filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	sessions := service.NewSessionService(service.SessionServiceDeps{
		Sessions:    st,
		Writer:      st,
		Agents:      st,
		AgentReader: st,
		Settings:    st,
		Events:      service.NewCompositeEmitter(activity, nil, nil),
	})
	a := newAPIStoreFixture(&service.Container{
		Sessions: sessions,
		Streams:  service.NewStreamManager(),
		Activity: activity,
	}, st)
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/sessions/{id}", a.handleDeleteSession)
	return a, mux, sink
}

func createArchiveTestSession(t *testing.T, a *testAPI) *store.Session {
	t.Helper()
	sess := &store.Session{Provider: "anthropic", Model: "m", Status: "active"}
	if err := a.store.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sess
}

// TestDeleteSessionRunsArchiveHook pins that DELETE archives through
// SessionService, so the onArchive hook (chat's CloseAgentSession in
// production) releases the session's runtime immediately.
func TestDeleteSessionRunsArchiveHook(t *testing.T) {
	a, mux, _ := newArchiveTestAPI(t)
	sess := createArchiveTestSession(t, a)

	hooker, ok := a.Services.Sessions.(interface {
		SetArchiveHook(func(ctx context.Context, sessionID string))
	})
	if !ok {
		t.Fatalf("Sessions (%T) has no SetArchiveHook", a.Services.Sessions)
	}
	var mu sync.Mutex
	var closed []string
	hooker.SetArchiveHook(func(_ context.Context, id string) {
		mu.Lock()
		closed = append(closed, id)
		mu.Unlock()
	})

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/sessions/"+sess.ID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE = %d body=%s", w.Code, w.Body.String())
	}
	if want := `{"archived":"` + sess.ID + `"}`; w.Body.String() != want+"\n" && w.Body.String() != want {
		t.Fatalf("DELETE body = %q, want %s", w.Body.String(), want)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(closed) != 1 || closed[0] != sess.ID {
		t.Fatalf("archive hook calls = %v, want exactly [%s]", closed, sess.ID)
	}
	got, err := a.store.GetSession(context.Background(), sess.ID)
	if err != nil || got.Status != "archived" {
		t.Fatalf("session after DELETE = %+v, %v; want archived", got, err)
	}
}

// TestDeleteSessionEmitsSessionEndedOnce pins that the activity
// session-ended event is sent once: by SessionService.Archive, with the
// handler's former copy removed.
func TestDeleteSessionEmitsSessionEndedOnce(t *testing.T) {
	a, mux, sink := newArchiveTestAPI(t)
	sess := createArchiveTestSession(t, a)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/sessions/"+sess.ID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE = %d body=%s", w.Code, w.Body.String())
	}

	// The emit is asynchronous: wait for the first event, then give a
	// duplicate time to arrive before counting.
	deadline := time.Now().Add(5 * time.Second)
	for sink.count(chat.EventSessionEnded, sess.ID) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if n := sink.count(chat.EventSessionEnded, sess.ID); n != 1 {
		t.Fatalf("%s events for %s = %d, want exactly 1", chat.EventSessionEnded, sess.ID, n)
	}
}

// TestDeleteSessionResponsesUnchanged pins DELETE's other outcomes. An
// unknown id still archives nothing and answers 200 (there is no 404 path).
// A failed archive write still answers 500 with the store's own message,
// unwrapped by the service.
func TestDeleteSessionResponsesUnchanged(t *testing.T) {
	_, mux, _ := newArchiveTestAPI(t)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/sessions/does-not-exist", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE unknown session = %d body=%s; want 200", w.Code, w.Body.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/sessions/any", nil).WithContext(ctx))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("DELETE with a failing write = %d body=%s; want 500", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 500 body: %v (%s)", err, w.Body.String())
	}
	const storePrefix = "archive session any: begin tx:"
	if msg := body["error"]; len(msg) < len(storePrefix) || msg[:len(storePrefix)] != storePrefix {
		t.Fatalf("500 error = %q, want the store's message starting %q", msg, storePrefix)
	}
}
