# Reference `mcp.DevServerName` from `internal/toolclient` instead of redeclaring it

**Phase:** Wave 6 — Semantic duplication / migration drift
**Status:** not-started
**Depends on:** none
**Touches:** `internal/mcp/naming.go` (`DevServerName`, canonical declaration), `internal/toolclient/broker.go` (redundant redeclaration).

`requires_architect_decision: false` — trivial, mechanical one-line fix.

## Context

### Findings addressed
- `GO-MCPTOOL-011` — severity low, confidence high. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.7; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-MCPTOOL-011`.

### Root cause

`DevServerName = "dev"` is independently declared in both `internal/mcp/naming.go` and `internal/toolclient/broker.go`, despite `internal/toolclient` already importing `internal/mcp` for other reasons — there is no import-cycle or package-boundary reason for the redeclaration; `internal/toolclient` could simply reference `mcp.DevServerName` directly. Classification **(1) textual-only boilerplate**, about as trivial as this batch's findings get.

### Current behavior

Confirm the exact declaration lines via `grep -n 'DevServerName' internal/mcp/naming.go internal/toolclient/broker.go` before editing — the audit's `findings.json` entry names both files but no specific line numbers.

## What to do — a candidate for the mechanical-cleanup batch instead

This is small and mechanical enough that it is also a reasonable candidate for batching into `TASKS/audit-remediation/13-mechanical-cleanup/`'s mechanical cleanup pass rather than being executed as its own standalone task here — flagging this explicitly per this batch's own guidance to note cross-folder homes for tiny fixes where they exist. Whichever folder actually executes it:

1. Delete the redundant `DevServerName` declaration in `internal/toolclient/broker.go`.
2. Update every reference to the deleted local constant within `internal/toolclient` to use `mcp.DevServerName` instead, adding the `internal/mcp` import if it is not already present in `broker.go` specifically (it is already imported elsewhere in the package per the audit, but confirm `broker.go` itself has the import).
3. Confirm no other package independently redeclares `DevServerName` — a quick `grep -rn 'DevServerName\s*=' --include='*.go' .` across the whole tree, in case the audit's two-site citation isn't exhaustive.

## Tests required

- Existing tests referencing `DevServerName` in either package (if any) must pass unchanged after the redirect — this is a pure identifier-reference change, not a value change (`"dev"` in both cases today).

## Prevention

None needed beyond the fix itself — a single canonical declaration has no divergence risk by construction.

## Verification

```bash
go build ./internal/mcp/... ./internal/toolclient/...
go vet ./internal/mcp/... ./internal/toolclient/...
grep -rn 'DevServerName\s*=' --include='*.go' .
```

Observable behavior required for PASS: `go build`/`go vet` pass; the `grep` shows exactly one declaration of `DevServerName` (in `internal/mcp/naming.go`) across the whole tree.

## Risk / rollback

Negligible risk — both constants are already the same value (`"dev"`); this is a pure DRY fix with zero behavior change. Rollback is a single-line revert.

## Done means

- [ ] `internal/toolclient/broker.go`'s redundant `DevServerName` declaration removed.
- [ ] All references within `internal/toolclient` use `mcp.DevServerName`.
- [ ] Exactly one declaration of `DevServerName` remains in the whole tree.
- [ ] `go build`, `go vet` pass for both packages.

## Work log

<!-- Worker fills this in. Note here whether this was executed standalone or folded into `13-mechanical-cleanup/`'s batch. -->

## Review notes

<!-- Reviewer fills this in. -->
