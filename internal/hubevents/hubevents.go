// Package hubevents publishes Nanite's own lifecycle to the shared event hub as
// resource.state_changed events (entity type service), through a
// mesh/eventbus publisher.
//
// It is a plain best-effort client: Nanite has no event outbox, so an event is
// queued in the publisher, spooled to disk while the hub is away, and lost only
// when the spool is full or the process dies before it is written. Nothing here
// ever blocks Nanite or fails it; with no hub configured every method does
// nothing. The hub owns the actor, the record time and the cursor, so none is
// sent.
package hubevents

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
)

// App is the name Nanite's events carry and its credential is issued for.
const App = "nanite"

const (
	schemaResourceState = "urn:hollis-labs:events:resource.state_changed@1"
	channelLifecycle    = "lifecycle"
)

// Service states Nanite reports (the catalog's resource.state_changed set).
const (
	StateStarting = "starting"
	StateRunning  = "running"
	StateStopped  = "stopped"
	StateFailed   = "failed"
)

// Emitter turns Nanite's lifecycle into hub events. A nil *Emitter is valid and
// does nothing.
type Emitter struct {
	sink       runtimeevents.Sink
	service    string
	generation uint64
	seq        atomic.Uint64
	now        func() time.Time
}

// New returns an Emitter that writes to sink for the service id (the noun in the
// event subject, msg://service/nanite/<service>). A nil sink gives a nil Emitter.
func New(sink runtimeevents.Sink, service string) *Emitter {
	if sink == nil || service == "" {
		return nil
	}
	e := &Emitter{sink: sink, service: service, now: time.Now}
	// The sequence restarts at 1 with every process; the generation says so.
	// Unix microseconds stay far below 2^53, so the number is exact in JSON.
	e.generation = uint64(e.now().UnixMicro()) // #nosec G115 -- positive, 2026 era value
	return e
}

// ServiceState reports a change in the state of the Nanite service. from may be
// empty (not known); reason is one of the catalog's closed codes or empty;
// triggerKind says what caused it (system, human, agent, scheduler).
func (e *Emitter) ServiceState(ctx context.Context, from, to, reason, triggerKind string) {
	if e == nil {
		return
	}
	if triggerKind == "" {
		triggerKind = "system"
	}
	p := map[string]any{
		"entity":  map[string]string{"type": "service", "id": e.service},
		"to":      to,
		"trigger": map[string]any{"kind": triggerKind},
	}
	if from != "" {
		p["from"] = from
	}
	if reason != "" {
		p["reason"] = reason
	}
	payload, err := json.Marshal(p)
	if err != nil {
		return // a map of strings always marshals
	}
	severity := "info"
	if to == StateFailed {
		severity = "warning"
	}
	ev := runtimeevents.Event{
		SchemaVersion: runtimeevents.SchemaVersion,
		ID:            runtimeevents.NewEventID(),
		Kind:          runtimeevents.EventKind("resource.state_changed"),
		Time:          e.now().UTC(),
		App:           App,
		Sequence:      e.seq.Add(1),
		Generation:    e.generation,
		Subject:       "msg://service/nanite/" + e.service,
		Source:        runtimeevents.Source{Channel: runtimeevents.SourceChannel(channelLifecycle)},
		Class:         &runtimeevents.Class{Category: "state", Severity: severity, Visibility: "environment", PayloadSchema: schemaResourceState},
		Payload:       payload,
	}
	// The sink reports only a malformed envelope, which is a bug here and must
	// not take Nanite down.
	if err := e.sink.Write(ctx, ev); err != nil {
		slog.Warn("hub event not published", "kind", ev.Kind, "err", err)
	}
}
