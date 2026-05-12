package describer

import (
	"context"
	"testing"
)

// TestRegistry_RegisterGetHas exercises the basic Registry contract
// (Register, Get, Has, Count, nil-safety) so downstream sites can rely
// on a stable API.
func TestRegistry_RegisterGetHas(t *testing.T) {
	reg := NewRegistry()

	if reg.Count() != 0 {
		t.Fatalf("empty registry: Count = %d, want 0", reg.Count())
	}
	if reg.Has("anything") {
		t.Fatal("empty registry: Has reported true")
	}
	if _, ok := reg.Get("anything"); ok {
		t.Fatal("empty registry: Get reported ok=true")
	}

	reg.Register("foo", Func(func(_ context.Context, _ CallerAgent) string { return "FOO" }))
	if !reg.Has("foo") {
		t.Fatal("Has(foo) = false after Register")
	}
	if reg.Count() != 1 {
		t.Fatalf("Count = %d, want 1", reg.Count())
	}
	d, ok := reg.Get("foo")
	if !ok {
		t.Fatal("Get(foo) ok=false after Register")
	}
	if got := d.Describe(context.Background(), CallerAgent{}); got != "FOO" {
		t.Fatalf("Describe = %q, want FOO", got)
	}

	// Last-write-wins re-register.
	reg.Register("foo", Func(func(_ context.Context, _ CallerAgent) string { return "FOO2" }))
	d, _ = reg.Get("foo")
	if got := d.Describe(context.Background(), CallerAgent{}); got != "FOO2" {
		t.Fatalf("after re-register: Describe = %q, want FOO2", got)
	}
}

// TestRegistry_NilSafety verifies every method tolerates a nil receiver
// — the materialization site checks for nil up front, but other
// callers (tests, mux/devmode builds) may pass nil.
func TestRegistry_NilSafety(t *testing.T) {
	var reg *Registry

	// None of these may panic.
	reg.Register("x", Func(func(_ context.Context, _ CallerAgent) string { return "X" }))
	if reg.Has("x") {
		t.Fatal("nil registry: Has reported true")
	}
	if _, ok := reg.Get("x"); ok {
		t.Fatal("nil registry: Get reported ok=true")
	}
	if reg.Count() != 0 {
		t.Fatalf("nil registry: Count = %d, want 0", reg.Count())
	}
}

// TestRegistry_EmptyToolNameAndNilDescriber verifies that Register
// silently no-ops on a missing tool name or a nil Describer. The
// materialization site treats these as "no Describer registered"
// rather than panicking.
func TestRegistry_EmptyToolNameAndNilDescriber(t *testing.T) {
	reg := NewRegistry()
	reg.Register("", Func(func(_ context.Context, _ CallerAgent) string { return "X" }))
	if reg.Count() != 0 {
		t.Fatal("empty tool name was registered")
	}
	reg.Register("foo", nil)
	if reg.Count() != 0 {
		t.Fatal("nil Describer was registered")
	}
}
