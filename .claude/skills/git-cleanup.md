# Git Cleanup

Comprehensive cleanup of stale git worktrees, branches, and references across all managed Fragments Engine projects. Supersedes the narrower `/worktree-cleanup` skill.

## When to use

- When warned about excessive agent worktrees (> 5 detected by the `worktree-check.sh` hook)
- Before starting a large multi-agent session to reclaim disk space
- During periodic maintenance or end-of-sprint housekeeping
- When agent branch clutter makes `git branch` output unreadable

## Procedure

### Phase 1 — Inventory

1. **Read the project list.** Parse `config/repos.yaml` (in the Mentat repo) to get all project IDs and paths. Expand `~` to `$HOME`.

2. **Enumerate worktrees.** For each project path, run via Bash:
   ```bash
   cd <project_path> && git worktree list 2>/dev/null
   ```
   Record every worktree that is NOT the main working tree.

3. **Enumerate agent branches.** For each project path, run via Bash:
   ```bash
   cd <project_path> && git branch --list 'agent/*' --list 'worktree-agent-*' 2>/dev/null
   ```
   Also check for top-level `*-agent-*` directories under `~/Projects-apps/`.

### Phase 2 — Git status check

4. **Check for uncommitted changes** in every non-main worktree:
   ```bash
   cd <worktree_path> && git status --short 2>/dev/null
   ```
   Flag any worktree that has output (dirty state). These require special handling.

### Phase 3 — Volon cross-reference

5. **Extract task IDs from branch names.** Agent branches typically encode a task ID or session hash (e.g., `agent/TASK-20260314-098`, `worktree-agent-a9447a70`). Extract any recognizable task identifiers.

6. **Query Volon for task status.** Use the `volon_tasks_list` MCP tool (with appropriate `project_id`) to look up each extracted task ID. Classify:
   - **done** or **archived** — safe to remove
   - **doing** or **in_progress** — KEEP (active work)
   - **blocked** — KEEP (may resume)
   - **not found** — treat as stale if also inactive (see Phase 4)

### Phase 4 — Stale branch detection

7. **Check branch activity.** For branches that did not match a Volon task (or whose task was not found), check last commit age:
   ```bash
   cd <project_path> && git log -1 --format="%ci" <branch_name> 2>/dev/null
   ```
   Branches with no commits in 7+ days are considered stale.

8. **Check remote tracking.** For each stale branch, check whether it has been pushed:
   ```bash
   git branch -r --list "origin/<branch_name>" 2>/dev/null
   ```
   Remote-only cleanup (pruning) is separate from local branch deletion.

### Phase 5 — Present plan

9. **Build and display the cleanup plan.** Present a table to the user BEFORE executing anything:

   ```
   === GIT CLEANUP PLAN ===

   Project: volon (~/Projects-apps/volon)
   | Item                        | Type     | Task        | Status   | Last Commit  | Dirty | Action       |
   |-----------------------------|----------|-------------|----------|--------------|-------|--------------|
   | .claude/worktrees/agent-X   | worktree | TASK-Y      | done     | 5 days ago   | no    | REMOVE       |
   | .claude/worktrees/agent-Z   | worktree | TASK-W      | doing    | 1 hour ago   | no    | KEEP         |
   | worktree-agent-a9447a70     | branch   | (none)      | stale    | 12 days ago  | n/a   | DELETE       |
   | .claude/worktrees/agent-Q   | worktree | TASK-R      | done     | 3 days ago   | YES   | NEEDS REVIEW |

   [repeat for each project with findings]

   Summary:
   - Worktrees to remove: N
   - Branches to delete: N
   - Items kept (active): N
   - Items needing review (dirty): N
   - Estimated disk reclaim: ~N MB (if measurable)

   Proceed? [present options: all safe items / select individually / abort]
   ```

10. **Items marked NEEDS REVIEW** must be presented separately with their dirty file list, and the user must explicitly confirm each one.

### Phase 6 — Execute on confirmation

11. **Remove worktrees** (only confirmed items):
    ```bash
    cd <parent_repo> && git worktree remove <worktree_path>
    ```
    If removal fails due to untracked files AND the user has confirmed force-removal for that specific worktree:
    ```bash
    git worktree remove --force <worktree_path>
    ```

12. **Delete local branches** (only confirmed stale branches):
    ```bash
    cd <parent_repo> && git branch -d <branch_name>
    ```
    Use `-D` only if `-d` fails because the branch is not fully merged AND the user confirms.

### Phase 7 — Prune references

13. **Prune worktree metadata** in each project that had removals:
    ```bash
    cd <project_path> && git worktree prune
    ```

14. **Prune remote references** in each project:
    ```bash
    cd <project_path> && git remote prune origin
    ```

15. **Report results.** After all operations, present a summary:
    ```
    === GIT CLEANUP COMPLETE ===
    Worktrees removed: N
    Branches deleted: N
    References pruned: N projects
    Items kept: N
    ```
    Log the full list of removed items to stderr for audit trail.

## Safety invariants

These rules are non-negotiable. Violating any of them is a hard failure.

- **NEVER remove worktrees for tasks in `doing`, `in_progress`, or `blocked` status.** These represent active or paused work.
- **NEVER remove worktrees with uncommitted changes** without per-worktree explicit user confirmation. Present the dirty file list first.
- **NEVER force-delete branches (`-D`)** without explicit user confirmation for that specific branch.
- **Always present the full cleanup plan** (Phase 5) before executing any destructive operation.
- **Never delete the main/master branch** or the current checked-out branch of any project.
- **Never push deletions to remote** (no `git push origin --delete`). This skill handles local cleanup only. Remote branch cleanup is a separate, more dangerous operation.
- **Log every removal** to stderr so the user has an audit trail.
- **If Volon is unreachable**, skip the cross-reference phase and note it in the plan. Fall back to commit-age heuristics only, and be more conservative (require 14+ days stale instead of 7).

## Output

Console output only. No file writes. All destructive operations require confirmation.

## References

- `config/repos.yaml` — managed project paths
- `.claude/hooks/worktree-check.sh` — PostToolUse hook that triggers cleanup warnings
- Volon MCP `volon_tasks_list` — task status cross-referencing
- Supersedes the older `/worktree-cleanup` skill (which now redirects here)
