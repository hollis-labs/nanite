# [Medium] Store layer: 11 source files with no matching test file

**Scope:** internal/store/
**Topic:** Test Quality — data integrity layer gaps
**Date:** 2026-04-11

## Problem

The `internal/store/` package has 28 source files (8369 lines). 17 have matching test files (2793 lines of tests). 11 source files have no matching test file.

## Evidence

Store source files without matching `*_test.go`:

| File | Lines | Data sensitivity |
|------|-------|-----------------|
| `skills.go` | 296 | Agent-skill bindings, permission-adjacent |
| `prompt_templates.go` | 421 | Template CRUD, used in chat context assembly |
| `seed.go` | 359 | Seed data (agents, templates, skills, modes) — tested for idempotency only |
| `agents.go` | 442 | Agent profile CRUD — has `agents_test.go` (232 lines), **actually tested** |
| `artifacts.go` | 141 | Artifact metadata storage |
| `plugin_settings.go` | 153 | Plugin config persistence |
| `providers.go` | 168 | Provider registration in DB |
| `templates.go` | 180 | Template CRUD |
| `bookmarks.go` | ~100 | Bookmark CRUD |
| `events.go` | ~80 | Event log storage |
| `mcp_servers.go` | ~120 | MCP server persistence |
| `broker.go` | ~50 | Broker settings |
| `agent_projects.go` | ~60 | Agent-project bindings |
| `agents_hash.go` | ~40 | Agent config hashing |

Note: `agents.go` does have `agents_test.go` (232 lines) covering CRUD operations. The table above is corrected to exclude it from the "untested" list. Actual untested count: ~10 files.

Files WITH good test coverage:
- `sessions.go` (688 lines) — `sessions_test.go` (218 lines)
- `modes.go` (244 lines) — `modes_test.go` (300 lines)
- `a2a.go` (252 lines) — `a2a_test.go` (227 lines)
- `usage.go` (159 lines) — `usage_test.go` (155 lines)
- `store.go` (149 lines) — `store_test.go` (72 lines) + seed idempotency test
- `todos.go` — `todos_test.go` (205 lines)
- `plans.go` — `plans_test.go` (179 lines)
- `trigger_rules.go` — `trigger_rules_test.go` (256 lines)
- `custom_actions.go` — `custom_actions_test.go` (233 lines)
- `execution_metrics.go` — `execution_metrics_test.go` (239 lines)
- `workspaces.go` — `workspaces_test.go` (118 lines)
- `session_overrides.go` — `session_overrides_test.go` (56 lines)
- `user_settings.go` — `user_settings_test.go` (95 lines)
- `catalog.go` — `catalog_test.go` (106 lines)

## Impact

The store layer uses raw SQL queries with `database/sql`. The reviewer-backend context states "SQL injection — raw database/sql queries throughout. Every query must use parameter placeholders. String concatenation in SQL is an automatic Critical." Without tests, there is no verification that queries use parameter placeholders correctly, or that CRUD operations handle edge cases (empty strings, null values, concurrent access).

`seed.go` is tested only for idempotency (call Seed twice, verify count). It is not tested for correctness of the seeded data (correct agent defaults, correct skill bindings, correct mode assignments).

## Recommendation

Priority:
1. `skills.go` (296 lines) — agent-skill bindings affect what tools an agent can use
2. `prompt_templates.go` (421 lines) — templates affect chat context
3. `plugin_settings.go` (153 lines) — plugin config persistence
4. `seed.go` — add correctness tests beyond idempotency (verify specific seeded agents exist with expected fields)

Use the existing `store_test.go:newTestStore()` helper for isolated SQLite databases.

## References

- Reviewer-backend context: "SQL injection — raw database/sql queries throughout"
- `internal/store/store_test.go` — `newTestStore()` helper pattern
