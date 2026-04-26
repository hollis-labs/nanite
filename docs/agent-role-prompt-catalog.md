# Agent + Role + Prompt Catalog

**Source ticket:** CW-20260426-0015 (M1 audit)
**Date:** 2026-04-26
**Authority spec:** `docs/superpowers/specs/2026-04-21-agent-platform-harness-design.md` (three-role: Chat / Worker / Planner)

---

## Locations Cataloged

### In-repo (nanite, `feat/arch-seq-phase-1-foundation`)

| Path | What it contains |
|------|-----------------|
| `internal/agent/builtin/default.md` | Base Chat Agent prompt (Go embed, 39 lines) |
| `internal/agent/builtin/embed.go` | Go embed wrapper — `DefaultAgent()` |
| `internal/agent/parser.go` | `agent.Definition` struct; `SystemPrompt` field from MD body |
| `internal/agent/convert.go` | Definition → `store.AgentProfile` conversion |
| `internal/store/prompt_templates.go` | `PromptTemplate` store type, CRUD, `BuiltinPromptTemplates` slice, `PlatformPromptTemplate` var, `ComposePromptForAgent()` |
| `internal/api/prompt_templates.go` | REST API: 8 handlers for CRUD + agent assignment |
| `internal/api/api.go` (lines 160–167) | Route registration for `/api/prompt-templates` and `/api/agents/{id}/prompt-templates` |
| `internal/builders/prompt_template_builder.go` | Builder wizard for user-created templates |
| `internal/builders/registry.go` (line 69) | Registers prompt_template builder |
| `internal/chat/context.go` | `assembleSystemPrompt()` (legacy) + `assembleSystemPromptFromTemplates()` (template path) |
| `internal/chat/context_client.go` | Agent context slot assembly; calls `ComposePromptForAgent` |
| `internal/mcp/self_tools.go` (line 206) | `nanite_start_builder` description lists `prompt_template` as a builder target |
| `config/agents/worker.yaml` | Built-in Worker agent YAML definition |
| `.nanite/config.yaml` | Project agentrc — 7 agent definitions (nanite-backend, nanite-frontend, nanite-plugin-dev, nanite-planner, nanite-reviewer, nanite-reviewer-backend, nanite-reviewer-frontend) |
| `.nanite/agents/backend.md` | Context doc for nanite-backend agent |
| `.nanite/agents/frontend.md` | Context doc for nanite-frontend agent |
| `.nanite/agents/planner.md` | Context doc for nanite-planner agent |
| `.nanite/agents/plugin-dev.md` | Context doc for nanite-plugin-dev agent |
| `.nanite/agents/plugin-dev-tasks.md` | Task context doc for nanite-plugin-dev |
| `.nanite/agents/reviewer-backend.md` | Context doc for nanite-reviewer-backend |
| `.nanite/agents/reviewer-frontend.md` | Context doc for nanite-reviewer-frontend |
| `ui/src/components/settings/PromptTemplateEditor.tsx` | Frontend CRUD UI for prompt templates |
| `ui/src/components/settings/AgentProfileManager.tsx` | Loads prompt templates for agent assignment |
| `ui/src/components/settings/agents/AgentDetailView.tsx` | Shows assigned templates per agent |
| `ui/src/components/settings/agents/PromptCreateWizard.tsx` | Create-new-template wizard |
| `ui/src/components/settings/agents/PromptDetailView.tsx` | Detail/edit view for a template |

### User home (`~/.nanite/`)

