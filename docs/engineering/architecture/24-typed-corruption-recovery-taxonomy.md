# Typed Corruption / Recovery Taxonomy

A follow-up architecture topic from the harness audit: prefer deterministic replay with named inconsistency reasons over heuristic recovery, for conversation-state corruption specifically (missing/duplicate turn transitions, orphan tool results, unresolved calls, invalid transitions, identity mismatch, sequence gaps).

**Status: documented target only. Not decided to implement.** Per "do not generalize until real pressure exists" — no production incident or measured cost currently demonstrates this gap is costing anything. Revisit if/when one does.

## What's already there, and better than expected

`internal/recovery/broker`'s `Classify()` output is genuinely typed: `Class` (`ClassTransient`/`ClassConfigPermissions`/`ClassPermanent`), `Remediation` (four named values), `Action`, `Outcome` — real Go enums with `String()` methods, persisted on a `Breadcrumb`. The decision tree runs over structured input (a typed `ExitError.Cause` enum, exit code, signal, sandbox/MCP-transport state), not string-matching. One heuristic survives inside it (`isAuthFailure`, substring-matching stderr for `"401"`/`"403"`/`"unauthorized"`) — a real, narrow exception, not evidence the whole classifier is heuristic.

Also stale in the prior version of this area's doc, now corrected: the `internal/recovery/*` namespace restructure ([06-session-lifecycle-and-recovery.md](06-session-lifecycle-and-recovery.md) previously described as pending) is already done — `internal/recovery/{broker,orphansweep,pack}/` exists. Event-log postmortem logging has also been extended beyond the Recovery Broker: `orphansweep.SweepOrphans` writes `event_log` rows too. (Recovery Pack and interrupted-turn detection weren't independently re-verified in this pass — treat as unconfirmed, not as still-missing.)

## What's genuinely absent

Conversation-state inconsistency detection — the actual subject of this follow-up topic — doesn't exist anywhere. No detection for orphaned `tool_use` blocks with no matching `tool_result`, duplicate or missing turn transitions, out-of-order sequence numbers, or dangling unresolved calls after a crash. `DetectInterruptedTurn` is a two-fact heuristic (last message role is `user` AND no live in-memory stream) with one fixed reason string (`"service_restart"`) — not an enumerated taxonomy, and not what this follow-up topic is asking for. `compaction.go`'s tool-result deduplication indexes `tool_use`/`tool_result` pairs by content hash, but only to dedupe — it never validates pairing completeness. No sequence-number column exists on `messages` at all.

## Why the peer's exact mechanism doesn't transplant

The peer harness's typed-corruption pattern depends on being event-sourced: an immutable, append-only intent/result log, replayed through a pure reducer that throws a named error on inconsistency. Nanite's persistence is structurally different — direct-write/mutate-in-place. `messages` has a single `content TEXT` column (`UpdateMessageContent` does an in-place `UPDATE`); the `tool_use`/`tool_result` content-block sequence for an HTTP-provider turn is built entirely **in-memory** (`chat_generate.go`'s `chatMessages`) and only the final flattened text ever lands in a row — the discrete step-by-step log the peer's reducer replays simply doesn't exist in Nanite today.

Adopting the peer's mechanism as-is would mean building new durable machinery — a real append-only step log — not writing a validator against today's schema. That's a materially bigger lift than "add a typed error enum," and is exactly why this stays undecided rather than becoming a target design committed to now.

## What the target would look like, if pursued

Documented for reference, not committed: a candidate `Kind` taxonomy for conversation-state inconsistency, in the same enum style the Recovery Broker already uses (`missing_tool_result`, `duplicate_transition`, `orphan_tool_call`, `sequence_gap`, `identity_mismatch`, `invalid_transition`) — each a precondition check a pure function could run against a persisted step log, the same shape as the Recovery Broker's own `Classify()`. This is gated behind the append-only log existing in the first place; there's no meaningful partial version that bolts onto the current mutate-in-place schema.

Filesystem recovery stays a confirmed-separate axis, as already documented ([18-filesystem-snapshots.md](18-filesystem-snapshots.md)) — worth noting it isn't wired into Nanite's own code yet either: `ShadowGit`/`FilesystemSnapshotProvider` lives entirely in the sibling `libs/go-agent-wrapper` repo, with zero references from Nanite's `internal/` today. A status note, not a design change.

## Decision

Document the target shape; do not build it now. No observed corruption incident currently justifies the append-only-log investment this would require. Revisit when one does — the candidate taxonomy above is the starting point, not a spec to implement blind.
