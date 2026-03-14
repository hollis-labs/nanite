# workflow-feature-branch

End-to-end feature branch workflow: create worktree, implement, review with quality gates, and open PR.

## Usage
`/workflow-feature-branch <phase> [args]`

**phase**: Which phase to execute
- `start <branch-name> <task-id>` — Create feature branch and worktree
- `review` — Run quality gates on current changes
- `pr` — Open PR with quality gate results
- `full <branch-name> <task-id>` — Run all phases

## Instructions

### Phase: Start
1. Validate the task exists in Volon (`volon_task_get`)
2. Claim the task (`volon_task_transition` to "doing")
3. Create a feature branch from main:
   ```
   git checkout -b <branch-name>
   ```
   Or if using worktrees:
   ```
   git worktree add ../<project>-<branch-name> -b <branch-name>
   ```
4. Confirm branch creation and working directory

### Phase: Review (Quality Gates — ADR-021)
Run all quality gate checks on the current branch changes:

1. **Get the diff**: `git diff main...HEAD` to see all changes
2. **Gate 1 — Format & Lint**:
   - Go: `gofmt -l` on changed `.go` files
   - Go: `golangci-lint run --new` (lint only new/changed code)
   - TS: `npx biome check` on changed `.ts`/`.tsx` files (if frontend changes)
3. **Gate 2 — Build**:
   - `go build ./...`
   - `cd ui && npm run build` (if frontend changes)
4. **Gate 3 — Test**:
   - `go test ./changed/packages/...` (scoped to changed packages)
5. **Gate 4 — Code Review**:
   - Run `/code-review branch` for structured review
6. **Gate 5 — Cross-Impact**:
   - Run `/cross-impact branch` to check portfolio impact
7. **Gate 6 — Security**:
   - Check for secrets in diff (`gitleaks detect --no-git`)
   - Check for SQL injection, command injection patterns

Report results:
```
QUALITY GATES:
  Format & Lint: <PASS|FAIL>
  Build:         <PASS|FAIL>
  Tests:         <PASS|FAIL> (<N> passed, <N> failed)
  Code Review:   <PASS|WARN|FAIL> (<N> issues)
  Cross-Impact:  <None|Low|Medium|High>
  Security:      <PASS|FAIL>

Overall: <READY|NOT READY>
```

### Phase: PR
1. Ensure all changes are committed
2. Push branch to remote: `git push -u origin <branch-name>`
3. Generate PR description:
   - Title from task title (keep under 70 chars)
   - Body includes:
     - Task reference (TASK-ID)
     - Summary of changes
     - Quality gate results
     - Test plan
4. Create PR: `gh pr create --title "..." --body "..."`
5. Update the Volon task with PR URL
6. Transition task to "review" if applicable

### Phase: Full
Execute all phases: start -> (implement — user does this) -> review -> pr

```
=== FEATURE BRANCH: <phase> ===
Branch: <branch-name>
Task: <task-id>
Phase: <current phase>

<phase-specific output>

Next: <what comes next>
=== END ===
```

## When to Use
- When starting work on a new feature or bug fix
- When ready to submit changes for review
- When the user says "let's start a feature branch" or "open a PR"
- For any change that should go through quality gates before merging
