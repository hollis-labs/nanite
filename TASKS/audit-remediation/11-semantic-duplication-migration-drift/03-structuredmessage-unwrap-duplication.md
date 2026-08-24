# Confirm the import-cycle constraint behind StructuredMessage-unwrap duplication before deciding whether to unify

**Phase:** Wave 6 — Semantic duplication / migration drift
**Status:** reviewed
**Depends on:** none
**Touches:** `internal/chat/structured.go` (`replayContent`), `internal/recovery/pack/pack.go` (`MessagePlainText`).

```yaml
requires_architect_decision: true
```

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 6 — semantic duplication and migration drift · **Dispatch unit:** `W6b`
> - **Depends on:** Wave 6a complete
> - **Blocks:** none
> - **Parallel-safe with:** `11/12`, `11/14`, `11/16`
> - **Gated on:** none
> - **requires_security_review:** false · **requires_regression_test:** true

## Context

### Findings addressed
- `GO-CHAT-002` — severity medium, confidence high. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.9; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-CHAT-002`.

### Root cause

`chat.replayContent` (`internal/chat/structured.go`) and `recovery/pack.MessagePlainText` (`internal/recovery/pack/pack.go`) implement the **identical** StructuredMessage-JSON-unwrap algorithm — one using the full shared `StructuredMessage` type, the other a hand-rolled anonymous struct re-deriving the same two fields. This is **self-documented as intentional** — a comment in `internal/chat/structured.go` states the duplication exists specifically to avoid a cross-package import from `internal/recovery/pack` into `internal/chat` (or vice versa; confirm the exact direction when reading the comment). However, per the audit's own text: "the reviewer's own dependency check found no actual cycle risk in that direction" — the audit independently traced the import graph and did not find the cycle the in-code comment cites as the reason for the duplication.

This puts the finding in an unusual position for this batch: it currently reads as **classification (2) same semantics/stable** (both implementations do the identical thing today, no divergence found), but the comment's stated rationale for keeping it that way (an import cycle) may be **factually wrong**, which is exactly the guide's §27 "trust comments over executable behavior" trap in reverse — here the comment isn't lying about what the code does, it's asserting a *constraint* that may not actually hold. If the constraint is confirmed false, the two implementations become a pure DRY question (classification 1, textual-only boilerplate, safe to unify); if the constraint is confirmed real (the audit's own check may have missed a transitive path, or the codebase may have changed since), the duplication should stay and the comment is accurate.

### Current behavior

The audit's evidence array for this finding (`findings.json`) is empty — no file:line citation was captured beyond the two file names. **Locate `replayContent` in `internal/chat/structured.go` and `MessagePlainText` in `internal/recovery/pack/pack.go` via `grep -n` before starting**, and read the actual comment text in `structured.go` stating the cross-package-import avoidance rationale — the task brief above paraphrases the audit's summary of that comment, not the comment's exact wording, which should be quoted verbatim once located.

### Desired invariant

If the wire contract for `StructuredMessage`'s JSON shape changes, both `replayContent` and `MessagePlainText` must be updated together, either because they are now the same code (if the import-cycle constraint is confirmed false) or because a decision has recorded that they are deliberately independent and someone has audited whether a parity test is warranted (if the constraint is confirmed real).

## What to do

### Scope
- `internal/chat/structured.go` — `replayContent`, and specifically the in-code comment explaining the cross-package-import avoidance.
- `internal/recovery/pack/pack.go` — `MessagePlainText`, and the anonymous struct it uses to re-derive the same two fields the shared `StructuredMessage` type already carries.
- The actual import graph between `internal/chat` and `internal/recovery/pack` (and any transitive path either package pulls in that could create a cycle if the other imported it directly).

### Proposed direction

**Step 1 — confirm the import-cycle constraint, independently, before deciding anything.** Run `go list -deps` (or equivalent) on both packages and manually trace whether `internal/recovery/pack` importing `internal/chat` (or `internal/chat` importing `internal/recovery/pack`, in whichever direction the existing comment claims is the risk) would actually create a cycle. Do not just re-trust the audit's own conclusion either — the audit's own text frames this as its own independent finding, not a re-verification of an existing comment's claim to a fixed standard; verify it fresh against current source, since this is exactly the kind of fact that can flip if either package's import list has changed since the audit's commit.

**Step 2a — if no cycle risk is confirmed:** export a minimal shared unwrap helper (a small function taking the raw JSON and returning the two fields both call sites need) that both `internal/chat` and `internal/recovery/pack` import from a third, lower-level package (likely wherever `StructuredMessage` itself is already defined, to avoid introducing a new cross-cutting package for a two-line helper). Update both call sites to use it. This resolves the finding as classification (1) — pure boilerplate removal, no behavior change.

**Step 2b — if a real cycle risk is confirmed:** leave the two implementations separate, but correct the in-code comment in `structured.go` to state the confirmed constraint precisely (which direction of import creates the cycle, and through what transitive path), so a future reader doesn't have to re-derive this. Consider adding a small parity test asserting both implementations produce identical output for the same `StructuredMessage`-shaped JSON, so a future wire-format change that's applied to only one of the two copies is caught by CI.

### Non-goals
- Not a broader refactor of `internal/chat`'s or `internal/recovery/pack`'s package boundaries.
- Not moving `StructuredMessage` itself to a new location unless Step 2a's shared-helper approach requires it — and even then, prefer the smallest viable extraction (a helper function) over a package reorganization.

## Tests required

- Under Step 2a: existing tests for both `replayContent` and `MessagePlainText` continue to pass after both are routed through the shared helper; add a test asserting both call sites now use identical logic (e.g. by testing the shared helper directly and confirming both call sites are thin wrappers around it).
- Under Step 2b: a new parity test comparing `replayContent`'s and `MessagePlainText`'s output for the same input `StructuredMessage` JSON, so a future wire-format change applied to only one is caught by CI rather than discovered as a runtime divergence.

## Prevention

The remediation guide's "Semantic Duplication" standard: a rule that's duplicated because of a *documented* constraint should have that constraint verified before being trusted going forward — an unverified comment asserting "we can't share this" is itself a form of technical debt if the constraint turns out to be false. Whichever path is taken, the follow-up (shared helper or parity test) is what prevents the two implementations from silently drifting on the next wire-format change.

## Verification

```bash
go list -deps ./internal/chat/... | grep recovery/pack
go list -deps ./internal/recovery/pack/... | grep internal/chat
go build ./internal/chat/... ./internal/recovery/pack/...
go vet ./internal/chat/... ./internal/recovery/pack/...
go test ./internal/chat/... ./internal/recovery/pack/... -run 'Replay|MessagePlainText|Structured' -v
```

Observable behavior required for PASS: the import-cycle claim is confirmed true or false, in writing, with the evidence (the `go list -deps` output or equivalent); whichever path follows is implemented; existing tests for both `replayContent` and `MessagePlainText` pass unchanged.

## Risk / rollback

Low risk either way — this is an internal implementation-sharing question with no external contract change. Under Step 2a, the main risk is introducing an actual import cycle if the verification in Step 1 was wrong; `go build` will fail immediately and loudly if so, making this a safe, self-checking change. Rollback is a single-commit revert in either direction.

## Done means

- [x] Import-cycle constraint independently re-verified against current source (not just re-citing the audit's own conclusion), with the evidence recorded in Work log.
- [x] If no cycle: shared unwrap helper extracted; both `replayContent` and `MessagePlainText` use it; existing tests pass.
- [x] If cycle confirmed: N/A — no cycle was found, so the shared-helper path
  was taken instead.
- [x] `go build`, `go vet`, `go test ./internal/chat/... ./internal/recovery/pack/...` all pass.

## Work log

- 2026-08-24 worker: Independently verified the import graph before editing. `go list -deps ./internal/chat/... | grep internal/recovery/pack` produced no output, and `go list -deps ./internal/recovery/pack/... | grep internal/chat` produced no output. Re-ran the same checks after the helper extraction and both still produced no output. This confirms there is no current `chat` <-> `recovery/pack` dependency edge; the implementation still avoids importing the large `internal/chat` package into `internal/recovery/pack`.
- Located the current source with `grep -RInE "replayContent|MessagePlainText|StructuredMessage|recovery/pack|structured message" internal/chat internal/recovery/pack`. The actual current `structured.go` comment did not state a concrete import-cycle path; its exact cross-package sentence was: "Content that isn't StructuredMessage-shaped (plain user text, legacy pre-structured rows, envelope_response rows formatted via FormatEnvelopeResponseContent, which are prefixed \"[envelope:...]\" rather than \"{\") is returned unchanged — same fallback shape as internal/recovery/pack.MessagePlainText, which unwraps the same JSON envelope for a different purpose (Recovery Pack replay text)." I replaced that cross-reference with a comment pointing at the shared leaf helper.
- Added `internal/structuredmessage.UnwrapText`, a lower-level helper that parses only the persisted `{"v":...,"text":...}` shape and returns `(text, ok)`. It treats `v > 0` as the valid StructuredMessage-shaped contract. The helper deliberately does not import `internal/chat` or move the full `StructuredMessage` type.
- Updated `chat.replayContent` and `recovery/pack.MessagePlainText` to call the helper while preserving caller-specific behavior: `replayContent` returns original `content` on fallback and returns raw structured text, while `MessagePlainText` trims fallback content and trims structured text.
- Added focused coverage in `internal/structuredmessage`, plus caller tests for invalid versions and whitespace behavior in `internal/chat` and `internal/recovery/pack`.
- Verification passed: `go build ./internal/chat/... ./internal/recovery/pack/... ./internal/structuredmessage/...`; `go vet ./internal/chat/... ./internal/recovery/pack/... ./internal/structuredmessage/...`; `go test ./internal/chat/... ./internal/recovery/pack/... ./internal/structuredmessage/... -run 'Replay|MessagePlainText|Structured|Unwrap' -v`; baseline `go build ./cmd/nanite/`; baseline `go vet ./...`; baseline `go test ./...`.

## Review notes

- 2026-08-24 fresh review PASS. Verified the shared leaf helper removes the
  duplicated StructuredMessage-shaped JSON parse rule without importing
  `internal/chat` into `internal/recovery/pack`. `chat.replayContent` preserves
  raw fallback/untrimmed text behavior, and `recovery/pack.MessagePlainText`
  preserves trimmed fallback/text behavior. Focused package checks and full
  `go build ./cmd/nanite/`, `go vet ./...`, `go test ./... -count=1` passed.
