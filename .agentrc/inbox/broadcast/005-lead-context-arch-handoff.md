---
from: lead
to: context-arch-agent
type: handoff
priority: high
timestamp: 2026-03-14T18:00:00Z
subject: Agent Context Architecture (EPIC-21360) — S1 Boot & Directives
---

## Mission

Implement the agent context architecture from ADR-024. Focus on S1 first (boot & directives), then S2 if time permits.

## Scope

EPIC-20260314-21360: Agent Context Architecture Implementation
Sprint S1: ACTX-S1-BOOT-AND-DIRECTIVES (4 tasks)

## Tasks (priority order)

1. **TASK-20260314-340** (A) — Add auto-boot imperative to CLAUDE.md and MEMORY.md
   - Agents shouldn't need to be told "boot" — it should be automatic from CLAUDE.md instructions
   - The auto-boot sequence is already documented in CLAUDE.md but agents don't always follow it

2. **TASK-20260314-337** (A) — Redesign MEMORY.md as boot-level directive injection
   - Current MEMORY.md is ~200 lines of storage (facts, decisions, project state)
   - Should be ~50 lines of directives that POINT to where information lives
   - Actual memory content stays in individual memory files (already exists)
   - MEMORY.md becomes a boot-time injection: "load these directives, they tell you where to find everything"

3. **TASK-20260314-339** (A) — Update frag launcher — set CLAUDE_CODE_REMOTE_MEMORY_DIR per project
   - The frag launcher doesn't exist yet (TASK-186 was archived, superseded by EPIC-14784)
   - For now, document the env var pattern and what it should do
   - Don't build the full frag CLI — just the env setup part

4. **TASK-20260314-338** (B) — Implement boot hash generation and verification
   - Generate a hash of the boot state (CLAUDE.md + MEMORY.md + bootstrap.md + boot profile)
   - Store in session context — if the hash changes mid-session, something was modified
   - Enables context loss detection between sessions

## Key Context

- **ADR-024**: Should be in the adr/ directory — read it first
- **Current MEMORY.md**: ~/.claude/projects/-Users-chrispian-Projects-apps-mentat/memory/MEMORY.md
- **Memory files**: ~/.claude/projects/-Users-chrispian-Projects-apps-mentat/memory/*.md (many individual files)
- **Boot profiles**: .agentrc/boot/*.md (meta-agent, worker, architect, reviewer, ops-tester, analyst)
- **CLAUDE.md**: Root of mentat repo — has the auto-boot instructions
- **Bootstrap**: .agentrc/bootstrap.md — iteration state

## CRITICAL: CC#15897 Hook Matcher Bug

DO NOT add multiple PreToolUse hooks matching the same tool. Claude Code runs them in parallel and silently discards updatedInput from all but the last to return. tokf owns Bash PreToolUse globally. See memory file: feedback_hook_matcher_overlap.md.

## Constraints

- TASK-351 (hook registration) is DONE — settings.json is already updated
- Do NOT modify .claude/settings.json hook matchers without checking for overlap
- MEMORY.md redesign: keep it under 50 lines. Move content to individual memory files, leave pointers.
- Use /send-message skill for ALL inbox communication (not raw file writes)
- Claim tasks in Volon before starting, update when done
- Follow docs/process/task-completion-workflow.md

## Communication

- Your session ID: `context-arch-agent`
- Project lead inbox: .agentrc/inbox/lead/
- Use /send-message lead info <subject> — <body> for status updates
- Use /send-message owner question <subject> — <body> for decisions
- The MEMORY.md redesign may need owner input — message them with your proposed structure before implementing
