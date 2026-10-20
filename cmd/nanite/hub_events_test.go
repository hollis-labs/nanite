package main

import (
	"encoding/json"
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
	h.running()
	h.stopped()
}

// Enabled but with no hub URL there is no hub: the announcer is harmless.
func TestHubEventsWithoutAURLAreHarmless(t *testing.T) {
	t.Setenv(eventbus.EnvURL, "")
	h := startHubEvents(config.BusConfig{Enabled: true, SpoolDir: filepath.Join(t.TempDir(), "spool")})
	h.starting()
	h.running()
	h.stopped()
	h.stopped() // a second stop is a no-op
}

// Against a fake hub, the lifecycle arrives in order, passes the hub's ingest
// checks, and a stop reached twice is announced once.
func TestHubEventsDeliverTheLifecycle(t *testing.T) {
	hub := &fakeHub{}
	srv := httptest.NewServer(hub)
	defer srv.Close()
	t.Setenv(eventbus.EnvURL, srv.URL)

	h := startHubEvents(config.BusConfig{Enabled: true, SpoolDir: filepath.Join(t.TempDir(), "spool")})
	h.starting()
	h.running()
	h.stopped()
	h.stopped()

	got := hub.seen()
	want := []string{"starting", "running", "stopped"}
	if len(got) != len(want) {
		t.Fatalf("hub accepted %v (rejected %v), want %v", got, hub.rejected, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("hub accepted %v, want %v", got, want)
		}
	}
	if len(hub.rejected) != 0 {
		t.Fatalf("hub rejected %v", hub.rejected)
	}
}
