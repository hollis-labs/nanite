package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func envelopeRespondBody(t *testing.T, kind, id string, extra map[string]any) []byte {
	t.Helper()
	m := map[string]any{"v": 1, "kind": kind, "id": id, "status": "submitted"}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return b
}

// Each refusal keeps its status and body, in the order they are checked.
func TestEnvelopeRespond_Precedence(t *testing.T) {
	a, mux := newTestAPI(t)
	sessID := seedSessionForEnvelope(t, a)
	inst := seedEnvelopeInstance(t, a, sessID, "precedence-kind")
	path := "/api/envelopes/" + inst.ID + "/respond"

	for _, c := range []struct {
		name string
		path string
		body []byte
		code int
		msg  string
	}{
		{"id mismatch", path, envelopeRespondBody(t, "precedence-kind", "other", nil), 400, "response id does not match path id"},
		{"not found", "/api/envelopes/nope/respond", envelopeRespondBody(t, "precedence-kind", "nope", nil), 404, "envelope not found"},
		{"kind mismatch beats session mismatch", path, envelopeRespondBody(t, "other-kind", inst.ID, map[string]any{"session_id": "x"}), 400, "response kind does not match envelope type"},
		{"session mismatch", path, envelopeRespondBody(t, "precedence-kind", inst.ID, map[string]any{"session_id": "x"}), 403, "session mismatch"},
	} {
		w := doPost(mux, c.path, c.body)
		if w.Code != c.code || errorBody(t, w) != c.msg {
			t.Fatalf("%s: %d %s, want %d %q", c.name, w.Code, w.Body.String(), c.code, c.msg)
		}
	}

	// Answered once; the second answer gets the first one back.
	if w := doPost(mux, path, envelopeRespondBody(t, "precedence-kind", inst.ID, map[string]any{"session_id": sessID})); w.Code != http.StatusOK {
		t.Fatalf("respond: %d %s", w.Code, w.Body.String())
	}
	w := doPost(mux, path, envelopeRespondBody(t, "precedence-kind", inst.ID, nil))
	var conflict struct {
		Error          string         `json:"error"`
		ResponseStatus string         `json:"response_status"`
		Response       map[string]any `json:"response"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &conflict); err != nil || w.Code != http.StatusConflict ||
		conflict.Error != "envelope already responded" || conflict.ResponseStatus != "submitted" || conflict.Response["kind"] != "precedence-kind" {
		t.Fatalf("second respond: %d %s", w.Code, w.Body.String())
	}
}

func TestEnvelopeRespond_FollowUpAndMessageID(t *testing.T) {
	a, mux := newTestAPI(t)
	sessID := seedSessionForEnvelope(t, a)
	const kind = "follow-up-kind"
	chat.RegisterResponseHandler(kind, chat.HandlerFunc(func(context.Context, store.EnvelopeInstance, chat.ResponseV1) (chat.HandlerResult, error) {
		return chat.HandlerResult{FollowUp: "next step"}, nil
	}))
	t.Cleanup(func() { chat.UnregisterResponseHandler(kind) })
	inst := seedEnvelopeInstance(t, a, sessID, kind)
	w := doPost(mux, "/api/envelopes/"+inst.ID+"/respond", envelopeRespondBody(t, kind, inst.ID, nil))
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK || out["follow_up"] != "next step" || out["message_id"] == "" || out["ok"] != true {
		t.Fatalf("respond: %d %s", w.Code, w.Body.String())
	}
	msg, err := a.Services.Store.GetMessage(context.Background(), out["message_id"].(string))
	if err != nil || msg.Role != chat.RoleEnvelopeResponse || msg.SessionID != sessID {
		t.Fatalf("transcript message = %+v, %v", msg, err)
	}
}

// raceEnvelopes loses the claim to a concurrent response.
type raceEnvelopes struct{ reads int }

func (f *raceEnvelopes) GetEnvelopeInstance(context.Context, string) (*store.EnvelopeInstance, error) {
	f.reads++
	inst := &store.EnvelopeInstance{ID: "e", SessionID: "s", EnvelopeType: "race-kind"}
	if f.reads > 1 {
		now := time.Now()
		inst.RespondedAt = &now
		inst.ResponseStatus = "canceled"
		inst.ResponseJSON = `{"winner":true}`
	}
	return inst, nil
}
func (f *raceEnvelopes) ClaimEnvelopeForResponse(context.Context, string) error {
	return store.ErrEnvelopeAlreadyResponded
}
func (f *raceEnvelopes) UpdateEnvelopeResponse(context.Context, string, string, string) error {
	return nil
}
func (f *raceEnvelopes) CreateMessage(context.Context, *store.Message) error { return nil }

func TestEnvelopeRespond_LosingTheClaimRaceReturnsTheWinner(t *testing.T) {
	a := &API{Services: &service.Container{Envelopes: service.NewEnvelopeService(&raceEnvelopes{})}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/envelopes/{id}/respond", a.handleEnvelopeRespond)
	w := doPost(mux, "/api/envelopes/e/respond", envelopeRespondBody(t, "race-kind", "e", nil))
	if w.Code != http.StatusConflict || strings.TrimSpace(w.Body.String()) != `{"error":"envelope already responded","response":{"winner":true},"response_status":"canceled"}` {
		t.Fatalf("race: %d %s", w.Code, w.Body.String())
	}
}

// TestEnvelopeRespond_HandlerTimeoutLeavesEnvelopeStuck_CurrentBehaviour_PendingCW20260930_0241
// pins that a handler timeout leaves the envelope claimed with no response:
// the "failed" status is written under the handler's already-expired
// context, so it never lands, and every later answer gets a 409 with an
// empty body. CW-20260930-0241 fixes it; whoever lands that flips this test.
func TestEnvelopeRespond_HandlerTimeoutLeavesEnvelopeStuck_CurrentBehaviour_PendingCW20260930_0241(t *testing.T) {
	a, mux := newTestAPI(t)
	a.Services.Envelopes.SetHandlerTimeout(50 * time.Millisecond)
	sessID := seedSessionForEnvelope(t, a)
	const kind = "slow-kind"
	chat.RegisterResponseHandler(kind, chat.HandlerFunc(func(ctx context.Context, _ store.EnvelopeInstance, _ chat.ResponseV1) (chat.HandlerResult, error) {
		<-ctx.Done()
		return chat.HandlerResult{}, ctx.Err()
	}))
	t.Cleanup(func() { chat.UnregisterResponseHandler(kind) })
	inst := seedEnvelopeInstance(t, a, sessID, kind)
	path := "/api/envelopes/" + inst.ID + "/respond"

	start := time.Now()
	if w := doPost(mux, path, envelopeRespondBody(t, kind, inst.ID, nil)); w.Code != http.StatusGatewayTimeout || errorBody(t, w) != "response handler timed out" {
		t.Fatalf("first: %d %s", w.Code, w.Body.String())
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timed out after %s; the configured 50ms handler timeout was not applied", elapsed)
	}
	after, err := a.Services.Store.GetEnvelopeInstance(context.Background(), inst.ID)
	if err != nil || after.ResponseStatus != "handling" || after.ResponseJSON != "" {
		t.Fatalf("stored after timeout: status=%q json=%q err=%v; if CW-20260930-0241 landed, flip this test", after.ResponseStatus, after.ResponseJSON, err)
	}
	if w := doPost(mux, path, envelopeRespondBody(t, kind, inst.ID, nil)); w.Code != http.StatusConflict || w.Body.Len() != 0 {
		t.Fatalf("retry: %d %q; if CW-20260930-0241 landed, flip this test", w.Code, w.Body.String())
	}
}

// answeredEnvelopes serves an envelope that already has a response and counts
// claim attempts.
type answeredEnvelopes struct{ claims int }

func (f *answeredEnvelopes) GetEnvelopeInstance(context.Context, string) (*store.EnvelopeInstance, error) {
	now := time.Now()
	return &store.EnvelopeInstance{ID: "e", SessionID: "s", EnvelopeType: "done-kind", RespondedAt: &now, ResponseStatus: "submitted", ResponseJSON: `{}`}, nil
}
func (f *answeredEnvelopes) ClaimEnvelopeForResponse(context.Context, string) error {
	f.claims++
	return store.ErrEnvelopeAlreadyResponded
}
func (f *answeredEnvelopes) UpdateEnvelopeResponse(context.Context, string, string, string) error {
	return nil
}
func (f *answeredEnvelopes) CreateMessage(context.Context, *store.Message) error { return nil }

// An envelope already answered is refused from the read, without a claim.
func TestEnvelopeRespond_AnsweredEnvelopeNotClaimed(t *testing.T) {
	fake := &answeredEnvelopes{}
	a := &API{Services: &service.Container{Envelopes: service.NewEnvelopeService(fake)}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/envelopes/{id}/respond", a.handleEnvelopeRespond)
	if w := doPost(mux, "/api/envelopes/e/respond", envelopeRespondBody(t, "done-kind", "e", nil)); w.Code != http.StatusConflict {
		t.Fatalf("respond: %d %s", w.Code, w.Body.String())
	}
	if fake.claims != 0 {
		t.Fatalf("claimed %d times, want 0", fake.claims)
	}
}
