# Git Status All

Show git status across all managed Tiamat projects in a unified view.

## When to use

- When the user asks about repo status across projects
- Before committing changes across multiple repos
- To check for uncommitted work or divergent branches

## Procedure

1. **Read project list.** Parse `config/repos.yaml` to get all project IDs and paths. Expand `~` to `$HOME`.

2. **For each project**, run via Bash:
   ```bash
   cd <project_path>
   BRANCH=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "n/a")
   DIRTY=$(git status --porcelain 2>/dev/null | wc -l | tr -d ' ')
   UNPUSHED=$(git log --oneline @{u}..HEAD 2>/dev/null | wc -l | tr -d ' ')
   LAST_COMMIT=$(git log -1 --format="%cr" 2>/dev/null || echo "n/a")
   ```

3. **Format output** as a markdown table:
   ```
   | Project | Branch | Dirty | Unpushed | Last Commit |
   |---------|--------|-------|----------|-------------|
   | volon   | main   | 0     | 0        | 2 hours ago |
   ```

4. **Highlight issues.** After the table, list any projects with:
   - Dirty files > 0 (uncommitted changes)
   - Unpushed commits > 0
   - Branch != main

## Output

Console output only. No file writes. Read-only operation.

## Invariants

- Never modify any git state
- Always show all configured projects, even if git commands fail
- Expand `~` paths correctly
