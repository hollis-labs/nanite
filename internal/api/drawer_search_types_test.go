package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

var drawerCardViewKeys = []string{"id", "session_id", "card_type", "content_ref", "title", "payload", "position", "created_at"}

var searchResultViewKeys = []string{"session_id", "message_id", "role", "snippet", "created_at", "session_title", "session_short_code"}

func TestDrawerCardViewJSON(t *testing.T) {
	var c store.BottomDrawerPinnedCard
	populate(t, &c)
	assertKeys(t, "DrawerCardView", mustJSON(t, drawerCardToView(&c)), drawerCardViewKeys)
	assertSameJSON(t, "populated", drawerCardToView(&c), c)
	assertSameJSON(t, "zero", drawerCardToView(&store.BottomDrawerPinnedCard{}), store.BottomDrawerPinnedCard{})
	assertSameJSON(t, "empty list", drawerCardsToView([]store.BottomDrawerPinnedCard{}), []store.BottomDrawerPinnedCard{})
	assertSameJSON(t, "nil list", drawerCardsToView(nil), []store.BottomDrawerPinnedCard(nil))
	assertSameJSON(t, "list", drawerCardsToView([]store.BottomDrawerPinnedCard{c, {}}), []store.BottomDrawerPinnedCard{c, {}})
}

func TestSearchResultViewJSON(t *testing.T) {
	var r store.SearchResult
	populate(t, &r)
	assertKeys(t, "SearchResultView", mustJSON(t, searchResultToView(&r)), searchResultViewKeys)
	assertSameJSON(t, "populated", searchResultToView(&r), r)
	assertSameJSON(t, "zero", searchResultToView(&store.SearchResult{}), store.SearchResult{})
	assertSameJSON(t, "empty list", searchResultsToView([]store.SearchResult{}), []store.SearchResult{})
	assertSameJSON(t, "nil list", searchResultsToView(nil), []store.SearchResult(nil))
}

func newB2aSession(t *testing.T, a *API) *store.Session {
	t.Helper()
	sess := &store.Session{Provider: "anthropic", Model: "m", Status: "active", Title: "B2a"}
	if err := a.Services.Store.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sess
}

