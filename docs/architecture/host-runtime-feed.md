# Host runtime feed

Nanite exposes the normalized `go-agent-wrapper` activity stream to its own UI
through a public, versioned projection. The feed is session-scoped and remains
available while no message stream is active.

## Transport and replay

`GET /api/sessions/{id}/runtime-events` is an SSE endpoint. Each
`host_runtime.v1` frame has two deliberately different ordering fields:

- SSE `id` and JSON `cursor` are a Nanite-owned, committed, monotonically
  increasing cursor within the session. `?after=` takes precedence over
  `Last-Event-ID`.
- `source_sequence` is the wrapper event's sequence. It is diagnostic source
  provenance, not a replay cursor: recovery can create a new wrapper for the
  same session and restart that sequence at one.

Every sink instance also receives an immutable `runtime_run_id` and a durable,
per-session monotonic `runtime_generation` reserved when the sink is created.
Old and new runtime processes can overlap during replacement, so clients use
the numeric generation—not event arrival time or UUID ordering—to keep any
delayed predecessor lifecycle event from reclaiming successor state. The
durable head stores the latest generation and its run ID atomically. Replay-gap
frames carry that floor even if retention leaves only late predecessor rows, so
a reconnect cannot adopt stale lifecycle state. A fresh connection without a
cursor receives the retained snapshot from cursor zero.

The feed keeps 512 committed records per session. If a requested cursor has
been pruned or is ahead of the session head, the server first emits a
`host_runtime.gap.v1` control frame describing the unavailable cursor span and
the oldest available record, plus the current runtime-generation floor.
Ingestion is serialized through a bounded 1,024-record FIFO so wrapper IO never
waits for SQLite. Queue-overflow and persistence-failure loss is accumulated in
session-scoped order across runtime replacements and represented by a durable
`host_runtime.ingest_gap` record before the next event for that session. Both
loss ledgers are fixed at 256 sessions; count arithmetic saturates rather than
wrapping, and distinct-session saturation fails the feed closed with an error
and host log instead of claiming continuity. Shutdown cancels in-flight
persistence and joins the worker before returning. A database that remains
unavailable through shutdown can only be reported in the host log.

The short event history has a separate compact identity ledger. Event hashes
survive replay pruning, so an identical retransmission remains idempotent and
conflicting identity reuse remains an integrity error for the most recent
4,096 session cursors. The ledger is pruned by cursor beyond that documented
horizon; a much older retransmission is admitted as a new observation rather
than growing tombstones without bound.

Records are committed before the SSE DB tail can observe them. Slow or
disconnected clients do not affect ingestion; they resume from their last
committed SSE ID. This feed has no relationship to `StreamManager`'s
single-owner, message-scoped EventSource and therefore cannot cause chat
stream takeover.

## Public projection

The source `runtimeevents.Event` envelope is internal. `host_runtime.v1`
preserves bounded identity, time, source, process provider/runtime,
provider-session ID as observed on that exact event, turn/parent IDs, and an
allowlisted per-kind payload. It never retroactively attributes a provider
session ID to earlier events.

The v0.1.2 vocabulary is handled as follows:

| Source kinds | Public treatment |
| --- | --- |
| `process.*`, `session.*`, `turn.*` | Lifecycle state; process exit/outcome; numeric usage. A native usage-bearing `turn.completed` is marked nonterminal because native emits a later terminal completion. |
| `agent.tool_use`, `agent.tool_result` | One normalized tool ID/name/status/stage. Flat ACP results without status are updates; known nested native/Copilot results are terminal. Tool arguments, titles that can contain commands, and results are omitted. |
| `interrupt.*` | Requested/acknowledged state, correlated by the preserved parent ID. |
| `agent.permission_*` | Requested/resolved state only; no tool call, options, or input. |
| `stdin.write`, `stdout.*`, `stderr.*`, `agent.delta` | Metadata-only record with content omitted. |
| `agent.subagent_spawn` | Spawn observation without prompt or instructions. |
| `policy.*` | Metadata explicitly labeled observational; it does not establish enforcement. |
| `plant.*`, `sandbox.applied` | Metadata-only lifecycle observation; paths, planted files, profile details, and diagnostics omitted. |
| Future/unknown kinds | Envelope metadata is retained with `unsupported_kind`; opaque payload is default-denied. |

Projection recursively redacts secret-bearing keys and credential-shaped
string values, bounds individual strings and collections, and caps encoded
payloads at 4 KiB and the complete persisted event at 8 KiB. Raw stdout/stderr,
stdin and prompts, conversation deltas,
tool arguments/results, and permission inputs are never persisted in this
public table.

## Compatibility and plugin boundary

The canonical sink still runs before the existing compatibility projection.
Legacy message SSE (`delta`, `tool_call`, `tool_result`, `stream_end`, and
`error`) is unchanged; the runtime feed has its own reducer and UI activity
tab, so it does not re-inject canonical events into legacy chat state.

Plugins do not receive the normalized source event or this feed implicitly.
Plugin hooks remain their existing separately permissioned contract. A future
plugin subscription must opt in at an explicit trust boundary and consume the
same bounded public DTO; passing the wrapper's opaque payload to third-party
code is not permitted by this contract.
