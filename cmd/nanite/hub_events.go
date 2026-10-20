package main

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/hollis-labs/libs/harness/core/adapters/eventbussink"
	"github.com/hollis-labs/libs/mesh/eventbus"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/hubevents"
)

// hubLifecycle announces Nanite's service lifecycle to the event hub.
type hubLifecycle struct {
	em      *hubevents.Emitter
	closeFn func(context.Context) error
	once    sync.Once
}

// startHubEvents returns the lifecycle announcer. With bus.enabled false (the
// default), or with no HOLLIS_EVENTS_URL, every method does nothing. The hub is
// found through the environment (HOLLIS_EVENTS_URL and the app's token file, as
// in mesh/eventbus). It logs and carries on when it cannot start: telling the
// hub about Nanite is never a reason to stop Nanite.
func startHubEvents(cfg config.BusConfig) *hubLifecycle {
	h := &hubLifecycle{}
	if !cfg.Enabled {
		return h
	}
	if cfg.SpoolDir != "" {
		if err := os.MkdirAll(cfg.SpoolDir, 0o700); err != nil {
			slog.Warn("hub events: spool dir", "err", err)
			return h
		}
	}
	pub, err := eventbus.FromEnv(hubevents.App, eventbus.Config{
		SpoolDir: cfg.SpoolDir,
		OnError:  func(err error) { slog.Warn("hub events: client", "err", err) },
	})
	if err != nil {
		slog.Warn("hub events: client", "err", err)
		return h
	}
	h.em = hubevents.New(eventbussink.New(pub, eventbussink.WithApp(hubevents.App)), "nanite")
	h.closeFn = pub.Close
	return h
}

// starting, running and failed announce those states; they are safe on a nil or
// disabled announcer.
func (h *hubLifecycle) starting() {
	h.em.ServiceState(context.Background(), "", hubevents.StateStarting, "", "system")
}

func (h *hubLifecycle) running() {
	h.em.ServiceState(context.Background(), hubevents.StateStarting, hubevents.StateRunning, "", "system")
}

func (h *hubLifecycle) failed() {
	h.em.ServiceState(context.Background(), "", hubevents.StateFailed, "", "system")
}

// stopped announces the stop once, however many paths reach it, and gives the
// client a moment to hand the event over before the process exits.
func (h *hubLifecycle) stopped() {
	if h == nil {
		return
	}
	h.once.Do(func() {
		h.em.ServiceState(context.Background(), "", hubevents.StateStopped, "", "system")
		if h.closeFn != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := h.closeFn(ctx); err != nil {
				slog.Warn("hub events: close", "err", err)
			}
		}
	})
}
