# Build/extend the assignment UI and API for the new role/scope/agent composition model

**Phase:** 1
**Status:** not-started
**Depends on:** `01`-`07` (every new column/table this UI needs to expose must exist first)
**Touches:** `ui/src/components/settings/agents/AgentDetailView.tsx`, `AgentBuilderWizard.tsx`, `AgentCreateWizard.tsx`, `editors/ToolPermissionsEditor.tsx`, `editors/ConstraintsEditor.tsx`, `editors/SystemPromptEditor.tsx`, `AgentCapabilitiesPanel.tsx`, new `RoleDetailView.tsx`/`RoleCreateWizard.tsx` (roles has no existing UI at all), `internal/api/api.go` (existing `/api/agents/*` routes — extend; new `/api/roles/*` routes)

## Context

TASKS.md Phase 1: *"Build the assignment UI/API."* This is **not a greenfield build** — verified a substantial, already-real UI and REST surface exists for agent management today: `AgentDetailView.tsx`, `AgentBuilderWizard.tsx`, `AgentCreateWizard.tsx`, dedicated editors (`ToolPermissionsEditor.tsx`, `ConstraintsEditor.tsx`, `SystemPromptEditor.tsx`, `McpServerList.tsx`), `AgentCapabilitiesPanel.tsx` (the live `agent_known_tools`/`agent_known_skills` roster UI), `AgentReflexesPanel.tsx`. REST: a full `/api/agents/{id}/...` surface already covers known-tools, known-skills, procedures, knowledge-seeds, reflexes, projects (`internal/api/api.go:102-145`) — plus `boot-plan`/`modes` sub-resources that Phase 0 (`18a`, `21`) cuts before this task starts.

This task's real scope is **extending** that existing surface to expose the new composition model's fields — `role_id` selection (and the new `roles` CRUD UI, which genuinely doesn't exist yet), `consumer_id` tagging, `model_id` (FK-based, replacing whatever free-text model entry exists today), `instance_mode`/`activation_mode` (whichever `02` settles on), `runtime_kind`, and FK-based tool assignment via `agent_tools` (replacing whatever raw JSON editing `ToolPermissionsEditor.tsx` currently does) — not building agent management from nothing.

## What to do

1. Build `roles` UI: a list/detail/create view for the new `roles` table (name, system_prompt, default tool/skill hints) — this genuinely doesn't exist today, unlike everything else in this task.
2. Extend `AgentDetailView.tsx`/`AgentBuilderWizard.tsx`/`AgentCreateWizard.tsx` to add: a role picker (select an existing `roles` row, see the cascade-resolved defaults it supplies), a consumer picker (nullable, defaults to operator-owned), a model picker sourced from the now-DB-authoritative `models` table (`06`) instead of free text, an `instance_mode`/`activation_mode` selector (whichever `02` settled on), and a `runtime_kind` display/selector (read-only display is acceptable for Phase 1 — Phase 2 is what makes it functionally load-bearing).
3. Replace `ToolPermissionsEditor.tsx`'s raw JSON/free-text tool editing with a real picker against `known_tools` (checkbox/multi-select against the catalog, not a text field) — wire to `agent_tools` (`04`).
4. Extend `internal/api/api.go`'s `/api/agents/*` surface (or add `/api/roles/*`) as needed to back the above UI changes — reuse the existing route-naming conventions already established by the current agent CRUD surface.
5. Do not build any UI for `agent_dispatch_allowlist` unless `04`'s naming-collision question resolved in favor of a genuinely new, distinct concept from `parent_dispatch_allowlist` — if they turn out to be the same concept, this task inherits whatever UI (if any) already covers `parent_dispatch_allowlist`, it doesn't build a second one.

## Done means

- A user can create a `roles` row, create an `agents` composition bound to it, see the cascade-resolved defaults reflected in the UI, and override them at the composition level — exercised end to end in a real browser session, not just component-level tests.
- Tool assignment happens via a real picker against `known_tools`, not free-text JSON editing.
- `cd ui && npm run build` passes.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
