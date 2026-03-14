# ADR-015: Central Agent Filesystem

**Status:** Accepted
**Date:** 2026-03-14
**Context:** Docs scattered across 7+ repos, agents write to project repos causing git/worktree conflicts

## Problem

Agent-generated documents (architecture docs, reports, scripts, logs, drafts) are stored in project repos alongside code. This creates:
1. Six+ locations to check for docs (repo docs/, adr/, .agentrc/pcc/, Cortex, memory, Nanite)
2. Git conflicts when multiple agents write via worktrees
3. No cross-project document discovery without checking each repo
4. Operational artifacts (logs, output, scripts) polluting project git history
5. No single place for a human or agent to review all project documentation

## Decision

**Establish `~/.agentrc/files/` as the central agent filesystem for all disk writes that are not code-coupled.**

### What goes central (`~/.agentrc/files/`):
- Agent-generated artifacts (scripts, output, reports, drafts)
- Cross-project and portfolio-level docs (roadmap, system map)
- Agent-specific resources (per Special Agent)
- Shared resources (templates, reference docs)
- All service logs (`~/.agentrc/logs/`)

### What stays in project repos:
- Architecture docs *about the code* (ARCHITECTURE.md, inline docs)
- ADRs that are *decisions about that codebase*
- PCC (project-specific context cache — auto-generated)
- Code, tests, configs

### The distinction:
If a doc would make sense in a PR review or to a contributor reading the repo, it stays in the repo. If it's operational, meta, or cross-cutting, it goes central.

### Discovery contract:
- **Files** = the document (human-readable, git-backed)
- **Cortex** = the pointer + metadata (queryable, searchable, typed)
- **PCC** = auto-generated cache (never manually maintained)

Every doc written to the central filesystem also gets a Cortex pointer record with metadata (type, date, tags, summary, file path). Discovery happens through Cortex; reading happens through the filesystem.

## Structure

```
~/.agentrc/
├── agentrc.yaml
├── boot/
├── files/
│   ├── projects/
│   │   ├── volon/
│   │   │   ├── docs/
│   │   │   ├── output/
│   │   │   └── scripts/
│   │   ├── mentat/
│   │   ├── cortex/
│   │   └── _portfolio/        # Cross-project docs
│   ├── agents/
│   │   ├── mentat/            # Mentat-specific resources
│   │   ├── carrier/
│   │   └── shared/
│   └── templates/
├── logs/
│   ├── volon.log
│   ├── hadron.log
│   ├── cerberus.log
│   └── sessions/
└── .git/                      # Private repo for backup
```

## Consequences

- One place to find all operational/meta docs
- Git worktree safe — agent doc writes don't conflict with code worktrees
- Enables agent file sharing across projects
- Log aggregation in one directory
- Private git backup without polluting project repos
- Requires discipline: code-coupled vs operational distinction must be maintained
- Cortex pointer contract adds one MCP call per doc write
- Adds one more "thing to know about" — mitigated by clear convention docs

## References

- ADR-013 (Conduit Separation) — central `~/.agentrc/` model
- CONDUIT-S3-CENTRAL-AGENTRC sprint — implementation vehicle
