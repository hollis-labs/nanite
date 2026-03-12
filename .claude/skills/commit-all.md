# Commit All

Commit pending changes across all managed Tiamat projects with a consistent commit message.

## When to use

- When changes span multiple managed projects
- At the end of a work session to commit everything cleanly
- After cross-project operations (PCC refresh, standardization)

## Procedure

1. **Read project list.** Parse `config/repos.yaml` for all project paths.

2. **Check each project** for dirty state:
   ```bash
   cd <project_path>
   DIRTY=$(git status --porcelain 2>/dev/null | wc -l | tr -d ' ')
   ```
   Skip clean projects.

3. **Show preview.** For each dirty project: project name, changed files count, `git diff --stat` summary.

4. **If not `--dry-run`**, for each dirty project:
   ```bash
   cd <project_path>
   git add -A
   git commit -m "<message>

   Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
   ```

5. **Emit summary.**
   ```
   === COMMIT ALL ===
   Committed: N projects
   Skipped: N projects (clean)
   ==================
   ```

## Output

- Git commits in each dirty project
- Console: summary

## Invariants

- Never force-push or amend existing commits
- Always include Co-Author trailer
- Always show preview before committing
- If any commit fails, continue with remaining projects and report failures
