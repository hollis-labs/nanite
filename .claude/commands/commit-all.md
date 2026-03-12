Run the commit-all skill. For each project in `config/repos.yaml`, check git status. For dirty projects, show a preview of changes (git diff --stat), then commit all changes with the provided message (or auto-generated summary) and Co-Author trailer. Use `--dry-run` to preview without committing.

$ARGUMENTS
