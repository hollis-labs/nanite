package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/hollis-labs/nanite/internal/tetherregistry"
)

// Background retry of pending Tether deregistrations (CW-20261024-0017
// follow-up). Retire already succeeded locally; this only finishes the Tether
// side. It complements, and does not replace, the retry on the next retire call.
const (
	// DeregisterRetryInterval is how often a pass runs.
	DeregisterRetryInterval = 15 * time.Minute
	// DeregisterRetryStartDelay lets the HTTP server come up before the first pass.
	DeregisterRetryStartDelay = 5 * time.Second
	// DeregisterRetryBatch bounds the records one pass attempts.
	DeregisterRetryBatch = 20

	deregisterBackoffBase = 15 * time.Minute
	deregisterBackoffCap  = 2 * time.Hour
)

// deregisterBackoff is the wait after the nth consecutive failure of one
// record: 15m, 30m, 1h, then capped at 2h.
func deregisterBackoff(failures int) time.Duration {
	d := deregisterBackoffBase
	for i := 1; i < failures; i++ {
		d *= 2
		if d >= deregisterBackoffCap {
			return deregisterBackoffCap
		}
	}
	return d
}

type deregisterState struct {
	failures int
	next     time.Time
}

// DeregisterRetrier runs bounded, backed-off passes over pending deregistrations.
type DeregisterRetrier struct {
	Svc *EnrollmentService
	// Now is the clock; default time.Now.
	Now func() time.Time
	// Batch bounds one pass; default DeregisterRetryBatch.
	Batch int

	mu     sync.Mutex
	state  map[string]*deregisterState
	logged bool
}

func (r *DeregisterRetrier) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Pass attempts the pending records whose backoff has elapsed, at most Batch of
// them, and returns how many completed and how many failed. With no usable
// registry token it does nothing (logged once). Failures log the record id and
// an error class only.
func (r *DeregisterRetrier) Pass(ctx context.Context) (done, failed int) {
	if err := r.Svc.Registrar.Ready(); err != nil {
		r.mu.Lock()
		first := !r.logged
		r.logged = true
		r.mu.Unlock()
		if first {
			slog.Info("enrollment: pending deregistrations not retried; Tether registry credential unavailable", "reason", enrollErrorCode2(err))
		}
		return 0, 0
	}
	r.mu.Lock()
	r.logged = false
	if r.state == nil {
		r.state = map[string]*deregisterState{}
	}
	r.mu.Unlock()
	w := r.Svc.writer()
	recs, err := w.PendingDeregistrations(ctx)
	if err != nil {
		slog.Error("enrollment: list pending deregistrations", "error", err)
		return 0, 0
	}
	batch := r.Batch
	if batch <= 0 {
		batch = DeregisterRetryBatch
	}
	now := r.now()
	attempted := 0
	for _, rec := range recs {
		if ctx.Err() != nil || attempted >= batch {
			break
		}
		r.mu.Lock()
		st := r.state[rec.HostSettingsID]
		if st == nil {
			st = &deregisterState{}
			r.state[rec.HostSettingsID] = st
		}
		wait := now.Before(st.next)
		r.mu.Unlock()
		if wait {
			continue
		}
		attempted++
		derr := r.Svc.deregister(ctx, rec)
		if derr == nil {
			derr = w.MarkDeregistered(ctx, rec.HostSettingsID)
		}
		r.mu.Lock()
		if derr == nil {
			delete(r.state, rec.HostSettingsID)
			done++
		} else {
			st.failures++
			st.next = now.Add(deregisterBackoff(st.failures))
			failed++
		}
		r.mu.Unlock()
		if derr != nil {
			if !errors.Is(derr, context.Canceled) {
				w.NoteAttempt(ctx, rec.HostSettingsID, enrollErrorCode2(derr))
			}
			slog.Warn("enrollment: deregistration retry failed", "host_settings_id", rec.HostSettingsID, "reason", enrollErrorCode2(derr))
		} else {
			slog.Info("enrollment: pending deregistration completed", "host_settings_id", rec.HostSettingsID)
		}
	}
	return done, failed
}

// RetryLoopOptions tunes Run. Zero values are the production settings.
type RetryLoopOptions struct {
	StartDelay time.Duration
	Interval   time.Duration
	// Ticks, when set, replaces the interval timer (tests); Start is then not delayed.
	Ticks <-chan time.Time
	Now   func() time.Time
}

// StartDeregisterRetry runs the retry in the background until ctx is
// cancelled: one pass shortly after start, then one per interval. It is not
// started (returns nil) when no Tether address is configured. The returned
// func stops the loop and waits for it.
func (s *EnrollmentService) StartDeregisterRetry(ctx context.Context, opts RetryLoopOptions) (stop func()) {
	if err := s.Registrar.Ready(); errors.Is(err, tetherregistry.ErrNotConfigured) {
		slog.Info("enrollment: deregistration retry not started; Tether is not configured")
		return nil
	}
	r := &DeregisterRetrier{Svc: s, Now: opts.Now}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticks := opts.Ticks
		first := opts.StartDelay
		if first == 0 && ticks == nil {
			first = DeregisterRetryStartDelay
		}
		if ticks == nil {
			iv := opts.Interval
			if iv <= 0 {
				iv = DeregisterRetryInterval
			}
			t := time.NewTicker(iv)
			defer t.Stop()
			ticks = t.C
		}
		start := time.NewTimer(first)
		defer start.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-start.C:
			case <-ticks:
			}
			r.Pass(ctx)
		}
	}()
	return func() { cancel(); <-done }
}
