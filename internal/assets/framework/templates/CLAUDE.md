# Project

<!-- DEPRECATED: legacy loader-block template inherited from agentrc.
     Marked for retirement in plan Task 2.2 Phase 1. The nanite install
     CLI and the claude adapter now write managed sections directly; this
     template is kept only as a reference during the transition. Do not
     scaffold new projects from this file. -->

## Nanite
- If `.nanite/boot-prompt.md` exists, read it first for session context.
- If the user says "Boot <agent>", look up the agent in `.nanite/config.yaml` under `agents:`. Load each role file from `~/.nanite/roles/` (using the `file:` path from `~/.nanite/config.yaml` role definitions), load the listed skills, and read the project context file from `.nanite/` if specified.
- If the user says "Boot <role>" and no agent matches, fall back to loading that single role from `~/.nanite/roles/` by type directory (domain/, stack/, meta/).
- After context compaction, re-read the active role and project context files.
- Do not guess when uncertain. Stop and ask.
- Prefer focused, minimal output. No trailing summaries.
- Sub-agent output stays in the sub-agent. Main context gets one-line confirmations.
