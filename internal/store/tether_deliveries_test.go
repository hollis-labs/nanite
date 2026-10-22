package store

import (
	"context"
	"testing"
)

func TestTetherDeliveryLedger_IdempotentByMessageID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	d := TetherDelivery{MessageID: "m1", ActorURN: "msg://agent/nanite/agt_x", SessionID: "s", ProfileID: "p", EnvelopeJSON: "{}"}

	first, created, err := s.RecordTetherDelivery(ctx, d)
	if err != nil || !created || first.State != TetherDeliveryReceived {
		t.Fatalf("first record: %+v created=%v err=%v", first, created, err)
	}
	ok, err := s.AdvanceTetherDelivery(ctx, "m1", TetherDeliverySubmitted, "local-1", "", false)
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

	open, err := s.ListOpenTetherDeliveries(ctx, 10)
	if err != nil || len(open) != 1 {
		t.Fatalf("open = %d err=%v", len(open), err)
	}
	if _, err := s.AdvanceTetherDelivery(ctx, "m1", TetherDeliveryConsumed, "", "", false); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.AdvanceTetherDelivery(ctx, "m1", TetherDeliveryReceived, "", "", false); ok {
		t.Fatal("a terminal delivery must not move again")
	}
	if open, _ := s.ListOpenTetherDeliveries(ctx, 10); len(open) != 0 {
		t.Fatalf("consumed delivery still open: %d", len(open))
	}
}
