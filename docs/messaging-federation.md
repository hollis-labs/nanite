# Messaging — alignment to the shared `go-messaging` contract

**Status:** implemented (CW-20260518-0050, Torque Messaging program plan
CW-20260518-0038). Additive — the legacy subsystem is untouched.

## Why

Nanite's original messaging subsystem (`internal/messaging`) predates the
portfolio-shared `github.com/hollis-labs/go-messaging` library. It
addresses messages by a divergent `(session_id, agent_id)` tuple and has
its own `Store` interface, `Kind` vocabulary, and SQLite schema. That
divergence keeps Nanite from being a peer in app-to-app federated
messaging, where every participant must speak the same `Envelope` /
typed-URN-`Address` / closed-`Kind`-enum contract.

This change aligns Nanite onto the shared contract using the
*extend, don't rebuild* model from `docs/torque-messaging-design.md`:
the working legacy subsystem stays in place; a conforming layer is added
alongside it, plus a translation bridge so the two interoperate during
the migration.

## What landed

### `internal/messaging/gomsg` — the conforming peer layer

| Piece | Role |
|---|---|
| `SQLStore` | A durable, SQLite-backed `messaging.Store`. Conforms to the go-messaging Store contract identically to the reference impl — verified by `messagingtest.RunContract` (`sqlstore_test.go`). Backed by the `messaging_envelopes` table (migration 064). |
| `AgentAddress` / `Tuple` | Bidirectional mapping between Nanite's `(session_id, agent_id)` tuple and the typed go-messaging URN `Address`. |
| `IsLocal` | The federation predicate: *is this authority local?* — the one question that decides "internal vs external". |
| `Router` | The authority-routing `Store` decorator (the federation seam). |
| `ToGoKind` / `FromGoKind` | Alignment between Nanite's legacy `Kind` vocabulary and the closed go-messaging `Kind` enum. |

### `internal/messaging/envelope_bridge.go` — the migration bridge

`ToEnvelope` / `FromEnvelope` translate a legacy tuple-addressed
`Message` to and from a go-messaging `Envelope`. The mapping is lossless:
legacy-only columns (`subject`, `body`, `priority`, `status`, `type`)
have no native `Envelope` field and are preserved in `Envelope.Metadata`
under the `nanite.` prefix.

## Addressing

A Nanite tuple maps to a canonical URN:

```
agent:  (sessionID, agentID)  ->  msg://agent/<authority>/<sessionID>/<agentID>
user:   (sessionID, "user")   ->  msg://user/<authority>/<sessionID>
```

The session id is the routable `ID`; the agent id is the `SubID`, so the
same agent in two sessions keeps two distinct addresses (and inboxes) —
the per-`(session,agent)` semantics of the legacy model are preserved.

## Federation

`Router` wraps a local `Store` and a registry of foreign-authority peer
`Store`s. Address-keyed operations (`Send`, `Inbox`, `Subscribe`)
dispatch on the target address's `Authority`:

- local authority → the local `Store`
- registered peer → that peer's `Store`
- unknown authority → `ErrStoreUnavailable`

A **standalone install registers no peers** — every address is local and
`Router` behaves exactly as the bare local `Store`. Federation is purely
additive and zero-config by default.

ID-keyed operations (`Get`, `Thread`, `Consume`, `Cancel`) take an opaque
envelope id rather than an address and always target the local `Store`.
Cross-authority envelope-id resolution needs the federation transport +
auth mechanism specified by task **M2 (`CW-20260518-0040`)** and is out
of scope here.

## Scope / follow-ups

This task delivers the conforming contract layer, the federation seam,
and the legacy bridge — all behind passing contract tests. **Not** in
scope (deliberate follow-ups):

- Cutting the HTTP `/api/messaging/*` and MCP messaging tools over to
  `gomsg.SQLStore` — the legacy `internal/messaging.Service` still backs
  those front-ends.
- Back-filling existing `agent_messages` rows into `messaging_envelopes`.
- The concrete federation transport + cross-authority auth (task M2).
