package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// installTestSlog swaps slog.Default for the duration of a test and
// returns a buffer capturing its JSON output. The previous default is
// restored via t.Cleanup.
func installTestSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// TestRecoverMiddleware_EmitsStackAndSpanEvent asserts that when a
// downstream handler panics:
//   - HTTP response is 500
//   - slog output carries panic and stack attributes
//   - an OTel span event is recorded on the request's span
func TestRecoverMiddleware_EmitsStackAndSpanEvent(t *testing.T) {
	logBuf := installTestSlog(t)

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

	// slog JSON output — assert structured attrs rather than stdlib
	// prefix text. Keys: msg, panic, stack.
	logged := logBuf.String()
	if !strings.Contains(logged, `"msg":"http handler panic"`) {
		t.Errorf("slog output missing panic msg:\n%s", logged)
	}
	if !strings.Contains(logged, `"panic":"kaboom"`) {
		t.Errorf("slog output missing panic attr:\n%s", logged)
	}
	if !strings.Contains(logged, `"stack":`) {
		t.Errorf("slog output missing stack attr:\n%s", logged)
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
	logBuf := installTestSlog(t)

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
	if !strings.Contains(logBuf.String(), `"panic":"no-span boom"`) {
		t.Errorf("slog output missing panic attr:\n%s", logBuf.String())
	}
}
