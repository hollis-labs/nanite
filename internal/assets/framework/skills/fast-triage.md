# Fast Triage (:fast-triage)

Collect structured feedback or triage items through a browser UI via MCP, instead of grinding through CLI back-and-forth.

## When to use

- You need the user to answer multiple structured questions (ratings, selections, rankings, yes/no, free text)
- You need the user to triage a batch of items (links, tasks, quotes, files, images) with accept/backlog/note/delete decisions
- You want to present agent suggestions for the user to accept or override
- The interaction would take many CLI turns but fits naturally into a form or card-based walkthrough

## When NOT to use

- Simple yes/no that works fine as a CLI prompt
- Pure informational output (just print it)
- Tasks that don't need user input

## Procedure

1. **Verify MCP availability.** Confirm the fast-triage MCP tools are reachable by checking for `mcp__fast-triage__collect_feedback` and `mcp__fast-triage__triage_items`. If unavailable, tell the user to start the server: `cd ~/Projects-apps/fast-triage && pnpm start`.

2. **Choose mode.**
   - **Feedback** — varied question types, unrelated questions, or mixed input formats. Tool: `mcp__fast-triage__collect_feedback`.
   - **Triage** — same decision (accept/backlog/note/delete) across many items. Tool: `mcp__fast-triage__triage_items`.

3. **Build the envelope.** Construct a v1 envelope matching the schema below. Rules:
   - IDs must be unique across the envelope and across questions/items within it.
   - Use `context` to give background — markdown is fully rendered.
   - Use `suggestion` on questions/items to speed up the user's workflow.
   - Batch related questions into one envelope rather than many single-question envelopes.
   - Prefer triage over feedback when the user makes the same kind of decision across many items.

4. **Send the envelope.** Call the appropriate MCP tool with the envelope. The call blocks until the user submits in the browser.

5. **Read the response.** Check `status`:
   - `"submitted"` — process `answers` (feedback) or `decisions` (triage).
   - `"cancelled"` — user bailed. Acknowledge and ask how to proceed.

6. **Follow up if needed.** To send a follow-up envelope, set `meta.parentEnvelopeId` to the previous envelope's ID. The UI shows continuity context.

## Envelope Schema — Feedback

```json
{
  "v": 1,
  "kind": "feedback",
  "id": "<unique-id>",
  "title": "<short title shown at top of form>",
  "context": "<optional markdown context>",
  "layout": "<optional: inline | walkthrough>",
  "questions": [
    {
      "id": "<unique-question-id>",
      "type": "<radio|select|checkbox|multiselect|text|textarea|number|boolean|rank>",
      "label": "<question text>",
      "options": [{ "value": "<value>", "label": "<display label>" }],
      "suggestion": { "value": "<suggested-value>", "rationale": "<why>" },
      "help": "<optional markdown help text>",
      "required": true,
      "allowNote": true,
      "default": "<default-value>"
    }
  ],
  "meta": { "<arbitrary>": "<pass-through data returned in response>" }
}
```

### Question types

| Need | Type | Options required |
|------|------|-----------------|
| Pick one from a short list (2-5) | `radio` | Yes |
| Pick one from a longer list | `select` | Yes |
| Pick multiple | `checkbox` or `multiselect` | Yes |
| Free text, one line | `text` | No |
| Free text, multi-line | `textarea` | No |
| A number | `number` | No |
| Yes/no toggle | `boolean` | No |
| Rank items by priority | `rank` | Yes (items to rank) |

### Layout behavior

The form auto-selects layout based on question complexity:
- **Inline** — all questions visible at once. Used for simple types (radio, select, boolean, text, number, checkbox).
- **Walkthrough** — one question at a time with Next/Back/Submit. Used for textarea, rank, or many items.

Override with `"layout": "inline"` or `"layout": "walkthrough"` on the envelope.

### Feedback response

```json
{
  "v": 1, "kind": "feedback", "id": "<same-id>", "status": "submitted",
  "answers": [
    { "questionId": "<id>", "value": "<answer>", "acceptedSuggestion": true, "note": "<optional>" }
  ],
  "meta": { "<pass-through>" }
}
```

## Envelope Schema — Triage

```json
{
  "v": 1,
  "kind": "triage",
  "id": "<unique-id>",
  "title": "<triage session title>",
  "items": [
    {
      "id": "<item-id>",
      "type": "<link|quote|task|question|file|image>",
      "title": "<item title>",
      "body": "<optional markdown body>",
      "url": "<for links>",
      "path": "<for files>",
      "src": "<for images>",
      "mime": "<for files>",
      "suggestion": { "action": "accept", "rationale": "<why>" },
      "meta": { "<item-level metadata>" }
    }
  ],
  "meta": { "<envelope-level pass-through>" }
}
```

### Item types

| Type | Best for | Key fields |
|------|----------|------------|
| `link` | URLs, articles, PRs | `url`, `body` |
| `quote` | Text snippets, excerpts | `body`, `title` |
| `task` | Actionable items, todos | `title`, `body`, `meta` |
| `question` | Questions needing decisions | `title`, `body` |
| `file` | File references | `path`, `mime`, `body` |
| `image` | Visual content | `src` or `path`, `title` |

### Triage actions

- **accept** (`a`) — keep/approve
- **backlog** (`b`) — defer for later
- **note** (`n`) — attach a note
- **delete** (`x`) — discard

Navigation: `j`/`k` or arrow keys.

### Triage response

```json
{
  "v": 1, "kind": "triage", "id": "<same-id>", "status": "submitted",
  "decisions": [
    { "itemId": "<id>", "action": "accept", "note": "<optional>" }
  ],
  "meta": { "<pass-through>" }
}
```

## Output

- On submit: process the response data and continue the workflow.
- On cancel: acknowledge and ask user how to proceed. Do not retry the same envelope.
- No narration about the tool call itself — just act on the results.

## Invariants

- ALWAYS verify MCP availability before sending an envelope
- ALWAYS use unique IDs — collisions cause silent failures
- NEVER send empty envelopes (zero questions or zero items)
- NEVER retry a cancelled envelope — the user cancelled deliberately
- Prefer triage for batch same-decision workflows; prefer feedback for varied questions
- Use `suggestion` liberally — the response tells you whether the user accepted
- Batch related questions into one envelope; don't send many single-question envelopes
- `meta` is pass-through only — use it to carry state between send and response
