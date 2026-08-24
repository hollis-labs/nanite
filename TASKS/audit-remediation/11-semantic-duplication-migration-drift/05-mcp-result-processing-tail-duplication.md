# Extract the shared post-CallTool result-processing pipeline out of `Manager.ExecuteTool`/`ExecuteToolOnServer`

**Phase:** Wave 6 — Semantic duplication / migration drift
**Status:** reviewed
**Depends on:** none
**Touches:** `internal/mcp/manager.go` (`Manager.ExecuteTool`, `Manager.ExecuteToolOnServer`).

`requires_architect_decision: false` — mechanical extraction, both call sites are already confirmed genuinely necessary.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 6 — semantic duplication and migration drift · **Dispatch unit:** `W6a`
> - **Depends on:** `09/04` (shares `internal/mcp/manager.go`)
> - **Blocks:** `13/05`
> - **Parallel-safe with:** `11/06`, `11/07`, `11/09`
> - **Gated on:** AD-19
> - **requires_security_review:** false · **requires_regression_test:** true

## Context

### Findings addressed
- `GO-MCPTOOL-012` — severity low, confidence high. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.7; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-MCPTOOL-012`.

### Root cause

`Manager.ExecuteTool` and `Manager.ExecuteToolOnServer` (`internal/mcp/manager.go`) textually duplicate the **entire post-`CallTool` result-processing pipeline**: assemble the result, validate its size against the caller's trust-tier ceiling, and set span attributes for telemetry. The audit is explicit that **both call sites are genuinely necessary** — this is not a case of one function being a dead/redundant wrapper around the other; `ExecuteTool` and `ExecuteToolOnServer` serve different valid callers with different entry points into tool execution. Only the shared *tail* — everything that happens after `CallTool` returns — is duplicated. Classification **(1) textual-only boilerplate**.

### Current behavior

The audit's evidence array for this finding is empty; no file:line citation was captured beyond the file name and the two symbol names. **Locate both functions via `grep -n 'func (m \*Manager) Execute' internal/mcp/manager.go` before starting**, and read both bodies in full to confirm the extent of the shared tail (result assembly, size validation against the trust-tier ceiling documented in `docs/mcp-trust-model.md`, and span-attribute construction) before extracting it.

### Desired invariant

The post-`CallTool` result-processing pipeline (assemble → validate size → set span attributes) exists in exactly one place; both `ExecuteTool` and `ExecuteToolOnServer` call it with whatever caller-specific context (trust tier, span, server identity) it needs as parameters.

## What to do

1. Read both `ExecuteTool` and `ExecuteToolOnServer` in full and precisely identify where they diverge (the part before `CallTool` returns — different entry/resolution logic per the audit's own framing) versus where they are identical (the tail).
2. Extract the shared tail into a private helper method on `*Manager` (e.g. `processCallToolResult` or similar — name it to match the existing package's naming conventions, check `internal/mcp/manager.go`'s other private helpers for the house style) taking whatever parameters the tail needs (the raw `CallTool` result, the trust tier, the span, and any other caller-specific values both functions currently close over identically).
3. Update both `ExecuteTool` and `ExecuteToolOnServer` to call the new helper instead of inlining the tail.
4. Confirm the extraction is behavior-preserving by construction — since the audit confirmed the tail is byte-for-byte identical between the two call sites today, extracting it verbatim (not rewriting it) is the safest path; do not "improve" the logic while extracting it, since that would conflate a duplication fix with an unrelated behavior change (see the guide's own guardrail against opportunistic refactoring).

## Tests required

- Existing tests for both `ExecuteTool` and `ExecuteToolOnServer` (result-size validation, trust-tier ceiling enforcement, span attributes) must pass unchanged after the extraction — this is a structural refactor, not a behavior change, so the pre-existing test suite is the regression guard.
- If no existing test directly exercises the size-validation/span-attribute tail for both call sites, consider whether a shared table-driven test against the new helper (covering both callers' invocation shapes) is worth adding — not strictly required since the guide treats this as optional DRY cleanup, but cheap given the extraction already isolates the logic into one testable unit.

## Prevention

A single shared helper is self-enforcing — a future change to the size-validation or telemetry logic can only land in one place, and both `ExecuteTool` and `ExecuteToolOnServer` automatically pick it up. No new lint rule or dedicated regression test is required beyond confirming the existing test suite still passes.

## Verification

```bash
go build ./internal/mcp/...
go vet ./internal/mcp/...
go test ./internal/mcp/... -run 'ExecuteTool' -v
```

Observable behavior required for PASS: `go build`/`go vet`/`go test` all pass; `ExecuteTool` and `ExecuteToolOnServer` both call the same extracted helper for their post-`CallTool` tail (confirm by reading the diff, not just passing tests).

## Risk / rollback

Very low risk — pure structural extraction of code the audit confirmed is already byte-for-byte identical between the two call sites. Rollback is a single-commit revert.

## Done means

- [x] Shared post-`CallTool` tail extracted into one private helper on `*Manager`.
- [x] `ExecuteTool` and `ExecuteToolOnServer` both call the extracted helper; no duplicated tail logic remains.
- [x] Existing tests for both functions pass unchanged.
- [x] `go build`, `go vet`, `go test ./internal/mcp/...` all pass.

## Work log

- 2026-08-23 — Re-derived current citations before editing: `Manager.ExecuteTool` starts at `internal/mcp/manager.go:697`, `ExecuteToolOnServer` starts at `internal/mcp/manager.go:781`, and the duplicated post-`CallTool` tails were `manager.go:746-759` and `manager.go:813-825`. AD-19's decided `11/05` direction is at `TASKS/audit-remediation/ARCHITECT-DECISIONS.md:1039-1054`; the trust-model pipeline is documented at `docs/mcp-trust-model.md:89-112`.
- Extracted the duplicate assemble → result-size validation → `nanite.mcp.result_len` span attribute tail into private `(*Manager).processCallToolResult`. Both public entry points keep their existing pre-`CallTool` resolution/error behavior and call the helper after successful `CallTool`.
- Added `TestManager_ExecuteToolOnServer_UsesResultProcessingTail` so the explicit-server path now directly covers ANSI stripping and third-party tier result-cap enforcement through the shared tail.
- Verification passed: `go build ./internal/mcp/...`, `go vet ./internal/mcp/...`, `go test ./internal/mcp/... -run 'ExecuteTool' -v`, `go build ./cmd/nanite/`, `go vet ./...`, and `go test ./...`.

## Review notes

- 2026-08-24 fresh re-review PASS. Verified both MCP execution paths now call
  `processCallToolResult` for the post-`CallTool` tail: result assembly,
  trust-tier result-size validation, and span attributes live in one helper.
  Targeted MCP `ExecuteTool` checks, `go build`, `go vet`, and full
  `go test ./... -count=1` passed.
