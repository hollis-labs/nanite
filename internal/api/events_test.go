package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
)

func TestUnifiedEvents(t *testing.T) {
	api, mux := newTestAPI(t)
	host := naniteplugin.NewHost(http.NewServeMux(), naniteplugin.NewLogger("test"))
	api.SetPluginHost(host)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	events, cancel := openEventStream(t, srv.URL+"/api/events")
	defer cancel()

	// 1. Live presence dispatch
	api.Services.Streams.BroadcastPresence(chat.PresenceEvent{
		Type:      "stream_start",
		SessionID: "sess-test-1",
		AgentID:   "agent-a",
		Timestamp: "2026-09-15T00:00:00Z",
	})

	gotPresence := waitForEvent(t, events, "presence", 2*time.Second)
	if !strings.Contains(gotPresence, `"session_id":"sess-test-1"`) {
		t.Errorf("presence event missing session_id: %s", gotPresence)
	}
	if !strings.Contains(gotPresence, `"type":"stream_start"`) {
		t.Errorf("presence event missing type: %s", gotPresence)
	}

	// 2. Plugin lifecycle event dispatch
	host.EmitPluginEnabled("plugin-test-1")
	gotPlugin := waitForEvent(t, events, "plugin", 2*time.Second)
	if !strings.Contains(gotPlugin, `"plugin_id":"plugin-test-1"`) {
		t.Errorf("plugin event missing plugin_id: %s", gotPlugin)
	}
	if !strings.Contains(gotPlugin, naniteplugin.EventPluginEnabled) {
		t.Errorf("plugin event missing type: %s", gotPlugin)
	}

	// 3. Filtered non-lifecycle plugin event does not arrive as a plugin event
	host.EmitSessionStart("sess-2", "agent-2", "default")
	host.EmitPluginDisabled("plugin-test-2")
	gotPlugin2 := waitForEvent(t, events, "plugin", 2*time.Second)
	if !strings.Contains(gotPlugin2, `"plugin_id":"plugin-test-2"`) {
		t.Errorf("plugin event missing plugin_id: %s", gotPlugin2)
	}
}
