# Nanite Agent Framework — Design Guide

Reference document for the Nanite agent framework (previously known as agentrc). This document should always reflect the current state of the system. When the framework changes, the nanite-agent-manager role is responsible for updating this doc.

## Core Principles

1. **Composition over monoliths.** Agents compose domain roles + stack roles + skills + project context. No single file tries to do everything.
2. **If uncertain, stop and ask.** Guessing signals an incomplete system. "Context Needed" is a valid status.
3. **Focused agents > general agents.** Load context for the task, not the universe.
4. **Skills for knowledge, hooks for enforcement.** Skills load on demand. Hooks run silently unless they block.
5. **Sub-agent output isolation.** Heavy work runs in sub-agents. Main context gets one-line confirmations.
6. **Progressive context disclosure.** Boot gives identity + task. Skills give procedure. Vanta Conduit gives memory.

## Agent Model

An agent is a named configuration that composes roles, skills, and project context:

```yaml
agents:
  cerberus-backend:
    name: Cerberus Backend Engineer
    description: Go service development for the Cerberus daemon
    roles: [backend, go]           # domain + stack
    skills: [go-build, go-lint, go-test]
    plugins: [vercel]              # vendor plugins (claude plugins install)
    context: agents/backend.md     # project-specific context
    # Future extensions:
    # voice_profile: ...
    # permissions: { ... }
    # model_strategy: { ... }
    # settings: { ... }
```

### Activation

"Boot <agent-slug>" loads the agent config from `.nanite/config.yaml`:
1. Resolve each role from `~/.nanite/roles/{type}/{name}.md`
2. Load listed skills from `~/.nanite/skills/`
3. Check `plugins:` — vendor plugins should already be installed at project scope via `claude plugins install`
4. Read context file from `.nanite/` (relative to project)

Fallback: "Boot <role-name>" loads a single role if no agent matches.

### Nexus compatibility

The agent config schema maps directly to Nexus `AgentConfig`:
- `roles` → `capabilities.roles` + `capabilities.domains`
- `skills` → skill assignments (many-to-many)
- `plugins` → vendor plugin dependencies (informational, not auto-installed)
- `context` → project-specific settings/system_prompt content
- Future fields (`voice_profile`, `permissions`, `model_strategy`) map to their Nexus equivalents

A Nanite agent config can be serialized into a Nexus `RegisterInput` for DB-backed agent infrastructure.

## Playbook Model

A playbook is a parameterized session template — a reusable pattern for exploration, research, and cross-project work. Unlike agents (fixed persona for a project), playbooks take variable inputs and produce session contexts.

```yaml
# ~/.nanite/playbooks/explore.md frontmatter
name: Cross-Project Explorer
description: Ideation, research, and planning across multiple codebases
inputs:
  goal: { required: true, description: "What are we exploring and why" }
  projects: { required: true, description: "Project slugs or paths (comma-separated)" }
  output_dir: { required: false, default: "exploration/", description: "Where artifacts land" }
roles: [strategic-planner]
skills: [doc-search, adr, blg, vault-search]
read_only: true
```

### How playbooks differ from agents

| | Agent | Playbook |
|---|---|---|
| Identity | Named persona with fixed role composition | Session pattern with variable inputs |
| Reuse | Same config every time | Same template, different inputs per session |
| Scope | Single project | Often cross-project |
| Lifecycle | Persistent definition | Instantiated per session |
| Output | Code/artifacts in the target project | Plans, docs, analysis in the workspace |

### Activation

- `Boot <playbook-name>` — with inputs inline or prompted
- `/playbook <name> <args>` — via the playbook command/skill
- `/playbook` — list available playbooks

Resolution order: agents → playbooks → roles (first match wins).

### Design rules

