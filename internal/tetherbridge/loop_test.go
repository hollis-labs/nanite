package tetherbridge

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	messaging "github.com/hollis-labs/libs/mesh/core/messaging"
	"github.com/hollis-labs/libs/mesh/core/messaging/delivery"

	"github.com/hollis-labs/nanite/internal/store"
)

type fakeClaimer struct {
	mu       sync.Mutex
	stages   []delivery.ReceiptStage
	nacks    []NackOptions
	claims   int
	claimErr error
}

func (f *fakeClaimer) ClaimMessage(_ context.Context, id string, _ messaging.Address, _ ClaimOptions) (messaging.Envelope, delivery.LeaseRef, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims++
	if f.claimErr != nil {
		return messaging.Envelope{}, delivery.LeaseRef{}, 0, f.claimErr
	}
	return messaging.Envelope{ID: id}, delivery.LeaseRef{DeliveryID: "d", LeaseToken: "t"}, 120, nil
}

func (f *fakeClaimer) AckMessage(_ context.Context, _ string, _ messaging.Address, _ delivery.LeaseRef, st delivery.ReceiptStage) (delivery.RecipientDelivery, delivery.Attempt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stages = append(f.stages, st)
	return delivery.RecipientDelivery{}, delivery.Attempt{}, nil
}

func (f *fakeClaimer) NackMessage(_ context.Context, _ string, _ messaging.Address, _ delivery.LeaseRef, o NackOptions) (delivery.RecipientDelivery, delivery.Attempt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nacks = append(f.nacks, o)
	rd := delivery.RecipientDelivery{}
	if !o.Retryable {
		rd.Status = delivery.DeliveryDeadLettered
	}
	return rd, delivery.Attempt{}, nil
}

// memLedger mirrors the store's semantics: forward-only, terminal rows frozen,
// deferred rows hidden from the open list until due. failAdvance makes the
// next N Advance calls to a state fail, to model a ledger write that dies after
// the mailbox write.
type memLedger struct {
	mu          sync.Mutex
	now         func() time.Time
	rows        map[string]*store.TetherDelivery
	failAdvance map[string]int
	failRecord  int
}

func newMemLedger(now func() time.Time) *memLedger {
	return &memLedger{now: now, rows: map[string]*store.TetherDelivery{}, failAdvance: map[string]int{}}
}

func (m *memLedger) RecordTetherDelivery(_ context.Context, d store.TetherDelivery) (*store.TetherDelivery, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failRecord > 0 {
		m.failRecord--
		return nil, false, errors.New("db busy")
	}
	if r, ok := m.rows[d.MessageID]; ok {
		c := *r
		return &c, false, nil
	}
	d.State = store.TetherDeliveryReceived
	m.rows[d.MessageID] = &d
	c := d
	return &c, true, nil
}

var stateRank = map[string]int{store.TetherDeliveryReceived: 0, store.TetherDeliveryAccepted: 1, store.TetherDeliverySubmitted: 2}

func (m *memLedger) AdvanceTetherDelivery(_ context.Context, id, state, local, lastErr string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failAdvance[state] > 0 {
		m.failAdvance[state]--
		return false, errors.New("ledger write failed")
	}
	r := m.rows[id]
	if r == nil || r.Terminal() {
		return false, nil
	}
	if want, ok := stateRank[state]; ok && stateRank[r.State] > want {
		return false, nil
	}
	r.State, r.LastError, r.NextAttemptAt = state, lastErr, time.Time{}
	if local != "" {
		r.LocalMessageID = local
	}
	if r.Terminal() {
		r.EnvelopeJSON = ""
	}
	return true, nil
}

func (m *memLedger) DeferTetherDelivery(_ context.Context, id string, at time.Time, why string, bump bool) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rows[id]
	if r == nil || r.Terminal() {
		return 0, nil
	}
	r.NextAttemptAt, r.LastError = at, why
	if bump {
		r.Attempts++
	}
	return r.Attempts, nil
}

func (m *memLedger) ListOpenTetherDeliveries(context.Context, int) ([]store.TetherDelivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.TetherDelivery
	for _, r := range m.rows {
		if !r.Terminal() && !r.NextAttemptAt.After(m.now()) {
			out = append(out, *r)
		}
	}
	return out, nil
}

