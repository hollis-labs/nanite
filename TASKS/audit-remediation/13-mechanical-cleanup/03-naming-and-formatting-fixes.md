# Trivial naming fixes and repo-wide gofmt backlog

**Phase:** Wave 8 — Mechanical cleanup (audit-remediation batch, unsequenced — see folder README)
**Status:** not-started
**Depends on:** none within this batch.
**Touches:** `internal/service/install/adapters.go`, `internal/agent/permissions_test.go`, and (if the repo-wide gofmt option below is taken) all 122 files currently failing `gofmt -l` per `GO-HYG-001`'s baseline (`raw/golangci-baseline.log`), not just the 12 named by `GO-CHAT-007`.

## Context

All three findings in this task are trivial, mechanical, zero-design-ambiguity fixes — `requires_architect_decision: false` for all. Root cause / all-production-callers analysis is n/a for this task: none of these three touches a shared primitive, a security boundary, or a semantic contract; each is a local, self-contained fix.

## What to do

| Finding | File(s) | What |
|---|---|---|
| `GO-SVCCORE-008` | `internal/service/install/adapters.go:159,169` | A local `var any bool` shadows the Go builtin `any` type for the rest of that function's scope. Harmless today (revive already flags it) but confusing. Rename to something descriptive, e.g. `hasManaged`, at both cited line numbers and anywhere else in the same function that references the shadowed variable.
| `GO-AGENT-004` | `internal/agent/permissions_test.go` | After a prior removal task, this file's only remaining tests exercise `ParentDispatchAllowlist`, an unrelated concern — not permissions. The filename is now stale and misleading (guide's own §18 "naming should match current content" case). Rename to `parent_dispatch_allowlist_test.go`, or fold the remaining tests into `convert_test.go` if that's a more natural home (findings.json offers both options) — pick whichever keeps the test file's name accurately describing what it tests, and note the choice in the Work Log.
| `GO-CHAT-007` | 12 files in the `chat`/`messaging`/`envelope`/`elicitation`/`coordination` cluster (informational) | These 12 files fail `gofmt -l`. This is **not** a cluster-specific problem — it's part of a pre-existing, repo-wide 122-file gofmt gap already tracked as `GO-HYG-001` (see `12-quality-ratchet-and-standards/01-full-repo-scheduled-lint-gate.md` for the enforcement-gate side of that finding; no task in this inventory currently runs the actual `gofmt -w` backlog pass). Since `gofmt -w` is unconditionally safe and mechanical — it cannot change program behavior — there is no reason to fix only this cluster's 12 files and leave the other 110 for a later task. **Recommended: run `gofmt -w` across all 122 currently-failing files in this one step**, not just this cluster's 12, closing `GO-HYG-001`'s formatting-only portion (not its enforcement-gate portion, which stays separate) in the same pass. If a future planner prefers to keep this task scoped strictly to the 12 named files, that's a valid narrower alternative — but the wider pass is the lower-effort, lower-risk option and should be the default unless there's a reason (e.g. merge-conflict risk with concurrently in-flight work) to hold back.

## Non-goals

- Do not fix any other lint category while in these files (`errcheck`, `gosec`, etc.) — this task is naming/formatting only.
- Do not wire the `make lint` enforcement gate itself — that's `12-quality-ratchet-and-standards/01-full-repo-scheduled-lint-gate.md`'s job, tracking `GO-HYG-001`'s process-gap half. This task only clears the existing formatting debt.

## Done means

- [ ] `GO-SVCCORE-008`: `var any bool` renamed at both cited lines (and any other reference in the same function); `go vet`/revive no longer flag the shadow.
- [ ] `GO-AGENT-004`: `permissions_test.go` renamed (or its contents relocated) so the filename accurately reflects that it tests `ParentDispatchAllowlist`; the choice is recorded in the Work Log.
- [ ] `GO-CHAT-007`: either the 12 named files, or (recommended) all 122 files currently failing `gofmt -l` repo-wide, pass `gofmt -l` with zero output after this task; the scope decision (12 vs. 122) is recorded in the Work Log.
- [ ] `go build ./...` and `go vet ./...` pass; `gofmt -l .` returns empty for whatever scope was chosen.

## Work log

<!-- Worker fills this in as it goes: rename choices made, and whether the gofmt pass was scoped to 12 files or the full 122. -->

## Review notes

<!-- Reviewer fills this in: confirm gofmt -l is actually clean for the chosen scope, confirm the renamed test file's contents genuinely match its new name. -->
