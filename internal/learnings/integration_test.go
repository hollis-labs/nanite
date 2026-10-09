package learnings

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/memory"
	"github.com/hollis-labs/tesseract"
	tesseractMemory "github.com/hollis-labs/tesseract/memory"
)

// newTesseractMemory spins up a real embedded Tesseract instance backed by
// a temp dir. Mirrors the fixture in internal/memory/service_test.go so
// the integration tests here exercise the same code path production
// uses (memory.Service → tesseract.MemoryStore).
func newTesseractMemory(t *testing.T) *memory.Service {
	t.Helper()
	dir := t.TempDir()
	c, err := tesseract.Open(context.Background(), tesseract.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("tesseract.Open: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return memory.NewService(c.MemoryStore())
}

// TestIntegration_CaptureAndRecall_RoundTrip is the ticket's Tesseract
// integration tested end-to-end" acceptance check. It writes a learning
// via the Recorder, then reads it back via the Recaller — both pointed
// at the same real Tesseract instance.
func TestIntegration_CaptureAndRecall_RoundTrip(t *testing.T) {
	svc := newTesseractMemory(t)
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
	svc := newTesseractMemory(t)
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
	svc := newTesseractMemory(t)
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
	if out.Namespace != "app/nanite/memory/learnings" {
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

func TestIntegration_AppLearningsAreSeparatedByUserWithoutTrustingTags(t *testing.T) {
	svc := newTesseractMemory(t)
	rec := NewRecorder(svc)
	recall := NewRecaller(svc)
	ctx := context.Background()
	for _, user := range []string{"alice", "bob"} {
		_, err := rec.Capture(ctx, CaptureInput{Scope: ScopeToolUse, Subject: "card_show", Hint: "lesson for " + user, UserID: user, Tags: []string{"user:alice", "user:bob"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []string{"alice", "bob"} {
		hints := recall.RecallByToolName(ctx, user, "card_show")
		if len(hints) != 1 || hints[0].Summary != "lesson for "+user {
			t.Fatalf("user %s saw foreign rows: %+v", user, hints)
		}
	}
}

// The raw store seeds a historical fixture; production writers never assert a
// human actor to recreate these retained rows.
func TestIntegration_LegacyUserLearningsRemainReadableWithoutBackfill(t *testing.T) {
	instance, err := tesseract.Open(context.Background(), tesseract.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = instance.Close() }()
	ctx := context.Background()
	_, err = instance.MemoryStore().WriteRevision(ctx, tesseractMemory.WriteInput{Domain: tesseractMemory.DomainMemory, Namespace: memory.UserNamespace("alice", "learnings"), MemoryKey: "historic_card", Actor: "user", Summary: "retained lesson", Confidence: 0.9, Tags: []string{"learning", "tool:card_show"}, Status: tesseractMemory.StatusReviewed, Author: tesseractMemory.Author{AgentID: "fixture", AgentVersion: "1"}, Trigger: tesseractMemory.TriggerExplicit, SessionID: "fixture", DerivedFrom: tesseractMemory.DerivedFromUser})
	if err != nil {
		t.Fatal(err)
	}
	svc := memory.NewService(instance.MemoryStore())
	recall := NewRecaller(svc)
	own := recall.RecallByToolName(ctx, "alice", "card_show")
	if len(own) != 1 || own[0].Summary != "retained lesson" {
		t.Fatalf("legacy lesson missing: %+v", own)
	}
	if foreign := recall.RecallByToolName(ctx, "bob", "card_show"); len(foreign) != 0 {
		t.Fatalf("foreign historical lesson leaked: %+v", foreign)
	}
	if rows, err := svc.RecallAppForUser(ctx, "alice", memory.RecallOpts{Namespaces: []string{memory.AppNamespace("learnings")}}); err != nil || len(rows) != 0 {
		t.Fatalf("read created a backfill: %+v %v", rows, err)
	}
}
