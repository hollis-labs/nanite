# Epic / Sprint / Task Creation Guide

> Use this as an instruction prompt when asking an agent to create work items in Volon.

## Core Rules

1. **Never create empty shells.** Every epic gets at least one sprint. Every sprint gets concrete tasks. If scope is unclear, create a single "Scope and decompose" task as the first task.
2. **Not everything is Priority A.** Use the full range. A = blocks other work or is critical. B = important, do soon. C = nice to have, future.
3. **Tasks must be executable without follow-up questions.** Include enough context in description, pointers, and acceptance_criteria for an agent to pick it up cold.
4. **System-wide work needs clear scoping.** If an epic spans multiple projects, note "SYSTEM-WIDE" in the title. Create tasks under the project they actually modify, not the project that planned them.

---

## Epic Creation

```
volon_epic_create:
  project_id: "<project>"        # Required. The project this epic belongs to.
  title: "<verb> <what> — <why>" # Clear, specific. Under 80 chars.
  priority: "A" | "B" | "C"     # See priority guide below.
  description: |
    <2-3 sentence summary of what this epic achieves and why it matters.>

    Key context:
    - <relevant ADR references>
    - <dependencies on other epics>
    - <what this enables downstream>

    Phases/sprints planned:
    1. <phase 1 name> — <scope summary>
    2. <phase 2 name> — <scope summary>
```

**Priority guide for epics:**
- **A**: Blocks other epics, fixes broken things, or is the current iteration focus
- **B**: Important but not blocking. Next iteration candidate.
- **C**: Future work, research, nice-to-have

---

## Sprint Creation

```
volon_sprint_create:
  project_id: "<project>"
  title: "<Epic Short Name> — <Sprint Focus>"
  epic_id: "<epic_id>"           # Always link to parent epic.
  priority: "A" | "B" | "C"     # Usually matches epic priority.
```

**Sprint naming convention:** `<Epic Abbreviation> — <Phase/Focus>`
- Example: "Quality Gates — Shared Config & Core Tooling"
- Example: "Nanite RAG — Phase 2: Vault Profiles"
- Example: "Volon Stabilization — Critical Fixes"

**Sprint sizing:** 3-8 tasks per sprint. If you have more, split into multiple sprints.

---

## Task Creation

```
volon_task_create:
  project_id: "<project>"        # The project where the code change happens.
  sprint_id: "<sprint_id>"       # Always assign to a sprint.
  title: "<Verb> <what> — <specifics>"
  priority: "A" | "B" | "C"
  risk: "low" | "medium" | "high"
  description: |
    <What to do, why, and where. Enough for an agent to execute without asking questions.>

    Specifics:
    - <file paths or packages to modify>
    - <approach or implementation notes>
    - <what NOT to do / constraints>

    Related: <TASK-IDs, ADR references>
  acceptance_criteria: |
    - <Testable criterion 1>
    - <Testable criterion 2>
    - <Build passes, tests pass, etc.>
  pointers: "<file paths, directories, related task IDs>"
  tags: "<comma-separated tags from taxonomy>"
```

### Title Format

Start with an action verb. Be specific enough to understand without reading the description.

| Good | Bad |
|------|-----|
| "Add pre-close validation to sprint status updates" | "Fix sprint closing" |
| "Rename Hadron Task type to Step for cross-project clarity" | "Naming cleanup" |
| "Build /discover-tools skill for MCP tool inventory" | "New skill" |

### Priority Guide for Tasks

| Priority | When to Use | Examples |
|----------|-------------|---------|
| **A** | Blocks other tasks, fixes a bug, security issue, or is in the active sprint | Schema migration, broken query, security finding |
| **B** | Important, do this iteration. Most feature work. | New skill, refactor, new API endpoint |
| **C** | Future, research, nice-to-have, low urgency | Provider support, UI polish, research spikes |

**Rule of thumb:** In a sprint of 6 tasks, 2 should be A, 3 should be B, 1 can be C. If everything is A, nothing is.

### Risk Guide

