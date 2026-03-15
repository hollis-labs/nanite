---
intent: pcc_global
project: mentat
updated_at: "2026-03-14"
---

# Workflows

## Build

```bash
# Backend
go build ./cmd/mentat/

# Frontend
cd ui && npm install && npm run build
```

## Run

```bash
# Development mode (default port 8090)
./mentat serve -port 8090 -db ./mentat.db -dev
```

## Test

```bash
go test ./...
```

## Agent Boot

1. Read `.agentrc/agent-boot.md` (core rules)
2. List `.agentrc/boot/*.md` for available profiles
3. If one profile, auto-select; if multiple, ask user
4. Read selected profile (e.g., `meta-agent.md`, `worker.md`, `architect.md`)
5. Read `.agentrc/bootstrap.md` (current iteration state)
6. Emit boot confirmation and begin work

Boot profiles (6): meta-agent, worker, architect, analyst, reviewer, ops-tester

## Service Management

- Mentat and dependent services managed via Cerberus MCP
- MCP servers configured in `~/.claude.json` (user scope)
- Services: Volon (Postgres), Hadron, Cortex, Cerberus

## Skills (45)

Agent core: escalate, hadron-run, check-inbox, send-message, delegate, delegate-prompt, agent-status, agent-list, discover-tools, init-lead, reorient, vault-search, project-onboard, task-artifact-hook

Planning & review: standup, pcc-sync, pcc-refresh-all, health-check, qstatus, qhealth, git-status-all, commit-all, epic-summary, cross-impact, blueprint-builder, deep-review, adr, blg, sigil-ui, planning-session, worktree-cleanup

Interactive dialogs (EPIC-41370): sprint-review, task-triage, drift-resolve, code-review, epic-plan, session-handoff, memory-audit, git-cleanup, sprint-retro

Workflows: workflow-portfolio-boot, workflow-sprint-lifecycle, workflow-session-end-capture, workflow-drift-detection, workflow-feature-branch

## Slash Commands (26)

bootstrap-update, pause-task, resume-task, pcc-refresh, sprint-end, git-status-all, standup, pcc-sync, pcc-refresh-all, commit-all, qstatus, adr, blg, reorient, deep-review, sigil-ui, health-check, qhealth, sprint-review, task-triage, drift-resolve, code-review, epic-plan, git-cleanup, sprint-retro, memory-audit

## Process Docs

- `docs/process/00_behavior-rules.md` — core agent behavior rules
- `docs/process/interactive-dialog-pattern.md` — reusable AskUserQuestion pattern for structured user interaction (gather, present, parse, execute, confirm)
- `docs/process/skill-command-anatomy.md` — two-layer architecture: commands (entry points) + skills (full instructions)
- `docs/process/epic-sprint-task-creation-guide.md` — task hierarchy conventions
- `docs/process/task-completion-workflow.md` — task closeout procedure
- `docs/process/tag-taxonomy.md` — classification tags

## Context Sync

- PCC refresh via Cortex MCP or `/pcc-refresh` command
- Recurring sync target: 2AM/2PM daily via Hadron pipeline
- Envelope guard blocks direct writes to `.agentrc/pcc/global/`

## Evidence
- Last refreshed: 2026-03-14
- Sources: CLAUDE.md, .agentrc/ directory, .claude/skills/, .claude/commands/, docs/process/, Volon MCP
