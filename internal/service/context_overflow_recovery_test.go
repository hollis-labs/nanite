package service

import (
	"context"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestRecoverFromContextOverflow_DisabledFlag covers the plan §T9 requirement
// that UserSettings.ContextOverflowRecovery=false produces a no-op — callers
// see ok=false and fall through to the normal error path.
func TestRecoverFromContextOverflow_DisabledFlag(t *testing.T) {
	s, err := store.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	if err := s.Seed(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.UpdateUserSettings(&store.UserSettings{ContextOverflowRecovery: false}); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}

	svc := &chatServiceImpl{store: s}

	// A minimal result with a window so the helper has something to reach for
	// before short-circuiting on the flag.
	cw := ctxpkg.NewContextWindow(200000, ctxpkg.DefaultEstimator{})
	result := &SlotAssemblyResult{Window: cw}

	ch := make(chan struct{}, 0) // unused
	_ = ch

	_, _, ok := svc.recoverFromContextOverflow(
		context.Background(),
		"sess-1",
		result,
		&store.AgentProfile{ID: "a1"},
		[]provider.ChatMessage{{Role: "user", Content: "hi"}},
		nil,
		nil, // nil stream channel — helper must tolerate
		"prompt is too long", compactTriggerContextOverflow,
	)
	if ok {
		t.Fatalf("recovery should be a no-op when flag is disabled; got ok=true")
	}
}

// TestRecoverFromContextOverflow_NoSummarizerSkips covers the D9/T9 nil-summarizer
// fallback. If the provider registry has no summarizer, we can't compact, so
// recovery reports unsuccessful and the caller surfaces the original error.
func TestRecoverFromContextOverflow_NoSummarizerSkips(t *testing.T) {
	s, err := store.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	if err := s.Seed(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Flag enabled, but no provider.Registry is wired → buildSummarizer returns nil.
	if err := s.UpdateUserSettings(&store.UserSettings{ContextOverflowRecovery: true}); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}

	svc := &chatServiceImpl{store: s} // providers left nil

	cw := ctxpkg.NewContextWindow(200000, ctxpkg.DefaultEstimator{})
	result := &SlotAssemblyResult{Window: cw}

	_, _, ok := svc.recoverFromContextOverflow(
		context.Background(),
		"sess-1",
		result,
		&store.AgentProfile{ID: "a1"},
		[]provider.ChatMessage{{Role: "user", Content: "hi"}},
		nil,
		nil,
		"prompt is too long", compactTriggerContextOverflow,
	)
	if ok {
		t.Fatalf("recovery should be skipped when no summarizer is available; got ok=true")
	}
}

// TestRecoverFromContextOverflow_NilResult covers defensive nil-safety.
func TestRecoverFromContextOverflow_NilResult(t *testing.T) {
	svc := &chatServiceImpl{}
	_, _, ok := svc.recoverFromContextOverflow(
		context.Background(),
		"s",
		nil,
		&store.AgentProfile{},
		nil, nil, nil, "prompt is too long", compactTriggerContextOverflow,
	)
	if ok {
		t.Fatal("nil result should not succeed")
	}
}
