# Export and drop `agent_broker_decisions`

**Phase:** 4
**Status:** not-started
**Depends on:** `02-dispatch-to-agent-reflex-action-kind-and-broker-migration.md` (this table's writer, `attemptBrokerDispatch`/`persistAgentBrokerDecision`, is retired by that task — this task can't drop the table safely until nothing writes to it anymore)
**Touches:** `internal/store/broker_decisions.go` (`InsertAgentBrokerDecision`, `ListRecentAgentBrokerDecisions`), `internal/service/chat_broker_dispatch.go` (`persistAgentBrokerDecision` — should already be gone once task 01 lands; confirm), `cmd/nanite/admin_cmd.go` (the admin CLI subcommand that dumps recent rows), `internal/store/migrations/` (new drop migration for `agent_broker_decisions`)

## Context

`TASKS/ESCALATIONS.md`'s resolved entry "Item 23: `agent_broker_decisions` deferred to Phase 3" states: *"`agent_broker_decisions` excluded from Phase 0, picked up when Phase 3 retires the Agent Broker into reflexes."* Phase 0's `23-export-and-drop-decision-tables.md` already exports and drops `broker_decisions`/`strategy_decisions` — this task does the same thing for the third table in that original TASKS.md item (item 23), which Phase 0 couldn't touch because its writer (the agent broker itself) wasn't retired until this phase. Decision log §14: *"The three now-orphaned `*_decisions` tables (`broker_decisions`, `agent_broker_decisions`, `strategy_decisions`... get their data exported, then dropped."*

### Current state, verified during planning research

- Schema: migration `058_agent_broker_decisions.sql` (goose-renumbered from `057`): `id, session_id, turn_id, user_input_hash, mode_signal, scope_tier, reflex_id, decision, reason, confidence REAL, created_at`. Append-only by convention (no update/delete helpers exist).
- Single write call site: `Store.InsertAgentBrokerDecision` (`internal/store/broker_decisions.go:53`), called only from `persistAgentBrokerDecision` (`internal/service/chat_broker_dispatch.go:448-495`), itself called from `attemptBrokerDispatch` — the exact code path `01-dispatch-to-agent-reflex-action-kind-and-broker-migration.md` deletes.
- Read consumers: `Store.ListRecentAgentBrokerDecisions` (`broker_decisions.go:97`), consumed by a real admin CLI subcommand in `cmd/nanite/admin_cmd.go` that dumps recent rows. **No frontend consumer** — the `agent_broker_decision` SSE event this table's writer also emitted has no FE handler (confirmed dead wire, intentional per the emitting code's own comment: "FE inspector card intentionally out of scope").
- Reuse Phase 0's export mechanism/format from `TASKS/phase-0/23-export-and-drop-decision-tables.md` rather than inventing a new one — same table shape category (append-only decision/reasoning log), same disposal reasoning.

## What to do

1. Confirm task 01 has landed and `persistAgentBrokerDecision`/`attemptBrokerDispatch` no longer exist — `agent_broker_decisions` should have zero live writers before proceeding.
2. Export the full historical contents of `agent_broker_decisions`, using the same export format/location Phase 0's `23-export-and-drop-decision-tables.md` established for `broker_decisions`/`strategy_decisions`.
3. Drop `agent_broker_decisions` via a new goose migration.
4. Remove `Store.InsertAgentBrokerDecision`/`ListRecentAgentBrokerDecisions` and the admin CLI subcommand that reads them (or repoint the admin CLI at the exported archive if that command is still wanted for historical lookups — pick one, document the choice).
5. Confirm the dead `agent_broker_decision` SSE-event plumbing is also removed as part of task 01's broker retirement (not this task's job to re-verify beyond a quick grep — flag if it's somehow still present).

## Done means

- `agent_broker_decisions`'s historical data is exported to the same archive location/format Phase 0 used for the other two decision tables.
- The table is dropped; a fresh migration run produces no `agent_broker_decisions` table.
- No remaining Go code references `InsertAgentBrokerDecision`/`ListRecentAgentBrokerDecisions`/the dropped table.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Tested against a real copy of the backed-up database, not just an empty fixture.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
