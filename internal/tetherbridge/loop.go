package tetherbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	messaging "github.com/hollis-labs/libs/mesh/core/messaging"

	"github.com/hollis-labs/nanite/internal/store"
)

// The delivery loop (CW-20261022-0002) is the live caller of the
// claim/accept/submit/consume cycle in accept.go. It turns Tether mail for a
// bound durable actor into one Nanite mailbox row, which Nanite's existing
// wake reactor (internal/service/messaging_reactor.go) turns into a turn on
// an idle session. It adds no second injector.
//
// Idempotency is keyed on the Tether message id in the tether_deliveries
// ledger, and it holds without a cross-store transaction because the injector
// is idempotent on that id: Inject finds an existing mailbox row for the
// Tether message before it writes a new one. A crash or ledger failure between
// the mailbox write and the ledger write therefore re-adopts the same row on
// the next attempt instead of injecting twice.
//
// Nothing is lost on an ordinary error return. The ledger row is written
// before the route is resolved, so a message the inbox read has already marked
// delivered in Tether stays in the ledger. A failure defers the row (with
// backoff) rather than dropping it, and a deferred row sorts behind newer work.
//
//	seen (received) -> claimed+accepted -> injected (local id recorded)
//	  -> submitted receipt -> consumed receipt
//
// "Consumed" here means the message reached the agent's mailbox and its wake
// was handed to the harness; like the architecture says of every receipt, it
// does not claim the model acted on it.

// ErrBusy is returned by an Injector that declines because the session has a
// turn in flight. The delivery is nacked retryable; nothing is injected.
var ErrBusy = errors.New("tetherbridge: session busy")

// ErrSenderRejected marks an envelope the sender policy refused. The delivery
// is dead-lettered, not retried.
var ErrSenderRejected = errors.New("tetherbridge: sender rejected")

// Ledger is the idempotency store (*store.Store implements it).
type Ledger interface {
	RecordTetherDelivery(ctx context.Context, d store.TetherDelivery) (*store.TetherDelivery, bool, error)
	AdvanceTetherDelivery(ctx context.Context, messageID, state, localMessageID, lastErr string) (bool, error)
	DeferTetherDelivery(ctx context.Context, messageID string, at time.Time, lastErr string, bumpAttempt bool) (int, error)
	ListOpenTetherDeliveries(ctx context.Context, limit int) ([]store.TetherDelivery, error)
}

// Injector hands an accepted delivery to Nanite. Inject MUST be idempotent on
// the envelope id: called again for an envelope it already wrote, it returns
// the existing local message id and writes nothing. Busy reports a turn in
// flight on the session.
type Injector interface {
	Busy(sessionID string) bool
	Inject(ctx context.Context, route ActorRoute, env messaging.Envelope) (localMessageID string, err error)
}

// Actors lists the stable-actor URNs the loop should serve.
type Actors interface {
	ListDurableAgentInstances(ctx context.Context, includeArchived bool) ([]store.DurableAgentInstance, error)
}

// LoopConfig tunes the loop. Zero values take the defaults noted.
type LoopConfig struct {
	LeaseSeconds    int           // requested claim lease; default 120
	RetryDelay      time.Duration // busy/unbound/resolve retry delay; default 15s
	MaxBackoff      time.Duration // ceiling for failure backoff; default 10m
	SweepEvery      time.Duration // inbox sweep and ledger resume period; default 30s
	MaxAttempts     int           // failed attempts before a delivery is closed; default 5
	InboxLimit      int           // per-sweep fetch; default 50
	ResubscribeWait time.Duration // delay before re-opening a dropped stream; default 5s
}

func (c *LoopConfig) defaults() {
	if c.LeaseSeconds <= 0 {
		c.LeaseSeconds = 120
	}
	if c.RetryDelay <= 0 {
		c.RetryDelay = 15 * time.Second
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 10 * time.Minute
	}
	if c.SweepEvery <= 0 {
		c.SweepEvery = 30 * time.Second
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 5
	}
	if c.InboxLimit <= 0 {
		c.InboxLimit = 50
	}
	if c.ResubscribeWait <= 0 {
		c.ResubscribeWait = 5 * time.Second
	}
}

