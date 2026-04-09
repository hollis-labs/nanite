# Boot Prompt — Plan B: Nanite A2A Session Scoping

> **⚠️ Resuming from a prior session? Read this first.**
>
> Plan B is **partially complete**. Tasks 1–3 landed on branch `feature/a2a-session-scoping` in the worktree at `/Users/chrispian/Projects-apps/nanite-a2a`. The first session stopped at the top of Task 4 to hand off cleanly.
>
> **Before doing anything else, read the handoff doc:** `docs/superpowers/plans/2026-04-09-nanite-a2a-handoff.md`. It lists:
> - The 4 commits already on the branch (with SHAs)
> - **Three plan deviations** the first session already applied (one of which — the `AgentResolver` interface — changes the shape of Tasks 5, 6, 8, 9, 10)
> - Pre-verified facts so you do not waste a subagent re-checking them
> - The exact place to resume (Task 4)
>
> **To resume:**
>
> ```bash
> cd /Users/chrispian/Projects-apps/nanite-a2a
> claude
> ```
>
> Then inside the session:
>
> > Boot nanite-backend. Resume from `docs/superpowers/plans/2026-04-09-nanite-a2a-handoff.md` and start at Task 4.
>
> Do **not** create a new worktree. Do **not** cherry-pick or reset the existing commits. The worktree is persistent and the branch is clean.
>
> ---

Copy this into a fresh Claude Code session (or other CLI agent) started from `~/Projects-apps/nanite/` to execute Plan B in parallel with Plan A.

> **Note for fresh-start sessions only:** If you are starting Plan B from scratch (no prior worktree), follow the instructions below. If the worktree already exists, use the resume instructions above instead.

---

## Context

You are implementing Plan B of a two-plan effort to consolidate the agentrc framework into the Nanite app. Plan A (installer + rollout) is being executed in a separate session; you do **not** need to coordinate with it — the plans are parallel-safe and touch disjoint code paths.

**What Plan B does:**

Extend Nanite's A2A (agent-to-agent) messaging from unconstrained TEXT addressing to session-scoped `(session_id, agent_id)` addressing, add a handoff flow that mutates the existing `session_agents.is_primary` binding in a single transaction, and expose the full surface as CLI + MCP.

The reserved `"user"` sentinel is used to address the human user in a session; Nanite's existing agent IDs (UUIDs for DB agents, `file-<slug>` for file agents) are the canonical addresses on the other side.

## Before doing anything

1. **Read the spec section 2.4** in `docs/superpowers/specs/2026-04-09-nanite-agentrc-consolidation-design.md`. It defines the addressing scheme, schema changes, handoff semantics, and the `"user"` sentinel. It also lists the explicit decisions made during brainstorming — don't re-litigate them.

2. **Read the full plan** in `docs/superpowers/plans/2026-04-09-nanite-a2a-session-scoping.md`. 13 tasks, mostly TDD. Each step has runnable code or a concrete command.

3. **Verify parallel safety with Plan A.** Plan A is working in: `assets/framework/`, `internal/assets/`, `internal/service/install/`, `cmd/nanite/install_cmd.go`, plus MCP tool registrations in `internal/mcp/self_tools_transport.go`.

   Plan B touches: `internal/store/migrations/005_*.sql`, `internal/store/a2a.go`, `internal/store/agents.go` (one small add), `internal/service/a2a/`, `internal/api/a2a.go`, `cmd/nanite/a2a_cmd.go`, `cmd/nanite/main.go` (one-line switch case add), and MCP tool registrations in `internal/mcp/self_tools_transport.go`.

   The **two conflict-prone files** are `cmd/nanite/main.go` (both plans add one `case` to the switch) and `internal/mcp/self_tools_transport.go` (both plans add tool definitions and handlers). These are line-local additions that should merge cleanly with `git merge` or `git rebase`. If a conflict happens at merge time, the resolution is mechanical — keep both sets of additions.

4. **Work in a git worktree** for this plan so Plan A's worktree is unaffected:

   ```bash
   cd ~/Projects-apps/nanite
   git worktree add ../nanite-a2a -b feature/a2a-session-scoping
   cd ../nanite-a2a
   ```

   Execute all tasks inside `~/Projects-apps/nanite-a2a/`. When done, the worktree is merged back to main via normal PR or `git merge`.

## How to execute

