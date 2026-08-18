# Cut `templates` table (output-formatting-snippet mechanism)

**Phase:** 0
**Status:** not-started
**Depends on:** none
**Touches:** `internal/store/templates.go` (delete entire file); `internal/api/templates.go` (delete entire file); `internal/api/api.go` (remove 6 route registrations, lines 390-395); `internal/service/store.go` (lines 174-178, remove 5 interface method declarations); a new migration dropping `templates`

## Context

TASKS.md Phase 0 Cuts, item 30: "`templates` (the separate, unrelated output-formatting-snippet table) — has a real CRUD/REST surface but no confirmed real consumer found, and other ways to achieve the same thing exist today. Cut entirely. If revisited later, use a clearer name than the bare `templates` — `output_templates` or an `output/templates` grouping, not a name vague enough to need re-explaining two months later." **The decision log does not cover `templates` anywhere as its own topic** — the only decision-log hit is the Phase 0 Renames section's "Open question, not yet decided: `prompt_templates` vs. `templates`" note, which flags the naming collision but explicitly does not decide anything about cutting either table. This task's authority is TASKS.md alone; do not cite a decision-log section for the cut rationale, only for the naming-collision awareness.

**Explicit confirmation: `templates` and `prompt_templates` (cut separately by `29-cut-prompt-templates`) are two genuinely distinct tables, not the same thing under two names.** Verified by direct schema comparison, both defined in `internal/store/migrations/001_schema.sql`:

| | `prompt_templates` (line 225) | `templates` (line 267) |
|---|---|---|
| Columns | `id, name, slug, scope, template, variables, priority, is_builtin, icon, created_at, updated_at` | `id, name, template, is_builtin, created_at, updated_at` |
| Purpose | System-prompt fragments, composed per-agent via `agent_prompt_templates` + `ComposePromptForAgent`, `{{var}}` string-replace interpolation | Standalone Go-`text/template`-style output snippets (`{{.Title}}`, `{{.Content}}`, `{{.Status}}`, `{{.Date}}` — dot-prefixed struct-field placeholders, a different templating convention entirely) |
| Go type | `store.PromptTemplate` (`internal/store/prompt_templates.go`) | `store.Template` (`internal/store/templates.go`) |
| Junction table | `agent_prompt_templates` (per-agent assignment) | none — no per-agent binding concept at all |
| Builtin seed rows | `chat-role-harness`, `base-identity`, `workspace-context`, `project-context`, `mode-addendum`, `tool-awareness`, 4 compaction-disclosure variants | `task-summary`, `code-review`, `standup` (`BuiltinTemplates`, `internal/store/templates.go:22-74`) |
| REST surface | `/api/prompt-templates`, `/api/agents/{id}/prompt-templates` | `/api/templates`, `/api/templates/{name}/apply` |

The two tables, Go structs, files, and REST namespaces are completely separate — no shared rows, no FK between them, no code path that reads one thinking it's the other. `29-cut-prompt-templates` and this task can be worked independently; they happen to share only the English word "template(s)," which is precisely the naming collision TASKS.md's Renames section flags as worth avoiding if the concept is ever rebuilt.

**Verified no confirmed real consumer, three independent checks:**

1. **Seed function has zero callers**: `SeedBuiltinTemplates` (`internal/store/templates.go:77-90`) is defined but `grep -rn "SeedBuiltinTemplates" --include="*.go" internal/` finds only its own definition — never invoked anywhere at boot or otherwise. The 3 builtin templates (`task-summary`, `code-review`, `standup`) are never actually seeded into a live database.
2. **Real REST surface exists but no frontend caller**: `internal/api/api.go:390-395` registers a full CRUD surface plus an apply action (`GET/POST /api/templates`, `GET/PUT/DELETE /api/templates/{name}`, `POST /api/templates/{name}/apply` — the latter reads the last assistant message + session title from a real session and renders the named template against them, `internal/api/templates.go:84-134`). `grep -rln "api/templates\|listTemplates\|applyTemplate" ui/src/` returns nothing — no frontend component, hook, or API-client function calls any of these routes. Real, working backend machinery with no wired frontend.
3. **No other Go call site**: `grep -rn "SeedBuiltinTemplates\|ListTemplates\b|GetTemplate\b|CreateTemplate\b|UpdateTemplate\b|DeleteTemplate\b" --include="*.go" internal/` outside of `internal/store/templates.go` itself finds only the REST handlers in `internal/api/templates.go` and the interface declarations in `internal/service/store.go` — nothing in the chat engine, MCP self-tools, plugins, or anywhere else reads or writes this table.

## What to do

1. Delete `internal/store/templates.go` and `internal/api/templates.go` entirely.
2. Remove the 6 route registrations in `internal/api/api.go` (lines 390-395: `GET /api/templates`, `POST /api/templates`, `GET /api/templates/{name}`, `PUT /api/templates/{name}`, `DELETE /api/templates/{name}`, `POST /api/templates/{name}/apply`).
3. Remove the 5 interface method declarations from `internal/service/store.go` (lines 174-178: `ListTemplates`, `GetTemplate`, `CreateTemplate`, `UpdateTemplate`, `DeleteTemplate`).
4. Determine the next available migration number (check `internal/store/migrations/` at implementation time; use `09-adopt-goose-migrations`'s format if it has landed by then). Write a migration with `DROP TABLE IF EXISTS templates;` — consistent with the existing `DROP TABLE IF EXISTS` precedent in this codebase (e.g. `internal/store/migrations/018_rename_a2a_messages.sql`). Do not edit or delete migration 001's original `CREATE TABLE templates` statement — per this codebase's established convention, it keeps creating the table harmlessly every boot since there's no migration ledger yet, and the new migration drops it again afterward in file order.
5. Search for any remaining references to `store.Template` (the Go struct — distinct from `store.PromptTemplate`, don't confuse the two while searching) after the above and confirm none remain outside of tests that also need updating.
6. Confirm no frontend references exist (`grep -rn "api/templates\|listTemplates\|applyTemplate" ui/src/` — should already be empty; re-verify after this change in case something was missed).

## Done means

- `templates` table no longer exists after migrations run on a fresh boot.
- No references to `store.Template`, `SeedBuiltinTemplates`, `ListTemplates`, `GetTemplate` (store package), `CreateTemplate`, `UpdateTemplate`, or `DeleteTemplate` remain anywhere in the codebase.
- `prompt_templates`/`agent_prompt_templates` (a separate table, separate task) are unaffected by this change.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Migration tested against a real backed-up database copy, not just an empty fixture.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
