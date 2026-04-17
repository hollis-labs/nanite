package messaging

import (
	"context"
	"testing"
)

func TestCallerIdentity_IsZero(t *testing.T) {
	tests := []struct {
		name string
		id   CallerIdentity
		want bool
	}{
		{"both empty", CallerIdentity{}, true},
		{"session only", CallerIdentity{SessionID: "s"}, false},
		{"agent only", CallerIdentity{AgentID: "a"}, false},
		{"both set", CallerIdentity{SessionID: "s", AgentID: "a"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.id.IsZero(); got != tc.want {
				t.Fatalf("IsZero=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestWithCaller_RoundTrip(t *testing.T) {
	ctx := context.Background()
	id := CallerIdentity{SessionID: "sess-1", AgentID: "file-backend"}

	ctx = WithCaller(ctx, id)
	got, ok := CallerFromCtx(ctx)
	if !ok {
		t.Fatal("CallerFromCtx missed round-trip identity")
	}
	if got != id {
		t.Fatalf("got %+v, want %+v", got, id)
	}
}

func TestWithCaller_ZeroIsNoOp(t *testing.T) {
	ctx := context.Background()
	ctx = WithCaller(ctx, CallerIdentity{})
	if _, ok := CallerFromCtx(ctx); ok {
		t.Fatal("zero CallerIdentity must NOT be stamped on ctx")
	}
}

func TestCallerFromCtx_NilCtx(t *testing.T) {
	if _, ok := CallerFromCtx(nil); ok {
		t.Fatal("nil ctx must report caller-absent")
	}
}

func TestCallerFromCtx_AbsentReturnsFalse(t *testing.T) {
	ctx := context.Background()
	if _, ok := CallerFromCtx(ctx); ok {
		t.Fatal("bare ctx must report caller-absent")
	}
}

func TestCallerFromCtx_OverridesParent(t *testing.T) {
	ctx := WithCaller(context.Background(), CallerIdentity{SessionID: "outer", AgentID: "a"})
	ctx = WithCaller(ctx, CallerIdentity{SessionID: "inner", AgentID: "b"})

	got, ok := CallerFromCtx(ctx)
	if !ok {
		t.Fatal("expected override identity")
	}
	if got.SessionID != "inner" || got.AgentID != "b" {
		t.Fatalf("expected inner override, got %+v", got)
	}
}