1. **Playbooks don't build.** They explore, research, plan, and produce documents. Build work uses agents.
2. **Inputs are declared, not implied.** Every variable the playbook needs is in the `inputs:` frontmatter.
3. **`read_only: true` is enforced.** Sessions must not modify target project directories.
4. **Output goes to the workspace.** Playbook artifacts land in the output directory, not in target projects.
5. **Playbooks are templates, not scripts.** The LLM interprets the rendered playbook as session context. There is no execution engine — the structure guides the session.

### Current playbooks

| Playbook | Purpose |
|----------|---------|
| explore | Cross-project capability mapping, overlap analysis, gap identification |

## Role Architecture

Roles are organized by type in `~/.nanite/roles/`:

```
roles/
├── domain/     # What kind of work (backend, frontend, code-review, auditor)
├── stack/      # What tech (go, react)
└── meta/       # Framework/tooling (nanite-agent-manager)
```

**Domain roles** define thinking — how to approach a problem space. No tech stack specifics.
**Stack roles** define conventions — language idioms, tooling, project layout.
**Meta roles** are for framework development and don't compose with other roles.

Agents compose domain + stack: `[backend, go]`, `[frontend, react]`, `[auditor, go]`.

### Current roles

| Type | Role | Purpose |
|------|------|---------|
| domain | backend | API, service, and data engineering (`## What NOT to do` section) |
| domain | frontend | UI architecture, components, and UX (`## What NOT to do` section) |
| domain | code-review | Code review with BLOCK/WARN/NOTE severity |
| domain | project-docs | Structured documentation maintenance |
| domain | strategic-planner | Planning and scoping |
| domain | auditor | Codebase auditing for agent context docs |
| stack | go | Go language conventions and tooling |
| stack | react | React/Next.js/shadcn/Tailwind conventions |
| meta | nanite-agent-manager | Nanite framework development |

## Project Structure

### Source (`~/.nanite/`)

```
~/.nanite/
├── config.yaml        # Global config: version, roles, default_skills, default_tools, agent schema, projects
├── agent-boot.md      # Session boot rules (loaded at session start)
├── roles/             # Role definitions (domain/, stack/, meta/)
├── skills/            # Skill definitions (procedures)
├── commands/          # Command stubs (slash command entry points)
├── playbooks/         # Playbook templates (parameterized session patterns)
├── templates/         # Context doc templates, CLAUDE.md template
├── docs/              # Framework documentation (this file)
└── hooks/             # Hook scripts (enforcement)
```

### Project install (`<project>/.nanite/`)

```
<project>/.nanite/
├── config.yaml        # Project config: version, named agents
├── agents/            # Project-specific context docs
├── skills → ~/.nanite/skills
├── commands → ~/.nanite/commands
└── roles → ~/.nanite/roles
```

### LLM adapter (`<project>/.claude/`)

```
<project>/.claude/
├── skills → ../.nanite/skills
└── commands → ../.nanite/commands
```

Plus a CLAUDE.md loader block in the project root. The `templates/CLAUDE.md` template contains the canonical Nanite loader block — this is what `nanite install` writes during setup.

### Legacy archive (`<project>/.agentrc-legacy/`)

Created by the install procedure when pre-v2 artifacts are found. Standard archive location for v1/legacy content during migration.

Files archived: `agent-boot.md`, `bootstrap.md`, `agentrc.yaml` (from project root).
Directories archived: `boot/`, `pcc/`, `tasks/`, `logs/`, `state/`, `backlog/`, `templates/`.

Any `*.md` context docs found in `.nanite/` root (not in `agents/`) are moved to `.nanite/agents/` with a warning if no agent definition references the migrated doc.

## Default Skills and Tools

The global config (`~/.nanite/config.yaml`) defines `default_skills` and `default_tools` — loaded for every agent session on top of agent-specific lists.

```yaml
default_skills: [fast-triage, end-of-session, escalate]
default_tools:  [engine, cortex, hadron, cerberus]
```

**default_skills** — Skills every agent gets regardless of its `skills:` array. Agent-specific skills are additive; they never replace defaults. A skill belongs here when every agent benefits from having it (e.g., structured user input, session handoff, blocker escalation).

