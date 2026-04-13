// Package safego provides panic-safe goroutine and call wrappers.
//
// Every spawned goroutine in nanite must be tracked (see internal/lifecycle)
// and panic-safe. This package provides the recovery half: a goroutine that
// panics emits a structured slog.Error and an OTel span event rather than
// tearing down the whole process.
//
// Usage:
//
//	safego.Go(ctx, "worker.dispatch", func() {
//	    // work that must not kill the process if it panics
//	})
//
//	safego.Call(func() {
//	    // synchronous plugin-code invocation boundary
//	})
//
// Tests can observe panic recovery by installing a hook:
//
//	safego.SetPanicHook(func(label string, v any, stack []byte) { ... })
//
// This package is deliberately small — callers compose it with
// internal/lifecycle for tracked goroutine pools.
package safego

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// PanicHook is invoked after a recovered panic has already been reported via
// slog and an OTel span event. Tests use it to observe recovery; the default
// hook is a no-op.
type PanicHook func(label string, v any, stack []byte)

var (
	hookMu sync.RWMutex
	hook   PanicHook = func(string, any, []byte) {}
)

// SetPanicHook installs a PanicHook. Passing nil restores the no-op default.
// Returns the previous hook so tests can restore it.
func SetPanicHook(h PanicHook) PanicHook {
	hookMu.Lock()
	defer hookMu.Unlock()
	prev := hook
	if h == nil {
		hook = func(string, any, []byte) {}
	} else {
		hook = h
	}
	return prev
}

func currentHook() PanicHook {
	hookMu.RLock()
	defer hookMu.RUnlock()
	return hook
}

var tracer = otel.Tracer("nanite/safego")

// Go spawns fn as a goroutine wrapped in panic recovery.
//
// On panic: a slog.Error is emitted with the label, panic value, and stack,
// an OTel span event "safego.panic" is recorded on the span in ctx (if any),
// and the installed PanicHook is invoked. The goroutine returns normally; the
// process is not torn down.
//
// fn takes no arguments — callers close over whatever state they need. The
// context is used only for OTel span attachment; it is not passed to fn.
// Callers that need cancellation should use internal/lifecycle.Manager.Go.
func Go(ctx context.Context, label string, fn func()) {
	go func() {
		defer recoverAndReport(ctx, label)
		fn()
	}()
}

// Call runs fn synchronously in the calling goroutine and recovers from any
// panic, reporting it the same way Go does. Use this at plugin-code
// invocation boundaries where the caller must keep running even if plugin
// code panics.
func Call(ctx context.Context, label string, fn func()) {
	defer recoverAndReport(ctx, label)
	fn()
}

func recoverAndReport(ctx context.Context, label string) {
	r := recover()
	if r == nil {
		return
	}
	stack := debug.Stack()
	slog.Error("safego: recovered panic",
		slog.String("label", label),
		slog.Any("panic", r),
		slog.String("stack", string(stack)),
	)

	// OTel span event: prefer span from ctx, fall back to a new span.
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		_, s := tracer.Start(ctx, "safego.panic")
		span = s
		defer span.End()
	}
	span.AddEvent("safego.panic",
		trace.WithAttributes(
			attribute.String("label", label),
			attribute.String("panic", sprintAny(r)),
			attribute.String("stack", string(stack)),
		),
	)

	currentHook()(label, r, stack)
}

func sprintAny(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case error:
		return x.Error()
	default:
		return fmt.Sprintf("%v", v)
	}
}
