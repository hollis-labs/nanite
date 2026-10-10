package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// Characterization of POST /api/sessions/{id}/compact (the manual /compact),
// written against the handler before its orchestration moves into a
// service, and required to pass unchanged after the move.

// compactRecorder records the compaction events while passing everything
// else through.
type compactRecorder struct {
	service.EventEmitter
	pre  []string
	post []string
}

func (r *compactRecorder) EmitPreCompact(_ context.Context, sessionID string, messageCount int, reason string) {
	r.pre = append(r.pre, fmt.Sprintf("%s|%d|%s", sessionID, messageCount, reason))
}

func (r *compactRecorder) EmitPostCompact(_ context.Context, sessionID string, tokensSaved int, stages []string) {
	r.post = append(r.post, fmt.Sprintf("%s|%d|%s", sessionID, tokensSaved, strings.Join(stages, ",")))
}

type compactResponse struct {
	Summary       string   `json:"summary"`
	StagesApplied []string `json:"stages_applied"`
	TokensSaved   int      `json:"tokens_saved"`
	Mode          string   `json:"mode"`
}

func seedCompactSession(t *testing.T, a *testAPI, id string, messages int) {
	t.Helper()
	ctx := context.Background()
	if err := a.store.CreateSession(ctx, &store.Session{ID: id, Title: "Compact characterization"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	bindFixtureSessionActor(t, a.store, id)
	for i := 0; i < messages; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if err := a.store.CreateMessage(ctx, &store.Message{
			ID: fmt.Sprintf("%s-msg-%d", id, i), SessionID: id, Role: role, Content: strings.Repeat("filler ", 40),
		}); err != nil {
			t.Fatalf("CreateMessage: %v", err)
		}
	}
}

func TestCompactCharacterization_UnknownSession404(t *testing.T) {
	_, mux := newTestAPI(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sessions/nope/compact", nil))
	if w.Code != http.StatusNotFound || errorBody(t, w) != "session not found" {
		t.Fatalf("unknown session: %d %s", w.Code, w.Body.String())
	}
}

func TestCompactCharacterization_ResponseEventsBroadcastAndRecord(t *testing.T) {
	a, mux := newTestAPI(t)
	const sid = "compact-char"
	seedCompactSession(t, a, sid, 12)

	rec := &compactRecorder{EventEmitter: a.Services.Events}
	a.Services.Events = rec
	a.Services.Streams.CreateStream("compact-char-live", sid)
	sub, _, ok := a.Services.Streams.Subscribe("compact-char-live", 0)
	if !ok {
		t.Fatal("Subscribe failed")
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sessions/"+sid+"/compact", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("compact: %d %s", w.Code, w.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	if want := []string{"mode", "stages_applied", "summary", "tokens_saved"}; !reflect.DeepEqual(sortedStrings(keys), want) {
		t.Fatalf("response keys = %v, want %v", sortedStrings(keys), want)
	}
	var resp compactResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// No provider is configured in tests, so there is no summarizer: the
	// summary is the placeholder naming the stages that ran.
	if resp.Mode != "general" || !reflect.DeepEqual(resp.StagesApplied, []string{"drop_enrichment"}) {
		t.Fatalf("resp = %+v", resp)
	}
	if want := fmt.Sprintf("Compaction applied %d stage(s); no LLM summary produced (summarizer unavailable).", len(resp.StagesApplied)); resp.Summary != want {
		t.Fatalf("summary = %q, want %q", resp.Summary, want)
	}
	if resp.TokensSaved <= 0 {
		t.Fatalf("tokens_saved = %d, want > 0", resp.TokensSaved)
	}

	// Events: pre with the assembled message count and reason "manual";
	// post with the same tokens saved and stages as the response.
	if want := []string{sid + "|12|manual"}; !reflect.DeepEqual(rec.pre, want) {
		t.Fatalf("pre = %v, want %v", rec.pre, want)
	}
	if want := []string{fmt.Sprintf("%s|%d|drop_enrichment", sid, resp.TokensSaved)}; !reflect.DeepEqual(rec.post, want) {
		t.Fatalf("post = %v, want %v", rec.post, want)
	}

	// The session's live streams get a slot_changed for the conversation.
	var ev chat.StreamEvent
	select {
	case ev = <-sub:
	case <-time.After(2 * time.Second):
		t.Fatal("no slot_changed broadcast")
	}
	if ev.Type != "slot_changed" {
		t.Fatalf("stream event type = %q", ev.Type)
	}
	var slot chat.SlotChangedV1
	if err := json.Unmarshal([]byte(ev.Envelope), &slot); err != nil {
		t.Fatalf("decode slot_changed %q: %v", ev.Envelope, err)
	}
	if slot.V != 1 || slot.Slot != "conversation" || slot.Change != chat.SlotChangeSummarized ||
		slot.Reasoning != "/compact requested; 1 stage(s) applied." || slot.HelpLink != "/help/hot-swap" ||
		slot.TokensBefore-slot.TokensAfter != resp.TokensSaved {
		t.Fatalf("slot_changed = %+v (tokens_saved %d)", slot, resp.TokensSaved)
	}

	// The new continuity event follows the established conversation event;
	// its key identifies the actual stash written by the wired Container.
	select {
	case ev = <-sub:
	case <-time.After(2 * time.Second):
		t.Fatal("no handoff_loaded broadcast")
	}
	var handoffEvent struct {
		CacheKey string `json:"cache_key"`
	}
	if ev.Type != "handoff_loaded" {
		t.Fatalf("continuity event type = %q", ev.Type)
	}
	if decodeErr := json.Unmarshal([]byte(ev.Data), &handoffEvent); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	payload, stashID, handoffErr := service.ReadLatestGlass4Handoff(a.store, sid)
	if handoffErr != nil || payload == nil || stashID != handoffEvent.CacheKey {
		t.Fatalf("continuity event/stash mismatch: %+v, %q, %v", payload, stashID, handoffErr)
	}

	// The compaction is recorded for the session.
	ce, err := a.store.GetLatestCompactionEvent(context.Background(), sid)
	if err != nil || ce == nil || ce.SummaryMode != "general" || !reflect.DeepEqual(ce.StagesApplied, []string{"drop_enrichment"}) {
		t.Fatalf("compaction event = %+v, %v", ce, err)
	}
}

// Without an event emitter or a stream manager the compaction still runs
// and answers the same way.
func TestCompactCharacterization_NoEventsOrStreams(t *testing.T) {
	a, mux := newTestAPI(t)
	const sid = "compact-char-bare"
	seedCompactSession(t, a, sid, 12)
	events, streams := a.Services.Events, a.Services.Streams
	a.Services.Events, a.Services.Streams = nil, nil
	t.Cleanup(func() { a.Services.Events, a.Services.Streams = events, streams })

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sessions/"+sid+"/compact", nil))
	var resp compactResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || w.Code != http.StatusOK || resp.Mode != "general" || len(resp.StagesApplied) == 0 {
		t.Fatalf("compact: %d %s", w.Code, w.Body.String())
	}
}

// A session with nothing to compact applies no stages, has no placeholder
// summary, and saves nothing.
func TestCompactCharacterization_NothingToCompact(t *testing.T) {
	a, mux := newTestAPI(t)
	const sid = "compact-char-empty"
	seedCompactSession(t, a, sid, 0)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sessions/"+sid+"/compact", nil))
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"mode":"general","stages_applied":null,"summary":"","tokens_saved":0}` {
		t.Fatalf("compact: %d %s", w.Code, w.Body.String())
	}
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
