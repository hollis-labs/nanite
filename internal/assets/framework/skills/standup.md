# Standup (:standup)

Generate a standup report from recent task completions and git activity across managed projects.

## When to use

- When the user asks for a standup or status summary
- At the start of a work session to review recent progress
- When preparing for team standups or status updates

## Input Format

```
/standup [--hours N] [--format brief|full] [--project X]
```

- `--hours` — Lookback window. Default: 24.
- `--format` — `brief` (default) or `full` (includes commit messages and task descriptions).
- `--project` — Scope to a single Clockwork project. Default: all projects.

## Procedure

1. **Gather task activity.** Use `mcp__clockwork__clockwork_task_list` to find recently updated tasks. If `--project` is given, filter by that `project_id`. Group by status (done, doing, blocked).

2. **Gather git activity.** For each project directory known to the current workspace, run:
   ```bash
   git -C <project_path> log --oneline --since="<N> hours ago" --format="%h %s" 2>/dev/null
   ```
   If the workspace has a `.nanite/config.yaml` with a `projects` key, use those paths. Otherwise, use the current working directory only.

3. **Check blockers.** Use `mcp__clockwork__clockwork_task_list` filtered to `status=blocked`.

4. **Format report.**

   **Brief format (default):**
   ```
   === STANDUP — YYYY-MM-DD ===

   DONE (last 24h):
   - [CW-20260427-0001] Title here
   - [project] 3 commits: fix pipeline, add tests, update docs

   IN PROGRESS:
   - [CW-20260427-0002] Title here (doing)

   BLOCKED:
   - None
   ============================
   ```

   **Full format** (`--format full`): includes commit messages, task descriptions, and file-level change summaries.

## Output

- Console: formatted standup report
- Read-only operation — no file writes

## Invariants

- Read-only — never modify tasks or git state
- Include all managed projects, even if no activity
- Gracefully handle missing directories or empty git history
