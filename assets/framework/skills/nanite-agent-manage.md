# Nanite Agent Manage (:nanite-agent-manage)

Create roles, project context docs, skills, and agent definitions for the Nanite agent framework. Runs via sub-agent.

## When to use

- `/nanite-agent-manage role <name> <description>` — Create a new agent role
- `/nanite-agent-manage context <project> <domain>` — Audit a project and generate a context doc
- `/nanite-agent-manage skill <name> <description>` — Create a new skill
- `/nanite-agent-manage agent <slug> <project> [options]` — Create an agent definition with config entry and context doc

## IMPORTANT: Run in Sub-Agent

This skill MUST be executed via the Agent tool (subagent) to keep the main context clean.

## Mode 1: Create Role

```
/nanite-agent-manage role <name> <one-line description>
```

**Examples:**
- `/nanite-agent-manage role domain backend "API, service, and data engineering"`
- `/nanite-agent-manage role stack python "Python language conventions and tooling"`
- `/nanite-agent-manage role meta infra-ops "Infrastructure and DevOps tooling"`

**Procedure:** Launch a sub-agent with:

```
You are building a new agent role definition for the Nanite agent framework.

## Role to create
- Name: {NAME}
- Type: {TYPE} (domain, stack, or meta)
- Description: {DESCRIPTION}

## Steps

1. Read existing roles for reference:
   - Domain example: ~/.nanite/roles/domain/backend.md (domain thinking, no tech stack)
   - Stack example: ~/.nanite/roles/stack/go.md (language conventions and tooling)
   - Domain example: ~/.nanite/roles/domain/code-review.md (non-coding domain role)

2. Read the guide: ~/.nanite/docs/nanite-setup-guide.md — section "Creating a New Role"

3. Write the role file to: ~/.nanite/roles/{TYPE}/{NAME}.md

   For **domain** roles (what kind of work):
   ```markdown
   # Role: {Title}

   ## Identity
   One sentence: what kind of work you focus on.

   ## Thinking
   Bullet list of how you approach problems in this domain.
   No tech stack specifics — those come from stack roles.

   ## When assigned to a project
   What to read first, what to check before making assumptions.

   ## Handoff
   What to report when work is complete or blocked.
   ```

   For **stack** roles (what tech):
   ```markdown
   # Role: {Title} Stack

   ## Identity
   One sentence: what language/framework conventions you follow.
   Note: this role is combined with a domain role.

   ## Stack
   Bullet list of technologies, frameworks, tools.

   ## Rules
   Numbered list of opinionated, specific rules.

   ## When assigned to a project
   What project files to read first.
   ```

4. Key principles:
   - Domain roles define *thinking* — how to approach the problem space
   - Stack roles define *conventions* — language idioms and tooling
   - Agents compose both: `roles: [backend, go]`
   - Don't duplicate what agent-boot.md already covers

5. Also register the role in ~/.nanite/config.yaml under the `roles:` section.

6. Return ONLY: ✓ Created role: ~/.nanite/roles/{TYPE}/{NAME}.md — {one-line summary}
```

## Mode 2: Create Project Context

```
/nanite-agent-manage context <project> <domain>
```

**domain** is one of: `frontend`, `backend`, `infra`, `data`, or a custom domain name.

**Examples:**
- `/nanite-agent-manage context hadron frontend` — Audit Hadron's frontend and generate style guide
- `/nanite-agent-manage context vanta-conduit backend` — Audit Vanta Conduit's Go backend and generate conventions doc
- `/nanite-agent-manage context cerberus infra` — Audit Cerberus's infrastructure setup

**Procedure:** Launch a sub-agent with:

