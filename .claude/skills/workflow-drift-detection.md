# workflow-drift-detection

Scheduled cross-project consistency checks. Detects drift in shared modules, config, conventions, and state across the Fragments Engine portfolio.

## Usage
`/workflow-drift-detection [--scope <scope>]`

**--scope** (optional): What to check
- `all` (default) — run all checks
- `modules` — shared module version drift only
- `config` — configuration drift only
- `conventions` — naming and structure conventions only
- `state` — Volon/Cortex state consistency only

## Instructions

### Check 1: Shared Module Versions
1. Read `go.mod` from each Go project
2. Compare versions of shared modules:
   - `github.com/hollis-labs/otel` (tiamat-otel)
   - `github.com/hollis-labs/mcp-helpers` (tiamat-mcp-helpers)
   - `github.com/hollis-labs/tool-broker` (tiamat-tool-broker)
3. Check `replace` directives point to correct local paths
4. Flag any version mismatches or stale replace directives

### Check 2: Configuration Consistency
1. Compare port assignments across projects (no conflicts)
2. Check `.claude/settings.json` across projects for hook consistency
3. Verify MCP server config in `~/.claude.json` matches running services
4. Check `agentrc.yaml` for correct project_id values

### Check 3: Naming Conventions
1. Run the portfolio-alignment blueprint checks (or inline equivalent):
   - No `.volon/` references (should be `.agentrc/`)
   - No `tiamat-` prefixes in new code
   - No `fe-` prefixes
   - Module paths match expected naming
2. Check for consistent file naming patterns

### Check 4: State Consistency
1. Compare Volon task state with git state:
   - Tasks marked "done" but no corresponding commits?
   - Tasks marked "doing" but no recent activity?
2. Compare Cortex records with actual project state:
   - Are PCC files in sync with Cortex?
   - Are there stale Cortex records?
3. Check bootstrap.md accuracy against Volon

### Check 5: Hook Symlink Integrity
1. For each project, verify hook files are valid symlinks to `~/.agentrc/hooks/shared/`
2. Flag broken symlinks or missing hooks

### Output

```
=== DRIFT DETECTION ===
Scope: <scope>
Date: <date>
Projects scanned: <N>

MODULE DRIFT:
  <module>:
    <project1>: v<version> (replace: <path>)
    <project2>: v<version> (replace: <path>)
    Status: <aligned | DRIFTED>

CONFIG DRIFT:
  Port conflicts: <none | list>
  Hook drift: <none | list>
  MCP config: <aligned | issues>

NAMING DRIFT:
  Old references found: <N>
  <details per project>

STATE DRIFT:
  Stale doing tasks: <N>
  Untracked done tasks: <N>
  Cortex staleness: <N records>

SUMMARY:
  Total drift issues: <N>
  Critical: <N> (blocking)
  Warning: <N> (should fix)
  Info: <N> (cosmetic)

RECOMMENDED FIXES:
  1. <highest priority fix>
  2. <next fix>
=== END DRIFT DETECTION ===
```

## Scheduling
This workflow is designed to run on a schedule via Hadron:
- Recommended: twice daily (2AM and 2PM) via Hadron schedule
- Can also be run manually anytime
- Results can be written to Cortex for trend tracking

## When to Use
- On a regular schedule (daily or weekly)
- After major refactoring or renaming operations
- Before release cuts
- When something "feels off" across projects
