# ADR Capture (:adr)

Capture an architectural decision as a formal ADR. Runs via sub-agent to keep main context clean.

## When to use

- When the user types `:adr` or `/adr` with a decision description
- When an architectural decision is made in conversation and needs to be recorded
- Can be triggered with arguments: `/adr "We decided to use Volon as the write path for messaging"`

## IMPORTANT: Run in Sub-Agent

This skill MUST be executed via the Agent tool (subagent) to keep the main context clean. The sub-agent does the research, writing, and optional Tesseract registration. Main context gets only the confirmation.

## Procedure

Parse the user's input for the decision summary. If no explicit summary, use recent conversation context to infer the decision.

Launch an Agent with this prompt (fill in {DECISION_SUMMARY} from user input):

```
You are an ADR writer. Write a formal Architecture Decision Record.

## Decision to capture:
{DECISION_SUMMARY}

## Steps:

1. Find the next ADR number:
   - Determine the ADR directory: if the workspace has an `adr/` directory, use it. Otherwise, create `adr/` in the workspace root.
   - Glob for `adr/ADR-*.md` to find existing ADRs
   - Find the highest number, increment by 1
   - Format: ADR-NNN (zero-padded to 3 digits)

2. Generate a slug from the decision title (lowercase, hyphens, no special chars)

3. Write the ADR file to `adr/ADR-{NNN}-{slug}.md` using this format:

   # ADR-{NNN}: {Title}

   ## Status: Accepted

   ## Date: {today's date YYYY-MM-DD}

   ## Context

   {Why this decision was needed. What problem or question prompted it.}

   ## Decision

   {The decision itself. Clear, specific, actionable.}

   ## Consequences

   {What follows from this decision. Both positive and negative.
   Include migration needs, breaking changes, new patterns to follow.}

   ## References

   {Related ADRs, design docs, backlog items, or external resources.}

4. Register the ADR in Tesseract (if available):
   - Use `mcp__tesseract__knowledge_write` with namespace `user/{USER}/knowledge/{PROJECT}`, key `adr-{NNN}`, kind `doc`, source `filesystem`, pointer scheme `file`, pointer locator `{absolute file path}`, a concise summary, optional body, and the required `author_agent_id` and `session_id`
   - Encode tags as a JSON string, for example `["adr","project:{PROJECT}"]`
   - If not available, skip — the file is the primary artifact

5. Return ONLY this format:
   ✓ ADR-{NNN} written: {title}
   → {file_path}
```

## Output

Display the sub-agent's one-line confirmation. No additional commentary needed.

## Invariants

- ALWAYS run via sub-agent
- ADR files go in the workspace's `adr/` directory
- Never overwrite an existing ADR — always use the next available number
- Date is always today's date
- Status defaults to "Accepted" (can be changed manually later)
- Keep ADR concise — 1-2 paragraphs per section max