| Path | What it contains |
|------|-----------------|
| `~/.nanite/roles/domain/backend.md` | Backend domain role |
| `~/.nanite/roles/domain/frontend.md` | Frontend domain role |
| `~/.nanite/roles/domain/strategic-planner.md` | Strategic Planner role (B3) |
| `~/.nanite/roles/domain/code-review.md` | Code Review role |
| `~/.nanite/roles/domain/auditor.md` | Auditor role |
| `~/.nanite/roles/domain/project-docs.md` | Project Docs role (B2) |
| `~/.nanite/roles/meta/nanite-agent-manager.md` | Nanite Agent Manager role |
| `~/.nanite/roles/meta/steward.md` | Steward role (beta, user-local) |
| `~/.nanite/roles/stack/go.md` | Go stack role |
| `~/.nanite/roles/stack/react.md` | React stack role |
| `~/.nanite/roles/stack/vercel.md` | Vercel stack role (beta) |
| `~/.nanite/config.yaml` | User agentrc config (not audited — user-home scope) |

### Cross-refs (agent-workspaces)

| Path | What it contains |
|------|-----------------|
| `docs/superpowers/specs/2026-04-21-agent-platform-harness-design.md` | Three-role spec (Chat / Worker / Planner), fixed tool surfaces, middleware pipeline |
| `knowledge/ideas/agent-role-catalog-with-scope.md` | Role catalog vision; steward locked, specialists sketched |

---

## Roles Inventory

All roles live in `~/.nanite/roles/` and are composed per-agent via `.nanite/config.yaml` `roles:` lists.

