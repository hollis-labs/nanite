# memory-audit

Cross-reference MEMORY.md entries against current system state to find stale, missing, or contradictory memories.

## Usage
`/memory-audit [--fix]`

**--fix** (optional): Automatically fix issues found (update stale entries, remove dead references)

## Instructions

1. **Read the memory index**:
   - Read `~/.claude/projects/-Users-chrispian-Projects-apps-mentat/memory/MEMORY.md`
   - Parse all file references (links to memory files)

2. **Check each referenced memory file**:
   - Verify the file exists
   - Read its frontmatter (name, description, type)
   - Read its content

3. **Cross-reference against system state**:

   For **project** memories:
   - Check if referenced epics/sprints still exist in Volon
   - Check if task counts match current Volon state
   - Check if project paths still exist on disk
   - Check if referenced services are still configured

   For **reference** memories:
   - Verify referenced files/paths still exist
   - Check if URLs/endpoints are still valid (if checkable)
   - Verify referenced tools/services still exist

   For **user** memories:
   - Flag if contradictory information exists across memories
   - Check for duplicates or near-duplicates

   For **feedback** memories:
   - Check if the feedback is still relevant (e.g., does the referenced code still exist?)
   - Flag if feedback contradicts other feedback

4. **Check for orphaned memories**:
   - Find `.md` files in the memory directory that aren't referenced in MEMORY.md
   - These need to be indexed or removed

5. **Check for missing memories**:
   - Are there active epics/sprints not mentioned in memory?
   - Are there configured services not documented?
   - Are there recent ADRs not reflected in memory?

6. **Generate the audit report**:

```
=== MEMORY AUDIT ===
Total memories indexed: <N>
Files checked: <N>

STALE (<N>):
  - <filename>: <what's stale and why>
    Current state: <what system shows>
    Memory says: <what memory says>

MISSING (<N>):
  - <what should have a memory but doesn't>

ORPHANED (<N>):
  - <filename>: exists but not in MEMORY.md index

CONTRADICTIONS (<N>):
  - <memory1> vs <memory2>: <description of conflict>

DUPLICATES (<N>):
  - <memory1> and <memory2>: <overlap description>

HEALTHY (<N>):
  - All other memories are current and consistent

<If --fix>
FIXES APPLIED:
  - Updated <filename>: <what changed>
  - Removed <filename>: <why>
  - Added to index: <filename>
</If>

=== END AUDIT ===
```

## When to Use
- Periodically (weekly or after major changes)
- When memories seem outdated or contradictory
- Before a session handoff to clean up state
- When the user says "audit memory" or "check memory"
