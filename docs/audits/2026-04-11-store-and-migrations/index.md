# Deep Review: store-and-migrations

**Date:** 2026-04-11
**Reviewer:** nanite-reviewer-backend (deep-review)
**Branch:** audit-campaign-2026-04-11

## Scope

**Scope string:** `store-and-migrations`
**Interpretation:** Subsystem review of `internal/store/` -- all 24 Go source files (excluding 15 test files read for coverage context), 5 SQL migration files in `internal/store/migrations/`, and `seed.go`.

**Files read in full:**
- `internal/store/store.go` -- DB init, WAL config, migration runner, splitSQL
- `internal/store/seed.go` -- Seed() + SeedProviders()
- `internal/store/sessions.go` -- Session/Message CRUD, ForkSession, CopyMessages, SearchMessages
- `internal/store/agents.go` -- AgentProfile CRUD, DeleteAgent, session agents
- `internal/store/user_settings.go` -- singleton user settings, ext_settings JSON
- `internal/store/plugin_settings.go` -- per-plugin settings CRUD
- `internal/store/usage.go` -- token usage recording, model pricing
- `internal/store/a2a.go` -- A2A messaging CRUD
- `internal/store/skills.go` -- Skill CRUD, agent-skill bindings, builtin seed
- `internal/store/modes.go` -- Mode CRUD, agent-mode assignments, builtin seed
- `internal/store/workspaces.go` -- Workspace/Project CRUD
- `internal/store/templates.go` -- Template CRUD, builtin seed
- `internal/store/mcp_servers.go` -- MCP server config CRUD
- `internal/store/bookmarks.go` -- Bookmark CRUD
- `internal/store/artifacts.go` -- Artifact CRUD
- `internal/store/events.go` -- Event log, fire-and-forget
- `internal/store/broker.go` -- Broker decision log
- `internal/store/execution_metrics.go` -- Execution metrics recording
- `internal/store/trigger_rules.go` -- Trigger rule CRUD
- `internal/store/custom_actions.go` -- Custom action CRUD
- `internal/store/prompt_templates.go` -- Prompt template CRUD, composition
- `internal/store/providers.go` -- Provider/Model CRUD
- `internal/store/catalog.go` -- Catalog source CRUD
- `internal/store/todos.go` -- Todo CRUD
- `internal/store/plans.go` -- Plan CRUD, step updates
- `internal/store/session_overrides.go` -- Session agent overrides
- `internal/store/agent_projects.go` -- Agent-project bindings
- `internal/store/agents_hash.go` -- Agent content hash
- `internal/store/migrations/001_schema.sql` -- Squashed schema
- `internal/store/migrations/002_add_task_backend.sql`
- `internal/store/migrations/003_todos_and_plans.sql`
- `internal/store/migrations/004_session_agent_overrides.sql`
- `internal/store/migrations/005_a2a_session_scoping.sql`

**Skipped:** Test files were not audited for logic but were noted for coverage patterns. `internal/store/migration005_idempotent_test.go` and `migration005_verify_test.go` confirm migration 005 is tested.

## Methodology

**Categories applied:**
- Security (SQL injection -- primary focus)
- Transaction handling
- Migration safety
- Error handling
- Go community idioms
- Antipatterns

**Categories deferred:**
- Concurrency correctness -- the store package itself has no goroutines or mutexes. Concurrent access is handled by `database/sql` + SQLite WAL. Connection pool configuration is covered under "connection pool" findings.
- Test quality -- not primary scope. Noted coverage exists, not audited for completeness.

**Tools run:**
- `go vet ./internal/store/` -- clean (0 issues)
- `go test -race -count=1 ./internal/store/` -- pass (34.15s, no races)

**Tools deferred:**
- `golangci-lint` -- covered by `whole-repo-tooling-and-tests-sweep` (2026-04-11)
- `govulncheck` -- deferred to `dependency-supply-chain` scope

## Findings

### By severity

**Critical (0)**
- _none_

**High (3)**
- [01 -- DeleteAgent disables foreign keys process-wide](01-high-delete-agent-disables-foreign-keys.md)
- [02 -- SELECT/Scan column mismatch in ListAgentSkills and ListPromptTemplatesForAgent](02-high-scan-column-mismatch-skills-and-templates.md)
- [03 -- ForkSession and CopyMessages are non-atomic](03-high-fork-session-non-atomic.md)

**Medium (5)**
- [04 -- Squashed 001_schema.sql incoherent with post-squash migrations](04-medium-migration-schema-incoherence.md)
- [05 -- No connection pool limits configured for SQLite](05-medium-no-connection-pool-limits.md)
- [06 -- ListPluginSettings silently ignores JSON unmarshal errors](06-medium-plugin-settings-silent-json-errors.md)
- [07 -- LogEvent and CountSessionToolCalls discard errors](07-medium-events-fire-and-forget-errors.md)
- [08 -- Seed() first-provider INSERT lacks OR IGNORE](08-medium-seed-idempotency-gap.md)