Use the **superpowers:subagent-driven-development** skill to run the plan. One subagent per task. Between tasks, review the diff and tests before dispatching the next.

For each task in order:

1. Read the task's **Files** section to know what's being created/modified.
2. Execute each numbered step as-is. Every step has either a code block or a shell command — don't improvise.
3. Run the test commands after each TDD cycle. If a test fails unexpectedly, diagnose before moving on — do not "fix" by changing the test to match incorrect behavior.
4. Commit at the end of each task (the last step of every task has the commit command).

**Task order and dependency:**

- Tasks 1–3 are independent setup (migration, sentinel, validation package). Can run in any order but sequential execution is cleaner.
- Task 4 depends on Task 1 (migration must exist first).
- Tasks 5–7 depend on Task 4 (store layer must be rewritten).
- Task 8 depends on Task 5 (service layer must exist for API handlers).
- Task 9 depends on Task 5.
- Task 10 depends on Tasks 5, 6, and 7 (all service methods must exist).
- Task 11 depends on Tasks 5 + 6 (handoff integration test).
- Task 12 is docs — no code dependency.
- Task 13 is a runbook that depends on everything else + Plan A's Task 16 being done (Nanite itself must be running on the new installer before you run the smoke test).

If you can run any of Tasks 2, 3, or 12 in parallel with the critical path, feel free to dispatch them as sub-sub-agents. Otherwise, serial execution is fine — the plan is short.

## Things to watch for

- **SQLite DROP COLUMN support.** Task 1 uses `ALTER TABLE ... DROP COLUMN`, which requires SQLite 3.35+. If the Nanite binary's bundled SQLite is older, the task has a fallback strategy (rename-table + recreate). Check `go.mod` for the SQLite driver version before running the migration.

- **`store.Store.DB()` accessor.** Task 6's handoff transaction calls `svc.store.DB().BeginTx(...)`. If `Store` doesn't expose `DB()`, either add the method or adapt to use existing transaction helpers. Read `internal/store/store.go` first.

- **Agent ID lookup function name.** Task 3's `ValidateAgentID` calls `s.GetAgentByID(agentID)`. The real function may be named differently (check `internal/store/agents.go`). Use the correct name.

- **Test helpers.** The tests reference `newTestStore`, `newTestStoreWithFileAgents`, and `createTestSession`. These may or may not exist in Nanite's test utilities. If missing, follow the patterns in existing test files (e.g., `internal/store/agents_test.go`) to add them.

- **Subscribe MCP tool is deferred.** Task 10 registers 9 request/response A2A MCP tools but **not** `nanite_a2a_subscribe` — that one requires streaming support in `mcp-go` or a custom server-side handler. The plan documents this; don't try to force it into the `SelfToolsTransport` abstraction. Capture as a follow-up in the spec if you reach Task 10.

- **Don't commit broken intermediate state.** Task 4 temporarily breaks `internal/api/a2a.go` because the store schema changes. The plan recommends fixing `api/a2a.go` in the same task rather than using a build tag workaround — do that.

- **Plan A may drop `install_cmd.go` into `cmd/nanite/` and add cases to `main.go`** while you're also working in `main.go`. When you pull from Plan A's branch before merge, expect a two-line conflict in the switch statement in `main.go`. Keep both cases.

## Stopping conditions

Complete the full plan unless one of these happens:

- **A store layer test fails reproducibly and the root cause is a bug in existing Nanite code (not your test).** Stop, investigate, surface it. Don't paper over with test changes.
- **Plan A's Task 16 isn't done by the time you reach your Task 13.** Skip Task 13 and leave a note — it can run after both plans' code work is done.
- **The SQLite driver doesn't support DROP COLUMN and the fallback also fails.** Stop, report the constraint, ask what direction to take.
- **You discover a design issue in the spec itself** (e.g., the handoff transaction has a race condition the plan didn't anticipate). Stop, write up the issue, don't work around it silently.

## Done criteria

- All 13 tasks have a commit in the branch
- `go build ./...` clean
- `go test ./internal/store/... ./internal/service/a2a/... ./internal/mcp/... -v` all pass
- `./nanite a2a send --help` prints usage
- The smoke test in Task 13 completes successfully (or is explicitly deferred pending Plan A)

Report back with: commit range, test output summary, and any issues found. If Task 13 was deferred, note that explicitly.
