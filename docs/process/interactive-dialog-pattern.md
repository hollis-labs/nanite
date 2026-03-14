# Interactive Dialog Pattern

Reference pattern for building skills that use `AskUserQuestion` for structured user interaction instead of wall-of-text output.

## Core Flow

```
gather data → format as AskUserQuestion → parse structured response → execute decisions → confirm results
```

## AskUserQuestion Constraints

| Constraint | Limit |
|---|---|
| Questions per call | 1–4 |
| Options per question | 2–4 (+ automatic "Other") |
| Multi-select | Supported via `multiSelect: true` |
| Previews | Optional `preview` field on options — rendered as markdown in monospace box |
| Header | Max 12 chars — short label displayed as chip/tag |

## When to Use Interactive vs Non-Interactive

**Use interactive dialogs when:**
- The user needs to make decisions about multiple items (review, triage, approve)
- Actions are irreversible or affect shared state (transitions, deletions, promotions)
- Items need per-item decisions (not all-or-nothing)
- The agent needs structured input to execute batch operations

**Stay non-interactive when:**
- The skill is read-only (standup, retro analysis, health check)
- There's only one action to confirm (use a simple yes/no prompt)
- The output is informational and doesn't drive further actions

## Option Design Guidelines

### Labels
- 1–5 words, action-oriented: "Approve", "Defer to next sprint", "Archive"
- Put the recommended/most-common option first
- Add "(Recommended)" to the default option label when there's a clear best choice

### Descriptions
- One sentence explaining what happens if chosen
- Include consequences: "Transitions task to done and notifies the lead"

### Previews
Use previews when the user needs to compare concrete artifacts:
- Code snippets showing different implementations
- Task details side-by-side
- Configuration before/after

Do NOT use previews for simple preference questions where labels + descriptions suffice.

### Multi-Select
Use `multiSelect: true` when:
- Multiple items can be selected for the same action (e.g., "which tasks to approve?")
- Choices are not mutually exclusive

Use single-select when:
- Options represent mutually exclusive actions on one item
- Only one choice makes sense at a time

## Pagination Pattern

When reviewing more items than fit in one `AskUserQuestion` call (max 4 options):

1. **Group items** into batches of up to 4
2. **Present one batch at a time** as a single question with multiSelect
3. **Accumulate selections** across batches
4. **Execute all decisions** after the last batch
5. **Show summary** of all actions taken

For items that need per-item action choice (approve/defer/carry-over), present one item per question with the action as options, up to 4 questions per call.

## Response Parsing

`AskUserQuestion` returns structured answers keyed by question text:
```
answers: { "Question text here": "Selected option label" }
```

For multiSelect, the value is comma-separated labels.

Parse by matching the option label to determine the action. Always handle the "Other" case — the user can type free text.

## Batch Execution Pattern

After collecting all decisions:

1. **Group by action type** — all approvals together, all deferrals together, etc.
2. **Execute in order**: safe actions first (status updates), then destructive (archives, deletes)
3. **Handle partial failures** — if one transition fails, continue with the rest and report failures
4. **Show summary**:
   ```
   === ACTIONS TAKEN ===
   Approved: 3 tasks (TASK-001, TASK-002, TASK-003)
   Deferred: 1 task (TASK-004 → SPR-next)
   Failed: 0
   === END ===
   ```

## Registering as a Slash Command

Interactive skills need both a skill file and a command entry point to be user-invocable. See [skill-command-anatomy.md](skill-command-anatomy.md) for the full convention.

## Example Skill Template

```markdown
# my-interactive-skill

Short description of what this skill does interactively.

## Usage
`/my-interactive-skill [--project <project_id>]`

## Instructions

1. **Gather data**:
   - Fetch items from Volon/Cortex/git
   - Filter to actionable items only

2. **Present interactive dialog**:
   - Use `AskUserQuestion` with structured options
   - Group items into batches of 4 if needed
   - Use multiSelect when multiple items share the same action set
   - Use single-select with previews when items need comparison

3. **Parse responses**:
   - Map selected labels back to actions
   - Handle "Other" free-text responses
   - Accumulate across pagination batches

4. **Execute batch actions**:
   - Group by action type
   - Execute safe actions first
   - Log failures, continue on error

5. **Confirm results**:
   - Show summary of all actions taken
   - Report any failures with remediation hints
   - Suggest next steps

## Invariants
- Never execute without user selection — always present options first
- Handle partial failures gracefully
- Always show a summary of what was done
```