**Low (2)**
- [09 -- ext_settings JSON column unstructured](09-low-ext-settings-unstructured-json.md)
- [10 -- Dead columns and minor observations (5 items)](10-low-dead-column-and-minor-observations.md)

**Info (1)**
- [11 -- Praise and positive observations](11-info-praise-and-observations.md)

### By topic

**SQL injection**
- [11 -- Clean: all 120+ queries use parameter placeholders](11-info-praise-and-observations.md)

**Transaction handling**
- [01 -- DeleteAgent disables FK process-wide without synchronization](01-high-delete-agent-disables-foreign-keys.md)
- [03 -- ForkSession/CopyMessages non-atomic multi-step writes](03-high-fork-session-non-atomic.md)

**Migration safety**
- [04 -- Squashed schema incoherent with migrations 002-005](04-medium-migration-schema-incoherence.md)

**Seed idempotency**
- [08 -- Seed() has inconsistent OR IGNORE usage](08-medium-seed-idempotency-gap.md)

**Connection pool / Concurrency**
- [05 -- No pool limits, PRAGMAs not guaranteed on new connections](05-medium-no-connection-pool-limits.md)

**Error handling**
- [02 -- SELECT/Scan mismatch crashes on agent-skill and agent-template queries](02-high-scan-column-mismatch-skills-and-templates.md)
- [06 -- Plugin settings list swallows JSON parse errors](06-medium-plugin-settings-silent-json-errors.md)
- [07 -- Event log fire-and-forget pattern deviates from convention](07-medium-events-fire-and-forget-errors.md)

**ext_settings JSON**
- [09 -- No namespace protection, no key validation](09-low-ext-settings-unstructured-json.md)

**Plugin schema persistence**
- [06 -- Plugin settings JSON errors silently ignored in list path](06-medium-plugin-settings-silent-json-errors.md)
- Plugins do NOT write arbitrary SQL. All plugin data goes through `plugin_settings` table via typed Go methods. No injection vector.

**Antipatterns / Idioms**
- [10 -- Dead columns, inconsistent patterns (5 grouped items)](10-low-dead-column-and-minor-observations.md)

## Recommended next steps

1. **Fix finding 02 immediately.** The SELECT/Scan mismatch in `ListAgentSkills` and `ListPromptTemplatesForAgent` is a latent crash on any agent with assigned skills or prompt templates. One-line fix each (add the missing column to the SELECT).
2. **Fix finding 01.** Remove `PRAGMA foreign_keys=OFF` from `DeleteAgent` and use explicit DELETE statements. The junction tables don't have FKs back to `agent_profiles` anyway.
3. **Fix finding 05 (connection pool PRAGMAs).** Use DSN-based PRAGMA configuration to ensure every pooled connection has the correct settings. This compounds with finding 01.
4. **Fix finding 03.** Wrap `ForkSession` in a single transaction. Refactor `CopyMessages` to accept a `*sql.Tx` parameter.
5. **Re-squash migrations.** Incorporate 002-005 into a new clean `001_schema.sql` (finding 04).
6. **Remaining Medium/Low findings** can be addressed when touching the relevant code.

## Known issues skipped

- No pre-existing known issues from `docs/beta-known-issues.md` or the reviewer-backend context's no-flag list apply to `internal/store/`.
- The `golangci-lint` findings for `internal/store/` from the `whole-repo-tooling-and-tests-sweep` audit are not re-flagged here.

## Noticed but out of scope

- **`internal/store/prompt_templates.go:L292-403` -- `PlatformPromptTemplate`** contains a very large hardcoded prompt string (the "Mentat" platform capabilities prompt). This is data, not DDL/query code. A future review of the prompt composition system should verify this is the right place for it vs. a separate config/data file. Suggested scope: `prompt-composition`.
- **`internal/store/agents.go:L181-186` -- `DeleteAgent` swallows `GetAgentBySlug` error as "not found".** The error could be a DB connection failure, not just "agent not found". The function returns `nil` on any error from `GetAgentBySlug`, including infrastructure failures. This is a general error-classification concern across the package (several `Get*` functions return the same error type for "not found" and "DB error"). Suggested scope: `error-taxonomy`.
- **Event log unbounded growth.** The `event_log` table has no retention policy, TTL, or max-rows constraint. Over time it will grow without bound. The `execution_metrics`, `token_usage`, and `broker_decisions` tables have the same pattern. Suggested scope: `data-retention`.
- **`internal/store/custom_actions.go:L137-161` -- `ListCustomActionsByTrigger` uses `json_each`.** This relies on SQLite's JSON1 extension being available. `modernc.org/sqlite` includes JSON1 by default, but this is a runtime dependency worth documenting. Not a bug, but a portability note.
