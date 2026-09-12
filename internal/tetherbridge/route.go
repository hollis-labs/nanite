// Package tetherbridge crosses between Tether's addressing and Nanite's.
//
// # The two destination kinds
//
// This package exists because Tether and Nanite address different things, and
// the approved architecture names the distinction rather than treating it as a
// conflict:
//
//		A stable-actor destination and a concrete-session destination are
//		different promises: the former can follow authorized activation; the
//		latter cannot silently redirect to a replacement session.
//
//	  - TETHER addresses a STABLE ACTOR — a durable URN under the `nanite`
//	    authority. Mail accumulates against it whether or not anything is
//	    running. Their mailbox, their tools (`mux_message_*`).
//	  - NANITE addresses a CONCRETE SESSION — the pair
//	    (agent_messages.to_session_id, agent_messages.to_agent_id). Both halves
//	    are load-bearing; reflexes/state.go's unread count requires both. Our
//	    mailbox, our tools (`message_inbox`, `message_ack`).
//
// So this is a bridge between two kinds of destination, not a merge of two
// competing schemes. Nothing here replaces either mailbox.
//
// # What is deliberately NOT here
//
// There is no local accumulator for actor-addressed mail that has nowhere to
// land. Tether already holds mail for an unbound actor — "an actor with no
// binding still accumulates mail perfectly well … the next session to lease a
// binding picks it up" — and 16 of 18 live instances are unbound, so that is
// the normal case rather than an edge. Building a second place to hold it
// would create a second authoritative owner per recipient, which this
// adoption's own acceptance criteria forbid. Unbound resolves to a route with
// no session, and the caller leaves the mail where it is.
//
// # The asymmetry, and the condition that retires it
//
// The ACTOR is the INSTANCE (migration 159, CW-20260912-0017). The MAILBOX
// SLOT is (session, PROFILE). Those are different levels, deliberately:
// agent_messages is a concrete-session mailbox, and within one session the
// profile identifies which agent. The session half carries the disambiguation
// the profile half cannot.
//
// The cost is real and worth stating plainly: mail addressed to instance I1
// lands as (S, P), and the instance identity is DISCARDED on the way in. It is
// not reconstructible, because a profile has many instances and the mapping is
// one-way. That is the definition-is-not-a-recipient conflation migration 159
// removed, reappearing one level down, and it is accepted here only because
// the session half makes it unambiguous.
//
//	THE CONDITION UNDER WHICH THIS SHAPE IS EXHAUSTED: when a session
//	legitimately hosts two instances of one profile. At that point the slot
//	is genuinely ambiguous, no guard can resolve it, and agent_messages
//	to_agent_id must become the INSTANCE id.
//
// That is a condition rather than a date, because a date passes without
// anyone reading it. It is cheaper than it sounds: to_agent_id is internal,
// nothing outside Nanite addresses it, and it is a small table. The warning in
// Tether's messaging-integration.md §1 about migrating every message already
// addressed to an old URN does NOT apply — that is about re-minting addresses
// other parties hold.
//
// Until then, ResolveActorRoute refuses to guess. Nothing in the schema
// prevents the collision — there is no unique index on
// durable_agent_instances.current_session_id — so it is checked at the point
// of delivery and returned as an error somebody sees.
package tetherbridge

import (
	"context"
	"errors"
	"fmt"

	"github.com/hollis-labs/nanite/internal/store"
)

var (
	// ErrActorUnknown means no durable instance carries that URN. The caller
	// should leave the mail with Tether rather than inventing a recipient:
	// an actor Nanite does not know is not necessarily an actor nobody owns.
	ErrActorUnknown = errors.New("tetherbridge: no durable agent instance for actor URN")

	// ErrMailboxSlotAmbiguous means two or more live instances of one profile
	// are bound to the same session, so (session, profile) no longer
	// identifies a single recipient.
	//
	// This is the loud failure the package header's condition describes. It is
	// deliberately NOT resolved by picking one: the architecture is explicit
	// that concurrent sessions for one actor "require an explicit host routing
	// policy; never select whichever matching process happens to look newest."
	// Silently interleaving two actors' mail in one slot is worse than
	// refusing to deliver, because nothing downstream could detect it.
	ErrMailboxSlotAmbiguous = errors.New("tetherbridge: mailbox slot (session, profile) resolves to more than one live instance")
)

// ActorRoute is where a stable-actor delivery lands in Nanite.
type ActorRoute struct {
	// InstanceID is the actor. Carried for tracing and for the caller's own
	// records; it is NOT part of the mailbox address (see the package
	// header's asymmetry note).
	InstanceID string

	// ProfileID is the to_agent_id half of the mailbox slot.
	ProfileID string

	// SessionID is the to_session_id half. EMPTY means the actor is
	// currently unbound: there is no concrete session to deliver into, the
	// mail stays in Tether, and Nanite's own wake/resume/fresh policy decides
	// what happens next. Empty is a valid route, not a failure.
	SessionID string
}

// Bound reports whether this route has a concrete session to deliver into.
// A route that is not Bound is the normal case rather than an error.
func (r ActorRoute) Bound() bool { return r.SessionID != "" }

// instanceStore is the slice of *store.Store this package needs, named as an
// interface so a test can drive the collision path without standing up a
// database in a state the schema does not currently allow.
type instanceStore interface {
	GetDurableAgentInstanceByURN(ctx context.Context, urn string) (*store.DurableAgentInstance, error)
	CountInstancesSharingMailboxSlot(ctx context.Context, profileID, sessionID string) (int, error)
}

// ResolveActorRoute maps a Tether actor URN to the Nanite mailbox slot that
// should receive its mail.
//
// It answers three distinct outcomes and the caller must tell them apart:
//
//   - a bound route — deliver into (SessionID, ProfileID)
//   - an unbound route (Bound() == false) — leave the mail in Tether
//   - ErrMailboxSlotAmbiguous — refuse, and surface it
//
// Collapsing the first two loses the offline case, which is the majority of
// live instances. Collapsing the third into either is the silent
// mis-delivery this package exists to prevent.
func ResolveActorRoute(ctx context.Context, st instanceStore, actorURN string) (ActorRoute, error) {
	if st == nil {
		return ActorRoute{}, errors.New("tetherbridge: nil store")
	}
	inst, err := st.GetDurableAgentInstanceByURN(ctx, actorURN)
	if err != nil {
		if errors.Is(err, store.ErrDurableAgentInstanceNotFound) {
			return ActorRoute{}, fmt.Errorf("%w: %s", ErrActorUnknown, actorURN)
		}
		return ActorRoute{}, fmt.Errorf("tetherbridge: resolve actor %s: %w", actorURN, err)
	}

	route := ActorRoute{
		InstanceID: inst.ID,
		ProfileID:  inst.ProfileID,
		SessionID:  inst.CurrentSessionID,
	}
	if !route.Bound() {
		// Unbound. No slot to check, nothing to deliver into, and no error:
		// the mail stays where Tether already holds it.
		return route, nil
	}

	n, err := st.CountInstancesSharingMailboxSlot(ctx, route.ProfileID, route.SessionID)
	if err != nil {
		return ActorRoute{}, fmt.Errorf("tetherbridge: check mailbox slot for actor %s: %w", actorURN, err)
	}
	if n > 1 {
		return ActorRoute{}, fmt.Errorf(
			"%w: actor %s (instance %s) shares slot (session %s, profile %s) with %d live instances",
			ErrMailboxSlotAmbiguous, actorURN, route.InstanceID, route.SessionID, route.ProfileID, n)
	}
	return route, nil
}