**default_tools** — MCP tools expected in every session. Informational — nanite doesn't start MCP servers, but agents should expect these tools to be available and flag if they're missing.

When booting an agent, the effective skill set is: `default_skills ∪ agent.skills` (deduplicated).

## Skill Design Rules

### Output contracts matter
Every skill defines its output format. "Return ONLY this format" prevents agents from narrating and polluting the caller's context window.

### Sub-agent pattern for write operations
Skills that create/modify artifacts run via sub-agent. Main context gets a one-line confirmation.

### Read operations run inline
Skills that retrieve data run in the main context because the caller needs the results.

### Context cost awareness
Everything an agent outputs consumes context window tokens. Design skills to produce minimal, structured output.

### Current skills

20 shared skills (in `~/.nanite/skills/`) + 1 vendor skill (in `~/.nanite/vendor/`).

| Skill | Purpose | Mode |
|-------|---------|------|
| adr | Architectural decision capture | sub-agent |
| blg | Quick backlog capture | sub-agent |
| doc-note | Store docs in Vanta Conduit | sub-agent |
| doc-search | Query docs from Vanta Conduit | inline |
| boot-prompt | Session handoff document | manual |
| qstatus | Compact status snapshot | sub-agent |
| qhealth | Service health check | sub-agent |
| hadron-run | Blueprint execution | sub-agent |
| standup | Standup from activity + git | sub-agent |
| escalate | Convert blockers to repair tasks | sub-agent |
| blueprint-builder | Hadron blueprint generation | sub-agent |
| vault-search | Nanite vault search | inline |
| go-build | Build Go project | inline |
| go-lint | Lint Go project | inline |
| go-test | Run Go tests | inline |
| nanite-agent-manage | Create roles, context, skills, agent definitions | sub-agent |
| shadcn-install | Add shadcn MCP + skill to a project | inline |
| fast-triage | Structured feedback + item triage via browser UI | inline |
| playbook | List, inspect, or boot session playbooks | inline |
| nanite/ | Nanite knowledge vault skills | inline |

### Vendor skills

Vendor skills are third-party skill packages stored in `~/.nanite/vendor/` (not `~/.nanite/skills/`). This prevents them from auto-loading in all projects via the shared directory symlink.

| Vendor skill | Source | Purpose |
|-------------|--------|---------|
| shadcn-ui | `pnpm dlx skills add shadcn/ui` | shadcn component search, install, docs, coding conventions |

**Scoping:** Vendor skills are symlinked into specific projects' `.claude/skills/` directories. Projects that opt into vendor skills use per-file symlinks instead of a directory symlink for `.claude/skills/`. Use `/shadcn-install` to add shadcn to a project.

**Upgrading:** Re-run the skills installer to a temp directory, copy output to `~/.nanite/vendor/<skill>/`. Existing symlinks propagate automatically.

### Current commands

20 command stubs in `~/.nanite/commands/`. Each is a slash-command entry point that delegates to its matching skill.

| Command | Delegates to | Notes |
|---------|-------------|-------|
| /adr | adr | |
| /blg | blg | |
| /doc-note | doc-note | |
| /doc-search | doc-search | |
| /qstatus | qstatus | |
| /qhealth | qhealth | |
| /health-check | qhealth | Alias — redirects to `/qhealth` |
| /hadron-run | hadron-run | Run a Hadron blueprint |
| /standup | standup | Generate standup report from recent activity |
| /escalate | escalate | Convert a blocker into a repair task |
| /blueprint-builder | blueprint-builder | Generate a Hadron blueprint from description |
| /vault-search | vault-search | Search Nanite knowledge vaults |
| /go-build | go-build | |
| /go-lint | go-lint | |
| /go-test | go-test | |
| /nanite-agent-manage | nanite-agent-manage | |
| /shadcn-install | shadcn-install | Add shadcn to a frontend project |
| /fast-triage | fast-triage | Structured feedback + item triage via browser UI |
| /playbook | playbook | List/inspect/boot playbooks |
| /boot-prompt | boot-prompt | |

