# git-cleanup

Interactive git cleanup — inventory worktrees and stale branches across the portfolio, cross-reference with Volon task status, present findings with per-item actions (clean, keep, investigate), and execute batch cleanup.

## When to use

- When warned about excessive agent worktrees (> 5 detected by `worktree-check.sh` hook)
- Before starting a large multi-agent session to reclaim disk space
- During periodic maintenance or end-of-sprint housekeeping
- When agent branch clutter makes `git branch` output unreadable

## Usage

`/git-cleanup [--project <project_id>]`

**--project**: Scope to a single project (default: scan all projects in repos.yaml)

## Instructions

### Phase 1 — Inventory

1. **Read the project list.** Parse `config/repos.yaml` to get all project IDs and paths. Expand `~` to `$HOME`.

2. **Enumerate worktrees.** For each project path:
   ```bash
   cd <project_path> && git worktree list 2>/dev/null
   ```
   Record every worktree that is NOT the main working tree.

3. **Enumerate agent branches.** For each project path:
   ```bash
   cd <project_path> && git branch --list 'agent/*' --list 'worktree-agent-*' 2>/dev/null
   ```

### Phase 2 — Status enrichment

4. **Check for uncommitted changes** in every non-main worktree:
   ```bash
   cd <worktree_path> && git status --short 2>/dev/null
   ```
   Flag any with output as dirty.

5. **Extract task IDs from branch names** and **query Volon** for task status:
   - done/archived → safe to remove
   - doing/blocked → KEEP
   - not found → check commit age

6. **Check branch activity** for unmatched branches:
   ```bash
   cd <project_path> && git log -1 --format="%ci" <branch_name> 2>/dev/null
   ```
   Branches with no commits in 7+ days are stale (14+ days if Volon unreachable).

### Phase 3 — Present cleanup plan — interactive dialog

7. **Show inventory summary:**
```
=== GIT CLEANUP ===
Projects scanned: <N>
Worktrees found: <N> (main excluded)
Agent branches: <N>
Safe to remove: <N> | Active (keep): <N> | Dirty (needs review): <N>
```

8. **Present safe removals** using `AskUserQuestion` with **multiSelect: true**.

**Question format** (batch of up to 4 items):
- **header**: "Safe" or "Dirty"
- **question**: `Select items to clean up:`
- **options** (up to 4):
  - **label**: `[<project>] <branch or worktree name>`
  - **description**: Task status, last commit age, dirty state
  - **preview**: Show worktree path, branch name, Volon task status, last commit date, dirty files (if any)
- **multiSelect**: true

Present safe items first. Then present dirty items separately with explicit warnings.

9. **Items marked dirty** get their own batch with stronger warnings:
- Preview must show the full dirty file list
- Description must say "HAS UNCOMMITTED CHANGES — will be lost"

### Phase 4 — Execute on confirmation

10. **Remove confirmed worktrees:**
    ```bash
    cd <parent_repo> && git worktree remove <worktree_path>
    ```
    Use `--force` ONLY for dirty worktrees that the user explicitly confirmed.

11. **Delete confirmed branches:**
    ```bash
    cd <parent_repo> && git branch -d <branch_name>
    ```
    Use `-D` only if `-d` fails AND the user confirmed force deletion.

12. **Prune references** in each affected project:
    ```bash
    cd <project_path> && git worktree prune && git remote prune origin
    ```

### Phase 5 — Show results summary

```
=== CLEANUP COMPLETE ===

REMOVED:
  - [<project>] <worktree/branch> (task: <status>)

KEPT (active):
  - [<project>] <worktree/branch> (task: <status>)

NOT SELECTED:
  - [<project>] <worktree/branch>

Totals: <N> removed, <N> kept, <N> deferred
References pruned: <N> projects
Failed removals: <N or "none">
=== END CLEANUP ===
```

## Safety invariants

These rules are non-negotiable:

- **NEVER remove worktrees for tasks in `doing` or `blocked` status** without explicit override
- **NEVER remove dirty worktrees** without per-item explicit user confirmation showing the dirty file list
- **NEVER force-delete branches (`-D`)** without explicit user confirmation for that specific branch
- **Always present the full plan** before executing any destructive operation
- **Never delete main/master** or the current checked-out branch
- **Never push deletions to remote** — local cleanup only
- **If Volon is unreachable**, note it and use 14+ day stale threshold instead of 7

$ARGUMENTS