| Role | Location | Type | Tool surface claim | Prompt anchor | Maps to (Chat/Worker/Planner) | Notes |
|------|----------|------|-------------------|---------------|-------------------------------|-------|
| `backend` | `domain/backend.md` | domain | None explicit — implementation | General backend engineering identity + thinking model | Worker (implementation) | No system prompt; identity/thinking doc only |
| `frontend` | `domain/frontend.md` | domain | None explicit | Frontend engineering identity + thinking model | Worker (implementation) | No system prompt; paired with stack role |
| `strategic-planner` | `domain/strategic-planner.md` | domain | Nanite plans, Engine tasks/sprints, Vanta Conduit | Planning-only; never edits code | Planner | Explicitly bans code writes; plan-only role |
| `code-review` | `domain/code-review.md` | domain | None explicit | Review-only; BLOCK/WARN/NOTE severity scheme | Worker (review) | Sub-type: read-only worker producing findings |
| `auditor` | `domain/auditor.md` | domain | None explicit | Read-then-document; outputs to `.nanite/agents/*.md` | Worker (audit) | Sub-type: read-only worker producing context docs |
| `project-docs` | `domain/project-docs.md` | domain | `doc-note`, `doc-search`, Vanta Conduit | Observe-then-document; draft only | Worker (docs) | Sub-type: writes docs, not code |
| `nanite-agent-manager` | `meta/nanite-agent-manager.md` | meta | `nanite-agent-manage`, all universal skills | Framework development; install/test | Worker (framework) | Portfolio-scoped; no direct user chat |
| `steward` | `meta/steward.md` | meta | Vanta, Clockwork, inbox, KB oracle | Primary-chat surface for agent-workspaces; dispatches specialists | Chat | Closest match to Chat role; beta/user-local; owns memory hygiene and handoff |
| `go` | `stack/go.md` | stack | None (additive) | Go idioms/conventions overlay | N/A (additive) | Stack modifier combined with domain role |
| `react` | `stack/react.md` | stack | None (additive) | React/Next.js conventions overlay | N/A (additive) | Stack modifier; Next.js-centric (mismatches Nanite's Vite) |
| `vercel` | `stack/vercel.md` | stack | None (additive) | Vercel/Vite/React aesthetic + stack | N/A (additive) | Beta; better match for Nanite's Vite setup than `react` |

---

## Agents Inventory

### Project repo agents (`.nanite/config.yaml`)

| Agent | Location | Role(s) | Context doc | Skills | Notes |
|-------|----------|---------|-------------|--------|-------|
| `nanite-backend` | `.nanite/config.yaml` | backend, go | `agents/backend.md` | go-build, go-lint, go-test, sp-test-driven-development, sp-systematic-debugging, sp-verification-before-completion | Implementation worker for Go backend |
| `nanite-frontend` | `.nanite/config.yaml` | frontend, react | `agents/frontend.md` | frontend-design, shadcn-ui*, sp-test-driven-development, sp-systematic-debugging, sp-verification-before-completion | *`shadcn-ui` is a typo (should be `shadcn-install`); known pre-existing issue |
| `nanite-plugin-dev` | `.nanite/config.yaml` | backend, go | `agents/plugin-dev.md` | go-build, go-lint, go-test, doc-note, doc-search, shadcn-ui | Plugin system specialist |
| `nanite-planner` | `.nanite/config.yaml` | strategic-planner | `agents/planner.md` | adr, blg, doc-note, doc-search, sp-using-superpowers, sp-brainstorming, sp-writing-plans, sp-executing-plans, superpowers:dispatching-parallel-agents | Closest match to Planner role; read-only planning |
| `nanite-reviewer` | `.nanite/config.yaml` | code-review, go | (none) | go-lint, go-test | Lightweight PR review; no context doc |
| `nanite-reviewer-backend` | `.nanite/config.yaml` | code-review, go | `agents/reviewer-backend.md` | deep-review, plan-review, go-build, go-lint, go-test, doc-note, doc-search | Release-grade deep audit |
| `nanite-reviewer-frontend` | `.nanite/config.yaml` | code-review, react | `agents/reviewer-frontend.md` | deep-review, plan-review, doc-note, doc-search, shadcn-install | Frontend deep audit |

### Built-in runtime agent (`config/agents/worker.yaml`)

| Agent | Location | Role | System prompt | Tool config | Notes |
|-------|----------|------|---------------|-------------|-------|
| `worker` | `config/agents/worker.yaml` | Worker | Embedded YAML `system_prompt` block | `allow: ["*"]` | `can_execute: true`; MCP servers: engine + conduit; spawned by lead agents. References "Fragments Engine" — Mentat-era copy. |

### Built-in embedded agent (`internal/agent/builtin/default.md`)

| Agent | Location | Role | System prompt | Tool config | Notes |
|-------|----------|------|---------------|-------------|-------|
| `default` (Base Chat Agent) | `internal/agent/builtin/default.md` | Chat | 39-line grounding + tool cadence + style + judgment | None — uses session tool config | Embedded via Go `//go:embed`; used as fallback when no DB agent is set; `Source: "builtin"` |

---

## Prompts Inventory

### Built-in / embedded prompts (code-owned)

| Prompt | Location | Type | Consumer(s) | Notes |
|--------|----------|------|-------------|-------|
| Base Chat Agent prompt | `internal/agent/builtin/default.md` | built-in (Go embed) | `internal/service/container.go` → `builtin.DefaultAgent()` → DB seed; `internal/service/agent.go` fallback path | Loaded at container init; seeded to DB; used as system prompt for sessions with no agent or with the default agent selected |
| `PlatformPromptTemplate` | `internal/store/prompt_templates.go` (var) | built-in (Go var, NOT in DB) | `store.ComposePromptForAgent()` — prepended to every agent composition that has ≥1 template assigned | Contains "Mentat — Fragments Engine Operator" identity — references Engine, Vanta Conduit, Nanite, Hadron. Mentat-era identity text. Priority 5 (before all DB templates). NOT seeded to DB; injected at composition time only. |
| `BuiltinPromptTemplates` (5 templates) | `internal/store/prompt_templates.go` | built-in (seeded to DB via `SeedBuiltinPromptTemplates`) | `store.ComposePromptForAgent()` when templates are assigned to an agent | Templates: Base Identity (p10), Workspace Context (p20), Project Context (p30), Mode Addendum (p40), Tool Awareness (p50). `is_builtin=1` in DB. |
| Worker system prompt | `config/agents/worker.yaml` | built-in (YAML config) | `config/agents/worker.yaml` loader (if any); agent profile creation path | References "Fragments Engine" — Mentat-era copy. |

### User-authored prompts (DB-stored, runtime-created)

| Prompt | Location | Type | Consumer(s) | Notes |
|--------|----------|------|-------------|-------|
| User-created prompt templates | `prompt_templates` SQLite table | user-authored | `store.ComposePromptForAgent()` when assigned to an agent; REST API consumers; frontend `PromptTemplateEditor` | Scopes: system, mode, skill, context. Assigned to agents via `agent_prompt_templates` join table. CRUD via `/api/prompt-templates`. |

### Prompt composition pipeline

The two assembly paths in `internal/chat/context.go`:

1. **Legacy path** (`assembleSystemPrompt`): `agent.SystemPrompt` + mode addendum + workspace name. No template system. Used when agent has 0 assigned templates.
2. **Template path** (`assembleSystemPromptFromTemplates`): calls `store.ComposePromptForAgent` → prepends `PlatformPromptTemplate` (p5) → appends DB templates in priority order → falls back to legacy if composition returns empty. Always appends `thinkToolBlock`.

The template path is the **preferred** path but only activates when an agent has at least one prompt template assigned.

---

## Three-Role Reconciliation

### Chat — existing definitions that map

| Definition | Location | Fit | Gap |
|------------|----------|-----|-----|
| `default` (Base Chat Agent) | `internal/agent/builtin/default.md` | **Strong fit** — grounding rules, tool cadence, style, judgment. These are harness-level behaviors. | Tool surface not locked to "internal Nanite primitives only" per spec; currently gets session tool config |
| `steward` role | `~/.nanite/roles/meta/steward.md` | **Strong fit** — "primary-chat surface", dispatches specialists, never executes code | User-local only; not in repo; beta status |

### Worker — existing definitions that map

| Definition | Location | Fit | Gap |
|------------|----------|-----|-----|
| `worker` built-in | `config/agents/worker.yaml` | **Direct match** — `can_execute: true`, `allow: ["*"]` | System prompt references "Fragments Engine" / Mentat — stale copy |
| `nanite-backend` | `.nanite/config.yaml` | Good fit — implementation worker | No `can_execute` / tool surface declaration |
| `nanite-frontend` | `.nanite/config.yaml` | Good fit — implementation worker | Same; `react` role is Next.js-centric, not Vite |
| `nanite-plugin-dev` | `.nanite/config.yaml` | Good fit — implementation worker | Same |
| `nanite-reviewer` | `.nanite/config.yaml` | Fit as read-only Worker sub-type | No context doc |
| `nanite-reviewer-backend` | `.nanite/config.yaml` | Fit as read-only Worker sub-type | None |
| `nanite-reviewer-frontend` | `.nanite/config.yaml` | Fit as read-only Worker sub-type | None |
| `backend` role | `~/.nanite/roles/domain/backend.md` | Worker sub-type (implementation) | Generic; no tool surface |
| `frontend` role | `~/.nanite/roles/domain/frontend.md` | Worker sub-type | Generic |
| `code-review` role | `~/.nanite/roles/domain/code-review.md` | Worker sub-type (read-only) | None |
| `auditor` role | `~/.nanite/roles/domain/auditor.md` | Worker sub-type (read-only) | None |
| `project-docs` role | `~/.nanite/roles/domain/project-docs.md` | Worker sub-type (write-docs) | None |
| `nanite-agent-manager` role | `~/.nanite/roles/meta/nanite-agent-manager.md` | Worker sub-type (framework dev) | None |

### Planner — existing definitions that map

| Definition | Location | Fit | Gap |
|------------|----------|-----|-----|
| `nanite-planner` | `.nanite/config.yaml` | **Direct match** — planning only, no code, ADR/plan/blg skills | No fixed tool surface declaration |
| `strategic-planner` role | `~/.nanite/roles/domain/strategic-planner.md` | **Direct match** — "Plans, not code." | None |

### Sub-types / Specializations

| Sub-type | Based on | Examples |
|----------|----------|---------|
| Read-only Worker | Worker | `code-review`, `auditor`, `nanite-reviewer-*` |
| Docs Worker | Worker | `project-docs`, `nanite-plugin-dev` (docs portion) |
| Framework Worker | Worker | `nanite-agent-manager` (Nanite framework dev) |
| Planner-with-ADR | Planner | `nanite-planner` (adds `adr`, `blg`, `sp-brainstorming`) |
| Stack overlay | N/A (additive) | `go`, `react`, `vercel` — modifiers, not roles |

### Doesn't Fit (proposed disposition)

| Item | Issue | Proposed disposition |
|------|-------|---------------------|
| `PlatformPromptTemplate` (Mentat identity) | References "Mentat — Fragments Engine Operator", Engine, Hadron — wrong platform identity; injected at p5 for every agent with templates | B5-DF decision (see Gaps). Do NOT decide here. |
| `worker.yaml` system prompt | "Fragments Engine" / Mentat-era text | Update to match current Nanite identity when B5-DF resolves platform prompt question |
| `react` stack role | Next.js App Router-centric; Nanite uses Vite | M2 refinement: verify if `vercel` role supersedes for Nanite work |

---

## Relationship Map

```
File-based roles (~/.nanite/roles/)
  └─ domain/: backend, frontend, strategic-planner, code-review, auditor, project-docs
  └─ meta/:   steward, nanite-agent-manager
  └─ stack/:  go, react, vercel
       │
       ▼ (composed per .nanite/config.yaml `roles:` list)
Project agent definitions (.nanite/config.yaml)
  └─ nanite-backend (roles: backend, go)
  └─ nanite-frontend (roles: frontend, react)
  └─ nanite-planner (roles: strategic-planner)
  └─ nanite-reviewer-* (roles: code-review, go/react)
  └─ nanite-plugin-dev (roles: backend, go)
       │
       ▼ (each agent may have a context doc)
Agent context docs (.nanite/agents/*.md)
  └─ backend.md, frontend.md, planner.md, plugin-dev.md, reviewer-backend.md, reviewer-frontend.md
       │
       ▼ (skills listed in config.yaml)
Skill catalog (~/.nanite/skills/)
  └─ go-build, go-lint, go-test, deep-review, plan-review, adr, blg, doc-note, doc-search, sp-*, etc.

Built-in runtime agents (code-owned):
  internal/agent/builtin/default.md  →  DefaultAgent()  →  DB seed  →  session fallback (Chat)
  config/agents/worker.yaml          →  (Worker) spawned by lead agents

Prompt assembly pipeline (for DB-persisted agents):
  agent (DB) + assigned templates (agent_prompt_templates)
       │
       ▼ ComposePromptForAgent()
  PlatformPromptTemplate (p5, Go var, NOT in DB — Mentat identity)
       + BuiltinPromptTemplates (p10-p50, DB: base-identity, workspace-context, project-context, mode-addendum, tool-awareness)
       + user-created templates (DB, any priority)
       │
       ▼ assembleSystemPromptFromTemplates()
  Final system prompt  →  chat engine  →  LLM call

Legacy path (no templates assigned):
  agent.SystemPrompt  →  assembleSystemPrompt()  →  LLM call
```

---

## Gaps (Input to B5-DF)

### Missing prompts for declared roles

- **Chat role (three-role spec)** — no dedicated harness-level system prompt in DB. The Base Chat Agent (`default.md`) covers this for the built-in path, but there is no Chat-role-specific DB template. The three-role spec calls for fixed tool surfaces at boot; the current default agent has no tool locking.
- **Planner role** — no dedicated system prompt exists for the Planner role as defined in the spec (planning-appropriate surface, dispatched, returns structured output). `nanite-planner` uses the `strategic-planner` role which is planning-only, but no DB template exists specifically for the Planner harness role.
- **Worker role** — `worker.yaml` has an embedded system prompt, but its Mentat-era identity text makes it stale for the current platform.

### Dead role definitions

- `react` stack role is Next.js App Router-centric; Nanite frontend uses Vite. `vercel` role is the better fit. `react` is not wrong but is misaligned for this project.
- No `~/.nanite/agents/` directory exists. The spec (`docs/superpowers/specs/2026-04-21-agent-platform-harness-design.md` Section 2) reserves `~/.nanite/agents/` for folder-drop agent profiles, but the directory is absent. Project `.nanite/agents/` has context docs (not profile files).

### Prompt templates with no consumer

- **`PlatformPromptTemplate`** — defined as a Go var; prepended by `ComposePromptForAgent()` only when an agent has ≥1 DB templates assigned. If no templates are assigned (legacy path), `PlatformPromptTemplate` is NEVER injected. This means the Mentat identity text is not universally applied. Its consumer count depends entirely on whether agents have templates assigned. In the default configuration (no templates assigned to any agent), `PlatformPromptTemplate` has zero active consumers.
- **`BuiltinPromptTemplates`** (5) — seeded to DB with `is_builtin=1` but not auto-assigned to any agent. They exist in the `prompt_templates` table but have no rows in `agent_prompt_templates` by default. Consumer count: 0 unless the user explicitly assigns them.

### PlatformPromptTemplate consumers found

**Current consumer count: conditionally 0.** `ComposePromptForAgent()` is the sole call site (2 locations: `internal/store/prompt_templates.go:215`, `internal/chat/context_client.go:286`). It prepends `PlatformPromptTemplate` only when the agent has ≥1 assigned templates. No evidence of any agent having templates assigned by default. Frontend `PromptTemplateEditor` / `AgentDetailView` provide the UI to assign templates, but no seeding code assigns them.

### Base Chat Agent prompt consumers found

**Active consumer path:** `internal/service/container.go` calls `builtin.DefaultAgent()` and seeds it to DB. `internal/service/agent.go` uses `us.DefaultAgent` (settings) as a fallback. `internal/chat/context.go:assembleSystemPromptFromTemplates()` falls back to `assembleSystemPrompt()` (which uses `agent.SystemPrompt`) when no templates are assigned.

**Effective consumer count: HIGH** — the Base Chat Agent is the active system prompt for any session using the built-in default agent (no templates assigned), which is the default state.

---

## Recommendations for B5-DF

1. **RETIRE `PlatformPromptTemplate` as a Go var.** Its Mentat-era identity text (Fragments Engine, Hadron, Engine tool descriptions) is wrong for the current platform. Its zero-consumer-by-default status confirms it is not load-bearing. The three-role spec's Chat role harness prompt should replace it, seeded properly to DB and assigned to the default agent.

2. **NARROW `PlatformPromptTemplate`'s injection mechanism if retaining.** If the composition-layer auto-prepend is the right architecture (platform identity injected for every agent with templates), the content must change to current Nanite identity, not Mentat. Consider whether the "auto-prepend at composition time" design is correct given that the Base Chat Agent prompt (legacy path) already covers this for most sessions.

3. **Reconcile the two prompt paths.** Today, agents with no templates use the Base Chat Agent's `SystemPrompt` directly (clean, current). Agents with templates get `PlatformPromptTemplate` (stale Mentat text) prepended. This creates a bifurcated identity — the same chat session could have a Nanite identity or a Mentat identity depending on template assignment state. B5-DF must pick one canonical path.

4. **Preserve the template composition architecture.** The `BuiltinPromptTemplates` (Base Identity, Workspace Context, Project Context, Mode Addendum, Tool Awareness) are well-structured and reusable. The variable interpolation system (`{{agent_name}}`, `{{workspace_name}}`, etc.) is the right foundation for the three-role spec's fixed-surface prompt assembly. Retiring `PlatformPromptTemplate`'s content does not require retiring the composition system — the two are separable.

---

*Note for M2:* `knowledge/ideas/agent-role-catalog-with-scope.md` in `agent-workspaces` contains the scope-filter and role-instancing design sketch that feeds M2 (pattern catalog). Steward role is landed; specialist roles (architect, researcher, documentor, curator, tester, PM) remain sketch-stage pending Mux directory/profile registry.
