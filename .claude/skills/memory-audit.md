# memory-audit

Interactive memory audit — cross-reference MEMORY.md against system state, present findings with per-entry actions (fix, remove, keep, investigate), and execute selected fixes.

## Usage

`/memory-audit`

## Instructions

### 1. Read and cross-reference memories

- Read `~/.claude/projects/-Users-chrispian-Projects-apps-mentat/memory/MEMORY.md`
- Parse all file references (links to memory files)
- Read each referenced memory file's frontmatter and content

**Cross-reference checks:**

For **project** memories:
- Check if referenced epics/sprints still exist in Volon
- Check if task counts match current Volon state
- Check if project paths still exist on disk

For **reference** memories:
- Verify referenced files/paths still exist
- Verify referenced tools/services still exist

For **user** memories:
- Flag contradictory information across memories
- Check for duplicates or near-duplicates

For **feedback** memories:
- Check if referenced code still exists
- Flag if feedback contradicts other feedback

**Also check for:**
- Orphaned files: `.md` files in the memory directory not referenced in MEMORY.md
- Missing memories: active epics/services/ADRs not reflected in any memory

### 2. Show audit summary header

```
=== MEMORY AUDIT ===
Total memories indexed: <N>
Files checked: <N>
Findings: <N> stale | <N> orphaned | <N> missing | <N> contradictions
Healthy: <N>
```

If no findings, show:
```
All memories current and consistent. No action needed.
=== END AUDIT ===
```

### 3. Present findings — interactive dialog

Use `AskUserQuestion` with **multiSelect: true**, batched by finding type.

**Question format** (one question per batch of up to 4 findings):
- **header**: Finding type (e.g., "Stale", "Orphaned", "Missing") — max 12 chars
- **question**: `Select items to fix:`
- **options** (up to 4 per question):
  - **label**: `<filename>: <issue summary>`
  - **description**: What's wrong and what the fix would be
  - **preview**: Show current memory content vs actual system state
- **multiSelect**: true

Present stale items first, then orphaned, then contradictions.

For each selected item, determine the action based on finding type:

### 4. Determine actions per finding type

**Stale memories** — selected items get auto-fixed:
- Update the memory content to match current system state
- Update the frontmatter description if needed

**Orphaned files** — present follow-up:
- **header**: "Orphan"
- **question**: `[<filename>] — what should we do?`
- **options**: Add to index / Remove file / Keep as-is
- Show preview with file content

**Contradictions** — present follow-up:
- Show both conflicting memories
- Options: Keep first / Keep second / Merge / Remove both

**Missing memories** — selected items get created:
- Generate memory file with current state
- Add to MEMORY.md index

### 5. Execute selected fixes

1. **Update stale**: Edit memory files with current information
2. **Index orphans**: Add entries to MEMORY.md
3. **Remove orphans**: Delete the file
4. **Resolve contradictions**: Edit or remove as selected
5. **Create missing**: Write new memory files and update index

Handle partial failures — log errors and continue.

### 6. Show results summary

```
=== AUDIT COMPLETE ===

UPDATED (stale → current):
  - <filename>: <what changed>

INDEXED (orphan → tracked):
  - <filename>

REMOVED:
  - <filename>: <reason>

CREATED (missing → tracked):
  - <filename>: <what it covers>

DEFERRED:
  - <finding>: not selected

Totals: <N> updated, <N> indexed, <N> removed, <N> created, <N> deferred
Failed operations: <N or "none">
=== END AUDIT ===
```

## When to Use

- Periodically (weekly or after major changes)
- When memories seem outdated or contradictory
- Before a session handoff to clean up state
- When the user says "audit memory" or "check memory"

## Invariants

- Never modify or delete memory files without user selection
- Always show current vs actual state in previews
- Handle the MEMORY.md 200-line limit — warn if index is getting long
- Preserve memory frontmatter format when updating

$ARGUMENTS