func TestDrawerCards_HTTP(t *testing.T) {
	a, mux := newTestAPI(t)
	sess := newB2aSession(t, a)
	base := "/api/sessions/" + sess.ID + "/drawer-cards"

	if w := mcpDo(mux, "GET", base, ""); w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("empty list: %d %s", w.Code, w.Body.String())
	}
	if w := mcpDo(mux, "POST", base, `{"title":"x"}`); w.Code != http.StatusBadRequest || errorBody(t, w) != "card_type is required" {
		t.Fatalf("missing card_type: %d %s", w.Code, w.Body.String())
	}
	var first DrawerCardView
	for i := 0; i < store.BottomDrawerPinCap; i++ {
		w := mcpDo(mux, "POST", base, fmt.Sprintf(`{"card_type":"envelope","content_ref":"ref-%d"}`, i))
		if w.Code != http.StatusCreated {
			t.Fatalf("pin %d: %d %s", i, w.Code, w.Body.String())
		}
		if i == 0 {
			if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil || first.ID == "" {
				t.Fatalf("pin body %s: %v", w.Body.String(), err)
			}
		}
	}
	if w := mcpDo(mux, "POST", base, `{"card_type":"envelope"}`); w.Code != http.StatusConflict || errorBody(t, w) != "pin cap exceeded; unpin one first" {
		t.Fatalf("over cap: %d %s", w.Code, w.Body.String())
	}
	w := mcpDo(mux, "DELETE", "/api/drawer-cards/"+first.ID, "")
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"deleted":true,"id":"`+first.ID+`"}` {
		t.Fatalf("unpin: %d %s", w.Code, w.Body.String())
	}
	var cards []DrawerCardView
	if err := json.Unmarshal(mcpDo(mux, "GET", base, "").Body.Bytes(), &cards); err != nil || len(cards) != store.BottomDrawerPinCap-1 {
		t.Fatalf("list after unpin: %d cards, err %v", len(cards), err)
	}
}

func TestSearch_HTTP(t *testing.T) {
	a, mux := newTestAPI(t)
	sess := newB2aSession(t, a)
	if err := a.Services.Store.CreateMessage(context.Background(), &store.Message{SessionID: sess.ID, Role: "user", Content: "find the zzb2aneedle here"}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if w := mcpDo(mux, "GET", "/api/search", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("missing q: %d %s", w.Code, w.Body.String())
	}
	w := mcpDo(mux, "GET", "/api/search?q=zzb2aneedle", "")
	var hits []SearchResultView
	if err := json.Unmarshal(w.Body.Bytes(), &hits); err != nil || len(hits) != 1 || hits[0].SessionID != sess.ID || hits[0].SessionTitle != "B2a" {
		t.Fatalf("search: %d %s (%v)", w.Code, w.Body.String(), err)
	}
	if w := mcpDo(mux, "GET", "/api/search?q=zznothingmatches", ""); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("no hits: %s", w.Body.String())
	}
}

// With no live stream for a message, the stream endpoint falls back to the
// stored message to check it belongs to the requested session.
func TestSessionEvents_StoredMessageOwnership(t *testing.T) {
	a, mux := newTestAPI(t)
	owner := newB2aSession(t, a)
	other := newB2aSession(t, a)
	msg := &store.Message{SessionID: owner.ID, Role: "assistant", Content: "done"}
	if err := a.Services.Store.CreateMessage(context.Background(), msg); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if w := mcpDo(mux, "GET", "/api/harness/v1/sessions/"+other.ID+"/events?message_id="+msg.ID, ""); w.Code != http.StatusNotFound || errorBody(t, w) != "message not found for session" {
		t.Fatalf("other session: %d %s", w.Code, w.Body.String())
	}
	if w := mcpDo(mux, "GET", "/api/harness/v1/sessions/"+owner.ID+"/events?message_id=nope", ""); w.Code != http.StatusNotFound || errorBody(t, w) != "message not found" {
		t.Fatalf("unknown message: %d %s", w.Code, w.Body.String())
	}
}

// A command that produces a message has it persisted in the session.
func TestExecuteCommand_PersistsMessage(t *testing.T) {
	a, mux := newTestAPI(t)
	sess := newB2aSession(t, a)
	w := mcpDo(mux, "POST", "/api/commands/execute", `{"name":"status","session_id":"`+sess.ID+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("execute: %d %s", w.Code, w.Body.String())
	}
	var res struct {
		Action    string `json:"action"`
		MessageID string `json:"message_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || res.Action != "message" || res.MessageID == "" {
		t.Fatalf("result %s: %v", w.Body.String(), err)
	}
	msg, err := a.Services.Store.GetMessage(context.Background(), res.MessageID)
	if err != nil || msg.SessionID != sess.ID || msg.Role != "system" {
		t.Fatalf("persisted message = %+v, %v", msg, err)
	}
}

func TestCreateDurableAgent_UnknownProfile(t *testing.T) {
	_, mux := newTestAPI(t)
	w := mcpDo(mux, "POST", "/api/durable-agents", `{"name":"d","slug":"b2a-durable","profile_id":"no-such-profile"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(errorBody(t, w), "agent profile no-such-profile not found in agent_profiles") {
		t.Fatalf("unknown profile: %d %s", w.Code, w.Body.String())
	}
}

func TestSessionModel_NoSessionService(t *testing.T) {
	for name, a := range map[string]*API{
		"no services":        {Services: nil},
		"no session service": {Services: &service.Container{}},
	} {
		if got := a.sessionModel(context.Background(), "x"); got != "" {
			t.Fatalf("sessionModel with %s = %q", name, got)
		}
	}
}
