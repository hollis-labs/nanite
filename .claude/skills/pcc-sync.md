# PCC Sync

Synchronize Project Context Cache (PCC) from managed projects to Mentat's local cache and Cortex.

## When to use

- After completing a task that changed a managed project's state
- After running pcc-refresh on a project
- When context in Cortex may be stale

## Procedure

1. **Determine target project(s).** If `--project <id>` given, use that. If `--all`, read `config/repos.yaml` for all projects. Otherwise, infer from the most recently completed Volon task.

2. **Read source PCC.** For each project, read PCC files from `<project-path>/.agentrc/pcc/global/`:
   - `00_project.md` through `05_decisions.md`

   If the project's PCC directory doesn't exist, skip and warn.

3. **Sync to Cortex.** For each PCC file, use Cortex MCP `context_write`:
   ```
   namespace: app/mentat/pcc/<project-id>
   key: <filename-without-extension>
   payload: <file content as JSON string>
   ```
   Cortex sync is best-effort — warn on failure, don't block.

4. **Emit summary.**
   ```
   === PCC SYNC ===
   Project: <id>
   Source: <path>/.agentrc/pcc/global/
   Cortex: OK — namespace app/mentat/pcc/<id>
   ================
   ```

## Output

- Cortex namespace updated via MCP
- Console: sync summary

## Invariants

- Read-only on source PCC — never overwrite project files
- Cortex sync is best-effort (warn on failure, don't block)
- Each PCC file synced independently (partial sync acceptable)
- Use Cortex MCP `context_write`, not direct API calls
