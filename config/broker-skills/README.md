# Broker tool-preference skills

Files in this directory are operator-authored heuristics that bias the tool
broker's ranking toward tools the operator considers right for a class of
intents.

The broker reads its operator skills from `~/.nanite/skills/` by default
(controlled by `Config.SkillsDir` on the toolclient). The samples here are
copy-paste templates — drop them into `~/.nanite/skills/` to enable.

See `docs/decisions/ADR-003-reasoning-augmented-broker.md` for the full
authoring contract and ranking-signal precedence.

## Format

Each file is named `*.tools.preferences.md` and starts with a YAML frontmatter
block:

```markdown
---
pattern: "search code"          # substring match (case-insensitive) OR
                                # "re:<expr>" for a Go regexp
prefer:                         # ordered list of tool names to bias toward
  - dev_glob
  - dev_grep
weight: 7                       # default 5; larger = stronger bias
rationale: |
  Free-form prose explaining why this skill exists. Surfaced in slog
  when the skill matches an intent so an operator can audit drift.
---

# Free-form Markdown body

The body is for humans browsing the skills directory. The broker ignores
everything below the closing `---`.
```

## Patterns

- **Substring** (default): pattern is lower-cased and matched as a substring
  of the lower-cased intent. Example: `pattern: "audit"` matches "audit
  tasks", "audit the backlog", "I want to audit something".
- **Regex** (with `re:` prefix): pattern is compiled as a Go regexp under
  `(?i)` (case-insensitive). Example: `pattern: "re:audit (tasks|backlog)"`
  matches "audit tasks" and "audit backlog" but not "audit the meeting".

## Sample files

- `codebase-exploration.tools.preferences.md` — bias toward `dev_*` tools
  when the intent reads as code investigation.
- `task-management.tools.preferences.md` — bias toward
  `clockwork_task_list` + `clockwork_task_get` for "audit tasks/backlog/sprint"
  intents.