// Loop delivers Tether mail to bound Nanite actors.
type Loop struct {
	Claimer  claimer
	Mail     messaging.Store // Subscribe/Inbox side of the Tether client
	Store    instanceStore
	Actors   Actors
	Ledger   Ledger
	Injector Injector
	// AllowSender, when set, vets an envelope before it can wake a bound
	// actor. A non-nil error dead-letters the delivery.
	AllowSender func(messaging.Envelope) error
	Config      LoopConfig

	now func() time.Time // test seam

	mu       sync.Mutex
	inflight map[string]struct{}
	subs     map[string]context.CancelFunc
	wg       sync.WaitGroup
}

func (l *Loop) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// recordAttempts is how often the first ledger write is retried. The envelope
// has already been marked delivered in Tether by the inbox read, so this write
// is the one place an error loses mail; a short retry covers a busy database.
const recordAttempts = 4

// Handle processes one envelope for actorURN. It is safe to call repeatedly
// and concurrently for the same message id: a duplicate is a no-op once the
// delivery is terminal or deferred, and resumes from the recorded state
// otherwise.
func (l *Loop) Handle(ctx context.Context, actorURN string, env messaging.Envelope) error {
	l.Config.defaults()
	if env.ID == "" {
		return errors.New("tetherbridge: envelope without id")
	}
	if !l.begin(env.ID) {
		return nil
	}
	defer l.end(env.ID)

	raw, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("tetherbridge: marshal envelope %s: %w", env.ID, err)
	}
	// Ledger first: before the route is resolved or anything is claimed.
	var row *store.TetherDelivery
	for i := 0; ; i++ {
		row, _, err = l.Ledger.RecordTetherDelivery(ctx, store.TetherDelivery{
			MessageID: env.ID, ActorURN: actorURN, EnvelopeJSON: string(raw),
		})
		if err == nil {
			break
		}
		if i+1 >= recordAttempts || ctx.Err() != nil {
			return fmt.Errorf("tetherbridge: record %s (message may be lost to this sweep): %w", env.ID, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(i+1) * 100 * time.Millisecond):
		}
	}
	if row.Terminal() {
		slog.Debug("tetherbridge: duplicate of finished delivery, ignored", "message_id", env.ID, "state", row.State)
		return nil
	}
	if row.NextAttemptAt.After(l.clock()) {
		return nil // deferred; the sweep resumes it when due
	}

	route, err := ResolveActorRoute(ctx, l.Store, actorURN)
	if err != nil {
		l.postpone(ctx, row, l.Config.RetryDelay, err.Error(), false)
		return err
	}
	if !route.Bound() {
		// Mail stays open in the ledger and in Tether; try again later.
		l.postpone(ctx, row, l.Config.RetryDelay, "actor unbound", false)
		return nil
	}
	return l.drive(ctx, route, env, row)
}

// defer_ postpones a row and reports the attempt count; errors are logged,
// not returned, because the caller already has the more useful error.
func (l *Loop) postpone(ctx context.Context, row *store.TetherDelivery, wait time.Duration, why string, bump bool) int {
	n, err := l.Ledger.DeferTetherDelivery(ctx, row.MessageID, l.clock().Add(wait), why, bump)
	if err != nil {
		slog.Warn("tetherbridge: defer delivery", "message_id", row.MessageID, "err", err)
	}
	return n
}

// backoff grows RetryDelay with the attempt count up to MaxBackoff.
func (l *Loop) backoff(attempts int) time.Duration {
	d := l.Config.RetryDelay
	for i := 1; i < attempts && d < l.Config.MaxBackoff; i++ {
		d *= 2
	}
	if d > l.Config.MaxBackoff {
		d = l.Config.MaxBackoff
	}
	return d
}

// softFail counts a failed attempt on row and either defers it with backoff or,
// once MaxAttempts is exhausted, closes it so it cannot occupy the sweep.
// A closed row's message was never acknowledged, so Tether still holds it.
func (l *Loop) softFail(ctx context.Context, row *store.TetherDelivery, cause error) error {
	attempts := l.postpone(ctx, row, l.backoff(row.Attempts+1), cause.Error(), true)
	if attempts >= l.Config.MaxAttempts*4 {
		if _, err := l.Ledger.AdvanceTetherDelivery(ctx, row.MessageID, store.TetherDeliveryFailed, "", "giving up: "+cause.Error()); err != nil {
			slog.Warn("tetherbridge: close delivery", "message_id", row.MessageID, "err", err)
		}
		slog.Error("tetherbridge: delivery closed after repeated failures", "message_id", row.MessageID, "err", cause)
	}
	return cause
}

