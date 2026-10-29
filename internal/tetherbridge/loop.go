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

// errRecord marks a failed ledger write; the stream cursor must not pass it.
var errRecord = errors.New("tetherbridge: record delivery")

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

// Actors lists the stable-actor URNs the loop should serve: enrolled actors
// plus any actor already attached to a session.
type Actors interface {
	ListServedActorURNs(ctx context.Context) ([]string, error)
}

// Event kinds a MailSource watch delivers.
const (
	EventMessage      = "message"
	EventSynchronized = "synchronized"
	EventGap          = "gap"
)

// Event is one watch event. Seq is Tether's per-stream sequence for a message.
type Event struct {
	Kind string
	Seq  int64
	Env  messaging.Envelope
}

// MailSource is the NON-destructive read side of Tether (list + SSE watch);
// nothing it returns is marked read or delivered. Watch with afterSeq <= 0
// starts from now. The channel closes when the stream ends or after a gap event.
type MailSource interface {
	List(ctx context.Context, to string, unreadOnly bool, limit, offset int) ([]messaging.Envelope, int, error)
	Watch(ctx context.Context, to string, afterSeq int64) (<-chan Event, error)
}

// Cursors persists the watch cursor (after_seq) per actor.
type Cursors interface {
	GetTetherWatchCursor(ctx context.Context, actorURN string) (int64, error)
	SetTetherWatchCursor(ctx context.Context, actorURN string, seq int64, reset bool) error
}

// Waker starts a session for an enrolled actor that has none and returns its id.
// It is optional: without one an unbound actor's mail waits (as before).
type Waker interface {
	Wake(ctx context.Context, actorURN string) (sessionID string, err error)
}

// LoopConfig tunes the loop. Zero values take the defaults noted.
type LoopConfig struct {
	LeaseSeconds    int           // requested claim lease; default 120
	RetryDelay      time.Duration // busy/unbound/resolve retry delay; default 15s
	MaxBackoff      time.Duration // ceiling for failure backoff; default 10m
	SweepEvery      time.Duration // actor-set refresh and ledger resume period; default 30s
	CatchUpEvery    time.Duration // safety-net list catch-up per actor; default 5m
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
	if c.CatchUpEvery <= 0 {
		c.CatchUpEvery = 5 * time.Minute
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
	Mail     MailSource // non-destructive list + watch
	Cursors  Cursors    // persisted after_seq per actor; optional (nil: watch from now)
	Waker    Waker      // starts a session for an unbound enrolled actor; optional
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
			return fmt.Errorf("%w: %s (message may be lost to this stream): %v", errRecord, env.ID, err)
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
	if !route.Bound() && l.Waker != nil {
		// Tether never wakes a substrate=nanite actor: Nanite starts a session.
		sid, werr := l.Waker.Wake(ctx, actorURN)
		if werr != nil {
			l.postpone(ctx, row, l.backoff(row.Attempts+1), "wake failed: "+werr.Error(), true)
			return werr
		}
		if route, err = ResolveActorRoute(ctx, l.Store, actorURN); err != nil {
			l.postpone(ctx, row, l.Config.RetryDelay, err.Error(), false)
			return err
		}
		if !route.Bound() {
			route.SessionID = sid // the attach is not visible yet; the woken session is the target
		}
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

// Sweep runs one pass: ensure a watch per served actor, drop watches for actors
// no longer served, and resume due ledger rows (the restart path). Reading mail
// is the watch's job (plus a list catch-up on every (re)connect and every
// CatchUpEvery); nothing here consumes a Tether inbox.
func (l *Loop) Sweep(ctx context.Context) {
	l.Config.defaults()
	urns, err := l.Actors.ListServedActorURNs(ctx)
	if err != nil {
		slog.Warn("tetherbridge: list actors", "err", err)
		return
	}
	live := map[string]bool{}
	for _, urn := range urns {
		live[urn] = true
		l.ensureWatch(ctx, urn)
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

func (l *Loop) ensureWatch(ctx context.Context, urn string) {
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
		l.watch(sctx, urn)
	}()
}

// catchUp lists the actor's unread mail (non-destructive, paged) and handles
// each envelope. Handle is idempotent on the message id, so overlap with the
// watch or a previous catch-up is harmless.
func (l *Loop) catchUp(ctx context.Context, urn string) {
	const page, maxPages = 100, 5
	for p := 0; p < maxPages && ctx.Err() == nil; p++ {
		envs, _, err := l.Mail.List(ctx, urn, true, page, p*page)
		if err != nil {
			slog.Debug("tetherbridge: list catch-up", "actor", urn, "err", err)
			return
		}
		for _, env := range envs {
			if herr := l.Handle(ctx, urn, env); herr != nil {
				slog.Warn("tetherbridge: deliver", "actor", urn, "message_id", env.ID, "err", herr)
			}
		}
		if len(envs) < page {
			return
		}
	}
}

// watch serves one actor until ctx ends: catch up by list, then follow the SSE
// stream from the persisted cursor, reconnecting after a drop. A gap event
// resets the cursor (the stream restarts from now) and is followed by a list
// catch-up, so no mail is skipped.
func (l *Loop) watch(ctx context.Context, urn string) {
	l.Config.defaults()
	catchUpTick := time.NewTicker(l.Config.CatchUpEvery)
	defer catchUpTick.Stop()
	for ctx.Err() == nil {
		l.catchUp(ctx, urn)
		var after int64
		if l.Cursors != nil {
			after, _ = l.Cursors.GetTetherWatchCursor(ctx, urn)
		}
		ch, err := l.Mail.Watch(ctx, urn, after)
		if err != nil {
			slog.Debug("tetherbridge: watch", "actor", urn, "err", err)
		} else {
			l.follow(ctx, urn, ch, catchUpTick.C)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(l.Config.ResubscribeWait):
		}
	}
}

func (l *Loop) follow(ctx context.Context, urn string, ch <-chan Event, tick <-chan time.Time) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			l.catchUp(ctx, urn)
		case ev, ok := <-ch:
			if !ok {
				return
			}
			switch ev.Kind {
			case EventGap:
				if l.Cursors != nil {
					_ = l.Cursors.SetTetherWatchCursor(ctx, urn, 0, true)
				}
				return // reconnect from now, after a list catch-up
			case EventMessage:
				if ev.Env.To.URN() != urn {
					continue
				}
				herr := l.Handle(ctx, urn, ev.Env)
				if herr != nil {
					slog.Warn("tetherbridge: deliver", "actor", urn, "message_id", ev.Env.ID, "err", herr)
				}
				// The ledger row is written before any delivery work, so the
				// cursor may move past the message even when delivery is
				// deferred; only a failed ledger write (message lost to this
				// stream) keeps it back, and the next list catch-up covers that.
				if l.Cursors != nil && ev.Seq > 0 && !errors.Is(herr, errRecord) {
					_ = l.Cursors.SetTetherWatchCursor(ctx, urn, ev.Seq, false)
				}
			}
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
