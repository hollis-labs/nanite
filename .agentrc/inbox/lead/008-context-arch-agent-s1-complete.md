---
from: context-arch-agent
to: lead
type: info
priority: normal
timestamp: 2026-03-14T19:00:00Z
subject: ACTX-S1-BOOT-AND-DIRECTIVES sprint complete — all 5 tasks done
---

## Sprint Complete: ACTX-S1-BOOT-AND-DIRECTIVES

All 5 tasks in the Agent Context Architecture S1 sprint are done.

### Summary

1. **TASK-340** (done) — Auto-boot imperative added to mentat MEMORY.md, cortex MEMORY.md, and global `~/.claude/CLAUDE.md`. Agents now boot without user saying "boot".

2. **TASK-337** (done) — MEMORY.md redesigned from 200 lines of inline facts → 46 lines of directives (under 50-line budget). Created 4 new backing memory files: `project_architecture_direction.md`, `project_naming_conventions.md`, `project_standardization.md`, `user_preferences.md`. All prior content preserved in individual files.

3. **TASK-352** (done) — Added `CLAUDE_CODE_REMOTE_MEMORY_DIR=~/.agentrc/memory/{project_id}` to Volon executor env (runner.go line 548). Build + vet clean. Binary installed via `go install`.

4. **TASK-339** (done) — frag launcher env spec documented at `docs/architecture/frag-launcher-env-spec.md`. Covers: memory dir, Postgres DSN, boot hash, health pre-check, project ID resolution, example implementation.

5. **TASK-338** (done) — Boot hash generation added to `~/.agentrc/hooks/shared/session-inject.sh`. Format: `FE-YYYYMMDD-{4hex}`. Written to `/tmp/frag-boot-hash-{pid}.id`, exported as `FRAG_BOOT_HASH`, displayed in session context block for all project roles.

### Files Modified
- `~/.claude/CLAUDE.md` — strengthened auto-boot language
- `~/.claude/projects/-Users-chrispian-Projects-apps-mentat/memory/MEMORY.md` — full redesign
- `~/.claude/projects/-Users-chrispian-Projects-apps-cortex/memory/MEMORY.md` — added imperative
- `~/Projects-apps/volon/internal/runtime/scheduler/runner.go` — one-line env var addition
- `~/.agentrc/hooks/shared/session-inject.sh` — boot hash generation
- `~/Projects-apps/mentat/docs/architecture/frag-launcher-env-spec.md` — new doc

### No Conflicts
All changes are in boot/memory infrastructure. No overlap with lead's quality gates, GUI fixes, or hook registration work.
