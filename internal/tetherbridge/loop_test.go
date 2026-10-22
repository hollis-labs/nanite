package tetherbridge

import (
	"context"
	"errors"
	"testing"

	messaging "github.com/hollis-labs/libs/mesh/core/messaging"
	"github.com/hollis-labs/libs/mesh/core/messaging/delivery"

	"github.com/hollis-labs/nanite/internal/store"
)

type fakeClaimer struct {
	stages []delivery.ReceiptStage
	nacks  []NackOptions
	// deadLetterNacks makes a non-retryable nack report dead-lettered, as the
	// real daemon does.
	claims int
}

func (f *fakeClaimer) ClaimMessage(_ context.Context, id string, _ messaging.Address, _ ClaimOptions) (messaging.Envelope, delivery.LeaseRef, int, error) {
	f.claims++
	return messaging.Envelope{ID: id}, delivery.LeaseRef{DeliveryID: "d", LeaseToken: "t"}, 120, nil
}

func (f *fakeClaimer) AckMessage(_ context.Context, _ string, _ messaging.Address, _ delivery.LeaseRef, st delivery.ReceiptStage) (delivery.RecipientDelivery, delivery.Attempt, error) {
	f.stages = append(f.stages, st)
	return delivery.RecipientDelivery{}, delivery.Attempt{}, nil
}

func (f *fakeClaimer) NackMessage(_ context.Context, _ string, _ messaging.Address, _ delivery.LeaseRef, o NackOptions) (delivery.RecipientDelivery, delivery.Attempt, error) {
	f.nacks = append(f.nacks, o)
	rd := delivery.RecipientDelivery{}
	if !o.Retryable {
		rd.Status = delivery.DeliveryDeadLettered
	}
	return rd, delivery.Attempt{}, nil
}

type memLedger struct {
	rows map[string]*store.TetherDelivery
}

func newMemLedger() *memLedger { return &memLedger{rows: map[string]*store.TetherDelivery{}} }

func (m *memLedger) RecordTetherDelivery(_ context.Context, d store.TetherDelivery) (*store.TetherDelivery, bool, error) {
	if r, ok := m.rows[d.MessageID]; ok {
		c := *r
		return &c, false, nil
	}
	d.State = store.TetherDeliveryReceived
	m.rows[d.MessageID] = &d
	c := d
	return &c, true, nil
}

func (m *memLedger) AdvanceTetherDelivery(_ context.Context, id, state, local, lastErr string, bump bool) (bool, error) {
	r := m.rows[id]
	if r == nil || r.Terminal() {
		return false, nil
	}
	r.State, r.LastError = state, lastErr
	if local != "" {
		r.LocalMessageID = local
	}
	if bump {
		r.Attempts++
	}
	return true, nil
}

func (m *memLedger) ListOpenTetherDeliveries(context.Context, int) ([]store.TetherDelivery, error) {
	return nil, nil
}

type fakeInjector struct {
	busy    bool
	err     error
	injects int
}

func (f *fakeInjector) Busy(string) bool { return f.busy }
func (f *fakeInjector) Inject(context.Context, ActorRoute, messaging.Envelope) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.injects++
	return "local-1", nil
}

const testActor = "msg://agent/nanite/agt_testactor"

func newTestLoop(bound bool) (*Loop, *fakeClaimer, *memLedger, *fakeInjector) {
	sess := ""
	if bound {
		sess = "sess-1"
	}
	c, led, inj := &fakeClaimer{}, newMemLedger(), &fakeInjector{}
	return &Loop{
		Claimer: c, Ledger: led, Injector: inj,
		Store: &fakeStore{inst: &store.DurableAgentInstance{ID: "i", ProfileID: "p", CurrentSessionID: sess}, slotCount: 1},
	}, c, led, inj
}

func testEnv(id string) messaging.Envelope {
	return messaging.Envelope{ID: id, To: testRecipient, Kind: messaging.MsgKindRequest}
}