// fakeInjector is idempotent on the envelope id, as the Injector contract
// requires, and counts how many distinct mailbox rows it has written.
type fakeInjector struct {
	mu    sync.Mutex
	busy  bool
	err   error
	rows  map[string]string
	calls int
}

func (f *fakeInjector) Busy(string) bool { f.mu.Lock(); defer f.mu.Unlock(); return f.busy }
func (f *fakeInjector) Inject(_ context.Context, _ ActorRoute, env messaging.Envelope) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	if f.rows == nil {
		f.rows = map[string]string{}
	}
	if id, ok := f.rows[env.ID]; ok {
		return id, nil
	}
	id := "local-" + env.ID
	f.rows[env.ID] = id
	return id, nil
}
func (f *fakeInjector) written() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.rows) }

const testActor = "msg://agent/nanite/agt_testactor"

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func newTestLoop(bound bool) (*Loop, *fakeClaimer, *memLedger, *fakeInjector, *clock) {
	sess := ""
	if bound {
		sess = "sess-1"
	}
	clk := &clock{t: time.Now()}
	c, led, inj := &fakeClaimer{}, newMemLedger(clk.Now), &fakeInjector{}
	return &Loop{
		Claimer: c, Ledger: led, Injector: inj, now: clk.Now,
		Store: &fakeStore{inst: &store.DurableAgentInstance{ID: "i", ProfileID: "p", CurrentSessionID: sess}, slotCount: 1},
	}, c, led, inj, clk
}

func testEnv(id string) messaging.Envelope {
	return messaging.Envelope{ID: id, To: testRecipient, Kind: messaging.MsgKindRequest}
}

func TestHandle_DeliversOnceWithAllReceipts(t *testing.T) {
	l, c, led, inj, _ := newTestLoop(true)
	ctx := context.Background()
	if err := l.Handle(ctx, testActor, testEnv("m1")); err != nil {
		t.Fatal(err)
	}
	want := []delivery.ReceiptStage{delivery.StageHostAccepted, delivery.StageTurnSubmitted, delivery.StageConsumed}
	if len(c.stages) != 3 || c.stages[0] != want[0] || c.stages[1] != want[1] || c.stages[2] != want[2] {
		t.Fatalf("stages = %v, want %v", c.stages, want)
	}
	if inj.written() != 1 || led.rows["m1"].State != store.TetherDeliveryConsumed {
		t.Fatalf("written=%d state=%s", inj.written(), led.rows["m1"].State)
	}
	if led.rows["m1"].EnvelopeJSON != "" {
		t.Fatal("a finished delivery should not keep the message body")
	}
	if err := l.Handle(ctx, testActor, testEnv("m1")); err != nil {
		t.Fatal(err)
	}
	if inj.calls != 1 || c.claims != 1 {
		t.Fatalf("duplicate re-delivered: inject calls=%d claims=%d", inj.calls, c.claims)
	}
}

// The ledger write after a successful mailbox write fails once. The next
// attempt must not produce a second mailbox row.
func TestHandle_LedgerFailureAfterInjectDoesNotDoubleDeliver(t *testing.T) {
	l, c, led, inj, clk := newTestLoop(true)
	led.failAdvance[store.TetherDeliverySubmitted] = 1
	ctx := context.Background()
	if err := l.Handle(ctx, testActor, testEnv("m")); err == nil {
		t.Fatal("expected the ledger failure to surface")
	}
	if inj.written() != 1 {
		t.Fatalf("written=%d after first attempt", inj.written())
	}
	clk.Advance(time.Hour)
	if err := l.Handle(ctx, testActor, testEnv("m")); err != nil {
		t.Fatal(err)
	}
	if inj.written() != 1 {
		t.Fatalf("double delivery: %d mailbox rows", inj.written())
	}
	if led.rows["m"].State != store.TetherDeliveryConsumed || c.stages[len(c.stages)-1] != delivery.StageConsumed {
		t.Fatalf("row=%+v stages=%v", led.rows["m"], c.stages)
	}
}

func TestHandle_ResumeAfterCrashDoesNotReinject(t *testing.T) {
	l, c, led, inj, _ := newTestLoop(true)
	led.rows["m2"] = &store.TetherDelivery{MessageID: "m2", ActorURN: testActor, State: store.TetherDeliverySubmitted, LocalMessageID: "local-0"}
	if err := l.Handle(context.Background(), testActor, testEnv("m2")); err != nil {
		t.Fatal(err)
	}
	if inj.calls != 0 {
		t.Fatalf("re-injected after crash: %d", inj.calls)
	}
	if got := c.stages[len(c.stages)-1]; got != delivery.StageConsumed {
		t.Fatalf("last stage = %s", got)
	}
	for _, st := range c.stages {
		if st == delivery.StageTurnSubmitted && led.rows["m2"].State != store.TetherDeliveryConsumed {
			t.Fatal("state regressed")
		}
	}
}

