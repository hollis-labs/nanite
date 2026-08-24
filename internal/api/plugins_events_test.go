package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	goplugin "github.com/hollis-labs/plugin-sdk"
)

// TestPluginsEvents_LoadUnloadCycle drives a plugin through LoadPlugin and
// UnloadPlugin and verifies the SSE stream delivers plugin.installed and
// plugin.uninstalled in order. Acceptance criterion per plan §B.13:
// "GET /api/plugins/events streams events when a plugin is load/unload-cycled".
func TestPluginsEvents_LoadUnloadCycle(t *testing.T) {
	host := naniteplugin.NewHost(http.NewServeMux(), naniteplugin.NewLogger("test"))

	mux := http.NewServeMux()
	registerPluginsEventsRoute(mux, host)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	events, cancel := openEventStream(t, srv.URL+"/api/plugins/events")
	defer cancel()

	// Load the plugin via the full host path. host.LoadPlugin emits
	// plugin.installed asynchronously via safego.Go — we read the SSE stream
	// until we see it.
	p := &lifecycleTestPlugin{id: "b8-lifecycle-test"}
	if err := host.LoadPlugin(p); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}

	installed := waitForEvent(t, events, naniteplugin.EventPluginInstalled, 2*time.Second)
	if !strings.Contains(installed, `"plugin_id":"b8-lifecycle-test"`) {
		t.Errorf("installed event missing plugin_id: %s", installed)
	}
	if !strings.Contains(installed, `"version":"0.0.1"`) {
		t.Errorf("installed event missing version: %s", installed)
	}

	// Unload the plugin.
	if err := host.UnloadPlugin(p.ID()); err != nil {
		t.Fatalf("UnloadPlugin: %v", err)
	}

	uninstalled := waitForEvent(t, events, naniteplugin.EventPluginUninstalled, 2*time.Second)
	if !strings.Contains(uninstalled, `"plugin_id":"b8-lifecycle-test"`) {
		t.Errorf("uninstalled event missing plugin_id: %s", uninstalled)
	}
}

// TestPluginsEvents_EnabledDisabledLoadFailed covers the three emission
// points that aren't on the host's Load/Unload critical path: enable/disable
// handler calls and loader error paths.
func TestPluginsEvents_EnabledDisabledLoadFailed(t *testing.T) {
	host := naniteplugin.NewHost(http.NewServeMux(), naniteplugin.NewLogger("test"))

	mux := http.NewServeMux()
	registerPluginsEventsRoute(mux, host)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	events, cancel := openEventStream(t, srv.URL+"/api/plugins/events")
	defer cancel()

	host.EmitPluginEnabled("alpha")
	got := waitForEvent(t, events, naniteplugin.EventPluginEnabled, 2*time.Second)
	if !strings.Contains(got, `"plugin_id":"alpha"`) {
		t.Errorf("enabled event wrong: %s", got)
	}

	host.EmitPluginDisabled("beta")
	got = waitForEvent(t, events, naniteplugin.EventPluginDisabled, 2*time.Second)
	if !strings.Contains(got, `"plugin_id":"beta"`) {
		t.Errorf("disabled event wrong: %s", got)
	}

	host.EmitPluginLoadFailed("gamma", "manifest invalid")
	got = waitForEvent(t, events, naniteplugin.EventPluginLoadFailed, 2*time.Second)
	if !strings.Contains(got, `"plugin_id":"gamma"`) ||
		!strings.Contains(got, `"reason":"manifest invalid"`) {
		t.Errorf("load_failed event wrong: %s", got)
	}
}

// TestPluginsEvents_NonLifecycleFiltered verifies the stream drops events
// that aren't in the lifecycle set — the endpoint must not leak the full
// host event bus to the frontend.
func TestPluginsEvents_NonLifecycleFiltered(t *testing.T) {
	host := naniteplugin.NewHost(http.NewServeMux(), naniteplugin.NewLogger("test"))

	mux := http.NewServeMux()
	registerPluginsEventsRoute(mux, host)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	events, cancel := openEventStream(t, srv.URL+"/api/plugins/events")
	defer cancel()

	// Emit a non-lifecycle event. The SSE handler should filter it out.
	host.EmitSessionStart("sess-1", "agent-1", "default")

	// Follow up with a lifecycle event we expect to see.
	host.EmitPluginEnabled("after-filter")

	got := waitForEvent(t, events, naniteplugin.EventPluginEnabled, 2*time.Second)
	if !strings.Contains(got, `"plugin_id":"after-filter"`) {
		t.Errorf("expected enabled after filter: %s", got)
	}
	// If the filter were broken we'd have also seen a session.start event in
	// the buffer. waitForEvent skips past non-matching events, so we can't
	// assert absence directly without racing — but the handler's event-type
	// filter is unit-testable via the lifecycleEventTypes map check below.
	if lifecycleEventTypes[naniteplugin.EventSessionStart] {
		t.Error("session.start must not be in lifecycleEventTypes")
	}
}

// --- helpers ---

// sseEvent is a parsed SSE record.
type sseEvent struct {
	eventType string
	data      string
}

// openEventStream opens a long-lived HTTP GET and returns a channel that
// emits parsed SSE events. Cancel the returned func to tear down.
func openEventStream(t *testing.T, url string) (<-chan sseEvent, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("GET %s: %v", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		resp.Body.Close()
		cancel()
		t.Fatalf("expected text/event-stream, got %q", ct)
	}

	ch := make(chan sseEvent, 32)

	var once sync.Once
	closeCh := func() { once.Do(func() { close(ch) }) }

	go func() {
		defer resp.Body.Close()
		defer closeCh()
		sc := bufio.NewScanner(resp.Body)
		var cur sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if cur.eventType != "" || cur.data != "" {
					select {
					case ch <- cur:
					case <-ctx.Done():
						return
					}
				}
				cur = sseEvent{}
			case strings.HasPrefix(line, ":"):
				// comment / keepalive — ignore
			case strings.HasPrefix(line, "event: "):
				cur.eventType = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cur.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()

	return ch, func() {
		cancel()
		// drain so the reader goroutine can exit if it's mid-send
		go func() {
			for range ch {
			}
		}()
	}
}

// waitForEvent pulls SSE records until it finds one whose event-type equals
// want, or the deadline expires. Returns the matching record's data field.
func waitForEvent(t *testing.T, ch <-chan sseEvent, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("stream closed before %q arrived", want)
			}
			if ev.eventType == want {
				return ev.data
			}
		case <-deadline:
			t.Fatalf("timeout waiting for %q", want)
		}
	}
}

// lifecycleTestPlugin is a no-op plugin for host-level load/unload cycle
// testing. Distinct type from other test files to avoid cross-package
// collision, though synthPlugin would work too.
type lifecycleTestPlugin struct {
	id string
}

func (p *lifecycleTestPlugin) ID() string                 { return p.id }
func (p *lifecycleTestPlugin) Name() string               { return p.id }
func (p *lifecycleTestPlugin) Version() string            { return "0.0.1" }
func (p *lifecycleTestPlugin) Description() string        { return "b8 lifecycle test plugin" }
func (p *lifecycleTestPlugin) Dependencies() []string     { return nil }
func (p *lifecycleTestPlugin) Load(h goplugin.Host) error { return nil }
func (p *lifecycleTestPlugin) Unload() error              { return nil }
func (p *lifecycleTestPlugin) Status() goplugin.PluginStatus {
	return goplugin.PluginStatus{Loaded: true, Enabled: true}
}
