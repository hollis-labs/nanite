# Handoff — Nanite A2A Session Scoping (Plan B)

**Date:** 2026-04-09
**Branch:** `feature/a2a-session-scoping`
**Worktree:** `/Users/chrispian/Projects-apps/nanite-a2a`
**Parent boot prompt:** `docs/superpowers/plans/2026-04-09-nanite-a2a-boot-prompt.md`
**Plan:** `docs/superpowers/plans/2026-04-09-nanite-a2a-session-scoping.md`
**Spec:** `docs/superpowers/specs/2026-04-09-nanite-agentrc-consolidation-design.md` (§2.4)

---

## How to resume

Open a fresh Claude Code session with the worktree as the working directory:

```bash
cd /Users/chrispian/Projects-apps/nanite-a2a
claude
```

Then inside the session:

> Boot nanite-backend and see `docs/superpowers/plans/2026-04-09-nanite-a2a-boot-prompt.md`. Resume from the handoff at `docs/superpowers/plans/2026-04-09-nanite-a2a-handoff.md`. Start at Task 4.

The worktree already exists and is persistent — do **not** create a new one. Plan A continues to run in a separate worktree at `../nanite-install-and-rollout` and does not affect this branch.

Use `superpowers:subagent-driven-development` to execute remaining tasks. Serial dispatch (one implementer at a time) — the skill's red flags explicitly forbid parallel implementers.

---

## State at handoff

4 commits ahead of `main` (`be14204`). Working tree clean.

```
a54a5c2 feat(a2a): agent ID validation with user sentinel
ccbec70 feat(store): reject reserved 'user' slug/id in agent creation
453f9a5 fix(store): scope DROP COLUMN suppression and clean up test imports
50a22d8 feat(store): 005 A2A session scoping migration
```

| # | Task | Status | Commits |
|---|---|---|---|
| 1 | Schema migration 005 | ✅ done | `50a22d8`, `453f9a5` |
| 2 | `"user"` sentinel rejection in `CreateAgent` | ✅ done | `ccbec70` |
| 3 | Agent ID validator (`internal/service/a2a/validate.go`) | ✅ done (with plan deviation) | `a54a5c2` |
| 4 | Rewrite `internal/store/a2a.go` for new schema | ⏭ **next** | — |
| 5 | Service layer basics (send/inbox/thread/ack/resolve) | pending — signature deviation required | — |
| 6 | Handoff transaction | pending — needs `Store.DB()` accessor | — |
| 7 | Subscribe/pubsub for MCP streaming | pending | — |
| 8 | Rewrite `internal/api/a2a.go` | pending — partially touched in Task 4 | — |
| 9 | `nanite a2a` CLI command | pending | — |
| 10 | Register A2A MCP tools | pending (9 req/resp; `a2a_subscribe` deferred per plan) | — |
| 11 | Handoff round-trip integration test | pending | — |
| 12 | User-facing docs | pending | — |
| 13 | Phase 4b smoke test on Nanite | pending — depends on Plan A Task 16 |

---

## Deviations from the plan — propagate these forward

### Deviation 1 — Migration runner error suppression scope

**Where:** `internal/store/store.go:105-117` (in `runMigrations` or equivalent — read the file for current layout).

**What changed:** Beyond suppressing the existing `"duplicate column"` error (idempotency for `ADD COLUMN` re-runs), the runner now also suppresses `"no such column"` **but only on `DROP COLUMN` and `CREATE INDEX` DDL statements**. It does **not** suppress it on DML (SELECT/UPDATE/INSERT/DELETE), so typos in future migration DML will still surface.

**Why:** Migration 005 drops `to_agent` and `from_agent` from `a2a_messages`. On the second boot after applying 005, the runner replays migration 001, which contains `CREATE INDEX IF NOT EXISTS idx_a2a_inbox ON a2a_messages(to_agent, status, created_at DESC)`. Even with `IF NOT EXISTS`, SQLite still resolves the column names and fails with `"no such column: to_agent"` when `to_agent` is gone. The narrow suppression is what makes the restart idempotent.

**Architectural note for future work:** The migration runner re-executes every migration on every boot and relies on error suppression for idempotency. This is a pre-existing pattern, not introduced by Plan B. A proper versioned-migration table would be a cleaner fix and belongs in its own spec.

### Deviation 2 — Migration 005 drops `idx_a2a_inbox` explicitly

**Where:** `internal/store/migrations/005_a2a_session_scoping.sql`, first statement.

**What changed:** The migration starts with `DROP INDEX IF EXISTS idx_a2a_inbox;` before the `DROP COLUMN` statements. The spec §2.4 did not include this.

**Why:** SQLite refuses to `DROP COLUMN to_agent` while any index references that column. `idx_a2a_inbox` from migration 001 covers `(to_agent, status, created_at DESC)`, so it must be dropped first. The new `idx_a2a_to_session_agent` takes over the inbox query role.

### Deviation 3 — `ValidateAgentID` takes an `AgentResolver` interface, not `*store.Store`

**This is the important one. It affects Tasks 5, 6, 8, 9, 10.**

**Where:** `internal/service/a2a/validate.go` (new file, committed in `a54a5c2`).

**What changed:**

```go
// What the plan sketched (WRONG for Nanite's architecture):
func ValidateAgentID(s *store.Store, agentID string) error { ... }

// What's actually implemented:
type AgentResolver interface {
    Get(ctx context.Context, id string) (*store.AgentProfile, error)
}
func ValidateAgentID(ctx context.Context, r AgentResolver, agentID string) error { ... }
```

