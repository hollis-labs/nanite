# Add `known_tools` catalog, `agent_tools` join, `agent_dispatch_allowlist` — replace free-text tool assignment

**Phase:** 1
**Status:** not-started
**Depends on:** none functionally, but land before or alongside `08-kill-file-reingest-on-boot-pattern.md` (that task's `seedRoleToolsFromIngest` cleanup references this table)
**Touches:** new migration (`known_tools`, `agent_tools`, `agent_dispatch_allowlist` tables), `agent_profiles.tools`/`tool_permissions`/`role_tools` columns (deprecate, do not drop yet — see Context), `agent_profiles.parent_dispatch_allowlist` (**naming-collision risk with the new `agent_dispatch_allowlist` table — read Context before naming anything**), `internal/service/tool.go` (`SelectForAgent` — eventual real consumer, wiring itself may be a follow-up task, see What to do), `internal/service/ingest.go` (`seedRoleToolsFromIngest`)

## Context

Architecture doc `01-agent-construction.md`: *"`known_tools` — live-synced catalog. status=unavailable, not deleted, when a server disconnects. `agent_tools` — join, replaces `tools:`/`toolPermissions:`/`roleTools:` entirely... `agent_dispatch_allowlist` — separate concept from `agent_tools`: which tools this agent may authorize a subagent it dispatches to use."*

### Today's mechanism, verified — no separate YAML frontmatter field, three JSON-blob columns instead

There is no standalone `toolPermissions:`/`roleTools:` frontmatter field as such. `agent_profiles.tools`/`tool_permissions`/`mcp_servers` are plain JSON-TEXT columns, populated both from `.md` frontmatter (`internal/agent/parser.go`) and directly via the REST API (`internal/builders/agent_builder.go`). `agent_profiles.role_tools` (migration `070`) is the closest existing thing to a seed-the-roster mechanism: `internal/api/agents.go`'s boot-time hook parses `role_tools` JSON and inserts one `agent_known_tools` row per entry (`pinned=1, reason='role_seed'`) — but migration `070`'s own comment is explicit this is "NOT a contract the runtime enforces," a best-effort enrichment, not the real selection path. `known_tools`/`agent_tools` formalizes this into a real FK-based contract.

### `known_tools` (new) vs. `agent_known_tools` (existing, live, do not confuse)

`agent_known_tools` (`internal/store/migrations/069_per_agent_state.sql:22-32`) is a **live, currently-used** per-agent roster table: `agent_id` (real FK to `agent_profiles`), `tool_name TEXT` (bare string, no catalog to reference — this is exactly the gap `known_tools` closes), `pinned`, `activation_count`, `ttl_seconds`. It has a real REST CRUD API (`internal/api/agent_capabilities.go`) and a live GUI (`AgentCapabilitiesPanel.tsx`). **`known_tools` (this task's new table) is a different thing: a global tool catalog** (one row per real tool that exists in the system, live-synced against builtins + MCP discovery), not a per-agent roster. `agent_tools` (also new, this task) is the join between `agent_profiles` and `known_tools` — the actual replacement for `tools:`/`toolPermissions:`/`roleTools:`. **Do not merge or rename `agent_known_tools` as part of this task** — it stays as the per-agent pin/activation-tracking table; `agent_tools` is the new FK-based grant table sitting alongside it. Flag this distinction explicitly in code comments given the codebase's documented history of exactly this kind of naming collision (`GLOSSARY.md`'s stated reason for existing).

### `agent_dispatch_allowlist` (new) vs. `agent_profiles.parent_dispatch_allowlist` (existing) — likely different concepts, verify before naming

`agent_profiles.parent_dispatch_allowlist` (migration `060_agent_parent_dispatch_allowlist.sql:48`) already exists today: a JSON array of **role slugs** (e.g. `["researcher","planner","worker"]`) a parent agent may dispatch to, read by the Tool Broker Describe hook. The architecture doc's new `agent_dispatch_allowlist` is described as "which **tools** this agent may authorize a subagent it dispatches to use" — a different axis (tools, not target roles). **Before creating a table named `agent_dispatch_allowlist`, confirm with a direct read of the architecture doc's intent and a `GLOSSARY.md` check whether these are deliberately two different mechanisms that happen to share a near-identical name, or whether the new table is meant to supersede/absorb the existing column.** If both are real and distinct, rename one to avoid the collision (e.g. `agent_dispatch_tool_allowlist` for the new one, keeping `parent_dispatch_allowlist`'s existing name) rather than shipping two "dispatch allowlist" concepts with confusingly similar names — this is exactly the class of naming risk `GLOSSARY.md` exists to catch before it ships, not after.

## What to do

1. Create `known_tools`: `id`, `name` (unique), `source` (builtin/mcp/plugin), `status` (`available`/`unavailable` — not deleted on disconnect, per architecture doc), `description`, `concurrency_safe` (placeholder for `06-tool-concurrency-safety-classification.md`'s Phase 3 work — coordinate naming, don't duplicate the column), `created_at`/`updated_at`. Live-sync against builtins + current MCP discovery at startup (mirroring how `agent_known_tools.tool_name` values are populated today, but as a global catalog, not per-agent).
2. Create `agent_tools`: `agent_id FK -> agent_profiles`, `tool_id FK -> known_tools`, replacing `tools:`/`tool_permissions:`/`role_tools:`'s selection semantics. Mark a subset of `known_tools` rows as always-included defaults (per architecture doc: *"so a new agent can't accidentally ship without the tool-discovery escape hatch — `request_tools`/`tool_list`/`tool_describe`... a flag on `known_tools`... not a per-creation-flow default that can be silently dropped"*).
3. Resolve the `agent_dispatch_allowlist` naming question above before creating any new table/column; implement per that resolution.
4. Deprecate (do not drop) `agent_profiles.tools`/`tool_permissions`/`role_tools` — mark clearly as legacy, migrate their current values into `agent_tools` for every existing agent as part of this task (not deferred to `10`, since `10`'s scope is the `.nanite/agents/*.md` → `roles`/`agents` migration specifically, not the tool-assignment migration). Actually wiring `internal/service/tool.go`'s `SelectForAgent` to read `agent_tools` instead of the legacy columns may be substantial enough to warrant its own follow-up task if it grows beyond a straightforward read-path swap — assess during implementation and escalate/split if so, rather than silently under-scoping this task.
5. Update `internal/service/ingest.go`'s `seedRoleToolsFromIngest` to write into `agent_tools` (or confirm it should be retired entirely once `known_tools`/`agent_tools` is the real contract, superseding the "best-effort enrichment" it currently provides) — coordinate with `08`'s reingest cleanup so this isn't done twice.

## Done means

- `known_tools`/`agent_tools`/(resolved-name) dispatch-allowlist tables exist, populated for every current agent with equivalent selection behavior to today's `tools:`/`tool_permissions:`/`role_tools:`.
- The `agent_dispatch_allowlist`-vs-`parent_dispatch_allowlist` naming question is resolved and documented in this file's Work Log.
- `agent_known_tools` is confirmed untouched/uncconflated with the new `known_tools` catalog (regression check, given the naming-collision risk flagged above).
- At least one default-always-included tool flag exists and is verified to survive a fresh agent creation with zero explicit tool grants.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Tested against a real copy of the backed-up database.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
