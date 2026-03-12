# /pcc-refresh

Update the L0 Global Project Context Cache (`.agentrc/pcc/global/`) with minimal-diff changes reflecting current repo state.

## Usage

```
/pcc-refresh [--scope all|project|arch|conventions|workflows|backlog|decisions]
```

- `--scope`: which PCC sections to refresh (default: `all`)

## PCC file mapping

| File | Scope key | Primary sources |
|------|-----------|-----------------|
| `00_project.md` | `project` | `README.md`, `agentrc.yaml` |
| `01_architecture.md` | `arch` | `src/`, `agentrc.yaml` |
| `02_conventions.md` | `conventions` | `docs/process/`, `config/`, templates |
| `03_workflows.md` | `workflows` | `.claude/skills/`, `.claude/commands/`, `scripts/` |
| `04_backlog.md` | `backlog` | Volon tasks (via `volon_tasks_list`), `adr/` |
| `05_decisions.md` | `decisions` | `adr/`, CHANGELOG (APPEND ONLY) |

## Steps

1. **Read config.** Extract from `agentrc.yaml`:
   - `pcc.location` -> `.agentrc/pcc/` (default)
   - Global PCC dir: `<pcc.location>/global/`
   - Max section words: 300 (default)

2. **Determine scope.** If `--scope` is provided, filter to matching files only. If `all`, process all 6 files.

3. **Read current PCC files.** For each file in scope, read current content and note word count. If file doesn't exist, it will be created.

4. **Collect source data.** For each file in scope, read its primary sources (see mapping table above). Only read sources that are relevant to the scope.

5. **Apply minimal-diff updates.** For each PCC file:
   - Compare current content against source data
   - Change ONLY stale or missing content
   - Do NOT restructure headings or reorder sections
   - Do NOT rewrite accurate text
   - Mark unknown or missing information as **TBD**
   - For `05_decisions.md`: APPEND ONLY — never delete or rewrite existing entries

6. **Enforce word limits.** If any file exceeds max section words (300):
   - Trim non-essential bullets and verbose prose
   - Keep structural headings and key facts
   - Never trim the Evidence section

7. **Update Evidence section.** At the bottom of each updated file, write:
   ```markdown
   ## Evidence

   - **Last refreshed:** YYYY-MM-DD HH:MM
   - **Trigger:** /pcc-refresh --scope <scope>
   - **Sources read:** <list of files read>
   ```

8. **Write files.** Write updated PCC files to `.agentrc/pcc/global/`. Create the directory if it doesn't exist.

9. **Output.** Print summary:
   - Files updated: `<list>`
   - Files unchanged: `<list>`
   - Or: "No PCC changes required."

## Invariants

- **Minimal diff:** change only stale content — never rewrite accurate text
- **Never invent content:** only reflect observable repo state
- **Never delete decisions:** `05_decisions.md` is append-only
- **Never read/write outside scope:** if `--scope project`, only touch `00_project.md`
- **Word limits enforced:** max 300 words per file (configurable)
- **Mark unknowns as TBD:** never guess at missing information
- **Evidence required:** every updated file must have a current Evidence section

$ARGUMENTS
