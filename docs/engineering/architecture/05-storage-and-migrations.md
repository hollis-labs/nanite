# Storage & Migrations

A single SQLite database file (WAL mode), opened once per process, is the entire persistence layer. No separate read replica, no external database server.

## Migrations: adopting a real ledger

There is currently no `schema_migrations` table — every migration file re-runs in full on every boot, idempotency achieved by swallowing specific SQL errors. This already caused one real production crash-loop (three migrations independently rebuilding the same table from their own historical schema, each unaware of what a later migration had added). The fix applied at the time (`migrate:skip-if-column-exists`) was scoped to only the three migrations involved — the identical pattern exists unguarded elsewhere.

**Decision: adopt `pressly/goose`** rather than build custom migration infrastructure. Real version tracking, supports embedded SQL files, SQLite support, up/down rollback. This is a well-solved problem in the Go ecosystem — not a gap worth filling with bespoke tooling.

## Decision/telemetry tables

`event_log` (general-purpose, `event_type`/`category` discriminator + a flexible `metadata` JSON blob) is the right home for "why did the system do X" capture going forward — including the reflex-dispatch reasoning steering now needs. No new polymorphic table required. The three now-orphaned `*_decisions` tables (`broker_decisions`, `agent_broker_decisions`, `strategy_decisions` — all losing their writers once the steering consolidation lands) get their data exported, then dropped.

## "Workspace" — both concepts retired

The in-app `workspaces` table (UI session-grouping, never used) is retired. The filesystem-level `NANITE_WORKSPACE` multi-database mechanism is also retired as a distinct concept — see `GLOSSARY.md`'s **Instance** entry for what survives (a simpler, direct DB-path override, kept for dev/test isolation) and what doesn't (the multi-instance layer on top of it). A separate-DB-per-instance mechanism and `consumer_id` tagging (see [Agent Construction](01-agent-construction.md)) solve different problems — not steps on the same path. `consumer_id` was chosen specifically to avoid the cost a separate-instance-per-consumer model would reintroduce.

## Confirmed dead, cut

`workflows` table (zero rows, zero code path anywhere reads/writes it — `workflow_runs`/`workflow_run_steps` are what the real workflow feature uses), `session_stats` (zero rows, no live call site, schema registered outside the migration ledger entirely), `agent_messages_legacy_089`/`todos_legacy_d1` (rename-artifact tables from the pre-ledger migration pattern, safely droppable once the real ledger lands).

## Everything else

Most of the remaining zero-row tables get resolved as part of whichever subsystem doc they actually belong to (session lifecycle, messaging, plugins) rather than as a separate storage sweep.
