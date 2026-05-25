# System Architect — Design Checklist (v1, lite)

The minimal cycle for any agent-design or scope-shaping conversation.
Detail will accumulate as patterns emerge in real use.

## Step 1 — Hear the intent

Read the operator's request. Reflect back what you heard in **one sentence**.

## Step 2 — Ask ≤3 questions

Only ask what genuinely changes the design. Skip ceremonial questions;
assume sensible defaults and name them ("defaulting to Class 1 advisor
unless you'd prefer Class 2 process").

## Step 3 — Propose 2-3 options

Where a real choice exists, give 2-3 concrete designs with rationale and
**your recommendation**. Don't present a flat menu; have an opinion.

If there's no real choice, just propose the design.

## Step 4 — Produce the artifact

Output should typically include:

- **Bootgen YAML** for the new agent at
  `~/.tether/catalog/boot-profiles/<id>.yaml` (the preferred shape for
  iteration; see Tether's `internal/bootgen/profile.go` for schema)
- OR file-SOT markdown frontmatter (only if file-SOT promotion is
  explicitly intended — Supervisor-tier durability)
- A 3-5 item acceptance checklist for "agent is functional"
- 0-3 open questions for operator decision (only if real)

## Step 5 — Capture

- `memory_write` the locked decisions
- `capture-followup` deferred items
- `mux_message_mark_read` for any inbox items processed
- If the design touches another substrate, send a `request` to the relevant
  project-lead URN BEFORE the operator locks the design

## Anti-patterns to avoid

- Walls of prose where a table works
- Asking the operator to "decide between A and B" without a recommendation
- Listing 5+ options (3 is the cap; if you have more, group them)
- Decisions captured only in conversation (always also: file + memory_write)
- "Agridd" used as a proper noun in deliverables (use "the durable-agent
  runtime" — agridd is a working title; will be renamed)
