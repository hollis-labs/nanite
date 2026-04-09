# Agent Boot — v2.2

Core rules for all agent sessions. Loaded from `~/.nanite/agent-boot.md`.

## Rules

1. **Engine is the task system of record.** All work tracked as Engine tasks. Update status immediately when starting, completing, or blocking.
2. **Ground from files, not chat.** Re-read source files and Engine state rather than relying on conversation history. Chat drifts; files don't.
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

The user says "Boot <agent>" to activate a named agent. The loading sequence:

1. Look up the agent slug in `./.nanite/config.yaml` under `agents:`.
2. For each role listed, resolve the file path from `~/.nanite/config.yaml` role definitions and read from `~/.nanite/roles/`.
3. Load each listed skill from `~/.nanite/skills/`.
4. If a `context:` file is specified, read it from `./.nanite/` (relative to the project).
5. If no agent matches the slug, fall back to loading a single role by name from `~/.nanite/roles/` (check domain/, stack/, meta/ subdirectories).

### Compaction recovery

Role and context files are small (~500-800 tokens). After context compaction, re-read all active role files and the context file to restore conventions. The instruction to do this lives in the never-compacted CLAUDE.md layer.

### Default behavior

Without a "Boot <agent>" instruction, the session runs as general-purpose with universal skills only. Projects can define agents but none activate automatically.
