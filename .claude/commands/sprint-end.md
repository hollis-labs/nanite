# /sprint-end

Finalize a sprint: update bootstrap, get user sign-off, and commit all changes to git.

## Usage

```
/sprint-end [--sprint "<name>"]
```

- `--sprint`: sprint label for the commit message (default: inferred from bootstrap iteration)

## Steps

1. **Gather sprint summary.** Query Volon via `volon_sprint_get` or `volon_tasks_list` for tasks completed in this sprint. Read `.agentrc/bootstrap.md` for iteration number. List key deliverables.

2. **Update bootstrap.** Run `/bootstrap-update` to regenerate `.agentrc/bootstrap.md` with accurate counts and next actions.

3. **Present summary for sign-off.** Print to the user:
   ```
   === SPRINT END ===
   Sprint: <name or "Iteration N">
   Tasks completed: <count>
   Key deliverables:
     - <deliverable 1>
     - <deliverable 2>

   Pending for next sprint:
     - <open task or next action 1>
     - <open task or next action 2>

   Ready to commit? (git add -A && git commit)
   === END SPRINT END ===
   ```
   Wait for explicit user approval before proceeding to step 4.

4. **Git commit.** After user sign-off:
   - `git add -A` (review staged files — warn if secrets or large binaries detected)
   - Compose commit message:
     ```
     Sprint <name>: <one-line summary>

     Tasks completed:
     - TASK-ID: title
     - TASK-ID: title

     Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>
     ```
   - `git commit`
   - Print commit hash

5. **Post-commit.** Print: `Sprint <name> committed: <hash>. Bootstrap updated for next iteration.`

## Invariants

- **Never commit without user sign-off** — always present summary and wait for approval
- Never commit files that look like secrets (`.env`, `credentials.*`, `*.key`)
- Always run `/bootstrap-update` before committing so bootstrap reflects final state
- Commit message must list all completed tasks by ID
- Always include `Co-Authored-By` trailer

$ARGUMENTS
