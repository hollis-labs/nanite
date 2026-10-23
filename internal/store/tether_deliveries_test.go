package store

import (
	"context"
	"testing"
	"time"
)

func TestTetherDeliveryLedger_IdempotentByMessageID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	d := TetherDelivery{MessageID: "m1", ActorURN: "msg://agent/nanite/agt_x", EnvelopeJSON: `{"id":"m1"}`}

	first, created, err := s.RecordTetherDelivery(ctx, d)
	if err != nil || !created || first.State != TetherDeliveryReceived {
		t.Fatalf("first record: %+v created=%v err=%v", first, created, err)
	}
	ok, err := s.AdvanceTetherDelivery(ctx, "m1", TetherDeliverySubmitted, "local-1", "")
	if err != nil || !ok {
		t.Fatalf("advance: ok=%v err=%v", ok, err)
	}
	again, created, err := s.RecordTetherDelivery(ctx, d)
	if err != nil || created {
		t.Fatalf("second record must not create: created=%v err=%v", created, err)
	}
	if again.State != TetherDeliverySubmitted || again.LocalMessageID != "local-1" {
		t.Fatalf("second record clobbered the row: %+v", again)
	}
}

func TestTetherDeliveryLedger_NeverMovesBackward(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, _, _ = s.RecordTetherDelivery(ctx, TetherDelivery{MessageID: "m", ActorURN: "a", EnvelopeJSON: "{}"})
	_, _ = s.AdvanceTetherDelivery(ctx, "m", TetherDeliverySubmitted, "local", "")
	if ok, _ := s.AdvanceTetherDelivery(ctx, "m", TetherDeliveryAccepted, "", ""); ok {
		t.Fatal("a submitted delivery moved back to accepted")
	}
	got, _ := s.GetTetherDelivery(ctx, "m")
	if got.State != TetherDeliverySubmitted || got.LocalMessageID != "local" {
		t.Fatalf("row = %+v", got)
	}
}

func TestTetherDeliveryLedger_TerminalClearsBodyAndFreezes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, _, _ = s.RecordTetherDelivery(ctx, TetherDelivery{MessageID: "m", ActorURN: "a", EnvelopeJSON: `{"secret":"body"}`})
	if ok, err := s.AdvanceTetherDelivery(ctx, "m", TetherDeliveryConsumed, "", ""); err != nil || !ok {
		t.Fatalf("consume: %v %v", ok, err)
	}
	got, _ := s.GetTetherDelivery(ctx, "m")
	if got.EnvelopeJSON != "" {
		t.Fatalf("finished delivery kept its body: %q", got.EnvelopeJSON)
	}
	if ok, _ := s.AdvanceTetherDelivery(ctx, "m", TetherDeliveryReceived, "", ""); ok {
		t.Fatal("a terminal delivery moved again")
	}
	if n, _ := s.DeferTetherDelivery(ctx, "m", time.Now(), "x", true); n != 0 {
		t.Fatal("a terminal delivery was deferred")
	}
}

// A deferred row must not starve due ones, and a stuck row must not occupy
// the list once it is deferred.
func TestTetherDeliveryLedger_DeferredRowsAreHiddenUntilDue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, id := range []string{"old-stuck", "fresh"} {
		_, _, _ = s.RecordTetherDelivery(ctx, TetherDelivery{MessageID: id, ActorURN: "a", EnvelopeJSON: "{}"})
	}
	n, err := s.DeferTetherDelivery(ctx, "old-stuck", time.Now().Add(time.Hour), "claim failed", true)
	if err != nil || n != 1 {
		t.Fatalf("defer attempts=%d err=%v", n, err)
	}
	open, err := s.ListOpenTetherDeliveries(ctx, 1)
	if err != nil || len(open) != 1 || open[0].MessageID != "fresh" {
		t.Fatalf("open = %+v err=%v, want only the due row", open, err)
	}
	if n, _ := s.DeferTetherDelivery(ctx, "old-stuck", time.Now().Add(time.Hour), "busy", false); n != 1 {
		t.Fatalf("a non-failure deferral changed the attempt count: %d", n)
	}
	_, _ = s.DeferTetherDelivery(ctx, "old-stuck", time.Now().Add(-time.Second), "due", false)
	if open, _ := s.ListOpenTetherDeliveries(ctx, 10); len(open) != 2 {
		t.Fatalf("due row not listed: %d", len(open))
	}
}
