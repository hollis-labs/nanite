# Resolve Harness v1 vs. native durable-agent handler duplication — architect decision required first

**Phase:** Wave 6 — Semantic duplication / migration drift
**Status:** not-started
**Depends on:** none
**Touches:** `internal/api/harness_v1.go` (durable-agent start/resume/wake handlers), `internal/api/durable_agents.go` (native `/api/durable-agents/*` handlers), `internal/api/durable_agent_wake.go` (native wake handler).

```yaml
requires_architect_decision: true
```

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 6 — semantic duplication and migration drift · **Dispatch unit:** `W6a`
> - **Depends on:** `03/01` (shares `internal/api/durable_agents.go`)
> - **Blocks:** none
> - **Parallel-safe with:** `11/06`, `11/07`, `11/09`
> - **Gated on:** AD-19
> - **requires_security_review:** false · **requires_regression_test:** true

## Context

### Findings addressed
- `GO-API-007` — severity medium, confidence high. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.5; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-API-007`.

### Root cause

Harness v1's durable-agent start/resume/wake handlers in `internal/api/harness_v1.go` are **confirmed byte-for-byte duplicates** (via the `dupl` tool, not a manual eyeball match) of the native `/api/durable-agents/*` handlers in `internal/api/durable_agents.go` and `internal/api/durable_agent_wake.go`. The audit is explicit about the dupl-tool evidence: `durable_agents.go:226 ↔ harness_v1.go:411` (25 duplicated lines) and `durable_agent_wake.go:54 ↔ harness_v1.go:437` (16 duplicated lines) — same decode target, same service call, same tail, hand-copied rather than delegating.

**This finding has two genuinely different classifications depending on a fact this audit could not determine, which is exactly why it needs an architect decision before any code changes:**

1. **If the duplication is accidental** (the Harness v1 handlers were hand-copied during initial implementation and simply never refactored to delegate) — this is classification **(1) textual-only boilerplate**, and the fix is mechanical: have the Harness v1 handlers call the native handler functions (or a shared private helper) directly.
2. **If the duplication is a deliberate protocol-stability choice** — insulating an external control-plane contract (Harness v1, presumably consumed by an external caller who depends on its exact current shape) from internal refactors to the native `/api/durable-agents/*` handlers — this is classification **(5) intentionally independent**, and consolidating the two would be actively wrong: a future refactor to the native handler's internals could then silently change Harness v1's external contract, which is the exact failure mode the separation exists to prevent.

**No comment in either file states which of these is true.** The audit's `false_positive_considerations` field for this finding says exactly this: "Possibly deliberate protocol-stability choice insulating an external control-plane contract from internal refactors — no comment states this rationale though." This is not a case where the audit failed to look hard enough; it is a case where the answer genuinely depends on information only the architect (or whoever knows Harness v1's actual external-consumer contract, if any) has.

### Current behavior

`internal/api/harness_v1.go`'s durable-agent start/resume/wake handlers independently reimplement the same decode → service call → response tail as the native handlers in `internal/api/durable_agents.go` (start/resume, confirmed dupl-matched at `durable_agents.go:226` against `harness_v1.go:411`, 25 lines) and `internal/api/durable_agent_wake.go` (wake, confirmed dupl-matched at `durable_agent_wake.go:54` against `harness_v1.go:437`, 16 lines). Verify these line ranges against current source before starting — the audit's citations are from the audited commit `8feeee5c` and may have shifted if either file has had unrelated changes land since.

### Desired invariant

Depends entirely on the architect decision (see Proposed direction). Under reading 1 (accidental duplication), the desired invariant is: a fix to a durable-agent start/resume/wake handler's shared logic needs to land in exactly one place to take effect for both `/api/harness/v1/*` and `/api/durable-agents/*` callers. Under reading 2 (deliberate insulation), the desired invariant is the opposite: Harness v1's contract must be provably decoupled from the native handler's internals, and the current *accidental-looking* duplication should be replaced with an explicit, documented, intentional copy (or a versioned adapter) rather than left looking like unmaintained drift.

## What to do

### Scope
- `internal/api/harness_v1.go` — the durable-agent start/resume/wake handler bodies.
- `internal/api/durable_agents.go` — native start/resume handlers, read as reference (and, under reading 1, as the delegation target).
- `internal/api/durable_agent_wake.go` — native wake handler, same treatment.

### All production callers
Per the remediation guide's "fix every sibling path" principle, before implementing either reading, enumerate: (1) whatever calls `/api/harness/v1/*`'s durable-agent endpoints today (an external control plane, an internal test harness, or both — this determines whether reading 2's stability concern is real or hypothetical) and (2) whatever calls the native `/api/durable-agents/*` endpoints. If Harness v1 has zero real external callers today, that is itself decision-relevant evidence toward reading 1 (nothing to insulate) — record what was found either way, since "no confirmed external caller" is not the same as "confirmed no external caller."

### Proposed direction — do not resolve without architect sign-off

**This is the specific decision this task exists to queue, per the remediation guide's own architect-decision-queue framing** (guide §9, item 10: "Which duplicated semantics should share implementation vs parity tests"). Presented as two options, both fully specified, with the same discipline as this batch's other dual-reading tasks:

**Option A — the duplication is accidental; consolidate.**
Have the Harness v1 handlers call the native handler functions (or a shared private helper both call) directly, per the audit's own recommendation. This removes the duplicated logic and guarantees the two API surfaces stay behaviorally identical going forward — appropriate if Harness v1 is not actually a stability-insulated external contract, or if "stays identical to the native handler" is itself the desired Harness v1 contract.

**Option B — the duplication is a deliberate protocol-stability boundary; keep it, but make the intent explicit.**
Leave the two implementations separate, but add an explicit comment (and, ideally, a fixture/contract test) in `harness_v1.go` stating the insulation rationale, so a future reader does not mistake the duplication for unmaintained drift the way this audit initially read it. Consider whether a parity test (asserting both handlers currently produce equivalent responses for the same input, without coupling their implementations) is worth adding — this would let a future intentional divergence between the two surfaces be a deliberate, visible code change rather than an implicit side effect of "someone fixed a bug in one file and not the other."

### Non-goals
- Not a broader Harness v1 vs. native API audit beyond the durable-agent start/resume/wake handlers this finding names.
- Not deciding the fate of Harness v1 as a whole (deprecation, versioning strategy) — that is a larger question than this one duplication finding and is out of scope here.

## Tests required

- Under Option A: existing tests for both the native and Harness v1 durable-agent endpoints must continue to pass after consolidation, proving no behavioral change was introduced by the delegation.
- Under Option B: a new parity/contract test comparing Harness v1's and the native handler's responses for equivalent inputs, so a future accidental divergence is caught by CI rather than discovered externally.

## Prevention

The remediation guide's own "Semantic Duplication" standard applies directly once the architect decision is made: whichever option is chosen, the chosen invariant (either "these are the same code" or "these are deliberately independent, verified by a parity test") should be enforceable, not just documented in prose.

## Verification

```bash
go build ./internal/api/...
go vet ./internal/api/...
go test ./internal/api/... -run 'Harness|DurableAgent' -v
```

Observable behavior required for PASS: whichever option is chosen, existing Harness v1 and native durable-agent endpoint tests pass; under Option A, a change to the native handler's logic is provably reflected in Harness v1's behavior without a separate edit; under Option B, a parity test exists and passes, and the insulation rationale is documented in-code.

## Risk / rollback

Option A carries real risk if reading 2 turns out to be correct after the fact — an external Harness v1 consumer could be broken by a subsequent native-handler change that Harness v1 was supposed to be insulated from. This is precisely why the architect decision must be made and the caller-enumeration done *before* implementation, not discovered afterward. Option B carries lower risk (no behavior change) but leaves the duplication in place, deferring the maintenance cost the audit flagged. Rollback for Option A is a revert of the delegation; rollback for Option B is a revert of the added comment/test (low-risk either way).

## Done means

- [ ] Real external-caller enumeration for `/api/harness/v1/*`'s durable-agent endpoints completed and recorded (even if the answer is "no confirmed external caller found").
- [ ] Architect decision recorded: Option A (consolidate) or Option B (document and add parity test).
- [ ] Chosen option implemented; existing tests for both endpoint families pass.
- [ ] If Option B: parity test added and passing; insulation rationale documented in `harness_v1.go`.
- [ ] `go build`, `go vet`, `go test ./internal/api/...` all pass.

## Work log

<!-- Worker fills this in: what was actually done, any deviation from plan and why, anything escalated. -->

## Review notes

<!-- Reviewer fills this in: pass/fail, what was independently re-verified. -->
