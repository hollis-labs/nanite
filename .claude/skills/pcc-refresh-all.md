# PCC Refresh All

Refresh Project Context Cache across all managed Tiamat projects, then sync to Cortex.

## When to use

- When multiple projects may have stale PCC
- After a major cross-project change
- Periodically to keep context current

## Procedure

1. **Read project list.** Parse `config/repos.yaml` for project IDs and paths.

2. **For each project**, check if `.agentrc/pcc/global/` exists. Skip projects without PCC structure.

3. **Refresh PCC** for each project:
   - Read key project files (README, go.mod/package.json, architecture docs, recent git log)
   - Regenerate PCC files with minimal-diff updates (don't rewrite accurate content)
   - Write to `<project>/.agentrc/pcc/global/`

4. **Sync to Cortex** for each project using the pcc-sync skill procedure.

5. **Emit summary.**
   ```
   === PCC REFRESH ALL ===
   Refreshed: N projects
   Skipped: N (no PCC structure)
   Synced to Cortex: N
   =======================
   ```

## Output

- Updated PCC files in each project's `.agentrc/pcc/global/`
- Cortex namespaces updated via MCP
- Console: summary

## Invariants

- Never delete existing PCC content — only update with minimal diffs
- Skip projects without `.agentrc/pcc/global/`
- Cortex sync is best-effort
- Process projects sequentially
- Max 300 words per PCC file
- Append-only for decisions file (`05_decisions.md`)
