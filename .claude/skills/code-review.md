# code-review

Structured code review with a portfolio-aware checklist. Reviews changed files against quality gates (ADR-021) and Fragments Engine conventions.

## Usage
`/code-review [scope]`

**scope** (optional): What to review. Defaults to uncommitted changes.
- `staged` — review only staged changes
- `branch` — review all changes on current branch vs main
- `<commit-hash>` — review a specific commit
- `<file-path>` — review a specific file

## Examples
- `/code-review` — review uncommitted changes
- `/code-review branch` — review full branch diff
- `/code-review internal/chat/engine.go` — review specific file

## Instructions

1. **Determine scope and get the diff**:
   - Default: `git diff` (unstaged) + `git diff --cached` (staged)
   - `staged`: `git diff --cached`
   - `branch`: `git diff main...HEAD`
   - Commit hash: `git show <hash>`
   - File path: read the file + `git diff <file>`

2. **Identify changed files and their types**:
   - Go files (`.go`)
   - TypeScript/React files (`.ts`, `.tsx`)
   - Config files (`.yaml`, `.json`, `.toml`)
   - Markdown/docs (`.md`)
   - Skills/commands (`.claude/skills/`)

3. **Run the review checklist** on each changed file:

   ### Go Checklist
   - [ ] Error handling: errors wrapped with context (`fmt.Errorf("...: %w", err)`)
   - [ ] No `interface{}` or `map[string]any` — use typed structs
   - [ ] No bare `panic()` — return errors instead
   - [ ] Context propagation: functions accept `context.Context` where appropriate
   - [ ] Resource cleanup: `defer` for Close/Unlock/Cancel
   - [ ] Naming: follows Go conventions (camelCase unexported, PascalCase exported)
   - [ ] No hardcoded paths or magic strings — use constants or config
   - [ ] OTel: spans and attributes follow observability contract
   - [ ] MCP: tools follow naming conventions (`<service>_<action>`)
   - [ ] Tests: changed logic has corresponding test updates

   ### TypeScript/React Checklist
   - [ ] No `any` types — use proper TypeScript types
   - [ ] Components: props typed, no inline styles
   - [ ] State management: appropriate use of hooks
   - [ ] No console.log left in (use proper logging)
   - [ ] Accessibility: semantic HTML, ARIA where needed

   ### Portfolio-Aware Checks
   - [ ] Cross-project impact: does this change affect shared modules (core/otel, core/mcp, core/broker)?
   - [ ] MCP compatibility: do tool signatures match what other projects expect?
   - [ ] Naming conventions: follows `docs/architecture/naming-conventions.md`
   - [ ] No new `tiamat-` or `fe-` prefixes (use `core` or service name)
   - [ ] Config changes: do they need mirroring in other projects?
   - [ ] Breaking changes: are they documented? Do dependents need updates?

   ### Security Checks
   - [ ] No secrets/credentials in code
   - [ ] SQL: parameterized queries, no string concatenation
   - [ ] Input validation at system boundaries
   - [ ] No command injection vectors

4. **Rate each file**: PASS, WARN (non-blocking suggestions), FAIL (must fix)

5. **Output the review**:

```
=== CODE REVIEW ===
Scope: <description>
Files reviewed: <N>

<file_path> — <PASS|WARN|FAIL>
  [FAIL] <issue description>
    Line <N>: <code snippet>
    Fix: <suggestion>
  [WARN] <suggestion>
    Line <N>: <code snippet>

<file_path> — PASS
  No issues found.

SUMMARY:
  PASS: <N> files
  WARN: <N> files (<N> warnings total)
  FAIL: <N> files (<N> issues total)

<If any FAIL>
  Action required: Fix FAIL issues before committing.
<If only WARN/PASS>
  Ready to commit. Consider addressing warnings.
=== END REVIEW ===
```

## When to Use
- Before committing changes
- Before marking a Volon task as done (Gate 2 per ADR-021)
- When reviewing another agent's work
- When the user asks for a review or quality check