```
You are auditing a project's codebase to generate a project-level context doc for the Nanite agent framework.

## Target
- Project: {PROJECT}
- Domain: {DOMAIN}
- Project root: {resolve from ~/.nanite/config.yaml projects section, or check `~/Projects-apps/{PROJECT}/` as default. Ask user if not found.}

## Steps

1. Read the relevant template:
   - For frontend: ~/.nanite/templates/frontend-project.md
   - For backend: ~/.nanite/templates/backend-project.md
   - For other domains: use the closest template as structural reference, adapt sections

2. Read the setup guide: ~/.nanite/docs/nanite-setup-guide.md — section "Creating Project-Level Context"

3. Explore the project codebase thoroughly:
   - Find the directory structure
   - Read package.json / go.mod / equivalent for stack details
   - Read config files (tsconfig, tailwind.config, vite.config, etc.)
   - Identify all significant source files
   - Read representative files to understand patterns

4. Document what you find:
   - **Stack** — exact versions and libraries
   - **Project structure** — actual directory layout with file purposes
   - **Component/module inventory** — what exists, where, what it does
   - **Patterns in use** — how data flows, how state is managed, how errors are handled
   - **Anti-patterns found** — with file:line references. Look for:
     - God components/files (>200 lines with mixed concerns)
     - Code duplication (same function in multiple files)
     - Prop tunneling / tight coupling
     - Missing abstractions (same pattern reimplemented repeatedly)
     - Inconsistent patterns (some files do X, others do Y)
     - Hardcoded values that should be constants
     - Missing types, excessive `any` usage
   - **Reference implementations** — 2-3 well-structured files that exemplify good patterns
   - **Recommendations** — prioritized list of improvements

5. Create the .nanite/agents directory if needed:
   mkdir -p {PROJECT_ROOT}/.nanite/agents

6. Write the context doc to: {PROJECT_ROOT}/.nanite/agents/{DOMAIN}.md

7. Return ONLY:
   ✓ Created context: {PROJECT_ROOT}/.nanite/agents/{DOMAIN}.md
   — {total files audited}, {number of anti-patterns found}, {number of recommendations}
```

## Mode 3: Create Skill

```
/nanite-agent-manage skill <name> <description>
```

**Examples:**
- `/nanite-agent-manage skill deploy-pipeline "Run hadron deployment pipeline with validation"`
- `/nanite-agent-manage skill db-migrate "Generate and run database migrations for Go projects"`
- `/nanite-agent-manage skill test-runner "Run project tests with coverage and report results"`

**Procedure:** Launch a sub-agent with:

```
You are building a new skill for the Nanite agent framework.

## Skill to create
- Name: {NAME}
- Description: {DESCRIPTION}

## Steps

1. Read existing skills for reference on format and conventions:
   - ~/.nanite/skills/doc-note.md (good example: sub-agent pattern, input parsing, typed output)
   - ~/.nanite/skills/adr.md (good example: concise, single-purpose)
   - ~/.nanite/skills/escalate.md (good example: minimal, focused)

2. Read the setup guide: ~/.nanite/docs/nanite-setup-guide.md — section "Skill Tiers"

3. Determine the skill tier:
   - Universal (useful everywhere) → write to ~/.nanite/skills/{NAME}.md
   - Project-specific (one project only) → ask user which project, write to {PROJECT}/.nanite/skills/{NAME}.md

4. Write the skill file following this structure:
   ```markdown
   # {Title} (:{name})

   {One-line description}. Runs via sub-agent.

   ## When to use
   - Trigger conditions and example invocations

   ## IMPORTANT: Run in Sub-Agent
   (if the skill does significant work — skip for simple inline skills)

   ## Input Format
   /{name} [arguments]

   ## Procedure
   Step-by-step instructions for execution.

   ## Output
   What format to return results in.

   ## Invariants
   - Hard rules that must never be broken
   ```

5. Key principles:
   - Single purpose. One skill does one thing.
   - Clear input/output contract. The caller knows exactly what to provide and what to expect.
   - Sub-agent for anything that reads multiple files or does significant work.
   - Invariants are hard rules, not suggestions. "ALWAYS write with status: draft" not "consider writing as draft."

6. If the skill is universal, also create the command stub:
   Write to ~/.nanite/commands/{NAME}.md:
   ```markdown
   Invoke the {name} skill.
   ```

7. Return ONLY: ✓ Created skill: {path} — {one-line summary}
```

## Mode 4: Create Agent Definition