func TestHandle_BusyDefersAndNeverDeadLetters(t *testing.T) {
	l, c, led, inj, clk := newTestLoop(true)
	l.Config.MaxAttempts = 2
	inj.busy = true
	ctx := context.Background()
	for i := 0; i < 30; i++ { // far past any failure budget
		if err := l.Handle(ctx, testActor, testEnv("m3")); err != nil {
			t.Fatal(err)
		}
		clk.Advance(l.Config.RetryDelay + time.Second)
	}
	row := led.rows["m3"]
	if row.State == store.TetherDeliveryFailed || row.Attempts != 0 {
		t.Fatalf("busy must not consume attempts or dead-letter: %+v", row)
	}
	for _, n := range c.nacks {
		if !n.Retryable || n.NextAttemptSeconds < int(l.Config.RetryDelay/time.Second) {
			t.Fatalf("busy nack = %+v", n)
		}
	}
	if inj.written() != 0 {
		t.Fatal("injected into a busy session")
	}
	inj.busy = false
	if err := l.Handle(ctx, testActor, testEnv("m3")); err != nil {
		t.Fatal(err)
	}
	if inj.written() != 1 || led.rows["m3"].State != store.TetherDeliveryConsumed {
		t.Fatalf("after idle: written=%d state=%s", inj.written(), led.rows["m3"].State)
	}
}

func TestHandle_DeferredRowIsNotRetriedEarly(t *testing.T) {
	l, c, _, inj, clk := newTestLoop(true)
	inj.busy = true
	ctx := context.Background()
	_ = l.Handle(ctx, testActor, testEnv("m"))
	claims := c.claims
	_ = l.Handle(ctx, testActor, testEnv("m")) // Tether redelivers immediately
	if c.claims != claims {
		t.Fatal("a deferred row was claimed before it was due")
	}
	clk.Advance(time.Minute)
	inj.busy = false
	_ = l.Handle(ctx, testActor, testEnv("m"))
	if inj.written() != 1 {
		t.Fatal("not delivered once due")
	}
}

func TestHandle_UnclaimableRowsAgeOut(t *testing.T) {
	l, c, led, _, clk := newTestLoop(true)
	l.Config.MaxAttempts = 2 // closes after 8 counted failures
	c.claimErr = errors.New("lease held elsewhere")
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		_ = l.Handle(ctx, testActor, testEnv("stuck"))
		clk.Advance(time.Hour)
	}
	row := led.rows["stuck"]
	if row.State != store.TetherDeliveryFailed || row.Attempts == 0 {
		t.Fatalf("stuck row not closed: %+v", row)
	}
	open, _ := led.ListOpenTetherDeliveries(ctx, 10)
	if len(open) != 0 {
		t.Fatalf("closed row still open: %d", len(open))
	}
}

// The inbox read has already marked the message delivered in Tether. If the
// route cannot be resolved the message must stay in the ledger.
func TestHandle_RouteErrorKeepsMessageInLedger(t *testing.T) {
	l, c, led, inj, clk := newTestLoop(true)
	l.Store = &fakeStore{instErr: errors.New("db down")}
	ctx := context.Background()
	if err := l.Handle(ctx, testActor, testEnv("m")); err == nil {
		t.Fatal("expected the resolve error")
	}
	if row := led.rows["m"]; row == nil || row.Terminal() || row.EnvelopeJSON == "" {
		t.Fatalf("message lost on a resolve error: %+v", row)
	}
	l.Store = &fakeStore{inst: &store.DurableAgentInstance{ID: "i", ProfileID: "p", CurrentSessionID: "sess-1"}, slotCount: 1}
	clk.Advance(time.Hour)
	l.Config.defaults()
	l.Mail = &fakeMail{inbox: map[string][]messaging.Envelope{}, stream: make(chan Event)}
	l.Actors = &fakeActors{}
	l.Sweep(ctx) // no live actors: only the ledger resume path runs
	if inj.written() != 1 || c.stages[len(c.stages)-1] != delivery.StageConsumed {
		t.Fatalf("resume from ledger failed: written=%d", inj.written())
	}
}