func (l *Loop) drive(ctx context.Context, route ActorRoute, env messaging.Envelope, row *store.TetherDelivery) error {
	cfg := l.Config
	recipient := env.To
	claim, err := ClaimDelivery(ctx, l.Claimer, env.ID, recipient, ClaimOptions{LeaseSeconds: cfg.LeaseSeconds})
	if err != nil {
		return l.softFail(ctx, row, err) // counted, so unclaimable rows age out
	}

	if l.AllowSender != nil {
		if serr := l.AllowSender(env); serr != nil {
			_, _ = FailDelivery(ctx, l.Claimer, env.ID, recipient, claim, NackOptions{Reason: serr.Error()})
			if _, aerr := l.Ledger.AdvanceTetherDelivery(ctx, env.ID, store.TetherDeliveryFailed, "", serr.Error()); aerr != nil {
				return aerr
			}
			slog.Warn("tetherbridge: sender rejected", "message_id", env.ID, "from", env.From.URN(), "err", serr)
			return nil
		}
	}

	if _, err = AcceptDelivery(ctx, l.Claimer, env.ID, recipient, claim); err != nil {
		return l.softFail(ctx, row, err)
	}
	if row.State == store.TetherDeliveryReceived {
		if _, err = l.Ledger.AdvanceTetherDelivery(ctx, env.ID, store.TetherDeliveryAccepted, "", ""); err != nil {
			return l.softFail(ctx, row, err)
		}
	}

	localID := row.LocalMessageID
	if localID == "" {
		if l.Injector.Busy(route.SessionID) {
			return l.busy(ctx, env, claim, row)
		}
		localID, err = l.Injector.Inject(ctx, route, env)
		if err != nil {
			return l.injectFailed(ctx, env, claim, row, err)
		}
		// Idempotent injection makes a failure of this write harmless: the
		// next attempt re-adopts the same mailbox row.
		if _, err = l.Ledger.AdvanceTetherDelivery(ctx, env.ID, store.TetherDeliverySubmitted, localID, ""); err != nil {
			return l.softFail(ctx, row, err)
		}
	}
	if claim.Expired(time.Now()) { // the claim carries a wall-clock deadline
		// The lease lapsed during injection. The message is already in the
		// mailbox and the ledger knows it; a later attempt re-claims and sends
		// the receipts without injecting again.
		return l.softFail(ctx, row, fmt.Errorf("tetherbridge: lease for %s expired before receipts", env.ID))
	}
	if _, err = SubmitDelivery(ctx, l.Claimer, env.ID, recipient, claim); err != nil {
		return l.softFail(ctx, row, err)
	}
	if _, err = ConsumeDelivery(ctx, l.Claimer, env.ID, recipient, claim); err != nil {
		return l.softFail(ctx, row, err)
	}
	_, err = l.Ledger.AdvanceTetherDelivery(ctx, env.ID, store.TetherDeliveryConsumed, "", "")
	return err
}

// busy releases the lease with a retryable nack and defers the row by
// RetryDelay. Busy is never a failed attempt, so a long turn cannot
// dead-letter mail.
func (l *Loop) busy(ctx context.Context, env messaging.Envelope, claim Claim, row *store.TetherDelivery) error {
	wait := l.Config.RetryDelay
	_, ferr := FailDelivery(ctx, l.Claimer, env.ID, env.To, claim, NackOptions{
		Retryable: true, Reason: ErrBusy.Error(), NextAttemptSeconds: int(wait / time.Second),
	})
	l.postpone(ctx, row, wait, ErrBusy.Error(), false)
	if ferr != nil && !errors.Is(ferr, ErrDeadLettered) {
		return ferr
	}
	return nil
}

func (l *Loop) injectFailed(ctx context.Context, env messaging.Envelope, claim Claim, row *store.TetherDelivery, cause error) error {
	cfg := l.Config
	attempts := row.Attempts + 1
	retryable := attempts < cfg.MaxAttempts
	wait := l.backoff(attempts)
	_, ferr := FailDelivery(ctx, l.Claimer, env.ID, env.To, claim, NackOptions{
		Retryable: retryable, Reason: cause.Error(), NextAttemptSeconds: int(wait / time.Second),
	})
	if retryable {
		l.postpone(ctx, row, wait, cause.Error(), true)
	} else if _, err := l.Ledger.AdvanceTetherDelivery(ctx, env.ID, store.TetherDeliveryFailed, "", cause.Error()); err != nil {
		return err
	}
	if ferr != nil && !errors.Is(ferr, ErrDeadLettered) {
		return ferr
	}
	return nil
}

