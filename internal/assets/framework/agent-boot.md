# Agent Boot — v2.3

Core rules for all agent sessions. Loaded from `~/.nanite/agent-boot.md`.

## Rules

1. **Clockwork is the task system of record.** All work tracked as Clockwork tasks (`mcp__clockwork__*`). Update status immediately when starting, completing, or blocking.
2. **Ground from files, not chat.** Re-read source files and Clockwork state rather than relying on conversation history. Chat drifts; files don't.
3. **Single writer.** Only one agent writes to a given file at a time. If another agent owns it, message them.
4. **Record non-trivial decisions.** Use `/adr` before proceeding with architectural or design decisions.
5. **Stop if uncertain.** Ask for clarification rather than guessing. Wrong guesses cost more than a question.
6. **Minimal output.** No trailing summaries, no restating what was just done. Lead with the answer or action.
7. **Flag issues inline.** When you notice anti-patterns, code smells, or issues during your work, briefly note them with a file:line reference. Don't stop working to explain — flag and continue.

## Context loading

When booting in a directory:

1. Check `./.nanite/boot-prompt.md` — session context from a previous handoff.
2. Check `./.nanite/config.yaml` — project-local agent definitions and overrides.
3. Check `./CLAUDE.md` — project-specific rules (Claude adapter).

If none of these exist, you're in a plain directory. Universal skills are still available via `~/.nanite/skills/`.

## Agents

Agents are named configurations that compose roles, skills, and project context. Agent definitions live in project-level `.nanite/config.yaml` under the `agents:` key.

### Activation

The user says "Boot <name>" to activate a named agent or playbook. The loading sequence:

1. Look up the slug in `./.nanite/config.yaml` under `agents:`.
2. If found — **agent boot:**
   a. For each role listed, resolve the file path from `~/.nanite/config.yaml` role definitions and read from `~/.nanite/roles/`.
   b. Load `default_skills` from `~/.nanite/config.yaml`, then the agent's own `skills:` list. Defaults + agent-specific are additive (no duplicates).
   c. Note `default_tools` from `~/.nanite/config.yaml` — these MCP tools are expected to be available in every session.
   d. If a `context:` file is specified, read it from `./.nanite/` (relative to the project).
3. If no agent matches — check `~/.nanite/playbooks/<slug>.md`. If found — **playbook boot:**
   a. Read the playbook file and parse frontmatter for `inputs`, `roles`, `skills`, `read_only`.
   b. Load each role listed in the playbook's `roles:`.
   c. Note the listed skills as available for the session.
   d. Resolve required inputs from the boot command or prompt the user for missing ones.
   e. Replace `{{input_name}}` placeholders in the playbook body with resolved values.
   f. The rendered playbook becomes the active session context.
4. If neither agent nor playbook matches, fall back to loading a single role by name from `~/.nanite/roles/` (check domain/, stack/, meta/ subdirectories).

## Playbooks

Playbooks are parameterized session templates stored in `~/.nanite/playbooks/`. Unlike agents (fixed persona for a project), playbooks are reusable patterns with variable inputs — typically for cross-project exploration, research, and planning.

Use `/playbook` to list, inspect, or boot playbooks. Or `Boot <playbook-name>` with inputs.

Playbooks declare `inputs:` in frontmatter. Required inputs must be provided at boot time or the session will prompt for them. Playbooks with `read_only: true` enforce no writes to target project directories.

### Compaction recovery

Role and context files are small (~500-800 tokens). After context compaction, re-read all active role files and the context file to restore conventions. The instruction to do this lives in the never-compacted CLAUDE.md layer.

### Default behavior

Without a "Boot <agent>" instruction, the session runs as general-purpose with `default_skills` and `default_tools` from `~/.nanite/config.yaml`. All skills in `~/.nanite/skills/` remain available on demand. Projects can define agents but none activate automatically.