func TestHandle_DeliversOnceWithAllReceipts(t *testing.T) {
	l, c, led, inj := newTestLoop(true)
	ctx := context.Background()
	if err := l.Handle(ctx, testActor, testEnv("m1")); err != nil {
		t.Fatal(err)
	}
	want := []delivery.ReceiptStage{delivery.StageHostAccepted, delivery.StageTurnSubmitted, delivery.StageConsumed}
	if len(c.stages) != 3 || c.stages[0] != want[0] || c.stages[1] != want[1] || c.stages[2] != want[2] {
		t.Fatalf("stages = %v, want %v", c.stages, want)
	}
	if inj.injects != 1 || led.rows["m1"].State != store.TetherDeliveryConsumed {
		t.Fatalf("injects=%d state=%s", inj.injects, led.rows["m1"].State)
	}
	// The same Tether message arriving again (redelivery, subscribe + sweep
	// overlap) must not create a second turn or touch the daemon.
	if err := l.Handle(ctx, testActor, testEnv("m1")); err != nil {
		t.Fatal(err)
	}
	if inj.injects != 1 || c.claims != 1 {
		t.Fatalf("duplicate re-delivered: injects=%d claims=%d", inj.injects, c.claims)
	}
}

func TestHandle_ResumeAfterCrashDoesNotReinject(t *testing.T) {
	l, c, led, inj := newTestLoop(true)
	led.rows["m2"] = &store.TetherDelivery{MessageID: "m2", ActorURN: testActor, State: store.TetherDeliverySubmitted, LocalMessageID: "local-0"}
	if err := l.Handle(context.Background(), testActor, testEnv("m2")); err != nil {
		t.Fatal(err)
	}
	if inj.injects != 0 {
		t.Fatalf("re-injected after crash: %d", inj.injects)
	}
	if got := c.stages[len(c.stages)-1]; got != delivery.StageConsumed {
		t.Fatalf("last stage = %s", got)
	}
}

func TestHandle_BusyNacksRetryableThenDeliversOnce(t *testing.T) {
	l, c, led, inj := newTestLoop(true)
	inj.busy = true
	ctx := context.Background()
	if err := l.Handle(ctx, testActor, testEnv("m3")); err != nil {
		t.Fatal(err)
	}
	if inj.injects != 0 || len(c.nacks) != 1 || !c.nacks[0].Retryable {
		t.Fatalf("busy: injects=%d nacks=%+v", inj.injects, c.nacks)
	}
	if led.rows["m3"].State != store.TetherDeliveryReceived || led.rows["m3"].Attempts != 1 {
		t.Fatalf("row after busy = %+v", led.rows["m3"])
	}
	inj.busy = false
	if err := l.Handle(ctx, testActor, testEnv("m3")); err != nil {
		t.Fatal(err)
	}
	if inj.injects != 1 || led.rows["m3"].State != store.TetherDeliveryConsumed {
		t.Fatalf("after idle: injects=%d state=%s", inj.injects, led.rows["m3"].State)
	}
}

func TestHandle_UnboundActorLeavesMailInTether(t *testing.T) {
	l, c, led, inj := newTestLoop(false)
	if err := l.Handle(context.Background(), testActor, testEnv("m4")); err != nil {
		t.Fatal(err)
	}
	if c.claims != 0 || inj.injects != 0 || len(led.rows) != 0 {
		t.Fatalf("unbound touched things: claims=%d injects=%d rows=%d", c.claims, inj.injects, len(led.rows))
	}
}

func TestHandle_PersistentInjectFailureDeadLetters(t *testing.T) {
	l, c, led, inj := newTestLoop(true)
	l.Config.MaxAttempts = 2
	inj.err = errors.New("boom")
	for i := 0; i < 2; i++ {
		if err := l.Handle(context.Background(), testActor, testEnv("m5")); err != nil {
			t.Fatal(err)
		}
	}
	if led.rows["m5"].State != store.TetherDeliveryFailed {
		t.Fatalf("state = %s", led.rows["m5"].State)
	}
	if last := c.nacks[len(c.nacks)-1]; last.Retryable {
		t.Fatal("final nack should dead-letter")
	}
}
