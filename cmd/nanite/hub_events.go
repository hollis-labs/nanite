package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/hollis-labs/libs/harness/core/adapters/eventbussink"
	"github.com/hollis-labs/libs/mesh/eventbus"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/hubevents"
)

// hubLifecycle announces Nanite's service lifecycle to the event hub. It says a
// state only once it is true and never replaces a terminal state: after failed
// or stopped nothing else is announced. Every method is safe on a nil
// *hubLifecycle and does nothing when bus.enabled is off.
type hubLifecycle struct {
	em      *hubevents.Emitter
	closeFn func(context.Context) error

	mu      sync.Mutex
	running bool // the listener was bound
	done    bool // a terminal state (stopped or failed) was announced
	closed  bool
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

// starting announces that startup began.
func (h *hubLifecycle) starting() {
	if h == nil {
		return
	}
	h.em.ServiceState(context.Background(), "", hubevents.StateStarting, "", "system")
}

// ready announces running. Call it only once the listener is bound.
func (h *hubLifecycle) ready() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.done {
		return
	}
	h.running = true
	h.em.ServiceState(context.Background(), hubevents.StateStarting, hubevents.StateRunning, "", "system")
}

// failed announces failed, the terminal state of a start or run that ended in
// an error. Nothing is announced after it.
func (h *hubLifecycle) failed() { h.terminal(hubevents.StateFailed) }

// stopped announces the stop of a service that was running. A service that never
// reached running did not stop; it failed to start.
func (h *hubLifecycle) stopped() { h.terminal(hubevents.StateStopped) }

func (h *hubLifecycle) terminal(to string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.done {
		return
	}
	if to == hubevents.StateStopped && !h.running {
		to = hubevents.StateFailed
	}
	h.done = true
	h.em.ServiceState(context.Background(), "", to, "", "system")
	h.closeLocked()
}

// finish is the deferred end of cmdServe: whatever has not been announced is,
// then the client is closed. Leaving before the listener was bound is a failed
// start, not a stop.
func (h *hubLifecycle) finish() {
	if h == nil {
		return
	}
	h.stopped()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closeLocked()
}

// closeLocked gives the client a moment to hand the last event over. The caller
// holds h.mu.
func (h *hubLifecycle) closeLocked() {
	if h.closed || h.closeFn == nil {
		return
	}
	h.closed = true
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := h.closeFn(ctx); err != nil {
		slog.Warn("hub events: close", "err", err)
	}
}

// readyServer is the part of the HTTP server serveAnnouncing runs.
type readyServer interface {
	ListenAndServeReady(ready func()) error
}

// serveAnnouncing serves, announcing running only once the listener is bound,
// and the end of serving as stopped (a clean close) or failed (any other error,
// a bind failure included). The error is returned unchanged.
func serveAnnouncing(srv readyServer, h *hubLifecycle) error {
	err := srv.ListenAndServeReady(h.ready)
	switch {
	case err == nil:
	case errors.Is(err, http.ErrServerClosed):
		h.stopped()
	default:
		h.failed()
	}
	return err
}
