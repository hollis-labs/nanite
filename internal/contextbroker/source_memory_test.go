package contextbroker

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/memory"
	"github.com/hollis-labs/tesseract"
)

// newTestMemoryService spins up a real embedded Tesseract instance backed by a
// temp directory. Mirrors the fixture in internal/learnings/integration_test.go
// so MemorySource's tests exercise the same backend as production.
func newTestMemoryService(t *testing.T) *memory.Service {
	t.Helper()
	svc, _ := newTestMemoryServiceAndRoot(t)
	return svc
}

func newTestMemoryServiceAndRoot(t *testing.T) (*memory.Service, *tesseract.Tesseract) {
	t.Helper()
	dir := t.TempDir()
	c, err := tesseract.Open(context.Background(), tesseract.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("tesseract.Open: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return memory.NewService(c.MemoryStore()), c
}

// seedTestMemory writes a single memory record so Recall has something to
// match. Returns the memory key so tests can reference it.
func seedTestMemory(t *testing.T, svc *memory.Service, namespace, summary, body string, confidence float64) {
	t.Helper()
	err := svc.Store(context.Background(), memory.Memory{
		Namespace:  namespace,
		MemoryKey:  "auto_recall_test_seed",
		Summary:    summary,
		Body:       body,
		Origin:     "observation",
		Trigger:    "manual",
		Confidence: confidence,
		SessionID:  "test-session",
		Status:     "canonical",
	})
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
}

func TestMemorySource_Skip_WhenAutoRecallExplicitlyFalse(t *testing.T) {
	svc := newTestMemoryService(t)
	seedTestMemory(t, svc, memory.UserNamespace("default"),
		"any summary", "body about widgets", 0.9)

	src := NewMemorySource(svc)
	disabled := false
	intent := Intent{
		QueryText:  "widgets",
		SessionID:  "test-session",
		AgentID:    "agent-x",
		AutoRecall: &disabled,
	}

	items, err := src.Fetch(context.Background(), intent, 5000)
	if err != nil {
		t.Fatalf("Fetch with AutoRecall=false should not error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("Fetch with AutoRecall=false should return zero items, got %d", len(items))
	}
}

func TestMemorySource_Default_WhenAutoRecallNil(t *testing.T) {
	svc := newTestMemoryService(t)
	seedTestMemory(t, svc, memory.UserNamespace("default"),
		"widgets-doc", "body about widgets", 0.9)

	src := NewMemorySource(svc)
	intent := Intent{
		QueryText: "widgets",
		SessionID: "test-session",
		AgentID:   "agent-x",
		// AutoRecall nil — source should run as before.
	}

	items, err := src.Fetch(context.Background(), intent, 5000)
	if err != nil {
		t.Fatalf("Fetch nil AutoRecall: %v", err)
	}
	if len(items) == 0 {
		t.Fatalf("expected at least one recall hit; got 0")
	}
}

func TestMemorySource_HonorsLimitOverride(t *testing.T) {
	svc := newTestMemoryService(t)
	ns := memory.UserNamespace("default")
	for i := 0; i < 5; i++ {
		err := svc.Store(context.Background(), memory.Memory{
			Namespace:  ns,
			MemoryKey:  "auto_recall_limit_test_" + string(rune('a'+i)),
			Summary:    "widget topic " + string(rune('a'+i)),
			Body:       "body about widgets",
			Origin:     "observation",
			Trigger:    "manual",
			Confidence: 0.9,
			SessionID:  "test-session",
			Status:     "canonical",
		})
		if err != nil {
			t.Fatalf("Store seed %d: %v", i, err)
		}
	}

	src := NewMemorySource(svc)
	enabled := true
	intent := Intent{
		QueryText:       "widgets",
		SessionID:       "test-session",
		AgentID:         "agent-x",
		AutoRecall:      &enabled,
		AutoRecallLimit: 2,
	}

	items, err := src.Fetch(context.Background(), intent, 50000)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(items) > 2 {
		t.Fatalf("AutoRecallLimit=2 honored? got %d items", len(items))
	}
}

func TestMemorySource_TimeoutShortCircuits(t *testing.T) {
	svc := newTestMemoryService(t)
	seedTestMemory(t, svc, memory.UserNamespace("default"),
		"widgets-doc", "body about widgets", 0.9)

	src := NewMemorySource(svc)
	enabled := true
	intent := Intent{
		QueryText:         "widgets",
		SessionID:         "test-session",
		AgentID:           "agent-x",
		AutoRecall:        &enabled,
		AutoRecallTimeout: 1 * time.Nanosecond,
	}

	// 1ns timeout virtually guarantees the recall context expires before
	// Tesseract returns. Fetch should swallow the timeout and return empty.
	items, err := src.Fetch(context.Background(), intent, 5000)
	if err != nil {
		t.Fatalf("timeout should be silent (nil error), got: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("timeout should yield zero items, got %d", len(items))
	}
}

func TestMemorySource_MinConfidenceZeroIsHonored(t *testing.T) {
	// AutoRecallMinConfidence=0 must override the source default (0.4) so a
	// low-confidence memory still surfaces. Pre-fix, the > 0 guard treated
	// the literal zero as "use the default" and the seed below (confidence
	// 0.1) was filtered out.
	svc := newTestMemoryService(t)
	seedTestMemory(t, svc, memory.UserNamespace("default"),
		"low-confidence widget", "body about widgets", 0.1)

	src := NewMemorySource(svc)
	enabled := true
	intent := Intent{
		QueryText:               "widgets",
		SessionID:               "test-session",
		AgentID:                 "agent-x",
		AutoRecall:              &enabled,
		AutoRecallMinConfidence: 0,
	}

	items, err := src.Fetch(context.Background(), intent, 5000)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(items) == 0 {
		t.Fatalf("min_confidence=0 should return the low-confidence seed; got 0 items")
	}
}

func TestMemorySourceTouchesOnlyReturnedResults(t *testing.T) {
	svc, root := newTestMemoryServiceAndRoot(t)
	namespace := memory.UserNamespace("default")
	for _, key := range []string{"selected", "not_returned"} {
		if err := svc.Store(context.Background(), memory.Memory{
			Namespace: namespace, MemoryKey: key, Summary: "widget " + key,
			Body: "widget details", Origin: "observation", Trigger: "manual",
			Confidence: 0.9, SessionID: "test-session", Status: "canonical",
		}); err != nil {
			t.Fatalf("store %s: %v", key, err)
		}
	}

	enabled := true
	items, err := NewMemorySource(svc).Fetch(context.Background(), Intent{
		QueryText: "widget", AutoRecall: &enabled, AutoRecallLimit: 1,
	}, 5000)
	if err != nil || len(items) != 1 {
		t.Fatalf("Fetch items=%+v err=%v", items, err)
	}
	var totalAccesses, touchedRows int
	if err := root.MemoryStore().DB().QueryRowContext(context.Background(), `
		SELECT COALESCE(SUM(access_count), 0),
		       COALESCE(SUM(CASE WHEN access_count > 0 THEN 1 ELSE 0 END), 0)
		FROM memory_state WHERE namespace = ?`, namespace).Scan(&totalAccesses, &touchedRows); err != nil {
		t.Fatalf("read activation state: %v", err)
	}
	if totalAccesses != 1 || touchedRows != 1 {
		t.Fatalf("accesses total=%d touched_rows=%d, want 1/1", totalAccesses, touchedRows)
	}
}

func TestMemorySource_NilService_ReturnsError(t *testing.T) {
	// Sanity-check the long-standing precondition: a nil memory service
	// surfaces an explicit error rather than silently no-op'ing. This lets
	// container.go skip registering the source when memorySvc is nil.
	src := NewMemorySource(nil)
	_, err := src.Fetch(context.Background(), Intent{}, 5000)
	if err == nil {
		t.Fatal("expected error for nil memory.Service")
	}
}
