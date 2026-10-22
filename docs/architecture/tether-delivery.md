# Tether delivery

Mail addressed through Tether to a durable Nanite actor reaches the actor's
session as one turn. The bridge is off unless `NANITE_TETHER_ADDR` names a
Tether daemon (credentials come from the Tether client: `TETHER_TOKEN` or its
token file).

## Flow

`internal/tetherbridge/loop.go` serves every non-archived durable instance
that has a URN and a current session. Unbound actors keep their mail in
Tether. For each envelope:

1. `ResolveActorRoute` maps the actor URN to the mailbox slot
   `(session, profile)`; an ambiguous slot refuses.
2. The envelope is recorded in `tether_deliveries`, keyed on the Tether
   message id, before anything is claimed or injected.
3. `ClaimDelivery`, then `AcceptDelivery` (host_accepted receipt).
4. If the session has a turn in flight the delivery is nacked retryable and
   nothing is injected. Otherwise `tetherInjector` writes the message to the
   recipient's Nanite mailbox with `mailbox.Service.SendMessage`.
5. The local message id is recorded, then `SubmitDelivery` and
   `ConsumeDelivery` are sent.

Waking is not done by the bridge. `messagingWakeReactor` sees the mailbox
write and applies the session's `message_wake_policy`, for API and CLI
sessions alike, so there is one injector.

## Idempotency

The ledger row is the single record of a delivery. A repeat of a finished
message id is ignored; a repeat of an open one resumes from its state. The
local id is stored before the submitted receipt, so a crash after injection
re-sends receipts without a second turn. A restart resumes open rows from the
stored envelope.

## Sender identity

Tether mail enters the mailbox under the reserved external-sender participant
(`a2a.UserSentinel`). The bridge cannot enroll an actor, so the real sender URN
and Tether message id are in the message body and metadata, with a reply hint
(`in_reply_to`).

## Not covered

Unbound actors are not resumed on delivery. Tether-managed launches and the
provider contract are out of scope.

## Credentials and known gaps

Run the bridge with its own Tether service identity and a scoped token, not
an operator credential. The pinned Tether client (`mesh/core` v0.8.0) has no
`/messages/list` or `/messages/watch` call, so the live path is the
`/messages/subscribe` stream and the catch-up sweep uses `/messages/inbox`,
which marks mail delivered on read. The ledger row is written right after
that read and before the claim, but a crash between the two is not covered.
Moving the sweep to the non-destructive list call, and the stream to the
resumable watch, needs a client release. Tether sends carry no idempotency
key, so a caller retrying a timed-out send must check the sent list first.
