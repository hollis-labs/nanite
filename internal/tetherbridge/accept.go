package tetherbridge

import (
	"context"
	"errors"
	"fmt"
	"time"

	messaging "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/go-messaging/delivery"
	tether "github.com/hollis-labs/go-tether-client"
)

// Durable host acceptance — the claim/ack/nack cycle.
//
// C02's scope requires "durable host acceptance", and the approved
// architecture spells out why it is a cycle rather than one call: "An
// authorized host/consumer leases a delivery, validates the current binding
// generation, and durably accepts the handoff before acknowledging it." The
// point is that a crash between taking custody and doing the work is
// recoverable instead of silent.
//
// Two behaviors of the underlying client are easy to get wrong, and both are
// encoded here rather than left to each caller:
//
//  1. ClaimMessage returns the lease duration the daemon GRANTED, which may be
//     clamped below what was requested. Honoring the request instead of the
//     grant means believing a lease is alive after it expired.
//
//  2. A non-retryable NackMessage DEAD-LETTERS and returns a NIL ERROR. That
//     is success at the transport level and failure at the delivery level.
//     Reading nil-error as "still alive" retries something already
//     dead-lettered. Status is what says what happened, not err.
//
// Nanite owns what happens next. This layer reports transport and host
// receipts only; per the architecture, none of them "implies task success or
// that the model understood the content."

// claimer is the slice of the Tether client this file needs. Declared as an
// interface so the cycle can be tested against a stub without a live daemon —
// and, more usefully, so the two behaviors above can be exercised with a
// clamped lease and a dead-lettering nack, which a healthy daemon will not
// produce on demand.
type claimer interface {
	ClaimMessage(ctx context.Context, messageID string, recipient messaging.Address, opts ClaimOptions) (messaging.Envelope, delivery.LeaseRef, int, error)
	AckMessage(ctx context.Context, messageID string, recipient messaging.Address, lease delivery.LeaseRef, stage delivery.ReceiptStage) (delivery.RecipientDelivery, delivery.Attempt, error)
	NackMessage(ctx context.Context, messageID string, recipient messaging.Address, lease delivery.LeaseRef, opts NackOptions) (delivery.RecipientDelivery, delivery.Attempt, error)
}

// ClaimOptions and NackOptions are ALIASES of the client's own option types,
// not copies.
//
// They started as copies, which compiled and was wrong: with local structs the
// claimer interface could never be satisfied by *tether.Client, whose methods
// take tether.ClaimOptions. A stub would have satisfied the interface happily
// and the real client would not have fitted at all — a fake agreeing with an
// interface the production type cannot implement. The assertion below is what
// makes that impossible to reintroduce.
type ClaimOptions = tether.ClaimOptions

type NackOptions = tether.NackOptions

// *tether.Client must satisfy claimer. This line is the guard: without it the
// interface is only ever checked against test doubles, and the first sign of a
// mismatch would be at the wiring site rather than here.
var _ claimer = (*tether.Client)(nil)

// ErrDeadLettered reports that a delivery is dead-lettered and will not be
// retried. It exists because the client signals this with a nil error, so a
// caller that only checks err cannot tell it from a scheduled retry.
var ErrDeadLettered = errors.New("tetherbridge: delivery dead-lettered")

// Claim is a lease taken on one delivery, with the granted duration rather
// than the requested one.
type Claim struct {
	Envelope messaging.Envelope
	Lease    delivery.LeaseRef

	// GrantedFor is the lease duration the daemon actually granted. It may be
	// SHORTER than requested; the daemon clamps to its own maximum.
	GrantedFor time.Duration

	// ExpiresAt is GrantedFor from the moment the claim returned. Derived here
	// so a caller reasons about a deadline rather than re-deriving one from a
	// duration and its own idea of "now".
	ExpiresAt time.Time
}

// Expired reports whether the granted lease has run out as of now.
func (c Claim) Expired(now time.Time) bool {
	return !c.ExpiresAt.IsZero() && now.After(c.ExpiresAt)
}

