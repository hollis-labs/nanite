# Migrate `resolveSubagentCompletionPolicy` onto the shared `override.Resolve` cascade

**Phase:** Wave 6 — Semantic duplication / migration drift
**Status:** not-started
**Depends on:** none
**Touches:** `internal/service/subagent_reactor.go` (`resolveSubagentCompletionPolicy`), `internal/service/messaging_reactor.go` (`resolveMessageWakePolicy`, reference implementation), the shared `override.Resolve` cascade primitive (package not pinned down in the audit's evidence — locate via the import used by `messaging_reactor.go` before starting).

```yaml
requires_architect_decision: true
```

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 6 — semantic duplication and migration drift · **Dispatch unit:** `W6a`
> - **Depends on:** `10/01`
> - **Blocks:** none
> - **Parallel-safe with:** `11/06`, `11/07`, `11/09`
> - **Gated on:** AD-19 (share implementation vs. parity test)
> - **requires_security_review:** false · **requires_regression_test:** true

## Context

### Findings addressed
- `GO-SVCEXEC-004` — severity medium, confidence high. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.4; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-SVCEXEC-004`.

### Root cause

Two "resolve policy → busy-check → trigger" reactors exist in the same package, covering two related but distinct events (a new message arriving vs. a subagent completing). `resolveMessageWakePolicy` (`internal/service/messaging_reactor.go`) is the newer of the two and is built on the shared `override.Resolve` cascade primitive. `resolveSubagentCompletionPolicy` (`internal/service/subagent_reactor.go`) is the older of the two and hand-rolls the identical three-tier walk independently, re-implementing the same validate-or-warn pattern the newer function now delegates to a shared primitive. The audit's own report is explicit that the package's own comment **self-acknowledges these two functions as siblings** ("same shape... different default policy") — this is not an accidental parallel-invention case (classification 2, same semantics/stable) so much as one sibling having been migrated onto a shared mechanism after the other was written, and the older one never getting the same treatment: classification **(4) migration drift**.

### Current behavior

`docs/audits/2026-08-21-go-quality/REPORT.md` §8.4 (GO-SVCEXEC-004 entry) and §8.4's introductory catalog entry both describe the divergence at the function level; the audit's `findings.json` evidence array for this finding is empty — no file:line citation was captured beyond the two file names and two symbol names (`resolveMessageWakePolicy`, `resolveSubagentCompletionPolicy`). **Locate both functions' current line ranges via `grep -n` in `internal/service/messaging_reactor.go` and `internal/service/subagent_reactor.go` before starting** — do not assume the audit's file-level pointers are still accurate to a specific line, since this is exactly the kind of file that sees incidental churn between an audit and a remediation pass.

The report's own framing of the divergence: "a future merge/precedence bug fix is likely to land in one and be missed in the other" — the risk is not that the two functions currently disagree (no behavioral divergence was reported — this finding is explicitly not classification (3)), but that they *will* disagree the next time either one needs a correctness fix to its override-resolution logic, because there is only one canonical implementation of the cascade rule and one copy of it.

### Desired invariant

Both reactors resolve their respective override cascades through the same shared primitive (`override.Resolve` or whatever it is currently named), so a future fix to cascade-resolution semantics (precedence order, tie-breaking, warn-on-invalid behavior) only has to land once to protect both reactors.

## What to do

### Scope
- `internal/service/subagent_reactor.go` — `resolveSubagentCompletionPolicy`, migrated onto the shared primitive.
- `internal/service/messaging_reactor.go` — `resolveMessageWakePolicy`, read as the reference implementation; not modified unless the migration surfaces a genuine shared-primitive limitation that also needs fixing there.
- The shared override-cascade primitive itself — read its current signature and confirm it is general enough to serve both the message-wake and subagent-completion policy shapes without a behavior change to either. If it is not, that gap is itself part of the architect decision below, not something to route around with an ad hoc shim.

### All production callers
Both `resolveMessageWakePolicy` and `resolveSubagentCompletionPolicy` are internal package functions reached by the respective reactor's own event-handling entry point (a new message arriving; a subagent run completing). Enumerate both call sites in `internal/service` before changing `resolveSubagentCompletionPolicy`'s signature or return shape, and confirm neither caller depends on an incidental behavioral quirk of the current hand-rolled walk (e.g. an ordering guarantee the shared primitive doesn't preserve) — if one is found, that quirk needs to be either preserved in the shared primitive or explicitly called out as an intentional behavior change.

### Proposed direction

**This is the architect decision the audit itself flags (`requires_architect_decision: true` in `findings.json`).** The audit's own recommendation is unambiguous — "migrate `resolveSubagentCompletionPolicy` onto `override.Resolve` the same way its sibling was, since the two documented reasons they're separate resolvers don't require different merge mechanisms" — but the decision is flagged for architect sign-off because it touches the subagent-completion reactor's precedence semantics, which is exactly the kind of shared-primitive change the remediation guide's "fix every sibling path" principle asks to be checked carefully rather than mechanically applied. Confirm before implementing:

1. The two reactors' documented reasons for being separate resolvers (different default policy per the package comment) are preserved as *configuration* passed into the shared primitive, not as divergent code paths.
2. `resolveSubagentCompletionPolicy`'s current hand-rolled three-tier walk does not encode any subagent-completion-specific behavior that `override.Resolve` doesn't already support — trace this by hand, not by assumption, since the finding's evidence array is empty and no prior diff was captured proving equivalence.
3. If the shared primitive needs a new parameter or extension point to support the subagent-completion case, that extension is itself in scope for this task (not a separate follow-up), since a subtly-incompatible "migration" that silently changes subagent-completion behavior would itself become a new instance of the same class of bug this task is fixing.

### Non-goals
- Not a broader refactor of `messaging_reactor.go`, `subagent_reactor.go`, or `override.Resolve` beyond what the migration requires.
- Not merging the two reactors into one function — the audit explicitly treats "two separate resolvers with different default policy" as intentional and correct; only the *mechanism* each uses should converge, not the policies themselves.

## Tests required

- A regression test asserting `resolveSubagentCompletionPolicy`'s observable behavior is unchanged for the cases the existing test suite already covers (busy-check outcome, trigger/no-trigger decision, default-policy selection) before and after the migration — run the existing subagent-reactor test suite against both the pre- and post-migration implementation and confirm no behavioral delta beyond what's explicitly intended.
- A test (new or existing, confirm which) that would fail if the two reactors' cascade-resolution mechanisms diverge again in the future — e.g. a shared table-driven test exercised against both reactors' policy-resolution entry points, proving they route through the same primitive rather than two copies of similar-looking logic.

## Prevention

The remediation guide's own "Semantic Duplication" standard (§4 Wave 7): "Duplicating syntax is a maintainability concern. Duplicating a semantic rule is a correctness concern." A shared table-driven test exercising both reactors' resolution paths through the same primitive is the mechanism that makes a future re-divergence visible immediately (a broken test) rather than silently (a bug report months later).

## Verification

```bash
go build ./internal/service/...
go vet ./internal/service/...
go test ./internal/service/... -run 'Reactor|WakePolicy|CompletionPolicy' -v
```

Observable behavior required for PASS: `resolveSubagentCompletionPolicy` and `resolveMessageWakePolicy` both route through the same shared cascade primitive; existing subagent-reactor tests pass unchanged; a new or extended test demonstrates the two reactors can no longer silently diverge on cascade-resolution mechanism.

## Risk / rollback

Low-to-medium risk — the function is internal (unexported), so no external caller signature changes, but subagent-completion wake behavior is a real runtime-affecting decision path; a subtle behavioral regression here would show up as subagents not waking (or waking incorrectly) after completion. Mitigate by running the full existing subagent-reactor and messaging-reactor test suites before and after, not just the new test. Rollback is a single-file revert of `subagent_reactor.go`, since `messaging_reactor.go` is the reference and should not need to change.

## Done means

- [ ] Architect decision recorded: confirms `override.Resolve` (or equivalent) is extended, if needed, to support `resolveSubagentCompletionPolicy`'s policy shape without behavior change.
- [ ] `resolveSubagentCompletionPolicy` routes through the same shared cascade primitive `resolveMessageWakePolicy` uses.
- [ ] Existing subagent-reactor and messaging-reactor tests pass unchanged.
- [ ] New/extended test demonstrates the two reactors cannot silently re-diverge on cascade mechanism.
- [ ] `go build`, `go vet`, `go test ./internal/service/...` all pass.

## Work log

<!-- Worker fills this in: what was actually done, any deviation from plan and why, anything escalated. -->

## Review notes

<!-- Reviewer fills this in: pass/fail, what was independently re-verified. -->
