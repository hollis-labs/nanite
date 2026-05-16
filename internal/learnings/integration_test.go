package learnings

import (
	"context"
	"testing"

	conduit "github.com/hollis-labs/tesseract"
	"github.com/hollis-labs/nanite/internal/memory"
)

// newConduitMemory spins up a real embedded Conduit instance backed by
// a temp dir. Mirrors the fixture in internal/memory/service_test.go so
// the integration tests here exercise the same code path production
// uses (memory.Service → conduit.MemoryStore).
func newConduitMemory(t *testing.T) *memory.Service {
	t.Helper()
	dir := t.TempDir()
	c, err := conduit.Open(context.Background(), conduit.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("conduit.Open: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return memory.NewService(c.MemoryStore())
}

// TestIntegration_CaptureAndRecall_RoundTrip is the ticket's "Vanta
// integration tested end-to-end" acceptance check. It writes a learning
// via the Recorder, then reads it back via the Recaller — both pointed
// at the same real Conduit instance.
func TestIntegration_CaptureAndRecall_RoundTrip(t *testing.T) {
	svc := newConduitMemory(t)
	rec := NewRecorder(svc)
	rcl := NewRecaller(svc)

	ctx := context.Background()
	hint := "report-card requires {title, metrics}; sections are not allowed"
	out, err := rec.Capture(ctx, CaptureInput{
		Scope:         ScopeToolUse,
		Subject:       "card_show",
		Hint:          hint,
		SourceEventID: "evt-roundtrip",
		SessionID:     "test-session",
		UserID:        "default",
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if out == nil {
		t.Fatal("Capture returned nil outcome")
	}

	hints := rcl.RecallByToolName(ctx, "default", "card_show")
	if len(hints) == 0 {
		t.Fatal("RecallByToolName returned no hints; expected the just-captured lesson")
	}
	found := false
	for _, h := range hints {
		if h.Summary == hint {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("captured hint missing from recall results: %+v", hints)
	}
}

// TestIntegration_DifferentToolsDoNotBleed verifies the per-tool
// namespace isolation: a learning about card_show must NOT
// surface when recalling for giphy_search.
func TestIntegration_DifferentToolsDoNotBleed(t *testing.T) {
	svc := newConduitMemory(t)
	rec := NewRecorder(svc)
	rcl := NewRecaller(svc)
	ctx := context.Background()

	if _, err := rec.Capture(ctx, CaptureInput{
		Scope:     ScopeToolUse,
		Subject:   "card_show",
		Hint:      "report-card requires metrics",
		SessionID: "s1",
	}); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	hits := rcl.RecallByToolName(ctx, "default", "giphy_search")
	if len(hits) != 0 {
		t.Errorf("namespaces leaked: giphy_search recall returned %d hits, want 0", len(hits))
	}
}

// TestIntegration_AcceptanceFromTicket runs the ticket's headline
// scenario end-to-end: capture a tool_use lesson via the Recorder, then
// assert it lands at the documented namespace and surfaces back via
// RecallByToolName. The ticket cites this exact lesson body, so any
// regression in tag/namespace shape surfaces here loudly.
func TestIntegration_AcceptanceFromTicket(t *testing.T) {
	svc := newConduitMemory(t)
	rec := NewRecorder(svc)
	rcl := NewRecaller(svc)
	ctx := context.Background()

	const lesson = "report-card requires {title, metrics}; sections are not allowed"
	out, err := rec.Capture(ctx, CaptureInput{
		Scope:   ScopeToolUse,
		Subject: "card_show",
		Hint:    lesson,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if out.Namespace != "user/default/memory" {
		t.Errorf("unexpected namespace: %s", out.Namespace)
	}
	hints := rcl.RecallByToolName(ctx, "default", "card_show")
	if len(hints) == 0 {
		t.Fatal("recall surfaced zero hints; expected the just-captured lesson")
	}
	if hints[0].Summary != lesson {
		t.Errorf("recalled summary = %q, want %q", hints[0].Summary, lesson)
	}
}
