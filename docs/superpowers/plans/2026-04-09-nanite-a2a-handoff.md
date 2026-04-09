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

> Boot nanite-backend and see `docs/superpowers/plans/2026-04-09-nanite-a2a-boot-prompt.md`. Resume from the handoff at `docs/superpowers/plans/2026-04-09-nanite-a2a-handoff.md`. Start at Task 6.

The worktree already exists and is persistent — do **not** create a new one. Plan A continues to run in a separate worktree at `../nanite-install-and-rollout` and does not affect this branch.

Use `superpowers:subagent-driven-development` to execute remaining tasks. Serial dispatch (one implementer at a time) — the skill's red flags explicitly forbid parallel implementers.

---

## State at handoff

7 commits ahead of `main` (`be14204`). Working tree clean.

```
59b163e fix(a2a): wrap Ack/Resolve errors, stable thread ordering
721ab75 feat(a2a): service layer for send/inbox/thread/ack/resolve
b897f35 refactor(store,api): A2A session-scoped addressing
4887385 docs: handoff for Plan B resumption after tasks 1-3
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
| 4 | Rewrite `internal/store/a2a.go` for new schema | ✅ done | `b897f35` |
| 5 | Service layer basics (send/inbox/thread/ack/resolve) | ✅ done (deviation 3 applied) | `721ab75`, `59b163e` |
| 6 | Handoff transaction | ⏭ **next** — needs `Store.DB()` accessor or `WithTx` helper | — |
| 7 | Subscribe/pubsub for MCP streaming | pending — stub exists in `subscribe.go`, replace wholesale | — |
| 8 | Rewrite `internal/api/a2a.go` | pending — partially done in Task 4; Task 8 routes through `a2a.Service` for validation | — |
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

### Deviation 4 — `A2AStore` interface stays store-concrete in the service

**Where:** `internal/service/store.go:143-151` (the `A2AStore` interface) and `internal/service/a2a/service.go:18` (the `Service` struct).

**What changed:** The plan's Task 5 code snippet and the code reviewer's Important issue both assumed `GetA2AMessage` / `GetA2ARecent` should be added to the `A2AStore` interface for Task 5 and later. They are NOT. The `a2a.Service` holds a `*store.Store` (concrete type) rather than an `A2AStore` interface, so it can call the new methods directly without widening the shared interface. This matches the plan's struct definition at plan lines 785–789 (`store *store.Store`, not `store A2AStore`).

**Why:** The `A2AStore` interface in `internal/service/store.go` exists for the chat engine's `StoreInterface` composition — it's a test-seam for the engine layer, not a general-purpose boundary. The new `a2a.Service` is its own module and doesn't participate in that seam. Keeping the interface narrow avoids YAGNI scope creep into unrelated code.

**How it propagates:** When Task 6 (handoff) or Task 11 (integration test) need to mock the store, they mock `*store.Store` via `newTestA2AStore(t)`/`newTestStore(t)` — a real in-memory SQLite DB — not a stub implementing `A2AStore`. No changes to `internal/service/store.go` or `internal/service/chat_test.go`'s `stubA2AStore` beyond what Task 4 already did (`SendA2AMessage` return type, `GetA2AInbox` tri-arg, `A2AUnreadCount` bi-arg).

### Deviation 5 — `GetA2AThread` now has a `rowid ASC` tiebreak

**Where:** `internal/store/a2a.go` `GetA2AThread` (commit `59b163e`).

**What changed:** Added `rowid ASC` as a secondary sort key on the existing `ORDER BY created_at ASC` clause. Matches the pattern already in `GetA2ARecent` from Task 4.

**Why:** RFC3339 second resolution causes same-tick inserts to tie. Without a deterministic tiebreak, `TestService_Thread` had to sleep 1.1s between inserts (3.3s total) to get reliable ordering. The rowid fallback is cheap and semantically correct — rowid is monotonic per insert — and removes the sleep from the unit test.

**How it propagates:** None. This is a local store-layer fix; no call-site changes needed. Thread ordering is now stable regardless of timestamp granularity.

---

## Pre-verified facts (don't re-check)

Everything below was verified against `be14204` (baseline) and updated through `59b163e`:

- **SQLite driver:** `modernc.org/sqlite v1.48.1` — supports `ALTER TABLE ... DROP COLUMN`. No fallback strategy needed.
- **Real store agent-lookup method:** `GetAgent(id string) (*AgentProfile, error)` at `internal/store/agents.go:94` — **not** `GetAgentByID` as the plan assumed.
- **`CreateAgent` signature:** `func (s *Store) CreateAgent(a *AgentProfile) error` — returns `error` only, **not** `(*AgentProfile, error)`. Task 2 already adapted test snippets accordingly; apply the same correction if future tasks use `CreateAgent` in test setup.
- **`Store.DB`** is an **exported struct field** on `*Store`, not a method. See `internal/store/store.go:20-21`. Task 6 should do `svc.store.DB.BeginTx(ctx, nil)` directly. The handoff's earlier claim that a `DB()` method needs to be added is wrong — the field is already accessible.
- **`a2a.Service` fields:** `store *store.Store`, `resolver AgentResolver`, `pub *pubsub`. Constructor: `NewService(s *store.Store, r AgentResolver) *Service`. Task 6's handoff methods attach to `*Service` and can use `svc.store.DB.BeginTx(...)`.
- **Existing store methods available to the service** (Task 4 added these on top of the pre-existing ones): `SendA2AMessage(*A2AMessage) (*A2AMessage, error)`, `GetA2AMessage(id) (*A2AMessage, error)`, `GetA2AInbox(sessionID, agentID, status)`, `GetA2AThread(threadID)`, `GetA2ARecent(sessionID, limit)`, `AckA2AMessage(id)`, `ResolveA2AMessage(id)`, `A2AUnreadCount(sessionID, agentID)`.
- **`GetA2AThread` ordering** is now deterministic: `ORDER BY created_at ASC, rowid ASC` (Deviation 5). Tests do not need to sleep between inserts.
- **File agents:** not in the DB. See Deviation 3 above. The in-memory registry lives at `internal/service/agent.go` via `agentServiceImpl.fileDefs []*agent.Definition`. Discovery entry point is `agent.Discover()` in `internal/agent/`.
- **`AgentResolver` interface consumers already exist:** `internal/service.AgentService.Get(ctx, id)` at `internal/service/agent.go:73` structurally satisfies `a2a.AgentResolver`. Task 8/9/10 will wire the concrete `AgentService` into `NewService(store, agentService)` at the HTTP/CLI/MCP boundary.
- **Session-agent binding:** the `session_agents` junction table already exists (migrated pre-Task-1). Task 6's handoff transaction mutates `is_primary` on that table. Schema: check `internal/store/migrations/001_schema.sql` or equivalent for exact column names.
- **`session_handoffs` audit table:** created by migration 005 (`50a22d8`). Columns: `id`, `session_id`, `from_agent_id`, `to_agent_id`, `requested_by`, `status` (pending|approved|rejected|completed), `requested_at`, `approved_at`, `approved_by_user`, `context_message_count`, `notes`. Task 6 inserts into this table inside the same transaction that mutates `session_agents.is_primary`.
- **File agent ID helpers:** `agent.IsFileBasedID(id)` and `agent.SlugFromFileID(id)` already exist in `internal/agent/convert.go` — use them rather than inlining `strings.HasPrefix(id, "file-")` checks.
- **Test helper pattern for `internal/service/a2a/`:** use `newTestA2AStore(t)` (in `service_test.go`) because `store.newTestStore` is unexported. Use `newFakeResolver(ids...)` (in `validate_test.go`) for the resolver — do NOT re-define either helper in Task 6's test file; both are package-local and accessible.
- **Plan A worktree:** `/Users/chrispian/Projects-apps/nanite-install-and-rollout` on `feature/nanite-install-and-rollout`. Plan A touches `assets/framework/`, `internal/assets/`, `internal/service/install/`, `cmd/nanite/install_cmd.go`, plus MCP tool registrations. The two files that may conflict at merge time are `cmd/nanite/main.go` (both plans add a `case` to the command switch) and `internal/mcp/self_tools_transport.go` (both plans add tool definitions and handlers). Resolutions are mechanical — keep both sets of additions.
- **Pre-existing build failure:** `go build ./plugins/support-ticket/...` fails on `be14204` with a missing module error. Still present on `59b163e`. `cmd/nanite` transitively imports `internal/plugin/allplugins` which transitively imports it, so `go build ./...` will fail with this one error. This is **not** something Plan B caused and is **not** something Plan B must fix. Explicit package builds (`go build ./internal/...`) work cleanly.
- **Code review follow-ups from Task 5** (not blocking, noted for future polish):
  - `GetA2AInbox` priority ordering (`ORDER BY priority DESC, created_at ASC`) has no explicit test. Add one when convenient.
  - `GetA2ARecent` multi-session test case (seed `sess-2` and confirm it's excluded) would harden the `WHERE from_session_id = ? OR to_session_id = ?` clause.
  - `GetA2AMessage` wraps `sql.ErrNoRows` via `err == sql.ErrNoRows`; `errors.Is(err, sql.ErrNoRows)` would be more future-proof.
  - `Ack`/`Resolve` do not verify the caller actually owns the binding (impersonation gap). Comment marks this as MVP permissive; add a `TODO(a2a):` marker before GA.

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
