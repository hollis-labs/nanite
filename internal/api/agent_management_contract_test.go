package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/apiauth"
)

// Contract tests for what a non-browser agent-management client (Tachyon)
// relies on (CW-20261024-0027). The contract itself is documented in
// docs/architecture/native-agent-api.md, "Agent management contract".

func TestRetireHostSettingsOverHTTP(t *testing.T) {
	a, mux := newTestAPI(t)
	host := nativeAuthorityHost(t, a)
	path := "/api/agent-host-settings/" + host.ID + "/retire"

	// Credential and input refusals change nothing.
	for name, c := range map[string]struct {
		p    *apiauth.Principal
		body any
		want int
	}{
		"no principal":        {nil, map[string]any{"reason": "r"}, http.StatusForbidden},
		"agent credential":    {&agentPrincipal, map[string]any{"reason": "r"}, http.StatusForbidden},
		"operate-only client": {&apiauth.Principal{CredentialID: "c", Kind: apiauth.KindClient, Scopes: []apiauth.Scope{apiauth.ScopeRead, apiauth.ScopeOperate}}, map[string]any{"reason": "r"}, http.StatusForbidden},
		"no reason":           {&managerClientPrincipal, map[string]any{}, http.StatusBadRequest},
		"long reason":         {&managerClientPrincipal, map[string]any{"reason": strings.Repeat("x", 501)}, http.StatusBadRequest},
	} {
		if w := naReq(mux, c.p, "POST", path, c.body); w.Code != c.want {
			t.Errorf("%s: %d %s, want %d", name, w.Code, w.Body, c.want)
		}
	}
	if w := naReq(mux, &managerClientPrincipal, "POST", "/api/agent-host-settings/no-such/retire", map[string]any{"reason": "r"}); w.Code != http.StatusNotFound {
		t.Errorf("unknown id = %d %s, want 404", w.Code, w.Body)
	}
	var enabled int
	_ = a.store.DB.QueryRowContext(t.Context(), `SELECT enabled FROM agent_host_settings WHERE id=?`, host.ID).Scan(&enabled)
	if enabled != 1 {
		t.Fatal("a refused retire disabled the host")
	}

	// Retire disables, keeps the row, needs no revision and is repeatable.
	for i := 0; i < 2; i++ {
		w := naReq(mux, &managerClientPrincipal, "POST", path, map[string]any{"reason": "decommissioned"})
		var got struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &got)
		if w.Code != http.StatusOK || got.ID != host.ID || got.Enabled {
			t.Fatalf("retire #%d = %d %s", i, w.Code, w.Body)
		}
	}
	if n := authorityRows(t, a, "agent_host_settings"); n != 1 {
		t.Fatalf("retire removed the row: %d host settings", n)
	}
}

// DELETE /api/agent-host-settings/{id} is a real, revision-guarded hard delete,
// unlike retire.
func TestDeleteHostSettingsIsAHardRevisionGuardedDelete(t *testing.T) {
	a, mux := newTestAPI(t)
	host := nativeAuthorityHost(t, a)
	path := "/api/agent-host-settings/" + host.ID
	if w := naReq(mux, &managerClientPrincipal, "DELETE", path, map[string]any{"revision": "stale"}); w.Code != http.StatusConflict {
		t.Fatalf("stale delete = %d %s, want 409", w.Code, w.Body)
	}
	if n := authorityRows(t, a, "agent_host_settings"); n != 1 {
		t.Fatalf("stale delete removed the row")
	}
	if w := naReq(mux, &managerClientPrincipal, "DELETE", path, map[string]any{"revision": host.Revision}); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s, want 204", w.Code, w.Body)
	}
	if n := authorityRows(t, a, "agent_host_settings"); n != 0 {
		t.Fatalf("delete kept the row: %d", n)
	}
}

