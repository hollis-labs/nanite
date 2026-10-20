package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents/ingest"
	"github.com/hollis-labs/libs/mesh/eventbus"
	"github.com/hollis-labs/nanite/internal/config"
)

// fakeHub accepts what the hub's own ingest check accepts and records the
// resulting states. Nothing here touches a real hub.
type fakeHub struct {
	mu       sync.Mutex
	states   []string
	rejected []string
}

func (h *fakeHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var req struct {
		Events []json.RawMessage `json:"events"`
	}
	if json.Unmarshal(raw, &req) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	type result struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Cursor string `json:"cursor"`
	}
	var results []result
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, e := range req.Events {
		var env struct {
			ID      string `json:"id"`
			Payload struct {
				To string `json:"to"`
			} `json:"payload"`
		}
		_ = json.Unmarshal(e, &env)
		if _, rej := ingest.CheckItem("nanite", e, ingest.Options{}); rej != nil {
			h.rejected = append(h.rejected, env.ID)
			results = append(results, result{ID: env.ID, Status: "rejected"})
			continue
		}
		h.states = append(h.states, env.Payload.To)
		results = append(results, result{ID: env.ID, Status: "accepted", Cursor: "c" + string(rune('a'+i))})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
}

func (h *fakeHub) seen() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.states...)
}

// The default config sends nothing and starts nothing.
func TestHubEventsAreOffByDefault(t *testing.T) {
	if config.DefaultAppConfig().Bus.Enabled {
		t.Fatal("bus.enabled must default to false")
	}
	h := startHubEvents(config.BusConfig{})
	h.starting()
	h.ready()
	h.finish()
}

// Enabled but with no hub URL there is no hub: the announcer is harmless.
func TestHubEventsWithoutAURLAreHarmless(t *testing.T) {
	t.Setenv(eventbus.EnvURL, "")
	h := startHubEvents(config.BusConfig{Enabled: true, SpoolDir: filepath.Join(t.TempDir(), "spool")})
	h.starting()
	h.ready()
	h.stopped()
	h.stopped() // a second stop is a no-op
	h.finish()
}

// runHubLifecycle runs the announcer through steps against a fake hub and returns the
// states the hub accepted, in order. finish closes the client, so every event is
// delivered before it returns.
func runHubLifecycle(t *testing.T, steps func(h *hubLifecycle)) (*fakeHub, []string) {
	t.Helper()
	hub := &fakeHub{}
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)
	t.Setenv(eventbus.EnvURL, srv.URL)
	h := startHubEvents(config.BusConfig{Enabled: true, SpoolDir: filepath.Join(t.TempDir(), "spool")})
	steps(h)
	h.finish()
	return hub, hub.seen()
}

func wantStates(t *testing.T, hub *fakeHub, got []string, want ...string) {
	t.Helper()
	if len(hub.rejected) != 0 {
		t.Fatalf("hub rejected %v", hub.rejected)
	}
	if len(got) != len(want) {
		t.Fatalf("hub accepted %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("hub accepted %v, want %v", got, want)
		}
	}
}

// Against a fake hub that runs the ingest check, a normal run is
// starting, running, stopped, and a stop reached twice is announced once.
func TestHubEventsDeliverTheLifecycle(t *testing.T) {
	hub, got := runHubLifecycle(t, func(h *hubLifecycle) {
		h.starting()
		h.ready()
		h.stopped() // the signal path
		h.stopped() // and the deferred path
	})
	wantStates(t, hub, got, "starting", "running", "stopped")
}

// A listener error after running is a terminal failure; the deferred exit must
// not turn it into starting, running, failed, stopped.
func TestHubEventsKeepAFailureTerminal(t *testing.T) {
	hub, got := runHubLifecycle(t, func(h *hubLifecycle) {
		h.starting()
		h.ready()
		h.failed()
	})
	wantStates(t, hub, got, "starting", "running", "failed")
}

// An error before the listener is bound (initialisation, an occupied port) is a
// failed start: there is no running and no stopped.
func TestHubEventsAFailedStartIsFailedNotStopped(t *testing.T) {
	hub, got := runHubLifecycle(t, func(h *hubLifecycle) {
		h.starting()
		// cmdServe returns an error here; only the deferred finish runs.
	})
	wantStates(t, hub, got, "starting", "failed")

	hub, got = runHubLifecycle(t, func(h *hubLifecycle) {
		h.starting()
		h.failed() // ListenAndServeReady returned a bind error
	})
	wantStates(t, hub, got, "starting", "failed")
}

// running cannot follow a terminal state.
func TestHubEventsRunningNeverFollowsATerminalState(t *testing.T) {
	hub, got := runHubLifecycle(t, func(h *hubLifecycle) {
		h.starting()
		h.failed()
		h.ready()
	})
	wantStates(t, hub, got, "starting", "failed")
}

// A nil announcer (never created) is harmless.
func TestHubEventsNilAnnouncerIsHarmless(t *testing.T) {
	var h *hubLifecycle
	h.starting()
	h.ready()
	h.failed()
	h.stopped()
	h.finish()
}

// stubServer stands in for the HTTP server: it binds (or not), then ends with err.
type stubServer struct {
	binds bool
	end   error
}

func (s stubServer) ListenAndServeReady(ready func()) error {
	if s.binds {
		ready()
	}
	return s.end
}

// The serve path: running only after a bind, a bind error is a failed start, a
// clean close is a stop and any other error after running is a failure that the
// deferred exit does not overwrite.
func TestServeAnnouncingMapsTheEndOfServing(t *testing.T) {
	for _, c := range []struct {
		name string
		srv  stubServer
		want []string
	}{
		{"bind error", stubServer{binds: false, end: errors.New("listen tcp: address already in use")}, []string{"starting", "failed"}},
		{"clean close", stubServer{binds: true, end: http.ErrServerClosed}, []string{"starting", "running", "stopped"}},
		{"serve error", stubServer{binds: true, end: errors.New("accept: broken")}, []string{"starting", "running", "failed"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var gotErr error
			hub, got := runHubLifecycle(t, func(h *hubLifecycle) {
				h.starting()
				gotErr = serveAnnouncing(c.srv, h)
			})
			if !errors.Is(gotErr, c.srv.end) {
				t.Fatalf("serveAnnouncing returned %v, want %v unchanged", gotErr, c.srv.end)
			}
			wantStates(t, hub, got, c.want...)
		})
	}
}
