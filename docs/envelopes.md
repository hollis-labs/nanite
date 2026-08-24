# Envelopes & Typed Responses

Envelopes are structured blocks emitted into assistant messages that the frontend renders as interactive cards (forms, approvals, questions, resolution capture, plan previews, etc.). Responses to those cards flow back to the host as typed `ResponseV1` payloads, dispatched to per-type handlers before threading into the transcript.

## Flow

```
assistant message → envelope block (fenced `nanite-envelope`) → frontend card
                                                                    ↓
                                                              user action
                                                                    ↓
       POST /api/envelopes/{id}/respond  ←  onEnvelopeResponse(ResponseV1)
                      ↓
          ResponseHandler registered for envelope type
                      ↓
              HandlerResult
                   ├── Silent=false → envelope_response message in transcript
                   └── Silent=true  → record response only (pure side effect)
```

Emission identity is assigned at emit time: the backend generates an envelope instance ID, persists it in `envelope_instances`, and injects the ID into the envelope JSON before streaming. The frontend round-trips the ID on submission so the backend can correlate the response, dedupe duplicates (409), and dispatch to the right handler.

## `ResponseV1` schema

Package `internal/chat`:

```go
type ResponseV1 struct {
    V         int            // always 1
    Kind      string         // envelope type
    ID        string         // envelope instance id
    Status    ResponseStatus // submitted | canceled | partial
    Data      map[string]any
    Answers   []Answer       // convenience for collect_feedback
    Decisions []Decision     // convenience for triage_items
}
```

`Validate()` enforces schema version, required fields, and the status enum. `UnmarshalResponseV1` parses + validates in one call.

Status semantics:
- **submitted** — user completed the card.
- **canceled** — user dismissed the card; `Data` may be empty.
- **partial** — user saved progress on an explicit action; no auto-partial on session-close.

## Response handlers

Package `internal/chat`:

```go
type ResponseHandler interface {
    HandleResponse(ctx context.Context, env store.EnvelopeInstance, resp ResponseV1) (HandlerResult, error)
}

type HandlerResult struct {
    TranscriptData map[string]any
    Silent         bool
    FollowUp       string
}
```

Register via `RegisterResponseHandler(envelopeType, handler)` — typically at service boot for host-owned types, or during plugin load for plugin-owned types. `UnregisterResponseHandler` is called from plugin unload. Types with no handler fall back to a default passthrough that surfaces `Data` verbatim (and promotes `Answers` / `Decisions` into `TranscriptData`).

### When to use `Silent: true`

- Side-effect-only envelopes (persistence, telemetry) that don't belong in the agent's visible transcript.
- Events the agent should continue unaware of.

A silent handler still writes `responded_at` + `response_status` onto the envelope instance and returns `follow_up` to the frontend. It just skips the `envelope_response` message row.

## Message role `envelope_response`

Stored alongside `user | assistant | system | tool`. Content is a self-describing string:

```
[envelope:{type} status:{status}] {json_payload}
```

Built via `chat.FormatEnvelopeResponseContent(type, status, payload)`. `ContextClient.AssembleContext` maps `envelope_response` → `user` at provider egress (Anthropic accepts only `user` / `assistant`); the marker prefix carries the discrimination into the LLM's view without metadata lookups.

Migration `008_messages_envelope_response_role.sql` expands the `messages.role` CHECK constraint by recreating the table with `foreign_keys=OFF` inside a `BEGIN/END` block; `INSERT OR IGNORE` + `IF NOT EXISTS` make the migration idempotent.

## Endpoint contract

`POST /api/envelopes/{id}/respond`

| Code | Condition |
|------|-----------|
| 200  | Handler ran; response persisted. Body: `{ok, message_id?, follow_up?}` |
| 400  | Invalid JSON, version mismatch, or `id` disagrees with path |
| 403  | Body carries `session_id` that doesn't match the instance |
| 404  | Envelope instance not found |
| 409  | Envelope already has a terminal response. Body: prior `response_status` + `response` |
| 500  | Handler returned an error |
| 504  | Handler exceeded the 5s timeout (default). Instance is marked `response_status='failed'` |

## Trust boundary

Before Phase 3 S5, `chat_generate.go` scanned user content for a `<!--TICKET_DATA:...:TICKET_DATA-->` marker and injected an assistant-attributed `ticket-confirmation` envelope from the user-supplied JSON. Any user could forge arbitrary envelope shapes by typing the marker into a chat message (audit finding 05).

S5 closes this by:

1. Deleting the marker scan and `BuildTicketConfirmationEnvelope` from the host.
2. Moving all envelope responses onto the typed `POST /api/envelopes/{id}/respond` path, where strict envelope validation (B.11 Phase 2) still applies and per-type handlers run deterministically.
3. Treating tickets as entirely plugin responsibility — any plugin that wants to own ticket-confirmation emits it through the normal envelope channel.

## Plugin authoring notes

- Register a new envelope type with `chat.RegisterEnvelopeType` (validation) + optionally `chat.RegisterResponseHandler` (custom side effects).
- On unload, call both `Unregister` counterparts so stale entries don't survive a plugin hot-unload.
- Envelope-response round-trip to a waiting plugin (bidirectional response routing) is **deferred** — S5 covers host-owned envelopes only.
