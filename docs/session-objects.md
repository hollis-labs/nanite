# SessionObjects — operator notes

Ticket: `CW-20260420-0012` (Arc 3 / P2 primitive).

## What this ships

- New `session_objects` SQLite table (migration 023).
- Columns: `id` (ULID, PK), `session_id`, `content_type` (default `application/json`), `byte_size`, `payload` (opaque JSON), `created_at`.
- `*Store` methods: `PutSessionObject`, `GetSessionObject`, `ListSessionObjects`, `EvictSessionObjects`.
- Sentinels: `ErrSessionObjectNotFound`, `ErrSessionObjectTooLarge`.
- `ArchiveSession` now runs the status update + `DELETE FROM session_objects WHERE session_id = ?` in one transaction — archive + eviction are atomic.

## D5 hard-ephemeral guarantee

`GetSessionObject` requires both `session_id` and `id`. A mismatch on either returns `ErrSessionObjectNotFound` — there is no fallback. Cross-session card references are an error, not a convenience.

## Size cap

Each payload is capped at `DefaultSessionObjectMaxBytes` (1 MiB). Over-cap puts return `ErrSessionObjectTooLarge` with the observed byte size and cap in the message.

## Relationship to `tool_result_cache`

Separate table (parallel, not extension) per D3:
- `tool_result_cache` has tool-specific columns (`tool_name`, `tool_call_id`, `was_truncated`) and a TTL-based lifecycle.
- `session_objects` is generic opaque JSON with session-end cleanup.

Both are session-scoped. They do not share rows or eviction paths.

## Limits / deferred work

- **No in-session LRU.** Each put succeeds regardless of how many prior objects exist in the session (subject only to the per-object 1 MiB cap). Filed as a BLG for when card pipeline cardinality grows.
- **No TTL.** Active-session rows persist until archive. Long-lived sessions will accumulate. Filed as a BLG.
- **No admin surface.** Inspection is via SQL; there is no CLI or HTTP endpoint yet. Card pipeline (CW-20260420-0011) is the only planned consumer; an admin surface can follow if needed.