func (l *Loop) begin(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inflight == nil {
		l.inflight = map[string]struct{}{}
	}
	if _, busy := l.inflight[id]; busy {
		return false
	}
	l.inflight[id] = struct{}{}
	return true
}

func (l *Loop) end(id string) {
	l.mu.Lock()
	delete(l.inflight, id)
	l.mu.Unlock()
}

// Run serves until ctx ends: it keeps one subscription per bound actor,
// sweeps each actor's inbox, and resumes open ledger rows (the restart path).
// It returns only after every goroutine it started has exited.
func (l *Loop) Run(ctx context.Context) {
	l.Config.defaults()
	defer l.Close()
	t := time.NewTicker(l.Config.SweepEvery)
	defer t.Stop()
	for {
		l.Sweep(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Close stops every subscription and waits for its goroutine, including any
// delivery it was handling, to finish.
func (l *Loop) Close() {
	l.mu.Lock()
	for urn, cancel := range l.subs {
		cancel()
		delete(l.subs, urn)
	}
	l.mu.Unlock()
	l.wg.Wait()
}

// Sweep runs one pass: ensure subscriptions, drain inboxes, resume due rows.
func (l *Loop) Sweep(ctx context.Context) {
	l.Config.defaults()
	insts, err := l.Actors.ListDurableAgentInstances(ctx, false)
	if err != nil {
		slog.Warn("tetherbridge: list actors", "err", err)
		return
	}
	live := map[string]bool{}
	for _, in := range insts {
		if in.URN == "" || in.CurrentSessionID == "" {
			continue // unbound actors keep their mail in Tether
		}
		live[in.URN] = true
		l.ensureSub(ctx, in.URN)
		addr, perr := messaging.ParseURN(in.URN)
		if perr != nil {
			slog.Warn("tetherbridge: bad actor urn", "urn", in.URN, "err", perr)
			continue
		}
		envs, ierr := l.Mail.Inbox(ctx, addr, messaging.Filter{Limit: l.Config.InboxLimit})
		if ierr != nil {
			slog.Warn("tetherbridge: inbox", "actor", in.URN, "err", ierr)
			continue
		}
		for _, env := range envs {
			if herr := l.Handle(ctx, in.URN, env); herr != nil {
				slog.Warn("tetherbridge: deliver", "actor", in.URN, "message_id", env.ID, "err", herr)
			}
		}
	}
	l.dropStale(live)
	open, err := l.Ledger.ListOpenTetherDeliveries(ctx, 200)
	if err != nil {
		slog.Warn("tetherbridge: list open deliveries", "err", err)
		return
	}
	for _, d := range open {
		var env messaging.Envelope
		if json.Unmarshal([]byte(d.EnvelopeJSON), &env) != nil {
			continue
		}
		if herr := l.Handle(ctx, d.ActorURN, env); herr != nil {
			slog.Warn("tetherbridge: resume delivery", "message_id", d.MessageID, "err", herr)
		}
	}
}

func (l *Loop) ensureSub(ctx context.Context, urn string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.subs == nil {
		l.subs = map[string]context.CancelFunc{}
	}
	if _, ok := l.subs[urn]; ok {
		return
	}
	sctx, cancel := context.WithCancel(ctx)
	l.subs[urn] = cancel
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		l.subscribe(sctx, urn)
	}()
}

func (l *Loop) subscribe(ctx context.Context, urn string) {
	addr, err := messaging.ParseURN(urn)
	if err != nil {
		return
	}
	for ctx.Err() == nil {
		ch, serr := l.Mail.Subscribe(ctx, addr, messaging.Filter{})
		if serr == nil {
			for env := range ch {
				if env.To.URN() != urn {
					continue
				}
				if herr := l.Handle(ctx, urn, env); herr != nil {
					slog.Warn("tetherbridge: deliver", "actor", urn, "message_id", env.ID, "err", herr)
				}
			}
		} else {
			slog.Debug("tetherbridge: subscribe", "actor", urn, "err", serr)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(l.Config.ResubscribeWait):
		}
	}
}

func (l *Loop) dropStale(live map[string]bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for urn, cancel := range l.subs {
		if !live[urn] {
			cancel()
			delete(l.subs, urn)
		}
	}
}
