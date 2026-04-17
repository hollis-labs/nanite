# Setting Up Project-Level Agent Configurations

How to define agents that compose roles, skills, and project-specific context.

## The Pattern: Roles + Skills + Context → Agent

The Nanite system layers context in three tiers:

```
~/.nanite/                          # Global — roles, skills, commands, config (LLM-agnostic)
<project>/.nanite/                  # Project — symlinks to global + agents/ context + config
<project>/.claude/                   # LLM adapter — symlinks to .nanite/ (Claude-specific)
```

Concretely:
- `~/.nanite/roles/`, `~/.nanite/skills/`, `~/.nanite/commands/` hold the canonical definitions.
- `<project>/.nanite/skills/` → `~/.nanite/skills/`, etc. (directory symlinks).
- `<project>/.nanite/agents/` holds project-specific context files (not symlinked — unique per project).
- `<project>/.nanite/config.yaml` defines named agents that compose roles + skills + context.
- For Claude: `<project>/.claude/` points through `<project>/.nanite/`, plus a CLAUDE.md loader block.

The `nanite-agent init` command sets up all symlinks and scaffolding automatically.

**Context beats instructions.** "Use `Panel` for card containers — see `components/ui/panel.tsx`" is more useful than "use appropriate components."

## Roles

Roles live in `~/.nanite/roles/` and are organized by type:

```
roles/
├── domain/     # What kind of work (backend, frontend, code-review)
├── stack/      # What tech (go, react, python)
└── meta/       # Framework/tooling (nanite-agent-manager)
```

**Domain roles** define thinking — how to approach a problem space. No tech stack specifics.
**Stack roles** define conventions — language idioms, tooling, project layout expectations.
**Agents compose both:** `roles: [backend, go]` gives you backend thinking with Go conventions.

### Creating a New Role

Use the manage skill:

```
/nanite-agent-manage role domain backend "API, service, and data engineering"
/nanite-agent-manage role stack python "Python language conventions and tooling"
```

Or manually create in `~/.nanite/roles/{type}/{name}.md`. See existing roles for structure:
- Domain example: `roles/domain/backend.md`
- Stack example: `roles/stack/go.md`
- Domain (non-coding) example: `roles/domain/code-review.md`

Key principles:
- **Domain roles don't name technologies.** "Test behavior, not implementation" not "use pytest."
- **Stack roles are opinionated.** "Standard layout: cmd/, internal/, pkg/" not "organize your code well."
- **Every rule prevents a specific mistake.** If you can't point to a scenario where it helps, cut it.

### Current roles

| Type | Role | File | Purpose |
|------|------|------|---------|
| Domain | backend | `roles/domain/backend.md` | API, service, and data engineering |
| Domain | frontend | `roles/domain/frontend.md` | UI architecture, components, and UX |
| Domain | code-review | `roles/domain/code-review.md` | Review with BLOCK/WARN/NOTE severity |
| Domain | project-docs | `roles/domain/project-docs.md` | Documentation maintenance |
| Domain | strategic-planner | `roles/domain/strategic-planner.md` | Planning and roadmap |
| Stack | go | `roles/stack/go.md` | Go language conventions |
| Stack | react | `roles/stack/react.md` | React/Next.js/shadcn conventions |
| Meta | nanite-agent-manager | `roles/meta/nanite-agent-manager.md` | Nanite framework development |

## Creating Project-Level Context

Project context docs live in `<project>/.nanite/agents/` and give agents project-specific knowledge.

### Steps

1. Create the directory:

```bash
mkdir -p <project>/.nanite/agents
```

2. Create context docs for each agent profile the project needs:

```bash
touch <project>/.nanite/agents/backend.md
touch <project>/.nanite/agents/frontend.md
```

3. Fill in the context doc. Use templates at `~/.nanite/templates/` as starting points. Key sections:

   - **Stack** — Exact versions and libraries this project uses
   - **Project structure** — Directory layout with explanations
   - **Component/module inventory** — What exists, where it lives, when to use it
   - **Patterns to follow** — With file references to canonical examples
   - **Anti-patterns to avoid** — With file references to past mistakes
   - **Reference implementations** — Point at the best examples in the codebase

4. Define agents in `<project>/.nanite/config.yaml`:

```yaml
# <project>/.nanite/config.yaml
nanite_version: 2.2.0

agents:
  my-backend:
    name: My Backend Engineer
    description: Go backend development for this project
    roles: [backend, go]
    skills: [go-build, go-lint, go-test]
    context: agents/backend.md

  my-frontend:
    name: My Frontend Developer
    roles: [frontend, react]
    context: agents/frontend.md

  my-reviewer:
    name: My Code Reviewer
    roles: [code-review, go]
    skills: [go-lint, go-test]
```

5. Commit `.nanite/` to the project repo. It is part of the project, not personal config.

### What makes a good context doc

**Be specific.** Every statement should reference a real file, component, or pattern.

