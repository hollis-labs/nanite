# Boot Prompt (:boot-prompt)

Generate a boot prompt for the next session. Manual trigger only — never automated.

## When to use

- When the user explicitly asks to create a handoff for the next session
- When the user says "let's wrap up" or "save this for next time"
- NEVER automatically at session end

## Procedure

1. Ask the user: "What should the next session focus on?"
   - If they give specifics, use those
   - If they say "everything we discussed", synthesize the current state

2. Generate the boot prompt with this structure:

```markdown
# Session Boot — {date}

> **Memory + knowledge:** Tesseract v0.9 is primary. Recall first with `mcp__tesseract__tesseract_recall` using summary projection, hydrate selected `revision_id` values with `tesseract_get_revision`, and touch only summary-only hits that shaped work. Use typed memory namespaces and knowledge namespaces under `user/{id}/knowledge`; tasks remain in Torque. See `~/.nanite/docs/tesseract-v0.9-contract.md` for the full contract.

## Where We Left Off
{1-3 sentences: what was accomplished this session}

## Current State
{What's true right now — decisions made, files created, things changed}

## Next Actions
{Numbered list of what the next session should tackle, in priority order}

## Key Context
{Only include if there are non-obvious decisions or constraints the next agent needs to know.
Skip this section entirely if the next actions are self-explanatory.}
```

3. Write the file to: `./boot-prompt.md` in the current workspace root.
   - If `.nanite/` exists in the workspace, write to `.nanite/boot-prompt.md` instead.

4. Confirm: "Boot prompt written to {path}. Next session: read it first."

## Invariants

- NEVER run automatically. Only on explicit user request.
- Keep it under 1K tokens. If you can't, you're including too much.
- Focus on WHAT'S NEXT, not what happened. The next agent needs direction, not history.
- Do not include portfolio state, epic counts, or system context.
- Overwrite previous boot-prompt.md — there's only ever one.
- ALWAYS include the Tesseract anchor blockquote between the title and the first `##` section. It is part of the standard boot-prompt contract. Copy it verbatim from this skill's template.
