# Cut dead messaging/plugin remnants (18b — messaging + plugin sub-parts of item 18)

**Phase:** 0
**Status:** implemented
**Depends on:** none
**Touches:** `internal/messaging/gomsg/` (entire package — delete), a new migration dropping `messaging_envelopes`, `internal/store/migrations/064_messaging_envelopes.sql` (read-only reference), `internal/store/agent_mailbox_view.go`-equivalent (delete, if it exists as a separate file — verify exact location during implementation), a new migration dropping `agent_mailbox_view`, `internal/store/migrations/076_agent_mailbox_view.sql` (read-only reference), `internal/api/triggers.go`, `internal/api/actions.go`, `internal/api/api.go` (route removal — Part B only, pending verification), `internal/store/*.go` (trigger_rules/custom_actions store layer — Part B only, pending verification)

## Context

TASKS.md Phase 0 item 18 (partial — this file covers the messaging/plugin sub-parts NOT covered by `18a-cut-dead-storage-and-config.md`): "`internal/messaging/gomsg`, ..., the unscheduled known-tools/skills TTL reaper, ..., `agent_mailbox_view`, `trigger_rules`, `custom_actions`, ... — verify each against current code before cutting, not just this list." Decision log §8 (standing dead-code policy) retroactively strengthens all of these into "cut," not "flag and revisit." Decision log §27/§30 cover `gomsg`/`agent_mailbox_view` specifically (architecture doc `07-inter-agent-messaging.md`'s "Cut" section). Decision log §38a covers `trigger_rules`/`custom_actions` (architecture doc `09-plugin-system.md`'s "Cut" section).

**Every sub-item below was independently re-verified against the current tree, per `EXECUTION-PROCESS.md`'s "verify each against current code before cutting" instruction. Two sub-items (Part A) confirm cleanly. Two sub-items (Part B) come back with a real, material correction to the docs' "zero-caller" characterization — read Part B carefully before executing it.**

## Part A — `internal/messaging/gomsg` and `agent_mailbox_view`: confirmed dead, clean cut

### `internal/messaging/gomsg`

Decision log §27, architecture doc `07-inter-agent-messaging.md`: "`internal/messaging/gomsg` (`messaging_envelopes` table) — fully built, contract-tested, never constructed anywhere the process boots." Verified: `NewSQLStore`/`NewRouter` (defined in that package's `sqlstore.go`/`federation.go`) are called only from the package's own test files — zero production construction call sites anywhere in the codebase. Backing table `messaging_envelopes`, created by migration `064_messaging_envelopes.sql`. This confirms the decision log exactly — clean, no-mismatch cut.

### `agent_mailbox_view`

Decision log §30: "Zero call sites anywhere in the codebase (not even in `gomsg`), and its own migration comment attributes it to `internal/composer/source_mail.go`, a file that doesn't exist in the current tree." Verified: `internal/composer/` does not exist anywhere in this repo (the entire directory, not just the named file). Zero Go references to `agent_mailbox_view` found anywhere outside migration `076_agent_mailbox_view.sql` itself. Confirms the decision log exactly — clean, no-mismatch cut.

### What to do (Part A)

1. Delete `internal/messaging/gomsg/` in full.
2. Add a migration dropping `messaging_envelopes` (`DROP TABLE IF EXISTS messaging_envelopes;`).
3. Find and remove whatever store-layer code creates/references `agent_mailbox_view` (locate the exact file at implementation time — not pinned down precisely in this planning pass beyond confirming the migration and zero-caller status).
4. Add a migration dropping `agent_mailbox_view` (it may be a SQL `VIEW`, not a table — confirm which and use the correct `DROP VIEW IF EXISTS` or `DROP TABLE IF EXISTS` accordingly).
5. Determine the next available migration number at implementation time (check `internal/store/migrations/`; use `09-adopt-goose-migrations`'s format if it has landed by then).

## Part B — `trigger_rules` and `custom_actions`: real doc/reality mismatch, cut anyway (operator-confirmed)

Decision log §38a: "`trigger_rules` (an early version of what became reflexes) and `custom_actions` (meant to be slash-command-triggered UI actions) — both zero-caller, zero-row. Whatever real need either was reaching for is served by reflexes and the existing, real plugin `commands[]` registration going forward."

**"Zero-caller" does not hold at the HTTP-routing layer — verified directly during planning.**

- **`trigger_rules`**: `internal/api/triggers.go` has 5 full CRUD handlers, registered at `internal/api/api.go` (~lines 418-423, under `/api/plugins/triggers*`), backed by real store calls (not stubs returning empty/not-implemented).
- **`custom_actions`**: `internal/api/actions.go` has 6 handlers, including a real `POST /api/actions/{id}/execute` — an actual execution endpoint, not just CRUD — plus slash-command registration logic that fires on create. Registered at `internal/api/api.go` (~lines 434-439).

**Operator decision, 2026-08-18 — final: these are being deleted on purpose per the original design-review decision, live REST handlers included. Delete both tables and their full API surface outright — no verify-then-maybe-escalate step.** Per the sharpened `docs/engineering/EXECUTION-PROCESS.md` escalation rule, `TASKS.md`'s decided action stands regardless of whether "zero-caller" holds up literally — a registered-but-real HTTP surface with no confirmed frontend consumer is not grounds to stop and re-litigate the decision.

## What to do (Part B)

1. Delete `internal/api/triggers.go` and `internal/api/actions.go` in full.
2. Remove their route registrations from `internal/api/api.go` (`/api/plugins/triggers*`, ~lines 418-423; `/api/actions*`, ~lines 434-439).
3. Delete the corresponding store-layer CRUD for both tables (locate at implementation time).
4. Grep `ui/src/` for any caller of these routes and remove it if found (record in the Work Log whether anything was actually found — this is useful information even though it doesn't change the outcome).
5. Add a migration dropping both `trigger_rules` and `custom_actions`.
6. Determine the next available migration number at implementation time, consistent with Part A.

## Done means

- `internal/messaging/gomsg/` and `agent_mailbox_view` no longer exist; `messaging_envelopes` table dropped.
- `trigger_rules`/`custom_actions` and their full API surface (`internal/api/triggers.go`/`internal/api/actions.go`) no longer exist.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Migration(s) tested against a real copy of the backed-up database, not just an empty fixture.

## Work log

**2026-08-18 — worker report:** Part A: `internal/messaging/gomsg/` deleted in full, plus `envelope_bridge.go`/`_test.go` (a real gomsg dependent, itself dead — zero callers, scope expansion per worker step 7). `agent_mailbox_view` confirmed a table (not view) with zero Go references. Part B: deleted `internal/api/triggers.go`/`actions.go`, their routes/types, and the `trigger_rules`/`custom_actions` store layer. Found and removed two live things beyond the task's own Touches list: `internal/plugin/triggers.go`'s `TriggerDispatcher` (a real, wired-in event→connector dispatch engine called from every `Host.EmitEvent`) and `useKeyboardShortcuts.ts`'s `useActionKeybindings` (a live, app-wide-mounted `custom_actions` consumer independent of the Settings `ActionsPanel`). Three migrations added (goose format, real tested Down sections), renumbered by the Orchestrator from 095-097 to 097-099 to avoid colliding with tasks 19/30. Verified against a real copy of the production backup: all four tables dropped cleanly, sentinel row counts unchanged, integrity check clean. `go build`/`go vet`/`go test` all pass (only pre-existing unrelated findings). No blocking escalations.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
