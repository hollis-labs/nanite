package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/envelope"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/selftools"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// newToolCallTestAPI builds an API plus the store backing it, so the test
// can wire a SelfToolsTransport against the same DB.
func newToolCallTestAPI(t *testing.T) (*API, *store.Store) {
	t.Helper()
	s, err := store.New(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	svc, err := service.NewContainer(service.ContainerConfig{
		Store:     s,
		Providers: provider.NewRegistry(),
	})
	if err != nil {
		t.Fatalf("service.NewContainer: %v", err)
	}
	return New(svc), s
}

func postToolCall(t *testing.T, a *API, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/tools/call", bytes.NewReader(raw))
	// The endpoint is loopback-only; httptest.NewRequest defaults RemoteAddr
	// to a non-loopback test address, so pin it to loopback here.
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	a.handleSelfToolCall(rec, req)
	return rec
}

// TestHandleSelfToolCall_RejectsNonLoopback pins that a non-loopback caller
// is refused — the endpoint runs arbitrary self-tools and is internal-only.
func TestHandleSelfToolCall_RejectsNonLoopback(t *testing.T) {
	a, s := newToolCallTestAPI(t)
	a.SetSelfTools(selftools.NewSelfToolsTransport(s))

	req := httptest.NewRequest(http.MethodPost, "/api/tools/call", bytes.NewReader([]byte(`{"name":"todo_create"}`)))
	req.RemoteAddr = "203.0.113.7:40000" // non-loopback
	rec := httptest.NewRecorder()
	a.handleSelfToolCall(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a non-loopback caller", rec.Code)
	}
}

// TestHandleSelfToolCall_Unavailable pins that the endpoint 503s when no
// self-tools transport has been wired (SetSelfTools never called).
func TestHandleSelfToolCall_Unavailable(t *testing.T) {
	a, _ := newToolCallTestAPI(t)
	rec := postToolCall(t, a, map[string]any{"name": "todo_create"})
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// TestHandleSelfToolCall_MissingName pins the 400 for a nameless request.
func TestHandleSelfToolCall_MissingName(t *testing.T) {
	a, s := newToolCallTestAPI(t)
	a.SetSelfTools(selftools.NewSelfToolsTransport(s))
	rec := postToolCall(t, a, map[string]any{"args": map[string]any{}})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// TestHandleSelfToolCall_DispatchesUnknownTool pins that a well-formed
// request reaches SelfToolsTransport.CallTool: an unknown tool name comes
// back as a 200 with a tool-level error result (IsError), NOT a transport
// failure. This proves the routing without depending on any wired service.
func TestHandleSelfToolCall_DispatchesUnknownTool(t *testing.T) {
	a, s := newToolCallTestAPI(t)
	a.SetSelfTools(selftools.NewSelfToolsTransport(s))

	rec := postToolCall(t, a, map[string]any{"name": "definitely_not_a_real_tool"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (dispatch succeeded, tool-level error)", rec.Code)
	}
	var res mcp.ToolResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, rec.Body.String())
	}
	if !res.IsError {
		t.Errorf("unknown tool should yield IsError=true, got %#v", res)
	}
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "unknown tool") {
		t.Errorf("result should mention 'unknown tool', got %#v", res.Content)
	}
}

// recordingPanelSink captures BroadcastPanelSignal calls so a test can assert
// that a panel tool forwarded through /api/tools/call reached a wired sink.
type recordingPanelSink struct {
	sessionID  string
	signalType string
	payload    string
	calls      int
}

func (r *recordingPanelSink) BroadcastPanelSignal(sessionID, signalType, payload string) int {
	r.sessionID = sessionID
	r.signalType = signalType
	r.payload = payload
	r.calls++
	return 1
}

// TestHandleSelfToolCall_PanelOpenReachesWiredSink is the CW-20260516-0044
// regression: a CLI-launched chat agent's `nanite mcp` subprocess forwards
// panel_open through POST /api/tools/call. This pins that the endpoint
// dispatches against a SelfToolsTransport with PanelSignalSink wired and the
// session_id stamped, so the panel_signal IS broadcast (the prior gap was the
// subprocess's bare transport having no sink — the proxy closes it by routing
// to this fully-wired transport).
func TestHandleSelfToolCall_PanelOpenReachesWiredSink(t *testing.T) {
	a, s := newToolCallTestAPI(t)
	st := selftools.NewSelfToolsTransport(s)
	sink := &recordingPanelSink{}
	st.PanelSignalSink = sink
	a.SetSelfTools(st)

	rec := postToolCall(t, a, map[string]any{
		"session_id": "sess-cli-launch",
		"name":       "panel_open",
		"args":       map[string]any{"panel_id": "work"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody=%s", rec.Code, rec.Body.String())
	}
	var res mcp.ToolResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, rec.Body.String())
	}
	if res.IsError {
		t.Fatalf("panel_open should succeed, got %#v", res.Content)
	}
	if sink.calls != 1 {
		t.Fatalf("PanelSignalSink got %d broadcasts, want 1", sink.calls)
	}
	if sink.sessionID != "sess-cli-launch" {
		t.Errorf("broadcast session_id = %q, want sess-cli-launch (endpoint must stamp it)", sink.sessionID)
	}
	if sink.signalType != "panel_signal" {
		t.Errorf("broadcast signalType = %q, want panel_signal", sink.signalType)
	}
	if !strings.Contains(sink.payload, `"action":"open"`) || !strings.Contains(sink.payload, `"panel_id":"work"`) {
		t.Errorf("broadcast payload = %q, want open/work signal", sink.payload)
	}
}

// TestExtractEnvelopeMarker pins the marker-scan helper: it pulls the JSON
// payload out of a card_show tool result's <!--ENVELOPE_DATA:...--> marker
// and returns "" when no marker is present.
func TestExtractEnvelopeMarker(t *testing.T) {
	cases := []struct {
		name string
		res  *mcp.ToolResult
		want string
	}{
		{"nil", nil, ""},
		{"no marker", &mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: "plain result"}}}, ""},
		{
			"with marker",
			&mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text",
				Text: "metric-card\n<!--ENVELOPE_DATA:{\"type\":\"metric-card\"}:ENVELOPE_DATA-->"}}},
			`{"type":"metric-card"}`,
		},
		{
			"marker in a later block",
			&mcp.ToolResult{Content: []mcp.ToolContent{
				{Type: "text", Text: "no marker here"},
				{Type: "text", Text: "<!--ENVELOPE_DATA:{\"k\":1}:ENVELOPE_DATA-->"},
			}},
			`{"k":1}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractEnvelopeMarker(tc.res); got != tc.want {
				t.Errorf("extractEnvelopeMarker = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestHandleSelfToolCall_CardShowBroadcastsEnvelope is the CW-20260517-0041
// regression: a CLI-launched agent runs card_show in its own process and
// forwards the call through POST /api/tools/call. The endpoint must broadcast
// the resulting envelope as a plugin_envelope SSE event on the session's
// active per-message stream WITHIN the turn, rather than leaving it to be
// surfaced a turn late once the CLI agent echoes the marker into its text.
func TestHandleSelfToolCall_CardShowBroadcastsEnvelope(t *testing.T) {
	// card_show validates `data` against the per-type schema, which needs the
	// shared envelope registry installed (the production composition root
	// does this at startup).
	envelope.SetupForTesting()

	a, s := newToolCallTestAPI(t)
	a.SetSelfTools(selftools.NewSelfToolsTransport(s))

	const sessionID = "sess-cli-cardshow"
	const msgID = "msg-cli-cardshow"

	// Stand up a per-message stream + subscriber the same way a GUI-initiated
	// turn does — this is the stream BroadcastSessionStreamEvent fans onto.
	a.Services.Streams.CreateStream(msgID, sessionID)
	sub, _, ok := a.Services.Streams.Subscribe(msgID, 0)
	if !ok {
		t.Fatal("Subscribe: stream not found")
	}

	rec := postToolCall(t, a, map[string]any{
		"session_id": sessionID,
		"name":       "card_show",
		"args": map[string]any{
			"type": "metric-card",
			"data": map[string]any{
				"label": "Response Time",
				"value": "142",
				"unit":  "ms",
			},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody=%s", rec.Code, rec.Body.String())
	}
	var res mcp.ToolResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, rec.Body.String())
	}
	if res.IsError {
		t.Fatalf("card_show should succeed, got %#v", res.Content)
	}

	// The plugin_envelope event must land on the stream within the turn —
	// no turn boundary, no reconcile timer. The pump goroutine forwards
	// asynchronously, so allow a short bound rather than asserting on a
	// bare non-blocking read.
	select {
	case evt := <-sub:
		if evt.Type != "plugin_envelope" {
			t.Fatalf("stream event type = %q, want plugin_envelope", evt.Type)
		}
		if !strings.Contains(evt.Envelope, `"metric-card"`) {
			t.Errorf("broadcast envelope = %q, want a metric-card payload", evt.Envelope)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no plugin_envelope event on the stream — card would render late")
	}
}

// TestHandleSelfToolCall_StampsSessionAndDispatches pins the end-to-end
// path: the endpoint stamps the request's session_id onto the dispatch
// context, and a session-scoped todo_create (which needs both a wired
// TodoStore and a session in context) succeeds through it.
func TestHandleSelfToolCall_StampsSessionAndDispatches(t *testing.T) {
	a, s := newToolCallTestAPI(t)
	st := selftools.NewSelfToolsTransport(s)
	st.TodoStore = s // *store.Store satisfies the TodoStore interface
	a.SetSelfTools(st)

	rec := postToolCall(t, a, map[string]any{
		"session_id": "sess-tool-call-test",
		"name":       "todo_create",
		"args":       map[string]any{"title": "smoke item"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody=%s", rec.Code, rec.Body.String())
	}
	var res mcp.ToolResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, rec.Body.String())
	}
	if res.IsError {
		t.Errorf("todo_create should succeed with TodoStore wired + session stamped, got %#v", res.Content)
	}
}
