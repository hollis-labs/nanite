# Central Agent Filesystem: Write Path Conventions

**Reference**: ADR-015 (Central Agent Filesystem)
**Date**: 2026-03-14

## The Decision Rule

> **"Would a contributor reading the repo need this?"**
> - **Yes** -- it stays in the project repo.
> - **No** -- it goes to the central filesystem at `~/.agentrc/files/`.

This is the single test for every file an agent produces. Apply it before every write.

## What Goes Where

### Project Repo (`.agentrc/` and repo root)

Files that are meaningful in the context of the codebase itself.

| Path | Contents | Why it stays in repo |
|------|----------|---------------------|
| `.agentrc/boot/*.md` | Boot profiles (worker, meta-agent, architect, etc.) | Contributors need these to understand how agents operate on this project |
| `.agentrc/pcc/` | Project context cache | Auto-generated from project state; scoped to this codebase |
| `.agentrc/bootstrap.md` | Current iteration state | Tracks where the project left off between sessions |
| `agentrc.yaml` | Agent config for this project | Project-specific agent settings |
| `adr/` | Architecture decision records | Decisions about *this* codebase |
| `docs/` | Technical documentation, architecture | Describes *this* codebase for contributors |
| `CLAUDE.md` | Agent boot instructions | Tells agents how to work on *this* project |
| Application code, tests, configs | Everything else in the repo | Obviously repo-scoped |

### Central Filesystem (`~/.agentrc/files/`)

Files that are operational, cross-cutting, or agent-generated artifacts not tied to a single codebase.

| Path | Contents | Why it goes central |
|------|----------|---------------------|
| `~/.agentrc/files/projects/<id>/docs/` | Agent-generated reports, analysis output | Operational artifacts, not code documentation |
| `~/.agentrc/files/projects/<id>/output/` | Command output, build logs, test results | Ephemeral operational data |
| `~/.agentrc/files/projects/<id>/scripts/` | Agent utility scripts | Not part of the application |
| `~/.agentrc/files/projects/_portfolio/` | Cross-project docs (roadmap, system map) | Spans multiple repos, no single home |
| `~/.agentrc/files/agents/<name>/` | Per-Special-Agent resources | Agent-specific, not project-specific |
| `~/.agentrc/files/templates/` | Shared templates, reference docs | Reusable across projects |
| `~/.agentrc/logs/` | All service logs (volon.log, hadron.log, etc.) | Operational, not code |
| `~/.agentrc/logs/sessions/` | Session logs and transcripts | Ephemeral session data |

### Central Filesystem Structure

```
~/.agentrc/
├── agentrc.yaml                  # Global agent config
├── boot/                         # Global boot profiles (if any)
├── files/
│   ├── projects/
│   │   ├── volon/
│   │   │   ├── docs/             # Agent reports about Volon
│   │   │   ├── output/           # Build/test output
│   │   │   └── scripts/          # Utility scripts
│   │   ├── mentat/
│   │   │   ├── docs/
│   │   │   ├── output/
│   │   │   └── scripts/
│   │   ├── cortex/
│   │   └── _portfolio/           # Cross-project documents
│   ├── agents/
│   │   ├── mentat/               # Mentat Special Agent resources
│   │   ├── carrier/
│   │   └── shared/
│   └── templates/
├── logs/
│   ├── volon.log
│   ├── hadron.log
│   ├── cerberus.log
│   └── sessions/
└── .git/                         # Private git repo for backup
```

## Examples

| Scenario | Location | Reasoning |
|----------|----------|-----------|
| ADR about Mentat's plugin system | `adr/ADR-NNN-plugin-system.md` (repo) | Decision about this codebase |
| Session log from a planning meeting | `~/.agentrc/logs/sessions/` (central) | Ephemeral, not code-relevant |
| Cross-project dependency report | `~/.agentrc/files/projects/_portfolio/` (central) | Spans multiple repos |
| Boot profile for worker agent | `.agentrc/boot/worker.md` (repo) | Contributors need it to run agents here |
| PCC capsule for project context | `.agentrc/pcc/` (repo) | Auto-generated, project-scoped cache |
| Architecture doc for the chat engine | `docs/architecture/` (repo) | Describes this codebase |
| Agent-generated code scan results | `~/.agentrc/files/projects/mentat/output/` (central) | Operational artifact |
| Sprint standup summary | `~/.agentrc/files/projects/_portfolio/` (central) | Cross-project, operational |
| Shared hook scripts | `~/.agentrc/files/agents/shared/` (central) | Not tied to one project |

## Discovery Contract

The central filesystem stores the documents. Discovery happens through Cortex:

- **Files** = the document itself (human-readable, git-backed in the private `~/.agentrc/` repo)
- **Cortex** = pointer + metadata (queryable via MCP, searchable, typed)
- **PCC** = auto-generated cache (never manually maintained)

Every document written to `~/.agentrc/files/` should also get a Cortex pointer record with metadata (type, date, tags, summary, file path).

## Envelope Guard: Protected Paths

The hook at `.claude/hooks/envelope-guard.sh` enforces write restrictions. The following paths are guarded:

### Hard-blocked (all roles, exit 2)

| Path pattern | Reason | How to write instead |
|--------------|--------|---------------------|
| `.agentrc/state/` | Managed by gui-server | Use the GUI server API |
| `.agentrc/pcc/global/` | Refreshed only via `/pcc-refresh` | Run the `/pcc-refresh` slash command |

### Hard-blocked (volon-managed role)

| Path pattern | Reason | How to write instead |
|--------------|--------|---------------------|
| `.agentrc/tasks/` | Managed by Volon | Use `volon_task_create`, `volon_task_transition` MCP tools |
| `.agentrc/backlog/` | Managed by Volon | Use `volon_backlog_capture` MCP tool |
| `.agentrc/logs/` | Managed by Volon | Use Volon MCP tools |
| `.agentrc/bootstrap.md` (sub-agents only) | Only primary agent may update | Escalate to orchestrator |

### Warned (orchestrator role)

| Path pattern | Warning |
|--------------|---------|
| `config/` | "Ensure this is intentional" |
| `agentrc.yaml` | "Ensure this is intentional" |
| Any file (no task claimed) | "No task is currently 'doing'" |

### Additional guards

- **Destructive shell ops** (`rm`, `mv`, `chmod`) on `.agentrc/(state|pcc/global)` and `.agentrc/(tasks|backlog|state|logs)/` are blocked.
- **Direct service launches** (`go run ./cmd/...` or `./binary`) are blocked; use Cerberus instead.
- **Direct SQLite writes** to `volon.db` are blocked; use Volon MCP tools.
- **Task-driven mode** (when `VOLON_TASK_DRIVEN=1`): all writes to non-whitelisted paths are blocked until a task is claimed.

## Summary

The convention is simple: code-coupled documentation lives in the repo, everything else goes central. When in doubt, ask: "Would a PR reviewer or new contributor benefit from seeing this file?" If yes, repo. If no, central.
