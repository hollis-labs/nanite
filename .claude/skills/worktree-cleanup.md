# Worktree Cleanup

Remove stale git worktrees created by agent sessions across managed projects.

## When to use

- When warned about excessive agent worktrees (> 5 detected)
- Before starting a large multi-agent session to reclaim disk space
- During periodic maintenance

## Procedure

1. **Inventory all agent worktrees** by running via Bash:
   ```bash
   # List .claude/worktrees/agent-* across all managed projects
   for proj in ~/Projects-apps/*/; do
     if [ -d "$proj/.claude/worktrees" ]; then
       for wt in "$proj"/.claude/worktrees/agent-*; do
         [ -d "$wt" ] && echo "$wt"
       done
     fi
   done

   # List top-level *-agent-* directories
   for d in ~/Projects-apps/*-agent-*; do
     [ -d "$d" ] && echo "$d"
   done
   ```

2. **Check git worktree status** for each parent repo:
   ```bash
   cd <parent-repo> && git worktree list
   ```

3. **Cross-reference with Volon task status** using the Volon MCP:
   - For each worktree, extract the task ID from the branch name (e.g., `agent-a67f3da7`)
   - Query `volon_tasks_list` with `status=done` to identify completed tasks
   - Worktrees for done/archived tasks are safe to remove

4. **Present cleanup plan** to the user:
   ```
   === WORKTREE CLEANUP PLAN ===
   | Worktree Path              | Task    | Status | Action |
   |----------------------------|---------|--------|--------|
   | .claude/worktrees/agent-X  | TASK-Y  | done   | REMOVE |
   | .claude/worktrees/agent-Z  | TASK-W  | doing  | KEEP   |
   ============================
   Disk estimate: ~N MB reclaimable
   ```

5. **Execute on user confirmation**:
   ```bash
   cd <parent-repo> && git worktree remove <worktree-path>
   ```
   Use `git worktree remove --force` only if the standard remove fails due to untracked files, and only after confirming with the user.

6. **Prune stale worktree references**:
   ```bash
   cd <parent-repo> && git worktree prune
   ```

## Output

Console output with cleanup summary. Reports total worktrees removed and disk space reclaimed.

## Invariants

- Always confirm with user before removing any worktree
- Never remove worktrees for tasks in `doing` or `blocked` status
- Never force-remove without explicit user approval
- Run `git worktree prune` after removals to clean up stale references
- Log removed worktrees to stderr for audit trail
