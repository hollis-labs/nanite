# Standup

Generate a standup report from recent task completions and git activity across all managed projects.

## When to use

- When the user asks for a standup or status summary
- At the start of a work session to review recent progress
- When preparing for team standups or status updates

## Procedure

1. **Gather task activity.** Use `volon_tasks_list` with project_id="mentat" to find recently updated tasks. Group by status (done, doing, blocked).

2. **Gather git activity.** For each project in `config/repos.yaml`, run:
   ```bash
   cd <project_path>
   git log --oneline --since="<N> hours ago" --format="%h %s" 2>/dev/null
   ```
   Default lookback: 24 hours. Override with `--hours N`.

3. **Check blockers.** Read `.agentrc/bootstrap.md` for any listed blockers.

4. **Format report.**

   **Brief format (default):**
   ```
   === STANDUP — YYYY-MM-DD ===

   DONE (last 24h):
   - [TASK-001] Title here
   - [volon] 3 commits: fix pipeline, add tests, update docs

   IN PROGRESS:
   - [TASK-002] Title here (doing)

   BLOCKED:
   - None
   ============================
   ```

   **Full format** (`--format full`): includes commit messages, task descriptions, and file-level change summaries.

## Output

- Console: formatted standup report
- Read-only operation (no file writes unless `--format full` requested, in which case optionally save to `outbox/reports/standup-YYYY-MM-DD.md`)

## Invariants

- Read-only — never modify tasks or git state
- Always include all managed projects, even if no activity
- Gracefully handle missing directories or empty git history