| Risk | Criteria | Examples |
|------|----------|---------|
| **low** | Single file, config change, additive, no breaking changes | New skill (markdown), config update, docs |
| **medium** | Multiple files, new feature, refactor, schema change | New API endpoint, rename across package, migration |
| **high** | Cross-project, breaking change, data migration, security-sensitive | Module rename across 7 repos, DB schema redesign, auth changes |

---

## Tag Taxonomy

Apply tags from these categories. Every task should have at least: one risk tag, one domain tag, one type tag, and one effort tag. See [tag-taxonomy.md](tag-taxonomy.md) for full reference and automation rules.

### Required Tags

**Risk** (exactly one):
- `risk-high`, `risk-medium`, `risk-low`

**Domain** (at least one):
- `backend`, `frontend`, `api`, `database`, `mcp`, `cli`, `gui`, `infra`, `docs`

**Type** (exactly one):
- `bug`, `feature`, `refactor`, `cleanup`, `research`, `audit`, `migration`, `config`

**Effort** (exactly one):
- `effort-small` (< 1 hour, single file)
- `effort-medium` (1-4 hours, multiple files)
- `effort-large` (4+ hours, cross-package or cross-project)

### Optional Tags (add when applicable)

**Scope:**
- `cross-project` — affects multiple repos

**Constraints:**
- `requires-build` — binary must be rebuilt after
- `requires-restart` — service restart needed
- `requires-migration` — DB migration involved
- `breaking-change` — breaks existing API/config
- `needs-human-review` — human must approve before merge

**Quality:**
- `security`, `stability`, `performance`, `type-safety`

**Execution:**
- `can-parallel` — safe to run alongside other tasks
- `manual-only` — needs human interaction, can't be automated

---

## Example: Complete Epic → Sprint → Tasks

```
Epic: "Add struct validation to all HTTP API handlers"
  Priority: B
  Description: "Install go-playground/validator and add struct-level
    validation tags to API request types. Replaces manual if-checks
    with declarative validation. Catches invalid inputs at the boundary."

Sprint: "Validation — Mentat & Volon APIs"
  Priority: B
  Epic: <epic_id>

Task 1: "Add go-playground/validator to Mentat API handlers"
  Priority: B | Risk: low | Sprint: <sprint_id>
  Tags: backend, feature, risk-low, effort-medium, api, can-parallel
  Description: "Install go-playground/validator. Add validate tags to
    request structs in internal/api/. Start with chat endpoints
    (SendMessage, CreateSession). Run go test ./internal/api/..."
  Acceptance: "All chat API request structs have validate tags.
    Invalid requests return 400 with field-level error messages.
    Tests pass."
  Pointers: "internal/api/handlers.go, internal/api/types.go"

Task 2: "Add go-playground/validator to Volon HTTP handlers"
  Priority: B | Risk: medium | Sprint: <sprint_id>
  Tags: backend, feature, risk-medium, effort-medium, api, can-parallel
  Description: "Same pattern as Mentat. Volon has 67 HTTP endpoints —
    start with task/sprint CRUD (highest traffic). Add validate tags
    to request structs in internal/guiserver/httpserver/."
  Acceptance: "Task and sprint CRUD endpoints validate input.
    Invalid requests return 400. Tests pass."
  Pointers: "internal/guiserver/httpserver/server.go"
```

---

## Anti-Patterns

| Don't | Do Instead |
|-------|-----------|
| Create an epic with no sprints | Always create at least one sprint with tasks |
| Set everything to Priority A | Use the full A/B/C range — 2A, 3B, 1C per sprint |
| Write vague task titles ("fix stuff") | Be specific ("Fix ClaimNextTodo query — flat YAML key fallback") |
| Leave description empty | Write enough for a cold-start agent to execute |
| Skip acceptance_criteria | Add testable criteria ("builds pass", "API returns 400 on invalid input") |
| Skip tags | Apply all required categories (risk, domain, type, effort) |
| Create tasks in wrong project | Task goes in the project where code changes happen |
| Forget pointers | Add file paths so agents don't waste time searching |