func TestHandle_RecordFailureIsRetriedThenSurfaced(t *testing.T) {
	l, _, led, inj, _ := newTestLoop(true)
	led.failRecord = 2
	if err := l.Handle(context.Background(), testActor, testEnv("m")); err != nil {
		t.Fatalf("transient ledger error should be retried: %v", err)
	}
	if inj.written() != 1 {
		t.Fatal("not delivered after retry")
	}
	led.failRecord = 100
	if err := l.Handle(context.Background(), testActor, testEnv("n")); err == nil {
		t.Fatal("persistent ledger failure must surface")
	}
}

func TestHandle_UnboundActorStaysOpenAndUntouched(t *testing.T) {
	l, c, led, inj, _ := newTestLoop(false)
	if err := l.Handle(context.Background(), testActor, testEnv("m4")); err != nil {
		t.Fatal(err)
	}
	if c.claims != 0 || inj.calls != 0 {
		t.Fatalf("unbound touched things: claims=%d injects=%d", c.claims, inj.calls)
	}
	if row := led.rows["m4"]; row == nil || row.Terminal() {
		t.Fatalf("unbound delivery must stay open, not be dropped: %+v", row)
	}
}

func TestHandle_PersistentInjectFailureDeadLetters(t *testing.T) {
	l, c, led, inj, clk := newTestLoop(true)
	l.Config.MaxAttempts = 2
	inj.err = errors.New("boom")
	for i := 0; i < 2; i++ {
		if err := l.Handle(context.Background(), testActor, testEnv("m5")); err != nil {
			t.Fatal(err)
		}
		clk.Advance(time.Hour)
	}
	if led.rows["m5"].State != store.TetherDeliveryFailed {
		t.Fatalf("state = %s", led.rows["m5"].State)
	}
	if last := c.nacks[len(c.nacks)-1]; last.Retryable {
		t.Fatal("final nack should dead-letter")
	}
}

func TestHandle_RejectedSenderIsDeadLetteredNotInjected(t *testing.T) {
	l, c, led, inj, _ := newTestLoop(true)
	l.AllowSender = func(messaging.Envelope) error { return ErrSenderRejected }
	if err := l.Handle(context.Background(), testActor, testEnv("m6")); err != nil {
		t.Fatal(err)
	}
	if inj.calls != 0 || led.rows["m6"].State != store.TetherDeliveryFailed {
		t.Fatalf("rejected sender woke the actor: calls=%d state=%s", inj.calls, led.rows["m6"].State)
	}
	if len(c.nacks) != 1 || c.nacks[0].Retryable {
		t.Fatalf("nacks = %+v", c.nacks)
	}
}

// --- Sweep, watch, Run and shutdown ---

type fakeActors struct{ urns []string }

func (f *fakeActors) ListServedActorURNs(context.Context) ([]string, error) { return f.urns, nil }

// fakeMail is the non-destructive MailSource: List never marks anything read.
type fakeMail struct {
	mu      sync.Mutex
	inbox   map[string][]messaging.Envelope
	stream  chan Event
	watches int
	afters  []int64
	listErr error
	read    []string
}

func (f *fakeMail) MarkRead(_ context.Context, _ string, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.read = append(f.read, id)
	return nil
}

func (f *fakeMail) List(_ context.Context, to string, _ bool, _, _ int) ([]messaging.Envelope, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, 0, f.listErr
	}
	out := f.inbox[to]
	return out, len(out), nil
}

func (f *fakeMail) Watch(ctx context.Context, _ string, after int64) (<-chan Event, error) {
	f.mu.Lock()
	f.watches++
	f.afters = append(f.afters, after)
	f.mu.Unlock()
	out := make(chan Event)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-f.stream:
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

type memCursors struct {
	mu sync.Mutex
	m  map[string]int64
}

func (c *memCursors) GetTetherWatchCursor(_ context.Context, urn string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[urn], nil
}

func (c *memCursors) SetTetherWatchCursor(_ context.Context, urn string, seq int64, reset bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]int64{}
	}
	if reset || seq > c.m[urn] {
		c.m[urn] = seq
	}
	return nil
}

