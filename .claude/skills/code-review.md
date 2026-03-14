# code-review

Interactive code review — run quality checks against changed files, present findings with per-finding actions (accept, fix now, create task, discuss), and execute selected fixes.

## Usage

`/code-review [scope]`

**scope** (optional): What to review. Defaults to uncommitted changes.
- `staged` — review only staged changes
- `branch` — review all changes on current branch vs main
- `<commit-hash>` — review a specific commit
- `<file-path>` — review a specific file

## Instructions

### 1. Determine scope and get the diff

- Default: `git diff` (unstaged) + `git diff --cached` (staged)
- `staged`: `git diff --cached`
- `branch`: `git diff main...HEAD`
- Commit hash: `git show <hash>`
- File path: read the file + `git diff <file>`

### 2. Identify changed files and run review checklist

**Go Checklist:**
- Error handling: errors wrapped with context (`fmt.Errorf("...: %w", err)`)
- No `interface{}` or `map[string]any` — use typed structs
- No bare `panic()` — return errors instead
- Context propagation: functions accept `context.Context` where appropriate
- Resource cleanup: `defer` for Close/Unlock/Cancel
- Naming: follows Go conventions
- No hardcoded paths or magic strings
- OTel: spans and attributes follow observability contract
- MCP: tools follow naming conventions
- Tests: changed logic has corresponding test updates

**TypeScript/React Checklist:**
- No `any` types — use proper TypeScript types
- Components: props typed, no inline styles
- State management: appropriate use of hooks
- No console.log left in
- Accessibility: semantic HTML, ARIA where needed

**Portfolio-Aware Checks:**
- Cross-project impact on shared modules
- MCP compatibility
- Naming conventions per docs
- No new `tiamat-` or `fe-` prefixes
- Config changes needing mirroring
- Breaking changes documented

**Security Checks:**
- No secrets/credentials in code
- SQL: parameterized queries
- Input validation at system boundaries
- No command injection vectors

Rate each finding: FAIL (must fix), WARN (suggestion), INFO (note).

### 3. Show review summary header

```
=== CODE REVIEW ===
Scope: <description>
Files reviewed: <N>
Findings: <N> FAIL | <N> WARN | <N> INFO
```

If there are no FAIL or WARN findings, skip the interactive step and show:
```
All clear — no issues found. Ready to commit.
=== END REVIEW ===
```

### 4. Present findings — interactive dialog

Present findings using `AskUserQuestion`, up to **4 findings per call** (one question per finding, single-select).

**Question format per finding:**
- **header**: Severity (e.g., "FAIL", "WARN") — max 12 chars
- **question**: `[<file>:<line>] <issue description> — what should we do?`
- **options**:
  1. **Fix now** — "Apply the suggested fix inline"
  2. **Accept (not an issue)** — "Acknowledge and move on"
  3. **Create task** — "Create a Volon follow-up task for later"
  4. **Discuss** — "Need more context before deciding"
- **multiSelect**: false
- **preview**: Show the code context (file path, line number, surrounding code, and suggested fix)

Present FAIL findings first, then WARN. Skip INFO findings in interactive mode (just list them in the summary).

### 5. Handle "Discuss" selections

- Print full context: file, surrounding code, why this was flagged, what the checklist says
- Ask follow-up with options minus "Discuss" (replace with "Skip for now")

### 6. Execute selected actions

After all decisions collected:

1. **Fix now**: Apply the edit using the Edit tool. Show the diff.
2. **Accept**: No action — record as acknowledged
3. **Create task**: Use `volon_task_create` with finding details (file, line, issue, suggestion)
4. **Skipped**: No action, report only

Handle partial failures — if an edit fails, log and continue.

### 7. Show results summary

```
=== REVIEW COMPLETE ===
Scope: <description>

FIXED:
  - <file>:<line> — <issue> (applied fix)

ACCEPTED:
  - <file>:<line> — <issue> (acknowledged)

TASKS CREATED:
  - TASK-<id>: <file>:<line> — <issue>

SKIPPED:
  - <file>:<line> — <issue>

INFO (no action needed):
  - <file>:<line> — <note>

Totals: <N> fixed, <N> accepted, <N> tasks created, <N> skipped
Failed edits: <N or "none">

<If any FAIL findings remain unfixed>
  Warning: <N> FAIL findings not yet resolved. Fix before committing.
<Else>
  Ready to commit.
=== END REVIEW ===
```

## When to Use

- Before committing changes
- Before marking a Volon task as done (Gate 2 per ADR-021)
- When reviewing another agent's work
- When the user asks for a review or quality check

## Invariants

- Never apply fixes without user selection
- Always present FAIL findings before WARN
- Skip interactive step if no actionable findings
- Show code context in previews for informed decisions
- Created tasks must include file, line, and full issue description

$ARGUMENTS