func TestNativeAuthorityToolsPaginate(t *testing.T) {
	a, mux := newTestAPI(t)
	nativeAuthorityHost(t, a)
	for _, name := range []string{"dev_write", "dev_exec"} {
		if _, err := a.store.UpsertKnownTool(t.Context(), name, "builtin", "available", "write"); err != nil {
			t.Fatal(err)
		}
	}
	list := func(q string) (int, []map[string]any) {
		w := naReq(mux, &managerClientPrincipal, "GET", "/api/native-authority/tools"+q, nil)
		var items []map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &items)
		return w.Code, items
	}
	code, all := list("")
	if code != 200 || len(all) < 3 {
		t.Fatalf("unpaged = %d %d items", code, len(all))
	}
	var walked []string
	for off := 0; off < len(all); off++ {
		code, page := list("?limit=1&offset=" + string(rune('0'+off)))
		if code != 200 || len(page) != 1 {
			t.Fatalf("page %d = %d %v", off, code, page)
		}
		walked = append(walked, page[0]["name"].(string))
	}
	var want []string
	for _, it := range all {
		want = append(want, it["name"].(string))
	}
	if !slices.Equal(walked, want) {
		t.Fatalf("walking pages = %v, want %v", walked, want)
	}
	if _, past := list("?limit=5&offset=99"); len(past) != 0 {
		t.Fatalf("offset past the end = %v", past)
	}
	for _, bad := range []string{"?limit=0", "?limit=501", "?limit=x", "?limit=1&offset=-1"} {
		if code, _ := list(bad); code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", bad, code)
		}
	}
}

// The capability document a client discovers the turn stream from. Pin the
// parts the contract promises: SSE chatstream/v1 turn events, the terminal and
// gap verbs, and the route hints. No session-level stream exists.
func TestAgentV1CapabilitiesContract(t *testing.T) {
	_, mux := newTestAPI(t)
	w := naReq(mux, &managerClientPrincipal, "GET", agentV1RoutePrefix+"/capabilities", nil)
	if w.Code != 200 {
		t.Fatalf("capabilities = %d %s", w.Code, w.Body)
	}
	var caps struct {
		StreamEncodings        []string `json:"stream_encodings"`
		StreamTransports       []string `json:"stream_transports"`
		SessionStreamSupported bool     `json:"session_stream_supported"`
		Operations             []string `json:"operations"`
		SupportedEventTypes    []struct {
			Value string `json:"value"`
		} `json:"supported_event_types"`
		TurnStates []struct {
			Value string `json:"value"`
		} `json:"turn_states"`
		RouteHints struct {
			TurnEvents string `json:"turn_events"`
			Sessions   string `json:"sessions"`
		} `json:"route_hints"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &caps); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(caps.StreamEncodings, []string{"chatstream/v1"}) || !slices.Equal(caps.StreamTransports, []string{"sse"}) || caps.SessionStreamSupported {
		t.Errorf("stream contract changed: %v %v session=%v", caps.StreamEncodings, caps.StreamTransports, caps.SessionStreamSupported)
	}
	if !slices.Contains(caps.Operations, "sessions.turns.events.stream") || strings.Contains(strings.Join(caps.Operations, ","), "sessions.events") {
		t.Errorf("operations = %v", caps.Operations)
	}
	var verbs []string
	for _, e := range caps.SupportedEventTypes {
		verbs = append(verbs, e.Value)
	}
	for _, must := range []string{"run.start", "run.finish", "run.error", "run.abort", "gap"} {
		if !slices.Contains(verbs, must) {
			t.Errorf("supported_event_types lacks %q: %v", must, verbs)
		}
	}
	var states []string
	for _, s := range caps.TurnStates {
		states = append(states, s.Value)
	}
	if len(states) == 0 {
		t.Error("turn_states empty")
	}
	if !strings.Contains(caps.RouteHints.TurnEvents, "/turns/{turnId}/events") {
		t.Errorf("turn_events hint = %q", caps.RouteHints.TurnEvents)
	}
}

// The admin MCP tools are the documented names; a client built against the
// contract fails loudly here if one is renamed or dropped. Retire over MCP and
// HTTP is the same write.
func TestAdminMCPToolNamesAreTheContract(t *testing.T) {
	want := []string{"agent_setup", "agent_list", "agent_get", "agent_enroll", "host_settings_list", "host_settings_create", "host_settings_update", "host_settings_retire",
		"authority_list", "authority_get", "tool_grant", "tool_ungrant", "skill_grant", "skill_ungrant", "authority_revoke",
		"session_start", "session_list", "session_get", "session_history", "session_stop", "turn_send", "turn_get", "turn_cancel"}
	g := newGuarded(t)
	res, err := g.mcp(g.secrets["operator"]).ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("admin MCP tools = %v, want %v", got, want)
	}
}
