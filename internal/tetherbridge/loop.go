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
// Idempotency is keyed on the Tether message id and lives in the
// tether_deliveries ledger. The row is written BEFORE anything is claimed or
// injected, and the local mailbox id is recorded BEFORE the submitted receipt
// is sent, so every crash point resumes instead of re-injecting:
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

// Ledger is the idempotency store (*store.Store implements it).
type Ledger interface {
	RecordTetherDelivery(ctx context.Context, d store.TetherDelivery) (*store.TetherDelivery, bool, error)
	AdvanceTetherDelivery(ctx context.Context, messageID, state, localMessageID, lastErr string, bumpAttempt bool) (bool, error)
	ListOpenTetherDeliveries(ctx context.Context, limit int) ([]store.TetherDelivery, error)
}

// Injector hands an accepted delivery to Nanite. Inject must write the
// message to the recipient's Nanite mailbox exactly once per call and return
// the local message id; Busy reports a turn in flight on the session.
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
	RetryDelay      time.Duration // busy/transient retry delay; default 15s
	SweepEvery      time.Duration // inbox sweep and ledger resume period; default 30s
	MaxAttempts     int           // before a delivery is dead-lettered; default 5
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
	Config   LoopConfig

	mu       sync.Mutex
	inflight map[string]struct{}
	subs     map[string]context.CancelFunc
}

// Handle processes one envelope for actorURN. It is safe to call repeatedly
// and concurrently for the same message id: a duplicate is a no-op once the
// delivery is terminal, and resumes from the recorded state otherwise.
func (l *Loop) Handle(ctx context.Context, actorURN string, env messaging.Envelope) error {
	l.Config.defaults()
	if env.ID == "" {
		return errors.New("tetherbridge: envelope without id")
	}
	if !l.begin(env.ID) {
		return nil
	}
	defer l.end(env.ID)

	route, err := ResolveActorRoute(ctx, l.Store, actorURN)
	if err != nil {
		return err
	}
	if !route.Bound() {
		return nil // stays in Tether; nothing recorded
	}

	raw, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("tetherbridge: marshal envelope %s: %w", env.ID, err)
	}
	row, _, err := l.Ledger.RecordTetherDelivery(ctx, store.TetherDelivery{
		MessageID: env.ID, ActorURN: actorURN, SessionID: route.SessionID,
		ProfileID: route.ProfileID, EnvelopeJSON: string(raw),
	})
	if err != nil {
		return err
	}
	if row.Terminal() {
		slog.Debug("tetherbridge: duplicate of finished delivery, ignored", "message_id", env.ID, "state", row.State)
		return nil
	}
	return l.drive(ctx, route, env, row)
}

func (l *Loop) drive(ctx context.Context, route ActorRoute, env messaging.Envelope, row *store.TetherDelivery) error {
	cfg := l.Config
	recipient := env.To
	claim, err := ClaimDelivery(ctx, l.Claimer, env.ID, recipient, ClaimOptions{LeaseSeconds: cfg.LeaseSeconds})
	if err != nil {
		// Held by a live lease elsewhere, or transient. The sweep retries.
		return err
	}
	if _, err = AcceptDelivery(ctx, l.Claimer, env.ID, recipient, claim); err != nil {
		return err
	}
	if _, err = l.Ledger.AdvanceTetherDelivery(ctx, env.ID, store.TetherDeliveryAccepted, "", "", false); err != nil {
		return err
	}

	localID := row.LocalMessageID
	if localID == "" {
		if l.Injector.Busy(route.SessionID) {
			return l.retryOrDeadLetter(ctx, env, claim, row, ErrBusy)
		}
		localID, err = l.Injector.Inject(ctx, route, env)
		if err != nil {
			return l.retryOrDeadLetter(ctx, env, claim, row, err)
		}
		// Recorded before the receipt so a crash here can never re-inject.
		if _, err = l.Ledger.AdvanceTetherDelivery(ctx, env.ID, store.TetherDeliverySubmitted, localID, "", false); err != nil {
			return err
		}
	}
	if claim.Expired(time.Now()) {
		// The lease lapsed during injection. The message is already in the
		// mailbox and the ledger knows it; the sweep re-claims and sends the
		// receipts without injecting again.
		return fmt.Errorf("tetherbridge: lease for %s expired before receipts", env.ID)
	}
	if _, err = SubmitDelivery(ctx, l.Claimer, env.ID, recipient, claim); err != nil {
		return err
	}
	if _, err = ConsumeDelivery(ctx, l.Claimer, env.ID, recipient, claim); err != nil {
		return err
	}
	_, err = l.Ledger.AdvanceTetherDelivery(ctx, env.ID, store.TetherDeliveryConsumed, "", "", false)
	return err
}

func (l *Loop) retryOrDeadLetter(ctx context.Context, env messaging.Envelope, claim Claim, row *store.TetherDelivery, cause error) error {
	cfg := l.Config
	busy := errors.Is(cause, ErrBusy)
	attempts := row.Attempts + 1
	retryable := busy && attempts < cfg.MaxAttempts*4 || !busy && attempts < cfg.MaxAttempts
	_, ferr := FailDelivery(ctx, l.Claimer, env.ID, env.To, claim, NackOptions{
		Retryable: retryable, Reason: cause.Error(),
		NextAttemptSeconds: int(cfg.RetryDelay / time.Second),
	})
	state := store.TetherDeliveryReceived
	if !retryable {
		state = store.TetherDeliveryFailed
	}
	if _, aerr := l.Ledger.AdvanceTetherDelivery(ctx, env.ID, state, "", cause.Error(), true); aerr != nil {
		return aerr
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
func (l *Loop) Run(ctx context.Context) {
	l.Config.defaults()
	t := time.NewTicker(l.Config.SweepEvery)
	defer t.Stop()
	for {
		l.Sweep(ctx)
		select {
		case <-ctx.Done():
			l.stopSubs()
			return
		case <-t.C:
		}
	}
}

// Sweep runs one pass: ensure subscriptions, drain inboxes, resume open rows.
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
	if l.subs == nil {
		l.subs = map[string]context.CancelFunc{}
	}
	if _, ok := l.subs[urn]; ok {
		l.mu.Unlock()
		return
	}
	sctx, cancel := context.WithCancel(ctx)
	l.subs[urn] = cancel
	l.mu.Unlock()
	go l.subscribe(sctx, urn)
}

func (l *Loop) subscribe(ctx context.Context, urn string) {
	addr, err := messaging.ParseURN(urn)
	if err != nil {
		return
	}
	for ctx.Err() == nil {
		ch, err := l.Mail.Subscribe(ctx, addr, messaging.Filter{})
		if err == nil {
			for env := range ch {
				if env.To.URN() != urn {
					continue
				}
				if herr := l.Handle(ctx, urn, env); herr != nil {
					slog.Warn("tetherbridge: deliver", "actor", urn, "message_id", env.ID, "err", herr)
				}
			}
		} else {
			slog.Debug("tetherbridge: subscribe", "actor", urn, "err", err)
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

func (l *Loop) stopSubs() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for urn, cancel := range l.subs {
		cancel()
		delete(l.subs, urn)
	}
}