| Weak | Strong |
|------|--------|
| "Use appropriate components" | "Use `Panel` for card containers — see `components/ui/panel.tsx`" |
| "Follow existing patterns" | "Data tables use `DataTable` with column defs — see `components/tasks/task-table.tsx`" |
| "Handle errors properly" | "All API routes use `handleApiError()` from `lib/errors.ts`" |

**Include reference files.** Point at 2-3 canonical implementations. The agent reads them and matches.

**Document anti-patterns with evidence.** Not "don't do X" but "don't do X — see the refactor in `components/dashboard/` where this caused problems."

**Don't repeat what roles cover.** The role says "use shadcn first." The project doc says "we have a custom `StatusBadge` that wraps Badge — use it for status displays."

## Activating an Agent

Say "Boot <agent>" at the start of a session:

```
Boot cerberus-backend
```

This triggers the loading chain:
1. Look up `cerberus-backend` in `.nanite/config.yaml` under `agents:`
2. Load each role: `~/.nanite/roles/domain/backend.md`, `~/.nanite/roles/stack/go.md`
3. Load listed skills from `~/.nanite/skills/`
4. Read context: `.nanite/agents/backend.md`

If no agent matches, falls back to loading a single role by name (checks domain/, stack/, meta/).

Without "Boot <agent>", the session runs general-purpose with `default_skills` and `default_tools` from `~/.nanite/config.yaml`.

After context compaction, role and context files are re-read automatically (~500-800 tokens each).

## Bootstrapping a New Project

1. Install Nanite:

```
nanite-agent init --project .
```

Creates `.nanite/` with symlinks and wires the LLM adapter.

2. Generate context by having an agent audit the codebase:

```
/nanite-agent-manage context hadron backend
```

3. **Review the generated doc.** The agent gets 80-90% right. You need to:
   - Correct misidentified patterns
   - Add anti-patterns from experience (agent can't see git history)
   - Verify reference implementations are good examples
   - Remove anything generic that roles already cover

4. **Define agents** in `.nanite/config.yaml` composing the roles and context.

5. **Commit:**

```bash
git add .nanite/
git commit -m "Add agent project context"
```

6. **Maintain it.** When patterns change, update context docs. The Project Docs role can help.

## Skill Tiers

### Default skills

Defined in `~/.nanite/config.yaml` under `default_skills:`. Loaded for every agent automatically, on top of agent-specific skills.

Current defaults: `fast-triage`, `end-of-session`, `escalate`.

All other skills in `~/.nanite/skills/` remain available on demand but are only loaded when listed in an agent's `skills:` array.

### Project skills

Location: `<project>/.nanite/skills/` (via symlink, inherits universal skills automatically)

For operations unique to a specific project. Use sparingly — if a skill could apply across projects, it belongs in `~/.nanite/skills/` (and possibly in `default_skills` if every agent needs it).

Create a project skill when:
- The operation is unique to this project
- It involves project-specific paths, services, or conventions
- You find yourself repeatedly explaining the same multi-step procedure

Don't create a project skill when:
- A role or context doc can cover it
- It's a one-off operation
- It duplicates a universal skill with minor variations

## Vendor Plugins

Vendor plugins are third-party skills installed via the Claude CLI. They provide deep framework knowledge maintained by the vendor (e.g., Vercel's Next.js plugin knows App Router patterns, server component rules, data fetching conventions). Nanite does not manage these — they are installed and updated separately.

### Installing a vendor plugin

Install at project scope so the plugin is only active in projects that need it:

```bash
cd <project>
claude plugins install <plugin-name> --scope project
```

Example — adding Vercel/Next.js support to a frontend project:

```bash
cd ~/Projects-apps/hadron
claude plugins install vercel --scope project
```

Do NOT install vendor plugins at `user` scope (global) unless every project needs them. Project scope keeps the plugin's context injection targeted to the projects where it's relevant.

### Declaring plugin dependencies in agent config

After installing a plugin, declare it in the agent's config so that agents and humans know it's expected:

```yaml
# <project>/.nanite/config.yaml
agents:
  my-frontend:
    name: My Frontend Developer
    roles: [frontend, react]
    plugins: [vercel]              # vendor plugins this agent depends on
    context: agents/frontend.md
```

The `plugins:` key is informational — it documents what the agent expects to be installed. It does not auto-install anything. If a listed plugin is missing, the agent should flag it rather than silently degrade.

### Managing plugins

```bash
claude plugins list                              # show installed plugins
claude plugins install <name> --scope project    # install for this project
claude plugins uninstall <name> --scope project  # remove from this project
claude plugins enable <name>                     # re-enable a disabled plugin
claude plugins disable <name>                    # disable without removing
```

### Nanite skills vs vendor plugins

| | Nanite skills | Vendor plugins |
|---|---|---|
| **Source** | `~/.nanite/skills/` | `claude plugins install` |
| **Managed by** | You | Plugin vendor |
| **Scope** | Global (via symlinks) | Per-project or per-user |
| **Declared in** | `skills: [...]` in agent config | `plugins: [...]` in agent config |
| **Examples** | `go-build`, `adr`, `doc-note` | `vercel`, `typescript-lsp` |