```
/nanite-agent-manage agent <slug> <project> [--roles role1,role2] [--skills skill1,skill2] [--context filename.md] [--description "one-liner"]
```

**Examples:**
- `/nanite-agent-manage agent conduit-plugin-dev conduit --roles backend,go --skills go-build,go-lint,go-test,doc-note,doc-search --context agents/plugin-dev.md --description "Develop and maintain Conduit plugins"`
- `/nanite-agent-manage agent engine-backend engine --roles backend,go --skills go-build,go-lint,go-test`
- `/nanite-agent-manage agent nanite-frontend nanite --roles frontend,react --context agents/frontend.md`

**Procedure:** Launch a sub-agent with:

```
You are creating a new agent definition for the Nanite agent framework. An agent combines roles, skills, and project-specific context into a named profile that can be activated with "Boot <slug>".

## Agent to create
- Slug: {SLUG}
- Project: {PROJECT}
- Name: {NAME} (derive from slug if not provided — title case, e.g., "conduit-plugin-dev" → "Conduit Plugin Developer")
- Description: {DESCRIPTION} (ask user if not provided)
- Roles: {ROLES} (default: [backend, go] — ask user if unclear)
- Skills: {SKILLS} (default: [] — ask user if task-specific skills are needed)
- Context file: {CONTEXT} (default: agents/{SLUG}.md — the path relative to .nanite/)

## Steps

1. Resolve the project root from ~/.nanite/config.yaml `projects:` section.
   - If not found, ask the user for the path. Do not guess.

2. Read the project's .nanite/config.yaml to check for:
   - Existing agents (avoid slug collisions)
   - The nanite_version in use

3. Read 2-3 existing agent context docs for reference on structure and quality:
   - Check the target project's .nanite/agents/ for existing docs
   - If none exist, read from another project (e.g., cerberus, hadron, or conduit)

4. Add the agent entry to {PROJECT_ROOT}/.nanite/config.yaml under `agents:`:

   ```yaml
     {SLUG}:
       name: {NAME}
       description: {DESCRIPTION}
       roles: [{ROLES}]
       skills: [{SKILLS}]
       context: {CONTEXT}
   ```

   - Append after existing agents. Maintain YAML formatting.
   - If no `agents:` key exists, create it after `nanite_version:`.

5. Create the context doc directory if needed:
   mkdir -p {PROJECT_ROOT}/.nanite/agents

6. Create the context doc at {PROJECT_ROOT}/.nanite/{CONTEXT}:

   ```markdown
   # Agent Context: {NAME}

   ## Purpose
   {One paragraph: what this agent does and when to use it.}

   ## Key Paths
   | Path | Purpose |
   |------|---------|
   {Table of important files/directories this agent works with.
    Include paths provided by the user. If none provided, populate
    from project structure — read the project root to identify key dirs.}

   ## Conventions
   {Project-specific rules this agent should follow.
    Read existing code to identify patterns — don't generate generic content.}
   ```

   - If the user provided specific paths or context, incorporate them.
   - If the user described tasks, add a "## Scope" section summarizing what this agent covers.
   - Keep it focused. Don't repeat what roles already cover.
   - Every statement should reference a real file or directory.

7. Verify:
   - The YAML is valid (no duplicate keys, correct indentation)
   - The context file path in the config matches the actual file created
   - The roles listed exist in ~/.nanite/config.yaml `roles:` section

8. Return ONLY:
   ✓ Created agent: {SLUG} in {PROJECT}
   — Config: {PROJECT_ROOT}/.nanite/config.yaml
   — Context: {PROJECT_ROOT}/.nanite/{CONTEXT}
   — Roles: {ROLES} | Skills: {SKILLS}
```

## Invariants

- ALWAYS run via sub-agent for all four modes.
- ALWAYS read existing examples before generating. Match the conventions.
- If the project is not found in ~/.nanite/config.yaml projects section (for context mode), ask the user for the path. Do not guess.
- Generated files must be complete and usable — no TODO placeholders, no "fill this in later."
- For context mode: the audit must actually read the codebase. Do not generate generic content.
