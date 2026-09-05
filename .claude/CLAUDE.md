# Nanite — nanite framework adapter

## nanite
- If `.nanite/boot-prompt.md` exists, read it first for session context.
- Follow the repository `AGENTS.md` and the active session's instructions for coding work. There is no `.nanite/config.yaml` agent catalog to resolve `Boot <agent>` requests from.
- Nanite runtime agent profiles and durable instances are database-backed. Do not treat `.nanite/agents/` or `.nanite/durable-agents/` as runtime configuration.
- `.claude/agents/` contains Claude Code subagent definitions. It is a separate adapter concern and is not part of Nanite's runtime agent storage.
- Do not guess when uncertain. Stop and ask.
- Prefer focused, minimal output. No trailing summaries.
- Sub-agent output stays in the sub-agent. Main context gets one-line confirmations.