// ClaimDelivery takes custody of one message for recipient and returns the
// granted lease. The returned Claim carries the daemon's grant, not the
// request: ClaimOptions.LeaseSeconds is a request the daemon may clamp.
func ClaimDelivery(ctx context.Context, c claimer, messageID string, recipient messaging.Address, opts ClaimOptions) (Claim, error) {
	if c == nil {
		return Claim{}, errors.New("tetherbridge: nil claimer")
	}
	env, lease, grantedSeconds, err := c.ClaimMessage(ctx, messageID, recipient, opts)
	if err != nil {
		return Claim{}, fmt.Errorf("tetherbridge: claim %s: %w", messageID, err)
	}
	granted := time.Duration(grantedSeconds) * time.Second
	claim := Claim{Envelope: env, Lease: lease, GrantedFor: granted}
	if granted > 0 {
		claim.ExpiresAt = time.Now().Add(granted)
	}
	return claim, nil
}

// AcceptDelivery records that the host has durably accepted the handoff. This
// is the acknowledgement the architecture requires BEFORE the work is
// reported as done — the whole reason the cycle is separate from Consume.
//
// The lease is passed back unmodified; the daemon checks it belongs to the
// message in the path, so a mismatched pair is a caller bug rather than a
// silent act on the wrong delivery.
func AcceptDelivery(ctx context.Context, c claimer, messageID string, recipient messaging.Address, claim Claim) (delivery.RecipientDelivery, error) {
	return ackStage(ctx, c, messageID, recipient, claim, delivery.StageHostAccepted)
}

// SubmitDelivery records that the accepted work reached a turn. Distinct from
// AcceptDelivery because the architecture reports them as separate observable
// stages, and neither implies the model did anything with the content.
func SubmitDelivery(ctx context.Context, c claimer, messageID string, recipient messaging.Address, claim Claim) (delivery.RecipientDelivery, error) {
	return ackStage(ctx, c, messageID, recipient, claim, delivery.StageTurnSubmitted)
}

// ConsumeDelivery closes the cycle: the work is done.
func ConsumeDelivery(ctx context.Context, c claimer, messageID string, recipient messaging.Address, claim Claim) (delivery.RecipientDelivery, error) {
	return ackStage(ctx, c, messageID, recipient, claim, delivery.StageConsumed)
}

func ackStage(ctx context.Context, c claimer, messageID string, recipient messaging.Address, claim Claim, stage delivery.ReceiptStage) (delivery.RecipientDelivery, error) {
	if c == nil {
		return delivery.RecipientDelivery{}, errors.New("tetherbridge: nil claimer")
	}
	rd, _, err := c.AckMessage(ctx, messageID, recipient, claim.Lease, stage)
	if err != nil {
		return delivery.RecipientDelivery{}, fmt.Errorf("tetherbridge: ack %s at %s: %w", messageID, stage, err)
	}
	return rd, nil
}

// FailDelivery reports that the host could not handle the delivery.
//
// It returns ErrDeadLettered when the delivery will NOT be retried, which the
// underlying client signals with a nil error and a status. That translation is
// the entire reason this wrapper exists: a caller that checks only err would
// treat a dead-lettered delivery as live and retry it, and a caller that
// retries a dead-lettered delivery never finds out, because nothing errors.
//
// A retryable failure returns a nil error and the delivery's scheduled state,
// so the two outcomes are distinguishable without reading a status enum at
// every call site.
func FailDelivery(ctx context.Context, c claimer, messageID string, recipient messaging.Address, claim Claim, opts NackOptions) (delivery.RecipientDelivery, error) {
	if c == nil {
		return delivery.RecipientDelivery{}, errors.New("tetherbridge: nil claimer")
	}
	rd, _, err := c.NackMessage(ctx, messageID, recipient, claim.Lease, opts)
	if err != nil {
		return delivery.RecipientDelivery{}, fmt.Errorf("tetherbridge: nack %s: %w", messageID, err)
	}
	if rd.Status == delivery.DeliveryDeadLettered {
		reason := rd.DeadLetterReason
		if reason == "" {
			reason = opts.Reason
		}
		return rd, fmt.Errorf("%w: %s (message %s): %s", ErrDeadLettered, rd.ID, messageID, reason)
	}
	return rd, nil
}
