# Tag Taxonomy

> Canonical reference for task tags used in Volon. All agents must apply tags from this taxonomy when creating tasks.

## Required Tags

Every task must have exactly one tag from each of these four categories.

### Risk (exactly one)

| Tag | When to use |
|-----|-------------|
| `risk-low` | Single file, config change, additive, no breaking changes |
| `risk-medium` | Multiple files, new feature, refactor, schema change |
| `risk-high` | Cross-project, breaking change, data migration, security-sensitive |

### Domain (at least one)

`backend`, `frontend`, `api`, `database`, `mcp`, `cli`, `gui`, `infra`, `docs`

### Type (exactly one)

`bug`, `feature`, `refactor`, `cleanup`, `research`, `audit`, `migration`, `config`

### Effort (exactly one)

| Tag | Criteria |
|-----|----------|
| `effort-small` | < 1 hour, single file |
| `effort-medium` | 1-4 hours, multiple files |
| `effort-large` | 4+ hours, cross-package or cross-project |

---

## Optional Tags

Add when applicable.

### Scope

- `cross-project` — affects multiple repos

### Constraints

- `requires-build` — binary must be rebuilt after
- `requires-restart` — service restart needed
- `requires-migration` — DB migration involved
- `breaking-change` — breaks existing API/config
- `needs-human-review` — human must approve before merge

### Quality

- `security`, `stability`, `performance`, `type-safety`

### Execution

- `can-parallel` — safe to run alongside other tasks
- `manual-only` — needs human interaction, can't be automated

---

## Automation Rules

These tag combinations drive automated task selection in Hadron pipelines and agent scheduling.

### Nightly Unattended

Tasks an agent can safely execute overnight without human supervision.

```
risk-low AND effort-small AND NOT requires-build AND NOT needs-human-review
```

### Quality Sprint

Tasks focused on hardening and reliability.

```
security OR stability OR type-safety
```

### Quick Wins

Low-effort tasks scoped to a single project.

```
effort-small AND NOT cross-project
```

### Frontend Sprint

All UI-related work.

```
frontend OR gui
```
