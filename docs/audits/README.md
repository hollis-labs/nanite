# Audits

Output directory for deep code review passes produced by the `deep-review` skill (see `~/.nanite/skills/deep-review.md`). Each audit is a self-contained folder:

```
docs/audits/<YYYY-MM-DD>-<scope-slug>/
├── index.md                        # Entry point: scope, methodology, summary tables, next steps
├── 01-critical-<topic>.md          # Finding detail files, sorted by severity
├── 02-critical-<topic>.md
├── 03-high-<topic>.md
├── ...
└── NN-info-<topic>.md
```

## Conventions

- **Folder name:** `<YYYY-MM-DD>-<scope-slug>`. Date is the audit date, slug is the scope (`plugin-system`, `full-backend-audit`, `tool-execution-e2e`, etc.). Append `-2`, `-3` if a folder with the same date and slug already exists.
- **Finding files:** `NN-<severity>-<topic>.md` where `NN` is zero-padded and assigned in severity order (Critical → High → Medium → Low → Info). `ls` output sorts by priority.
- **One topic per file.** Related findings (e.g., three goroutine leaks in the same subsystem) can share a file. Unrelated findings must not.
- **Index never contains finding bodies** — only links and one-line titles. See the skill's output contract for required `index.md` sections.
- **Severity rubric:** Critical / High / Medium / Low / Info. See `~/.nanite/skills/deep-review.md` for definitions.

## Running a deep review

Boot the appropriate reviewer agent and give it a scope:

```
Boot nanite-reviewer-backend
Review scope: plugin system
```

or

```
Boot nanite-reviewer-frontend
Review scope: chat transcript and SSE streaming
```

The reviewer reads its project context file (`.nanite/agents/reviewer-backend.md` or `reviewer-frontend.md`), invokes the `deep-review` skill, and writes the audit folder here. It returns a one-line confirmation pointing at the new `index.md`.

## What does NOT belong here

- PR-level comments. Use GitHub PR review or the lightweight `nanite-reviewer` agent.
- Design docs or ADRs. Those go in `docs/architecture/` or an `adr/` directory.
- Post-beta polish backlog. That's `docs/frontend-punchlist.md` and the Engine backlog.
- Findings that are already tracked in `docs/beta-known-issues.md` or `.nanite/agents/plugin-dev.md` §Known Limitations. The reviewer skips these deliberately.