## Hook Design Rules

### Hooks should be silent unless they block
Good: envelope-guard blocks a write and explains why. Bad: audit hook logs every operation.

### Hooks for enforcement, not capture
Use hooks to prevent bad actions. Don't use hooks for logging or context injection.

## Namespace Convention (Vanta Conduit)

```
{project}/
├── docs/          # Project documentation
├── history/       # Session snapshots, summaries
├── plans/         # Active plans, roadmaps
└── context/       # Agent-generated context

_shared/
└── docs/          # Cross-project documentation
```

### Keys
Auto-generated timestamps (YYYYMMDD-HHMMSS-4random). Meaning from namespace, type, tags, and search.

### Lifecycle
All agent writes start as `draft`. Promotion to `canonical` requires explicit action.

## Config Schema (v2.3.0)

Single config file at `~/.nanite/config.yaml`:

```yaml
version: 2.3.0

roles:
  <name>:
    file: <type>/<name>.md    # relative to roles/
    type: domain | stack | meta
    description: <one-liner>

projects:
  <slug>:
    root: <path>
    lang: <language>
    description: <one-liner>
```

Playbooks are file-based (not config-based). They live in `~/.nanite/playbooks/` and are discovered by directory listing. No config entry needed.

### Project registry

The global config lists both monorepos and individual modules as first-class project entries. Current registered projects (all on v2.2.0):

| Slug | Description |
|------|-------------|
| cerberus | — |
| hadron | — |
| nanite | — |
| nexus | — |
| carrier | — |
| sigil | — |
| lnklst | — |
| suds-v2 | — |
| fragmentsengine.com | — |
| agent-workspaces | — |
| fragments-engine | Monorepo root |
| engine | Fragments Engine Core (task/sprint/project management, Nexus agent routing) |
| conduit | Chat interface and conversation management |
| vanta-conduit | Vanta Conduit — context memory, RAG, and document storage |
| libs | Shared libraries (MCP, OTel, plugin, toolbroker) |

`engine`, `conduit`, `vanta-conduit`, and `libs` are modules within the `fragments-engine` monorepo, listed individually so agents can scope to a single module.

Project-level config at `<project>/.nanite/config.yaml`:

```yaml
nanite_version: 2.2.0

agents:
  <slug>:
    name: <display name>
    description: <one-liner>
    roles: [<role>, ...]
    skills: [<skill>, ...]
    plugins: [<plugin>, ...]       # vendor plugins (informational)
    context: agents/<file>.md
```

## MCP Tool Namespace

All skills and commands reference `mcp__engine__*` tools. The legacy `mcp__volon__*` namespace is no longer used. The service was previously labeled "Volon"; it is now "Engine" throughout.

## Version History

- **v2.3.0** — Playbooks: parameterized session templates. New primitive type in `~/.nanite/playbooks/`. `/playbook` command and skill. Boot loader updated: agents → playbooks → roles resolution order. First playbook: `explore` (cross-project capability mapping). 20 skills, 20 commands.
- **v2.2.0** — Agent composition model. Roles split into domain/stack/meta. Named agents in project config. projects.yaml absorbed into config.yaml. Context files moved to agents/ dir. 18 skills, 18 commands. Monorepo modules registered as individual projects. Legacy archive convention (`.agentrc-legacy/`). `mcp__engine__*` replaces `mcp__volon__*`. `backend` and `frontend` roles gain `## What NOT to do` sections. `/health-check` aliased to `/qhealth`. All 14 portfolio projects on v2.2.0.
- **v2.1.0** — Install/uninstall system. Directory symlinks. Three-tier install model.
- **v2.0.0** — Initial framework. Roles, skills, commands, hooks.
