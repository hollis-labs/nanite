# ADR-021: Automated Quality Gates — Tooling, Linting, Testing, and Code Review

**Status:** Accepted
**Date:** 2026-03-14
**Deciders:** chrispian, Mentat

## Context

Portfolio-wide audit revealed zero automated quality tooling across 11 Go projects and 4 frontends. No linting, no CI, no pre-commit hooks, no struct validation, no coverage tracking, no security scanning. Additionally, ~2,200 uses of `interface{}`/`map[string]any` across the portfolio undermine Go's type safety.

With AI-assisted development, quality gates are even more critical — they catch issues before they compound.

## Decision

### Gate Architecture

Three gate levels, each running progressively more checks:

```
Pre-commit (Lefthook)     → fast, local, blocks commit
  ↓
Task completion (agent)   → scoped code review + lint on changed files
  ↓
PR creation (GitHub)      → full CI + Codex code review
```

### Gate 1: Pre-Commit (Lefthook)

Fast checks that run before every commit:
- `gofmt` / `goimports` — formatting
- `golangci-lint run --new` — lint only changed files
- `go vet ./...` — basic correctness
- Biome check (for TS/React files) — lint + format
- Total target: <15 seconds

### Gate 2: Task Completion

When an agent finishes a task (volon_task_transition to "done"):
- **Scoped code review**: Use `git diff` to identify changed files, run LLM-based code review only on those files
- **Scoped linting**: `golangci-lint run` on changed packages only
- **Scoped tests**: Run tests for changed packages (`go test ./changed/pkg/...`)
- **Build check**: `go build ./...` to catch compile errors
- If issues found → task sent back with changes needed (transition back to "doing")
- If clean → task stays "done"

### Gate 3: PR Creation (GitHub)

Full CI pipeline + external review:
- `go test -race ./...` — full test suite with race detection
- `golangci-lint run` — full lint (not just changed files)
- `govulncheck ./...` — security vulnerability scan
- `go build ./...` — build verification
- Coverage report (fail if below threshold)
- **Request Codex code review** via `gh pr edit --add-reviewer` or GitHub API
- Biome CI check for frontend changes

### Tooling Stack

**Go:**
- golangci-lint with shared `.golangci.yml` (errcheck, errorlint, gosec, staticcheck, govet, revive, unconvert, unparam, exhaustive, modernize, nilaway)
- govulncheck for CVE scanning
- go-playground/validator for struct validation
- Lefthook for pre-commit hooks

**TypeScript/React:**
- Biome (replaces ESLint + Prettier)
- @tsconfig/strictest for maximum type safety

**Python (Carrier, while it exists):**
- Ruff for linting/formatting
- Pyright for type checking

**GitHub:**
- Codex as required reviewer on all PRs
- GitHub Actions for CI pipeline

### Smart Scoping (Save Time)

All tools should scope to changed files where possible:
- `golangci-lint run --new` — only lint new/changed code
- `go test ./changed/packages/...` — only test affected packages
- Code review — only review `git diff` output, not entire codebase
- Full runs reserved for: PR CI, nightly builds, release gates

## Consequences

### Positive
- Catches bugs before they reach main branch
- Type safety enforced at compile time + lint time
- Security vulnerabilities caught automatically
- AI-generated code held to same standards as human code
- Codex review provides independent AI perspective on PRs
- Scoped checks keep iteration fast

### Negative
- Initial setup effort across 11 projects
- Pre-commit hooks add ~15s to commit flow
- golangci-lint config needs tuning to avoid false positives initially
- Codex review adds latency to PR merge

### Risks
- Over-strict linting causing developer friction — start with errors only, add warnings gradually
- golangci-lint false positives on legacy code — use `--new` flag initially, fix legacy incrementally