**Why:** File agents (the ones with `file-<slug>` IDs) are not stored in the database. They are discovered from disk at bootstrap by `internal/agent/discovery.go`, held in memory as `[]*agent.Definition`, and resolved at the service layer only. The existing `service.AgentService.Get(ctx, id)` already handles the file-agent-first lookup (`internal/service/agent.go:73-85`) and satisfies the `AgentResolver` interface structurally.

`internal/service/a2a` is a **child package** of `internal/service`, so it cannot import `service.AgentService` directly (Go forbids child→parent imports). The interface is defined locally in `a2a` and the parent package wires the concrete `AgentService` in as a resolver.

**How this propagates through the remaining tasks:**

- **Task 5** — `NewService(s *store.Store)` becomes `NewService(s *store.Store, r AgentResolver)`. The plan's `ValidateAgentID(svc.store, msg.FromAgentID)` call sites all change to `ValidateAgentID(ctx, svc.resolver, msg.FromAgentID)`. The `a2a.Service` struct holds both the store (for A2A table CRUD) and the resolver (for validation).
- **Task 5 tests** — The plan says *"newTestStore must set up a backend file agent so file-backend validates as a known agent"*. Use the `fakeResolver` pattern from `validate_test.go` instead. The `a2a` package tests should not try to seed the `agent_profiles` table with fake `file-<slug>` rows — that does not match production.
- **Task 6** — `RequestHandoff` / `ApproveHandoff` validation uses the same pattern; the `a2a.Service` already holds the resolver, no new wiring.
- **Task 8** — `internal/api/a2a.go` handlers need access to the `a2a.Service`, which needs a resolver at construction. The HTTP server's service wiring (wherever it lives — likely `cmd/nanite/main.go` or a container) must pass the `AgentService` as the resolver.
- **Task 9** — CLI commands construct a transient `a2a.Service`. The CLI path already has the agent service available through the existing service container; pass it as the resolver.
- **Task 10** — Same wiring at the MCP tool registration layer in `internal/mcp/self_tools_transport.go`.

**Commit message on `a54a5c2` documents this deviation** — read it before touching Tasks 5–10.

---

## Pre-verified facts (don't re-check)

Everything below was verified against `be14204`:

- **SQLite driver:** `modernc.org/sqlite v1.48.1` — supports `ALTER TABLE ... DROP COLUMN`. No fallback strategy needed.
- **Real store agent-lookup method:** `GetAgent(id string) (*AgentProfile, error)` at `internal/store/agents.go:94` — **not** `GetAgentByID` as the plan assumed.
- **`CreateAgent` signature:** `func (s *Store) CreateAgent(a *AgentProfile) error` — returns `error` only, **not** `(*AgentProfile, error)`. Task 2 already adapted test snippets accordingly; apply the same correction if future tasks use `CreateAgent` in test setup.
- **`Store.DB()` accessor:** **Does not exist.** Only `DBPath()` is exposed at `internal/store/store.go:25`. Task 6 needs `svc.store.DB().BeginTx(...)` per the plan — you'll need to either add a `DB()` method or find/add a `WithTx(func(*sql.Tx) error)` helper. Check `internal/store/store.go` for existing transaction helpers first before adding new ones.
- **File agents:** not in the DB. See Deviation 3 above. The in-memory registry lives at `internal/service/agent.go` via `agentServiceImpl.fileDefs []*agent.Definition`. Discovery entry point is `agent.Discover()` in `internal/agent/`.
- **File agent ID helpers:** `agent.IsFileBasedID(id)` and `agent.SlugFromFileID(id)` already exist in `internal/agent/convert.go` — use them rather than inlining `strings.HasPrefix(id, "file-")` checks.
- **Plan A worktree:** `/Users/chrispian/Projects-apps/nanite-install-and-rollout` on `feature/nanite-install-and-rollout`. Plan A touches `assets/framework/`, `internal/assets/`, `internal/service/install/`, `cmd/nanite/install_cmd.go`, plus MCP tool registrations. The two files that may conflict at merge time are `cmd/nanite/main.go` (both plans add a `case` to the command switch) and `internal/mcp/self_tools_transport.go` (both plans add tool definitions and handlers). Resolutions are mechanical — keep both sets of additions.
- **Pre-existing build failure:** `go build ./plugins/support-ticket/...` fails on `be14204` with a missing module error. It is **not** something Plan B caused and is **not** something Plan B must fix. When you run `go build ./...` and see this error, it is pre-existing.

---

## Stopping conditions for the resumed session

The original boot prompt's stopping conditions still apply. Summarized:

- Store-layer test fails reproducibly due to an existing Nanite bug (not a test bug) → stop, investigate, surface.
- Plan A's Task 16 not landed by the time you reach your Task 13 → skip Task 13 with an explicit note.
- You discover a new design gap in the spec or plan that affects architecture → stop, write it up in a new deviation entry in this handoff doc, ask before working around it.
- `go build ./...` fails on anything other than the pre-existing `plugins/support-ticket` failure → stop, investigate.

---

## Done criteria (unchanged from original boot prompt)

- All 13 tasks have a commit on `feature/a2a-session-scoping`
- `go build ./...` clean (modulo the pre-existing `plugins/support-ticket` failure)
- `go test ./internal/store/... ./internal/service/a2a/... ./internal/mcp/... -count=1 -v` all pass
- `./nanite a2a send --help` prints usage
- The Task 13 smoke test completes successfully, or is explicitly deferred pending Plan A's Task 16

Report back with: commit range, test output summary, any new deviations, any issues found.
