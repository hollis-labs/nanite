# drift-resolve

Interactive drift resolution — scan portfolio for configuration drift, present findings with auto-fix options, and execute selected fixes.

## Usage

`/drift-resolve [--project <project_id>]`

**--project**: Scope to a single project (default: scan all projects in repos.yaml)

## Instructions

### 1. Run drift detection

Scan for drift across the portfolio. For each project in `config/repos.yaml`:

**go.mod consistency**: Check that go.mod exists and `go mod tidy` would produce no changes
```bash
cd <project_path> && go mod tidy -diff 2>&1
```

**PCC freshness**: Compare `.agentrc/pcc/global/` timestamps against source files
- If PCC files are older than their sources by >24h, flag as stale

**Bootstrap sync**: Compare `.agentrc/bootstrap.md` state against Volon task counts
- Query `volon_tasks_list` and compare done/total counts

**Port config**: Check for port conflicts across `config/mentat.yaml` and service configs

**Memory sync**: Check if MEMORY.md references files that no longer exist

### 2. Show drift summary header

```
=== DRIFT RESOLVE ===
Projects scanned: <N>
Drift items found: <N>
By severity: critical=<N> warning=<N> info=<N>
Auto-fixable: <N>

Presenting findings for resolution...
```

### 3. Present findings — interactive dialog

Use `AskUserQuestion` with **multiSelect: true** to let the user select multiple items to fix per batch.

**Question format** (one question per batch of up to 4 items):
- **header**: Severity (e.g., "Critical", "Warning", "Info") — max 12 chars
- **question**: `Select drift items to resolve:`
- **options** (up to 4 per question):
  - **label**: `[project] <drift type>` (e.g., "[volon] go.mod stale")
  - **description**: What's drifted + whether auto-fix is available
  - **preview**: Show actual vs expected values
- **multiSelect**: true

Group items by severity (critical first). Present one severity level per question where possible.

### 4. Execute selected fixes

For each selected item:

**Auto-fixable items** (execute directly):
- go.mod stale → run `go mod tidy` in the project
- PCC stale → run `/pcc-refresh` for the project
- Bootstrap sync → run `/bootstrap-update`
- Memory dead refs → remove the dead reference from MEMORY.md

**Manual-fix items** (create Volon tasks):
- Port conflicts → create a task with conflict details
- Breaking config changes → create a task with migration steps

Handle partial failures — log errors and continue.

### 5. Show results summary

```
=== DRIFT RESOLVED ===

AUTO-FIXED:
  - [volon] go.mod synced
  - [mentat] PCC refreshed

TASKS CREATED (manual fix needed):
  - TASK-<id>: [cortex] port conflict 8080 vs 8081

NOT SELECTED (deferred):
  - [hadron] memory ref stale — info severity

Totals: <N> auto-fixed, <N> tasks created, <N> deferred
Failed fixes: <N or "none">
=== END DRIFT ===
```

## Invariants

- Never auto-fix without user selection
- Always show severity levels clearly
- Auto-fixes must be safe and reversible (no destructive operations)
- Manual fixes always create Volon tasks — never attempt risky fixes automatically
- Show actual vs expected in previews so the user can make informed decisions

$ARGUMENTS
