# New reflex action kind: `run_python_sandbox`

**Phase:** 2 — Non-agentic reach
**Status:** not-started
**Depends on:** `01` (real: this task's hook calls `service.NewPythonToolDispatcher`, built by
`01`)
**Touches:** `internal/store/agent_reflexes.go` (new `ReflexAction*` constant), `internal/agent/
reflexes/executor.go` (new `HookFunc` type, `Executor` field, `case` branch), new migration
`internal/store/migrations/144_reflex_action_run_python_sandbox.sql` (re-verify the number is
still free before landing — see the batch README's "Migration numbering" section)
**[⚠️ `144` is taken — next free is 148 at `5ec930c8`; re-derive. See the annotation on
"What to do" item 1 below]**,
`internal/api/reflexes.go` (`validateReflexDefinition`'s action-kind switch),
`internal/service/container.go` (`reflexEngine.Executor` wiring, ~lines 950-973), new file
`internal/service/reflex_python_sandbox_hook.go`, `docs/engineering/GLOSSARY.md` (Reflexes entry
touch-up)

## Context

`internal/agent/reflexes.Executor.Apply(ctx, reflex, state)` (`executor.go:44-138`) already has
`state.SessionID` and `reflex.AgentID` available at exactly the point a new action kind's hook
fires (the same point `engine.go:142,145-146` calls into for every existing kind). Adding a new
action kind is a `HookFunc` + `Executor` field + `case` branch — the same shape as the four
existing live hooks (`Halt`/`Schedule`/`SendMessage`, `executor.go:12-27`, plus `dispatch_to_agent`
which deliberately has no hook here — see its own `case` comment, `executor.go`'s
`ReflexActionDispatchToAgent` branch, for why). `TASKS/loops/11-loop-event-predicate-trigger.md`
(adding `resume_loop_run` the same way) is the direct structural precedent this task follows.

**Correction to the outer brief that scoped this batch: this DOES need a real schema migration —
confirm before assuming otherwise.** `TASKS/reflex-taxonomy/` has landed since doc 27 was
originally drafted and is `reviewed` in full (`TASKS/reflex-taxonomy/PHASE-SUMMARY.md`).
`agent_reflexes.action_kind` is no longer a bare CHECK-constrained TEXT column — migration `124`
(`internal/store/migrations/124_reflex_action_taxonomy.sql`) rebuilt it with **both** a widened
CHECK list **and** a real `REFERENCES reflex_action_kinds(name)` FK, and migration `125`
(`125_reflex_action_kind_provenance_allow.sql`) added a presence-based
`reflex_action_kind_provenance_allow (kind_name, tier_name)` join table that gates which
provenance tier may even *declare* a row of a given action kind — enforced live at write time by
`internal/api/reflexes.go`'s `validateReflexDefinition` → `Store.ActionKindAllowsProvenanceTier`
(`internal/store/reflex_taxonomy.go:79-90`, a plain `COUNT(*) > 0` check: **zero rows for a kind
means no tier can ever declare it, full stop**). A new action kind therefore needs: (a) one new
`reflex_action_kinds` row, (b) new `reflex_action_kind_provenance_allow` rows for whichever tiers
may declare it, and (c) an `agent_reflexes` CHECK-widen rebuild — identical in shape to migrations
`119`/`124`'s own rename-recreate-copy precedent (see the illustrative DDL below).

**A second, separate, easily-missed gate**: `internal/api/reflexes.go:317-325`'s
`validateReflexDefinition` has its **own** hardcoded Go-side switch, checked *before* the
provenance-tier lookup:
```go
// internal/api/reflexes.go:317-325
validActionKind := true
switch row.ActionKind {
case store.ReflexActionInjectReminder, store.ReflexActionForceToolChoice,
    store.ReflexActionSendMessage, store.ReflexActionHaltSession, store.ReflexActionAddSchedule,
    store.ReflexActionDispatchToAgent:
default:
    validActionKind = false
    errs = append(errs, fmt.Sprintf("invalid action_kind %q", row.ActionKind))
}
```
Even with the migration and provenance-allow rows in place, a `run_python_sandbox` reflex written
through the CRUD API would still be rejected here with `"invalid action_kind"` unless this switch
also gets the new constant added — a real, necessary edit, not covered by the migration alone.

**Classification for the new `reflex_action_kinds` row** (Facets 1/2/4, `docs/engineering/
architecture/10-reflex-action-taxonomy.md`), this planning session's design call:
- **category = `execute_action`** — firing it runs a script directly with no LLM discretion
  involved (the taxonomy doc's own definition), matching `send_message`/`add_schedule`/
  `halt_session`/`dispatch_to_agent`, not the two `system_message` kinds.
- **combining_algorithm = `all_applicable`** — each firing is an independent, non-contradictory
  side-effecting computation (unlike `force_tool_choice`'s single-directive conflict or
  `halt_session`'s session-ending blast radius), the same reasoning that puts `send_message`/
  `add_schedule` at `all_applicable`.
- **default_recurrence_seconds = `NULL`** (inherit the system default cooldown) — unlike
  `dispatch_to_agent`'s deliberate `0` override (a routing decision needs fresh per-turn
  evaluation), a periodic computation has no equivalent "must re-evaluate every turn" need.
- **Provenance allow-list: all three tiers (`system`/`operator`/`plugin`) allowed, no
  restriction** — the taxonomy doc's two named "obvious candidates" for restriction
  (`halt_session`/`dispatch_to_agent`) are restricted specifically for their blast radius (session
  termination, agent routing); `run_python_sandbox` doesn't share that shape — every `tool_call()`
  it makes is fully permission-gated by task `01`'s fix regardless of who declared the reflex, and
  the sandbox itself is resource/time/network-capped independent of provenance. Adjustable later
  via a plain INSERT/DELETE against `reflex_action_kind_provenance_allow`, per migration `125`'s
  own stated precedent — not locked security policy.

**Wiring location — a real, non-obvious constraint, not a style choice.** `reflexEngine` and its
`Executor` are built and wired entirely inside `internal/service/container.go`'s `NewContainer`
(`container.go:950-973`) — see the existing `Halt`/`Schedule` hook wiring there for the exact
precedent to follow:
```go
// container.go:950-973 (existing)
reflexEngine := reflexes.NewEngine(cfg.Store, slog.Default())
...
reflexEngine.Executor.Halt = func(ctx context.Context, sessionID, reason string, evidence map[string]interface{}) error { ... }
reflexEngine.Executor.Schedule = NewReflexScheduleHook(cfg.Store)
```
This is a **different object graph** from `SelfToolsTransport` (task `01`'s target), which is
built entirely inside `cmd/nanite/main.go`'s `initMCP`, called *before* `NewContainer` even runs —
`container.go` has no reference to `selfTools` and cannot read back the `PythonPermChecker`/
`PythonDispatcher` fields task `01` wires onto it. Instead, this task's hook independently builds
its own `service.NewPythonToolDispatcher(tools)` (the exact same constructor task `01` builds,
reused — not duplicated) against `container.go`'s own local `tools`/`permissions` variables,
already in scope before line 950 (`tools := NewToolService(...)` at `container.go:697`;
`permissions := permission.NewEngine(...)` at `container.go:900`). Two independently-constructed,
behaviorally-identical, stateless adapter instances result (one wired onto `selfTools` by task
`01`, one wired onto `reflexEngine.Executor` by this task) — intentional, not a bug to "simplify"
into one shared field later.

**GLOSSARY touch-up.** `docs/engineering/GLOSSARY.md`'s **Reflexes** entry (line 39) enumerates the
action kinds in prose: "...injects a reminder, nudges a tool choice, sends a message, adds a
schedule, dispatches to another agent (`dispatch_to_agent`), or halts a session." This becomes
stale-by-omission once a seventh kind lands — update the sentence to include
`run_python_sandbox`.

## What to do

> **⚠️ STALE MIGRATION CLAIM — annotated 2026-08-24 at `5ec930c8`.** The `144` in item 1 is
> **taken**: `144_workflow_run_waiting_on_loop_status.sql`, landed by the Loops batch after
> this task file was written. The number below is left in place rather than silently rewritten
> so this correction is visible to whoever picks the task up.
>
> **Next free is 148**, derived at `5ec930c8`:
>
> ```
> $ ls internal/store/migrations/ | sort -t_ -k1 -n | tail -1
> 147_remove_untouched_official_catalog_source.sql
> ```
>
> **Re-derive it again at the moment you write the file — do not carry `148` forward from
> here.** A migration number is claimed by the file existing on `main`, not by a task file
> naming it, and several frozen batches resume in parallel. From a worktree branched before a
> sibling merged, ask `main`:
> `git ls-tree --name-only main -- internal/store/migrations/ | sort -t_ -k1 -n | tail -1`.
>
> **Do not "helpfully" use `135`.** It is an empty slot, and it is unusable: Nanite builds its
> goose provider without `WithAllowOutofOrder` (`internal/store/store.go:153`), so a migration
> numbered below a database's highest applied version aborts `Up` with
> `detected 1 missing (out-of-order) migration lower than database version (…)` and the service
> fails to start. Holes are permanently burned.
>
> Rule: `TASKS/INDEX.md`'s "Migration numbering — the claiming rule" banner;
> `docs/engineering/tracking-integrity.md` check 9.

1. **New migration** `internal/store/migrations/144_reflex_action_run_python_sandbox.sql`
   (re-verify `144` is still free immediately before landing — see the batch README). Illustrative
   DDL, following migration `119`/`124`'s exact rename-recreate-copy pattern for the
   `agent_reflexes` CHECK-widen (the worker documents any deviation):
   ```sql
   -- +goose Up
   INSERT INTO reflex_action_kinds (name, category, combining_algorithm, default_recurrence_seconds)
   VALUES ('run_python_sandbox', 'execute_action', 'all_applicable', NULL);

   INSERT INTO reflex_action_kind_provenance_allow (kind_name, tier_name) VALUES
       ('run_python_sandbox', 'system'),
       ('run_python_sandbox', 'operator'),
       ('run_python_sandbox', 'plugin');

   -- +goose NO TRANSACTION
   PRAGMA foreign_keys = OFF;
   BEGIN;

   CREATE TABLE IF NOT EXISTS agent_reflexes_new (
       id                          TEXT PRIMARY KEY,
       agent_id                    TEXT REFERENCES agent_profiles(id),
       class_tag                   TEXT,
       name                        TEXT NOT NULL,
       trigger_kind                TEXT NOT NULL CHECK (trigger_kind IN ('predicate','event','interval')),
       trigger_spec                TEXT NOT NULL,
       action_kind                 TEXT NOT NULL REFERENCES reflex_action_kinds(name) CHECK (
           action_kind IN ('inject_reminder','force_tool_choice','send_message','halt_session',
                           'add_schedule','dispatch_to_agent','run_python_sandbox')
       ),
       action_spec                 TEXT NOT NULL,
       status                      TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','paused','expired')),
       priority                    INTEGER NOT NULL DEFAULT 0,
       fired_count                 INTEGER NOT NULL DEFAULT 0,
       last_fired_at               TEXT,
       created_at                  TEXT NOT NULL DEFAULT (datetime('now')),
       created_by                  TEXT NOT NULL,
       opt_out_allowed             BOOLEAN NOT NULL DEFAULT TRUE,
       provenance_tier             TEXT NOT NULL DEFAULT 'operator' REFERENCES reflex_provenance_tiers(name),
       recurrence_override_seconds INTEGER
   );

   INSERT INTO agent_reflexes_new SELECT * FROM agent_reflexes;
   DROP TABLE agent_reflexes;
   ALTER TABLE agent_reflexes_new RENAME TO agent_reflexes;

   CREATE INDEX IF NOT EXISTS idx_agent_reflexes_agent ON agent_reflexes(agent_id, status);
   CREATE INDEX IF NOT EXISTS idx_agent_reflexes_class ON agent_reflexes(class_tag, status);

   END;
   PRAGMA foreign_keys = ON;

   -- +goose Down
   -- Mirror migration 124's Down precedent: rebuild agent_reflexes back to the
   -- 6-value CHECK (no run_python_sandbox), then remove this migration's three
   -- rows. Write the full Down block explicitly here, following 124's own Down
   -- section verbatim as the template — do not omit it.
   ```
   Confirm the exact current column list for `agent_reflexes` by reading the live schema (via
   `sqlite3 <db> .schema agent_reflexes` against a real copy, per `EXECUTION-PROCESS.md`'s
   migration-testing discipline) rather than trusting this file's copy verbatim — `INSERT INTO
   agent_reflexes_new SELECT * FROM agent_reflexes` only works if the column order matches exactly.
2. Add a new constant to `internal/store/agent_reflexes.go`'s `ReflexAction*` block (alongside
   `ReflexActionDispatchToAgent`, line 51):
   ```go
   ReflexActionRunPythonSandbox = "run_python_sandbox"
   ```
3. Add `store.ReflexActionRunPythonSandbox` to `internal/api/reflexes.go:319-321`'s
   `validateReflexDefinition` switch case list.
4. In `internal/agent/reflexes/executor.go`, add a new hook type (alongside `HaltHook`/
   `ScheduleHook`/`SendMessageHook`, lines 12-27):
   ```go
   // RunPythonSandboxHook is invoked when a reflex action_kind='run_python_sandbox'
   // fires. sessionID/agentID come from the firing reflex's state/AgentID; spec is
   // the parsed action_spec (expected keys: code, args, time_limit_seconds,
   // memory_limit_mb — the same shape python_run's own tool input takes). Nil = no
   // live run (logged only, matching Schedule/SendMessage's nil-safe convention).
   type RunPythonSandboxHook func(ctx context.Context, sessionID, agentID string, spec map[string]interface{}) error
   ```
   Add a `RunPythonSandbox RunPythonSandboxHook` field to the `Executor` struct, and a `case
   store.ReflexActionRunPythonSandbox:` branch in `Apply` matching `ReflexActionAddSchedule`'s
   exact log-and-continue shape:
   ```go
   case store.ReflexActionRunPythonSandbox:
       if e.RunPythonSandbox != nil {
           if err := e.RunPythonSandbox(ctx, state.SessionID, reflex.AgentID, spec); err != nil {
               e.Logger.Warn("reflex run_python_sandbox hook failed",
                   "reflex", reflex.Name, "err", err)
           }
       }
       return applied, nil
   ```
5. Create `internal/service/reflex_python_sandbox_hook.go` (mirrors `reflex_schedule_hook.go`'s
   shape exactly):
   ```go
   package service

   import (
       "context"
       "fmt"
       "log/slog"

       "github.com/hollis-labs/nanite/internal/agent/reflexes"
       "github.com/hollis-labs/nanite/internal/mcp"
       "github.com/hollis-labs/nanite/internal/permission"
       "github.com/hollis-labs/nanite/internal/selftools"
   )

   // NewReflexRunPythonSandboxHook builds the run_python_sandbox reflex action
   // kind's live hook, reusing the same NewPythonToolDispatcher adapter task 01
   // wires onto SelfToolsTransport — a second, independent instantiation against
   // this package's own locally-scoped tools/perm (container.go has no reference
   // to SelfToolsTransport; see this task's own Context for why).
   func NewReflexRunPythonSandboxHook(tools ToolService, perm *permission.Engine) reflexes.RunPythonSandboxHook {
       dispatcher := NewPythonToolDispatcher(tools)
       return func(ctx context.Context, sessionID, agentID string, spec map[string]interface{}) error {
           code, _ := spec["code"].(string)
           if code == "" {
               return fmt.Errorf("run_python_sandbox: action_spec.code is required")
           }
           var args map[string]any
           if raw, ok := spec["args"].(map[string]any); ok {
               args = raw
           }
           timeLimitSec := mcp.IntArg(spec, "time_limit_seconds", 0)
           memLimitMB := mcp.IntArg(spec, "memory_limit_mb", 0)

           ctx = mcp.WithSessionID(ctx, sessionID)
           ctx = mcp.WithCallerProfile(ctx, agentID)

           result, err := selftools.RunPythonSandbox(ctx, sessionID, code, args, timeLimitSec, memLimitMB, perm, dispatcher)
           if err != nil {
               return err
           }
           if result.Error != "" {
               return fmt.Errorf("run_python_sandbox: %s", result.Error)
           }
           slog.Info("reflex run_python_sandbox: completed", "session_id", sessionID)
           return nil
       }
   }
   ```
   Confirm `mcp.IntArg`'s exact signature (used already by `callRunPython`, `self_tools_python.go`
   — reuse it rather than writing a second int-extraction helper) and adjust if it doesn't accept
   a bare `map[string]interface{}` the way `action_spec` decodes to.
6. In `internal/service/container.go`, alongside the existing `Schedule` hook wiring
   (`container.go:973`), add:
   ```go
   reflexEngine.Executor.RunPythonSandbox = NewReflexRunPythonSandboxHook(tools, permissions)
   ```
   (`tools` and `permissions` are the same local variables already in scope from lines 697/900.)
7. Update `docs/engineering/GLOSSARY.md`'s **Reflexes** entry (line 39) to include
   `run_python_sandbox` in its prose list of action kinds.

## Done means

- `run_python_sandbox` is a real, migration-backed, registered action kind: present in
  `reflex_action_kinds`, allowed for all three provenance tiers in
  `reflex_action_kind_provenance_allow`, accepted by both `agent_reflexes`'s CHECK/FK and
  `internal/api/reflexes.go`'s Go-side switch.
- `reflexes.Resolve()` (`internal/agent/reflexes/resolve.go`) evaluates a firing
  `run_python_sandbox` reflex exactly like the other six kinds — no special-casing needed there
  (confirm this by reading `Resolve`, not by assuming; it resolves `combining_algorithm` generically
  via `kindLookup`).
- End-to-end test: seed (or directly insert) an `agent_reflexes` row with `action_kind =
  'run_python_sandbox'`, a predicate or event trigger that becomes true, and an `action_spec`
  containing a trivial script; drive it through `Engine.EvaluateState`/`Resolve` and confirm
  `Executor.RunPythonSandbox` is genuinely invoked and the script genuinely runs (via the real
  `RunPythonSandbox` call, not a test that manually invokes the hook function directly bypassing
  `Resolve`).
- `docs/engineering/GLOSSARY.md`'s Reflexes entry lists `run_python_sandbox`.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass. If feasible, test the migration
  against a real copy of a backed-up database (`~/.local/share/nanite/workspaces/default/
  backups/`) per `EXECUTION-PROCESS.md`'s migration-testing discipline, not just an empty fixture.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