func newSweepLoop(t *testing.T) (*Loop, *fakeMail, *memLedger, *fakeInjector) {
	t.Helper()
	l, _, led, inj, _ := newTestLoop(true)
	mail := &fakeMail{inbox: map[string][]messaging.Envelope{}, stream: make(chan Event, 4)}
	l.Mail = mail
	l.Cursors = &memCursors{}
	l.Actors = &fakeActors{urns: []string{testActor}}
	l.Config.ResubscribeWait = time.Millisecond
	return l, mail, led, inj
}

func waitWatching(mail *fakeMail) {
	for i := 0; i < 400; i++ {
		mail.mu.Lock()
		n := mail.watches
		mail.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSweep_CatchUpListsOncePerMessage(t *testing.T) {
	l, mail, led, inj := newSweepLoop(t)
	mail.inbox[testActor] = []messaging.Envelope{testEnv("a"), testEnv("b")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.Sweep(ctx)
	l.Sweep(ctx)
	waitWatching(mail)
	l.Close()
	if inj.written() != 2 || led.rows["a"].State != store.TetherDeliveryConsumed {
		t.Fatalf("written=%d", inj.written())
	}
	if mail.watches != 1 {
		t.Fatalf("watches = %d, want 1 (one per actor, once)", mail.watches)
	}
}

// List is non-destructive, so a failure after reading never loses the message,
// and the ledger resumes it on a later sweep.
func TestSweep_FailureAfterListIsRecoveredFromLedger(t *testing.T) {
	l, mail, led, inj := newSweepLoop(t)
	mail.inbox[testActor] = []messaging.Envelope{testEnv("a")}
	inj.err = errors.New("mailbox down")
	ctx := context.Background()
	l.Sweep(ctx)
	waitWatching(mail)
	l.Close()
	if row := led.rows["a"]; row == nil || row.Terminal() {
		t.Fatalf("message lost: %+v", row)
	}
	inj.err = nil
	led.mu.Lock()
	led.rows["a"].NextAttemptAt = time.Time{}
	led.mu.Unlock()
	mail.inbox[testActor] = nil
	l.Sweep(ctx)
	l.Close()
	if inj.written() != 1 {
		t.Fatalf("recovered written=%d", inj.written())
	}
}

func TestWatch_StreamDeliveryDuplicateAndCursor(t *testing.T) {
	l, mail, led, inj := newSweepLoop(t)
	ctx, cancel := context.WithCancel(context.Background())
	l.Sweep(ctx) // starts the watch
	mail.stream <- Event{Kind: EventMessage, Seq: 7, Env: testEnv("s1")}
	mail.stream <- Event{Kind: EventMessage, Seq: 8, Env: testEnv("s1")} // the same message again
	deadline := time.After(2 * time.Second)
	for {
		led.mu.Lock()
		r := led.rows["s1"]
		done := r != nil && r.State == store.TetherDeliveryConsumed
		led.mu.Unlock()
		cur, _ := l.Cursors.GetTetherWatchCursor(ctx, testActor)
		if done && cur == 8 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("stream delivery or cursor never completed")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	l.Close()
	if inj.written() != 1 || inj.calls != 1 {
		t.Fatalf("written=%d calls=%d", inj.written(), inj.calls)
	}
}

func TestWatch_GapResetsCursorAndReconnects(t *testing.T) {
	l, mail, _, _ := newSweepLoop(t)
	cur := l.Cursors.(*memCursors)
	_ = cur.SetTetherWatchCursor(context.Background(), testActor, 50, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.Sweep(ctx)
	waitWatching(mail)
	mail.stream <- Event{Kind: EventGap}
	deadline := time.After(2 * time.Second)
	for {
		mail.mu.Lock()
		n := mail.watches
		last := int64(-1)
		if n > 0 {
			last = mail.afters[n-1]
		}
		mail.mu.Unlock()
		if n >= 2 {
			if mail.afters[0] != 50 || last != 0 {
				t.Fatalf("afters = %v, want first 50 then 0 after the gap", mail.afters)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("no reconnect after gap")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	l.Close()
}

type fakeWaker struct {
	calls int
	st    *fakeStore
}

func (w *fakeWaker) Wake(_ context.Context, urn string) (string, error) {
	w.calls++
	w.st.bind("sess-woken")
	return "sess-woken", nil
}

func TestRun_StopsAndWaitsForWatches(t *testing.T) {
	l, _, _, _ := newSweepLoop(t)
	l.Config.SweepEvery = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { l.Run(ctx); close(done) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	l.mu.Lock()
	n := len(l.subs)
	l.mu.Unlock()
	if n != 0 {
		t.Fatalf("watches left after shutdown: %d", n)
	}
}

func (f *fakeStore) bind(sessionID string) { f.inst.CurrentSessionID = sessionID }

// Mail for an enrolled actor with no session starts one (the Waker) and is then
// delivered into it, in the same Handle call.
func TestHandle_WakesUnboundActorThenDelivers(t *testing.T) {
	l, _, led, inj, _ := newTestLoop(false)
	w := &fakeWaker{st: l.Store.(*fakeStore)}
	l.Waker = w
	if err := l.Handle(context.Background(), testActor, testEnv("w1")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if w.calls != 1 || inj.written() != 1 || led.rows["w1"].State != store.TetherDeliveryConsumed {
		t.Fatalf("wake calls=%d written=%d state=%s", w.calls, inj.written(), led.rows["w1"].State)
	}
}

// A busy session (Inject returns ErrBusy) is deferred, never counted as a failure.
func TestHandle_InjectBusyDefersWithoutFailing(t *testing.T) {
	l, c, led, inj, _ := newTestLoop(true)
	inj.err = ErrBusy
	if err := l.Handle(context.Background(), testActor, testEnv("b1")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if led.rows["b1"].Terminal() || led.rows["b1"].Attempts != 0 || len(c.nacks) != 1 || !c.nacks[0].Retryable {
		t.Fatalf("busy must defer without an attempt: row=%+v nacks=%+v", led.rows["b1"], c.nacks)
	}
}

// An undeliverable inject (a view that cannot take a wake turn) is dead-lettered with
// its reason and never reported consumed.
func TestHandle_UndeliverableIsDeadLettered(t *testing.T) {
	l, c, led, inj, _ := newTestLoop(true)
	mail := &fakeMail{inbox: map[string][]messaging.Envelope{}}
	l.Mail = mail
	inj.err = fmt.Errorf("%w: cli view", ErrUndeliverable)
	if err := l.Handle(context.Background(), testActor, testEnv("u1")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if led.rows["u1"].State != store.TetherDeliveryFailed || len(c.nacks) != 1 || c.nacks[0].Retryable {
		t.Fatalf("want failed + non-retryable nack: row=%+v nacks=%+v", led.rows["u1"], c.nacks)
	}
	if len(mail.read) != 1 {
		t.Fatalf("a dead-lettered message must be marked read, got %v", mail.read)
	}
}

// A consumed delivery is marked read so it leaves the unread catch-up window.
func TestHandle_ConsumedIsMarkedRead(t *testing.T) {
	l, _, _, _, _ := newTestLoop(true)
	mail := &fakeMail{inbox: map[string][]messaging.Envelope{}}
	l.Mail = mail
	if err := l.Handle(context.Background(), testActor, testEnv("m1")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(mail.read) != 1 || mail.read[0] != "m1" {
		t.Fatalf("mark read = %v", mail.read)
	}
}

// Tether answering 404 for an actor stops the watch instead of retrying forever.
func TestCatchUp_UnknownActorIsDropped(t *testing.T) {
	l, mail, _, _ := newSweepLoop(t)
	mail.listErr = fmt.Errorf("%w: 404", ErrActorUnknownToTether)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.Sweep(ctx)
	for i := 0; i < 200 && !l.isUnknown(testActor); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if !l.isUnknown(testActor) {
		t.Fatal("actor not marked unknown after a 404")
	}
	before := mail.watches
	l.Sweep(ctx) // inside the quiet period: no new watch
	time.Sleep(30 * time.Millisecond)
	if mail.watches != before {
		t.Fatalf("watch restarted inside the 404 quiet period: %d -> %d", before, mail.watches)
	}
	l.Close()
}

func TestWatchBackoffGrowsAndCaps(t *testing.T) {
	l := &Loop{Config: LoopConfig{ResubscribeWait: 5 * time.Second}}
	if d := l.watchBackoff(1); d != 5*time.Second {
		t.Fatalf("first = %v", d)
	}
	if d := l.watchBackoff(3); d != 20*time.Second {
		t.Fatalf("third = %v", d)
	}
	if d := l.watchBackoff(50); d != 5*time.Minute {
		t.Fatalf("cap = %v", d)
	}
}
