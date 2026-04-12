package server

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestRecoverMiddleware_EmitsStackAndSpanEvent asserts that when a
// downstream handler panics:
//   - HTTP response is 500
//   - logged output contains both "PANIC:" and "stack:"
//   - an OTel span event is recorded on the request's span
func TestRecoverMiddleware_EmitsStackAndSpanEvent(t *testing.T) {
	// Redirect the stdlib logger used by recoverMiddleware so we can
	// assert on the emitted line. Restored on t.Cleanup.
	var logBuf strings.Builder
	origOut := log.Writer()
	origFlags := log.Flags()
	origPrefix := log.Prefix()
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(origOut)
		log.SetFlags(origFlags)
		log.SetPrefix(origPrefix)
	})

	// In-memory span recorder attached to an SDK tracer provider. The
	// request handler starts a span with this provider's tracer so
	// recoverMiddleware's trace.SpanFromContext finds a valid span to
	// record the event on.
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	tracer := tp.Tracer("recover_test")

	var panicHandler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("kaboom")
	})

	// Order: span-starter is OUTERMOST, recoverMiddleware INSIDE it.
	// recoverMiddleware's defer must run before span.End() so it can
	// still add an event to an un-ended span — this mirrors how
	// production tracing middleware is typically layered (trace
	// wrapper outside, recover inside).
	s := &Server{}
	inner := s.recoverMiddleware(panicHandler)
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := tracer.Start(r.Context(), "request")
		defer span.End()
		inner.ServeHTTP(w, r.WithContext(ctx))
	})

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "internal server error") {
		t.Errorf("body = %q, want to contain %q", body, "internal server error")
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "PANIC:") {
		t.Errorf("log output missing \"PANIC:\":\n%s", logged)
	}
	if !strings.Contains(logged, "stack:") {
		t.Errorf("log output missing \"stack:\":\n%s", logged)
	}

	// Span event assertion. End happens via defer in spanStarter, but
	// the recorder captures events immediately via OnEnd once the span
	// ends. The defer in spanStarter fires before ServeHTTP returns
	// (the panic unwinds through span.End()). So by the time we reach
	// here, the span is ended and recorded.
	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	events := ended[0].Events()
	var found bool
	for _, ev := range events {
		if ev.Name == "http.panic" {
			found = true
			// Verify key attributes are present.
			attrs := map[string]string{}
			for _, kv := range ev.Attributes {
				attrs[string(kv.Key)] = kv.Value.AsString()
			}
			if !strings.Contains(attrs["panic"], "kaboom") {
				t.Errorf("panic attr = %q, want to contain kaboom", attrs["panic"])
			}
			if attrs["stack"] == "" {
				t.Errorf("stack attr empty")
			}
			if attrs["http.method"] != http.MethodGet {
				t.Errorf("http.method = %q, want GET", attrs["http.method"])
			}
			if attrs["http.target"] != "/boom" {
				t.Errorf("http.target = %q, want /boom", attrs["http.target"])
			}
			break
		}
	}
	if !found {
		t.Errorf("http.panic event not found; events=%v", events)
	}
}

// TestRecoverMiddleware_NoSpanInContext asserts that when no tracer
// span exists in the request context (OTel disabled or no tracing
// middleware above), the recover middleware still responds 500 and
// logs, without panicking on the no-op span path.
func TestRecoverMiddleware_NoSpanInContext(t *testing.T) {
	var logBuf strings.Builder
	origOut := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(origOut)
		log.SetFlags(origFlags)
	})

	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("no-span boom")
	})
	s := &Server{}
	wrapped := s.recoverMiddleware(panicHandler)

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(logBuf.String(), "PANIC:") {
		t.Errorf("log output missing PANIC:\n%s", logBuf.String())
	}
}
