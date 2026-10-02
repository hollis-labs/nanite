package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

var pinViewKeys = []string{"id", "session_id", "scope", "project_id", "content", "agent_id", "created_at", "updated_at"}

func TestPinViewJSON(t *testing.T) {
	var p store.PinnedContent
	populate(t, &p)
	assertKeys(t, "PinView", mustJSON(t, pinToView(&p)), pinViewKeys)
	assertSameJSON(t, "populated", pinToView(&p), p)
	assertSameJSON(t, "zero (omitempty)", pinToView(&store.PinnedContent{}), store.PinnedContent{})
	assertSameJSON(t, "empty list", pinsToView([]store.PinnedContent{}), []store.PinnedContent{})
	assertSameJSON(t, "nil list", pinsToView(nil), []store.PinnedContent(nil))
}

func TestPins_HTTP(t *testing.T) {
	a, mux := newTestAPI(t)
	sess := newB2aSession(t, a)
	ctx := context.Background()
	sid := sess.ID
	if err := a.store.CreatePinnedContent(ctx, store.PinnedContent{ID: "pin-1", SessionID: &sid, Scope: store.PinScopeSession, Content: "remember this", AgentID: "ag"}); err != nil {
		t.Fatalf("CreatePinnedContent: %v", err)
	}
	var pins []PinView
	w := mcpDo(mux, "GET", "/api/sessions/"+sid+"/pins", "")
	if err := json.Unmarshal(w.Body.Bytes(), &pins); err != nil || len(pins) != 1 || pins[0].Content != "remember this" {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	if rec := mcpDo(mux, "GET", "/api/sessions/empty-session/pins", ""); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("empty list: %s", rec.Body.String())
	}
	if rec := mcpDo(mux, "PATCH", "/api/pins/pin-1/scope", `{"scope":"turn"}`); rec.Code != http.StatusBadRequest ||
		errorBody(t, rec) != `update pin scope: invalid scope "turn" (turn pins are ephemeral)` {
		t.Fatalf("bad scope: %d %s", rec.Code, rec.Body.String())
	}
	w = mcpDo(mux, "PATCH", "/api/pins/pin-1/scope", `{"scope":"project","project_id":"p1"}`)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"id":"pin-1","project_id":"p1","scope":"project","updated":true}` {
		t.Fatalf("scope: %d %s", w.Code, w.Body.String())
	}
	if rec := mcpDo(mux, "DELETE", "/api/pins/pin-1", ""); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"deleted":true,"id":"pin-1"}` {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	// A session-scoped pin that is listed, then deleted, is gone from the list.
	if err := a.store.CreatePinnedContent(ctx, store.PinnedContent{ID: "pin-2", SessionID: &sid, Scope: store.PinScopeSession, Content: "second", AgentID: "ag"}); err != nil {
		t.Fatalf("CreatePinnedContent: %v", err)
	}
	if rec := mcpDo(mux, "GET", "/api/sessions/"+sid+"/pins", ""); !strings.Contains(rec.Body.String(), `"id":"pin-2"`) {
		t.Fatalf("pin-2 not listed: %s", rec.Body.String())
	}
	if rec := mcpDo(mux, "DELETE", "/api/pins/pin-2", ""); rec.Code != http.StatusOK {
		t.Fatalf("delete pin-2: %d %s", rec.Code, rec.Body.String())
	}
	if rec := mcpDo(mux, "GET", "/api/sessions/"+sid+"/pins", ""); strings.Contains(rec.Body.String(), `"id":"pin-2"`) {
		t.Fatalf("pin-2 listed after delete: %s", rec.Body.String())
	}
}

func TestAgentBuilderDryRun_UpdateUnknownProfile(t *testing.T) {
	_, mux := newTestAPI(t)
	w := mcpDo(mux, "POST", "/api/agent-builder/dry-run", `{"schema_version":1,"mode":"update_profile","profile":{"id":"no-such-agent","name":"x","system_prompt":"x"}}`)
	var resp AgentBuilderDryRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || w.Code != http.StatusOK {
		t.Fatalf("dry-run: %d %s", w.Code, w.Body.String())
	}
	found := false
	for _, e := range resp.Errors {
		if e == `profile "no-such-agent" not found` {
			found = true
		}
	}
	if !found || resp.Valid {
		t.Fatalf("errors = %v valid=%v, want profile not found", resp.Errors, resp.Valid)
	}
}

func TestLoomCuratorWake_NotProvisioned(t *testing.T) {
	_, mux := newTestAPI(t)
	w := mcpDo(mux, "POST", "/api/loom/curator-wake", `{"generator":"g","fragment":{"id":"frag-1"}}`)
	if w.Code != http.StatusServiceUnavailable || !strings.HasPrefix(errorBody(t, w), "loom curator durable-agent instance not provisioned") {
		t.Fatalf("not provisioned: %d %s", w.Code, w.Body.String())
	}
}
