package hubevents

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/hollis-labs/libs/harness/core/adapters/eventbussink"
	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents/ingest"
	"github.com/hollis-labs/libs/mesh/eventbus"
)

// capture is a publisher that keeps what it is given. No hub is involved.
type capture struct {
	mu    sync.Mutex
	items []eventbus.Item
}

func (c *capture) Publish(it eventbus.Item) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = append(c.items, it)
}
func (c *capture) Close(context.Context) error { return nil }

func TestServiceStateEventsPassTheHubsIngestChecks(t *testing.T) {
	pub := &capture{}
	e := New(eventbussink.New(pub, eventbussink.WithApp(App)), "nanite")
	e.ServiceState(t.Context(), "", StateStarting, "", "system")
	e.ServiceState(t.Context(), StateStarting, StateRunning, "", "system")
	e.ServiceState(t.Context(), StateRunning, StateStopped, "operator_stop", "human")
	e.ServiceState(t.Context(), StateStarting, StateFailed, "", "system")

	if len(pub.items) != 4 {
		t.Fatalf("published %d events, want 4", len(pub.items))
	}
	var lastSeq uint64
	for i, it := range pub.items {
		if _, rej := ingest.CheckItem(App, it.Body, ingest.Options{}); rej != nil {
			t.Fatalf("event %d rejected by the hub's ingest check: %+v\n%s", i, rej, it.Body)
		}
		var env map[string]json.RawMessage
		if err := json.Unmarshal(it.Body, &env); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"actor", "recorded_at", "cursor"} {
			if _, has := env[f]; has {
				t.Fatalf("event %d carries %q, which the hub assigns", i, f)
			}
		}
		if string(env["subject"]) != `"msg://service/nanite/nanite"` || string(env["app"]) != `"nanite"` {
			t.Fatalf("event %d subject/app = %s %s", i, env["subject"], env["app"])
		}
		var seq uint64
		_ = json.Unmarshal(env["sequence"], &seq)
		if seq != lastSeq+1 {
			t.Fatalf("event %d sequence = %d, want %d", i, seq, lastSeq+1)
		}
		lastSeq = seq
	}
	var failed struct {
		Class struct {
			Severity string `json:"severity"`
		} `json:"class"`
	}
	_ = json.Unmarshal(pub.items[3].Body, &failed)
	if failed.Class.Severity != "warning" {
		t.Fatalf("a failed service has severity %q, want warning", failed.Class.Severity)
	}
}

// With no hub configured the emitter is nil and every call does nothing.
func TestNilEmitterDoesNothing(t *testing.T) {
	var e *Emitter
	e.ServiceState(t.Context(), "", StateRunning, "", "")
	if New(nil, "nanite") != nil {
		t.Fatal("a nil sink should give a nil emitter")
	}
}

// eventbus.FromEnv without a URL is the no-op publisher, and the sink over it
// publishes nothing and does not fail.
func TestNoopPublisherIsHarmless(t *testing.T) {
	t.Setenv(eventbus.EnvURL, "")
	pub, err := eventbus.FromEnv(App, eventbus.Config{})
	if err != nil {
		t.Fatal(err)
	}
	New(eventbussink.New(pub), "nanite").ServiceState(t.Context(), "", StateRunning, "", "system")
	if err := pub.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
