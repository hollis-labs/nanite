# Tether delivery

Mail addressed through Tether to a durable Nanite actor reaches the actor's
session as one turn. The bridge is off unless `NANITE_TETHER_ADDR` names a
Tether daemon (credentials: `NANITE_TETHER_TOKEN_FILE`, a 0600 file holding the bridge's
service token, else `TETHER_TOKEN`, else the client's default operator token file, which
the bridge must not run on). Run it with its own Tether service identity and a scoped token,
not an operator credential.

## Flow

`internal/tetherbridge/loop.go` serves every non-archived durable instance
that has a URN and a current session. For each envelope:

1. The envelope is recorded in `tether_deliveries`, keyed on the Tether
   message id, before the route is resolved or anything is claimed.
2. `ResolveActorRoute` maps the actor URN to the mailbox slot
   `(session, profile)`; an ambiguous slot refuses. An unbound actor leaves
   the row open and deferred; the mail is not dropped.
3. `ClaimDelivery`, then the sender policy, then `AcceptDelivery`.
4. If the session has a turn in flight the delivery is nacked retryable and
   the row deferred by `RetryDelay`. Busy is never counted as a failure.
5. Otherwise `tetherInjector` writes the message to the recipient's Nanite
   mailbox, the local id is recorded, then `SubmitDelivery` and
   `ConsumeDelivery` are sent.

Waking is not done by the bridge. `messagingWakeReactor` sees the mailbox
write and applies the session's `message_wake_policy`, for API and CLI
sessions alike, so there is one injector.

## Idempotency and loss

The mailbox write and the ledger write are separate stores, so the injector
is idempotent on the Tether message id: it returns an existing mailbox row for
that id and slot instead of writing a second one. A crash or a failed ledger
write after the mailbox write therefore re-adopts the same row. The ledger
only moves forward, and a finished row clears its stored envelope.

A failed step defers the row with backoff instead of dropping it. Failures to
claim or acknowledge count as attempts and close the row after
`4 * MaxAttempts`; a closed row was never acknowledged, so Tether still holds
the message. Deferred rows are skipped by the sweep until due, so stuck rows
cannot fill the resume window.

## Sender identity and policy

Tether mail enters the mailbox under the reserved Tether participant
(`a2a.TetherSentinel`), which is neither the user nor an actor and cannot be
used as an agent slug. The real sender, Tether's verified principal and the
message id are in metadata. The body frames the message with two
bridge-written lines and quotes every line of sender text with `> `.

A sender must carry Tether's `from_verification: verified`. Set
`NANITE_TETHER_ALLOW_FROM` (comma-separated URNs or prefixes ending in `*`,
matched against the sender URN or verified principal) to restrict further.
A refused sender is dead-lettered and never wakes the actor.

## Known gaps

The pinned Tether client (`mesh/core` v0.8.0) has no `/messages/list` or
`/messages/watch` call. The live path is `/messages/subscribe` and the
catch-up sweep uses `/messages/inbox`, which marks mail delivered on read. The
ledger row is written immediately after the read, with a short retry; a process
crash, or a database that stays unwritable, between the read and that write
still loses the sweep's copy. Moving the sweep to the non-destructive list
call and the stream to the resumable watch needs a client release.

Tether sends carry no idempotency key, so a caller retrying a timed-out send
must check the sent list first. Unbound actors are not resumed on delivery.
