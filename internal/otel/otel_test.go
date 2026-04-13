package otel

import (
	"context"
	"reflect"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace/noop"
)

// TestInit_EnvDisabled asserts that NANITE_OTEL_DISABLED=1 installs the
// no-op tracer provider and returns the noop shutdown sentinel.
// Tracer("test") must still return a usable (non-nil) tracer so callers
// can remain branch-free.
func TestInit_EnvDisabled(t *testing.T) {
	t.Setenv(disabledEnvVar, "1")
	shutdown, err := Init(context.Background(), Config{ServiceName: "test"})
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("shutdown is nil")
	}
	if reflect.ValueOf(shutdown).Pointer() != reflect.ValueOf(noopShutdown).Pointer() {
		t.Fatal("expected no-op shutdown sentinel when env-disabled")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("noop shutdown returned error: %v", err)
	}
	tp := otel.GetTracerProvider()
	// Identity check: the provider must be a noop.TracerProvider instance.
	expected := noop.NewTracerProvider()
	if reflect.TypeOf(tp) != reflect.TypeOf(expected) {
		t.Fatalf("expected noop tracer provider, got %T", tp)
	}
	tr := otel.Tracer("test")
	if tr == nil {
		t.Fatal("otel.Tracer returned nil even with noop provider")
	}
	_, span := tr.Start(context.Background(), "op")
	if span == nil {
		t.Fatal("tracer Start returned nil span")
	}
	span.End()
}

// TestInit_CfgDisabled asserts that Config.Disabled installs the no-op
// path when the env var is absent.
func TestInit_CfgDisabled(t *testing.T) {
	t.Setenv(disabledEnvVar, "")
	shutdown, err := Init(context.Background(), Config{ServiceName: "test", Disabled: true})
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if reflect.ValueOf(shutdown).Pointer() != reflect.ValueOf(noopShutdown).Pointer() {
		t.Fatal("expected no-op shutdown sentinel when cfg-disabled")
	}
}

// TestInit_EnabledPath verifies that when neither gate is tripped, Init
// delegates to feotel and returns a non-sentinel shutdown. We don't
// assert on exporter connectivity — feotel uses a batch exporter that
// doesn't fail synchronously on dial errors. We only assert the code
// path diverges from the no-op branch.
func TestInit_EnabledPath(t *testing.T) {
	t.Setenv(disabledEnvVar, "")
	shutdown, err := Init(context.Background(), Config{ServiceName: "test", Disabled: false})
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("shutdown is nil")
	}
	if reflect.ValueOf(shutdown).Pointer() == reflect.ValueOf(noopShutdown).Pointer() {
		t.Fatal("expected feotel shutdown, got no-op sentinel")
	}
	// Flush + shut down to avoid leaking the batch exporter goroutine.
	_ = shutdown(context.Background())
}
