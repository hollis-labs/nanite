# Skill & Command Anatomy

How slash commands and skills are structured in the Fragments Engine agent system.

## Two-Layer Architecture

```
.claude/commands/foo.md   ← entry point (registered as /foo)
.claude/skills/foo.md     ← full instructions (referenced by command)
```

### Commands (`.claude/commands/`)

- **Auto-discovered** by Claude Code as `/slash-commands`
- Appear in the skills list on boot
- Invocable by the user with `/command-name` or `/command-name <args>`
- Should be **short** — a few lines describing intent + a pointer to the skill file
- The special token `$ARGUMENTS` is replaced with whatever the user passes after the command name

**Example command file** (`.claude/commands/sprint-review.md`):
```markdown
Interactive sprint review — present each task with per-task decisions.

Read the full skill instructions at `.claude/skills/sprint-review.md` and follow them exactly.

Arguments: $ARGUMENTS
```

### Skills (`.claude/skills/`)

- **Not auto-discovered** as slash commands
- Hold the full procedure: steps, invariants, display formats, edge case handling
- Referenced by command files, boot profiles, or agent instructions
- Can also be read directly by the agent when context requires it (e.g., during boot)

**Why separate?** The command index is loaded into context when Claude Code starts. Keeping commands short avoids bloating the prompt. The full skill instructions are only loaded when the command is actually invoked.

## When You Need Both

If the skill should be user-invocable as a `/slash-command`, you need both files:

1. **Skill file** in `.claude/skills/` — full instructions
2. **Command file** in `.claude/commands/` — short entry point that references the skill

## When You Only Need a Skill

Some skills are never invoked directly by the user — they're referenced by:
- Boot profiles (e.g., the meta-agent profile references messaging skills)
- Other skills or workflows (e.g., a workflow that calls sub-skills)
- Agent instructions (e.g., MEMORY.md pointing to a skill for a specific pattern)

These only need a `.claude/skills/` file. No command entry point needed.

## When You Only Need a Command

Simple commands that fit in a few paragraphs don't need a separate skill file. Put everything in `.claude/commands/` directly. Examples: `bootstrap-update`, `pause-task`, `resume-task`.

**Rule of thumb**: If the instructions exceed ~30 lines, split into command + skill.

## Naming

- Use the same filename for both: `sprint-review.md` in both directories
- Use kebab-case for filenames
- The command name becomes the slash command: `sprint-review.md` → `/sprint-review`

## Should This Be a Command?

Not every skill needs a slash command. Too many commands pollutes the user's view. Ask:

- **Will the user invoke this directly?** → Command
- **Is it only called by other skills, workflows, or boot profiles?** → Skill only
- **Is it an internal agent procedure?** → Skill only

Most skills are agent-internal. Reserve `/commands` for things the user will type regularly.
