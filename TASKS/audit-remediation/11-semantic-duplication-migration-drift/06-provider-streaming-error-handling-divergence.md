# Resolve OpenAI vs. Anthropic streaming malformed-tool-call-JSON divergence — highest-priority item in this folder

**Phase:** Wave 6 — Semantic duplication / migration drift
**Status:** not-started
**Depends on:** none
**Touches:** `internal/llm/openai/stream.go` (lines 96-123, including the comment at lines 104-105), `internal/llm/anthropic/stream.go` (lines 232-253).

```yaml
requires_architect_decision: true
requires_regression_test: true
```

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 6 — semantic duplication and migration drift · **Dispatch unit:** `W6a`
> - **Depends on:** Wave 5 complete
> - **Blocks:** none
> - **Parallel-safe with:** `11/01`, `11/02`, `11/07`, `11/09`
> - **Gated on:** AD-19 — this one may be **legitimately independent**; classify before collapsing. Two providers' streaming error handling can differ for real reasons.
> - **requires_security_review:** false · **requires_regression_test:** true

## Context

### Findings addressed
- `GO-INFRA-004` — severity medium, confidence high. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.2; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-INFRA-004`.

### Why this is the priority item in this folder

The remediation guide's Wave 6 instruction is explicit: **"Prioritize semantic divergence and migration drift over LOC reduction."** Of this folder's 16 items, this is the **only one classified (3) same semantics/divergent behavior** — every other item in this folder is either boilerplate duplication (1), stable-but-unenforced duplication (2), deliberate independence (5), or a naming collision. Classification (3) is explicitly the guide's highest-priority category within this wave, because it means two implementations of what is supposed to be **the same semantic contract** — "how should a provider adapter handle malformed streamed tool-call JSON" — currently produce **different, real, user-visible behavior** for the same failure class. This is not a maintainability question; per the guide's own "Semantic Duplication" standard, "duplicating a semantic rule is a correctness concern," and here the duplicated rule has already diverged, not merely at risk of diverging.

### Root cause

OpenAI's streaming implementation (`internal/llm/openai/stream.go:96-123`) and Anthropic's streaming implementation (`internal/llm/anthropic/stream.go:232-253`) both need to handle the same failure class: a malformed tool-call-argument JSON payload arriving mid-stream. They currently handle it in **opposite ways**:

- **OpenAI** (`stream.go:96-123`): on the first malformed tool-call-argument JSON, emits an `EventError` and then `return`s — aborting the **entire turn**. This drops any other queued tool calls, drops usage reporting, and drops the terminal `EventDone` event that would otherwise signal clean stream completion.
- **Anthropic** (`stream.go:232-253`): on the equivalent failure, degrades **gracefully** — falls back to a `{"_raw": raw}` payload and continues processing the rest of the stream normally.

This is a confirmed, real control-flow divergence — the audit traced both code paths directly, not via a mechanical tool, and states this explicitly: "confirmed control-flow divergence for the same failure class." Whether the downstream chat-loop consumer handles a stream that ends on `EventError` without a following `EventDone` cleanly was **not traced by the audit** (explicitly out of its scope) — this is the first thing to establish before deciding which provider's behavior is "correct," since the practical impact of OpenAI's abrupt termination depends entirely on how gracefully (or not) the consumer currently handles it.

**The "possibly deliberate" reading:** a comment at `stream.go:104-105` (OpenAI) suggests the early-return may be a deliberate "fail loud" design choice, not an oversight. Read this comment verbatim before assuming either reading — the audit's own `false_positive_considerations` field states this explicitly: "A comment at stream.go:104-105 suggests the early return may be a deliberate 'fail loud' choice, not an oversight." As with this folder's other dual-reading items, present both possibilities to the architect rather than picking one:

1. **The divergence is accidental** — OpenAI's handling was written before Anthropic's more resilient fallback pattern existed (or independently, without cross-referencing it), and the "fail loud" comment is a post-hoc rationalization rather than a deliberate design decision made with the Anthropic behavior in mind. Under this reading, OpenAI's path should very likely adopt Anthropic's graceful-fallback pattern, per the audit's own recommendation.
2. **The divergence is deliberate** — someone decided OpenAI-sourced malformed tool-call JSON is different in kind from Anthropic-sourced malformed tool-call JSON (e.g., a different trust signal, a different upstream failure mode that's worth surfacing loudly rather than silently degrading through), and the comment reflects that reasoning accurately. Under this reading, the "fix" is to document why the two providers are intentionally different (classification moves from (3) to (5)) and, ideally, verify the downstream consumer handles OpenAI's abrupt termination as gracefully as the "fail loud" design intends.

### Current behavior

```go
// internal/llm/openai/stream.go:96-123 — malformed tool-call-argument JSON
// aborts the entire turn: EventError, then return. Comment at 104-105
// (read verbatim before implementing — quoted paraphrase only here)
// suggests this may be an intentional "fail loud" choice.
```

```go
// internal/llm/anthropic/stream.go:232-253 — equivalent failure degrades
// gracefully: {"_raw": raw} fallback, continues processing the stream.
```

Confirm both line ranges against current source before starting (`grep -n` both files) — the audit's citations are from commit `8feeee5c` and may have shifted since.

### Desired invariant

Whatever the architect decides, the two provider adapters' handling of malformed streamed tool-call JSON should be **the intentional result of a decision**, not an artifact of independent implementation history. If the decision is "these should behave the same," both should degrade gracefully (or both should fail loud) for the same failure class. If the decision is "these should behave differently because the failure classes are genuinely different in kind," that reasoning must be documented in both files, and the downstream consumer's handling of both outcomes must be verified correct.

## What to do

### Scope
- `internal/llm/openai/stream.go` — the malformed-tool-call-JSON handling block and its "fail loud" comment.
- `internal/llm/anthropic/stream.go` — the equivalent graceful-fallback handling block.
- The downstream chat-loop consumer of both providers' stream events — specifically, how it handles a stream that terminates on `EventError` without a following `EventDone` (OpenAI's current behavior). This trace was explicitly not done by the audit and must be done here before deciding whether OpenAI's current behavior is merely inconsistent or actively broken downstream.

### All production callers
Per the remediation guide's "fix every sibling path" principle: enumerate every caller of both `internal/llm/openai/stream.go`'s and `internal/llm/anthropic/stream.go`'s streaming entry points (almost certainly funneled through a shared chat-loop / provider-abstraction layer in `internal/service` or `internal/llm` — locate the actual call sites, do not assume). Confirm what each caller does when it receives an `EventError` without a subsequent `EventDone`, since this is the concrete, observable consequence of OpenAI's current behavior that the audit did not trace.

### Proposed direction — do not resolve without architect sign-off

**This is the architect decision this task exists to queue.** Present both options in full, as this folder's other dual-reading items do:

**Option A — the divergence is accidental; make OpenAI match Anthropic's graceful degradation.**
Per the audit's own recommendation framing ("Architect decision on whether OpenAI's path should adopt the same graceful fallback Anthropic uses"), change OpenAI's malformed-tool-call-JSON handling to fall back to a `{"_raw": raw}`-shaped payload (matching Anthropic's pattern) and continue processing the rest of the stream, rather than aborting with `EventError` + `return`. This makes provider behavior for this failure class uniform, which is very likely the more defensible default for an agent-facing chat loop that shouldn't lose an entire turn's other queued tool calls and usage reporting over one malformed argument blob.

**Option B — the divergence is deliberate; document it and verify the consumer handles it correctly.**
Leave OpenAI's fail-loud behavior in place, but (1) rewrite the `stream.go:104-105` comment to state the actual rationale explicitly and completely (not just "fail loud" as a fragment) so a future reader — and future audits — don't have to re-derive whether this was intentional, and (2) trace and verify the downstream chat-loop consumer actually handles a stream ending on `EventError` without `EventDone` gracefully (no hang, no silent data loss beyond the documented "abort this turn" contract, a sensible surfaced error to the end user/caller). If the consumer does *not* handle this cleanly today, that is itself a correctness bug independent of the provider-parity question and should be fixed regardless of which option is chosen.

### Non-goals
- Not a broader unification of OpenAI's and Anthropic's streaming implementations beyond this one failure class — the audit explicitly notes the high-level shape is shared but concrete mechanics legitimately differ (named SSE events + rate-tracker/circuit-breaker vs. chunk-delta accumulation with no rate-tracker, tracked separately as a deferred parity gap in `followups.nanite.cw_20260508_0012`) — that broader gap is out of scope here.
- Not touching the rate-tracker/circuit-breaker parity gap referenced above — a different, already-tracked, separate finding.

## Tests required

- **Regression test reproducing the original defect**: a test that feeds a malformed tool-call-argument JSON payload through OpenAI's streaming path and asserts the *chosen* behavior (graceful fallback under Option A; documented fail-loud with a verified-clean downstream consumer reaction under Option B) — this test should fail against the current pre-fix OpenAI behavior if Option A is chosen, proving it reproduces the real defect.
- A parity test (if Option A is chosen) asserting OpenAI's and Anthropic's streaming paths now produce equivalent (not necessarily identical, but semantically equivalent) output for the same malformed-tool-call-JSON input class.
- A downstream-consumer test (either option) covering the "stream ends on `EventError` without `EventDone`" case explicitly — this is the one behavior the audit flagged as untraced, and it must be verified either way.

## Prevention

This finding is the direct motivating case for the guide's own "Semantic Duplication" standard applied at its sharpest: "Duplicating a semantic rule is a correctness concern." Whichever option is chosen, record the decision explicitly in both provider files' comments (not just one), so a future third provider adapter (or a future refactor of either existing one) has a clear, discoverable precedent to follow rather than having to reverse-engineer intent from behavior, the way this audit had to.

## Verification

```bash
go build ./internal/llm/openai/... ./internal/llm/anthropic/...
go vet ./internal/llm/openai/... ./internal/llm/anthropic/...
go test ./internal/llm/openai/... ./internal/llm/anthropic/... -run 'Stream' -v
go test ./internal/service/... -run 'Stream|Provider' -v
```

Observable behavior required for PASS: the downstream chat-loop consumer's handling of an `EventError`-without-`EventDone` stream termination is traced and verified correct (or fixed if it wasn't); whichever option is chosen is implemented and tested; existing tests for both providers' streaming paths pass unchanged except for the intentional behavior change (if Option A).

## Risk / rollback

**Option A** changes real, observable OpenAI streaming behavior — a malformed tool-call argument will no longer abort the turn, which could mask a class of upstream errors that a caller was previously relying on seeing surfaced loudly (this is precisely the risk the "fail loud" comment may have been guarding against, if Option B's reading turns out to be correct historically). Mitigate by confirming via the downstream-consumer trace whether anything currently depends on the abrupt-termination behavior before changing it. **Option B** carries lower behavioral risk (no change to OpenAI's handling) but requires the downstream-consumer trace to be done regardless, since an unverified fail-loud path could already be silently mishandled today. Rollback for either option is a single-file revert scoped to `internal/llm/openai/stream.go`.

## Done means

- [ ] Downstream chat-loop consumer's handling of `EventError`-without-`EventDone` traced and verified (documented in Work log, since the audit explicitly left this untraced).
- [ ] Architect decision recorded: Option A (unify on graceful degradation) or Option B (document deliberate fail-loud divergence, verify consumer handles it).
- [ ] Chosen option implemented; `stream.go:104-105`'s comment (OpenAI) is accurate and complete regardless of which option is chosen.
- [ ] Regression test reproducing the original malformed-JSON behavior added; fails against pre-fix behavior if Option A, passes against documented behavior if Option B.
- [ ] `go build`, `go vet`, `go test` pass for both provider packages and the downstream consumer.

## Work log

<!-- Worker fills this in: what was actually done, any deviation from plan and why, anything escalated. -->

## Review notes

<!-- Reviewer fills this in: pass/fail, what was independently re-verified. -->
