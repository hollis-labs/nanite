# `internal/mcp/elicitation.go` — unify near-duplicate client elicitation paths, and resolve the dead-code/stale-doc gap

**Phase:** Wave 6 — Semantic duplication / migration drift
**Status:** not-started
**Depends on:** none
**Touches:** `internal/mcp/elicitation.go` (`ElicitUserInput`, `routeClientElicitation`, `parseElicitationCreate`, package doc comment, `ClientElicitMiddleware` — the described-but-nonexistent type).

```yaml
requires_architect_decision: true
```

## Context

### Findings addressed
- `GO-CHAT-004` — severity low, confidence **medium**. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.9; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-CHAT-004`.
- `GO-MCPTOOL-004` — severity low, confidence **high**. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.7; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-MCPTOOL-004`.

Both findings are bundled into this one task because both live in the same file (`internal/mcp/elicitation.go`) and both touch the same subsystem (client-side elicitation) — but they are **two different kinds of problem** with two different fixes, and this task keeps them as clearly separated sub-sections rather than conflating them.

### GO-CHAT-004 — near-duplicate bodies with inconsistent cancellation handling

**Root cause:** `ElicitUserInput` and `routeClientElicitation` (both in `internal/mcp/elicitation.go`) are near-duplicate ~25-line function bodies with **inconsistent `context.Canceled` handling**: one swallows a cancellation into a graceful cancel result; the other surfaces it as **both** a cancel payload **and** a non-nil error — a genuinely different contract for the same underlying event. This is classification **(4) migration drift** — the two paths should plausibly have stayed in sync (they implement the same "call Elicit, translate to response" logic) but didn't, most likely because one was updated at some point (e.g. to add the non-nil error) without the same fix being applied to its sibling.

**Current behavior:** the audit's evidence array for this finding is empty; locate both functions via `grep -n 'func ElicitUserInput\|func routeClientElicitation' internal/mcp/elicitation.go` before starting, and read both bodies in full, specifically each one's `context.Canceled` branch, before touching either.

**Why this needs an architect decision, not just a mechanical fix:** the audit explicitly notes "downstream consumer of the second path's error wasn't traced — unclear if the inconsistency is currently user-visible." Before extracting a shared helper (which would force both call sites onto one contract), it must be established whether the two call sites' *callers* actually expect different contracts for a legitimate reason (e.g. one caller checks the returned error and needs it to distinguish cancellation from other failures; the other caller only inspects the payload and would break if a non-nil error suddenly appeared where none did before). This is exactly the kind of "two callers with a plausible but unconfirmed reason to differ" situation this batch's classification scheme exists to force a decision on, rather than assume.

### GO-MCPTOOL-004 — aspirational package doc describing a nonexistent direction, feeding confirmed dead code

**Root cause:** `elicitation.go`'s package doc describes an entire **client-side direction** (a type named `ClientElicitMiddleware`) that **does not exist anywhere in the codebase** — purely aspirational documentation, never implemented. The functions that appear to implement the direction the doc describes (`parseElicitationCreate`, `routeClientElicitation` — note `routeClientElicitation` appears in **both** findings, since it is both one half of GO-CHAT-004's near-duplicate pair **and** confirmed dead per GO-MCPTOOL-004) are confirmed dead under both normal and `-test` reachability analysis. Server-side elicitation (the other half of the file) is confirmed live and not affected by this finding. This is **not really a duplication case** in this folder's usual sense — it's dead-code-plus-stale-doc — but it is grouped here because it shares the same file/subsystem as GO-CHAT-004, and because `routeClientElicitation` is a shared symbol between the two findings that must be resolved coherently, not independently (see "Interaction between the two findings" below).

**Current behavior:** the package doc comment at the top of `internal/mcp/elicitation.go` (read it in full before starting — its exact wording matters for judging what "the described direction" actually promises) describes `ClientElicitMiddleware`. `grep -rn 'ClientElicitMiddleware' --include='*.go' .` across the whole tree should confirm the audit's claim that it doesn't exist anywhere; run this yourself rather than trusting the audit's own claim uncritically, since "confirmed dead" claims are exactly the kind of assertion the remediation guide's evidence rules ask to be re-validated against current source, not old audit text.

### Interaction between the two findings — read before implementing either

`routeClientElicitation` is central to both findings: GO-CHAT-004 treats it as one half of a near-duplicate pair that should probably be unified with `ElicitUserInput`; GO-MCPTOOL-004 treats it as dead code implementing an aspirational, never-built direction. **These two framings are not necessarily contradictory** — a function can be both "dead in production today" and "a near-duplicate of a live function's logic" if it's reachable only from tests or from the not-yet-built `ClientElicitMiddleware` direction the doc describes. Resolve GO-MCPTOOL-004's wire/delete decision **first**, since it determines what's left to actually unify under GO-CHAT-004:

- If the architect chooses to **delete** the dead client-side functions (see GO-MCPTOOL-004's proposed direction below) and `routeClientElicitation` is among them, GO-CHAT-004's "unify with `ElicitUserInput`" question becomes moot — there is nothing left to unify, and this task's GO-CHAT-004 half is closed by GO-MCPTOOL-004's cleanup.
- If the architect chooses to **build** the missing `ClientElicitMiddleware` direction instead, `routeClientElicitation` becomes a real, live call site, and GO-CHAT-004's cancellation-handling inconsistency becomes a real, user-facing bug that must be resolved as part of wiring the new direction up, not deferred.

## What to do

### Scope
- `internal/mcp/elicitation.go` — `ElicitUserInput`, `routeClientElicitation`, `parseElicitationCreate`, the package doc comment, and any other symbol implementing the `ClientElicitMiddleware`-described direction (enumerate via the dead-code check below).

### Proposed direction — do not resolve without architect sign-off

**GO-MCPTOOL-004's decision, resolve first:**

**Option A — delete.** Remove `parseElicitationCreate`, `routeClientElicitation`, and any other function confirmed dead under both normal and `-test` reachability, and correct the package doc to remove the `ClientElicitMiddleware` description entirely. This is the cheaper, lower-risk option and is appropriate if there is no near-term plan to build the described client-side middleware.

**Option B — build.** Implement the missing `ClientElicitMiddleware` the doc describes, wiring `routeClientElicitation` (and whatever else the doc's description implies) into the live call path. This is a real feature-build, not a cleanup task, and should only be chosen if there is an actual, current intent to ship this direction — confirm this with the architect rather than assuming; per the remediation guide's own "production reachability is part of done" principle, a feature isn't done until it's wired into a real production entry point, and choosing to build it here means committing to finishing that wiring, not just making the dead functions reachable by a test.

**GO-CHAT-004's decision, resolve second, informed by the above:**

If Option A (delete) is chosen for GO-MCPTOOL-004 and `routeClientElicitation` is among the deleted functions, GO-CHAT-004 is closed as a side effect — record this explicitly in Work log rather than leaving it looking unaddressed.

If Option B (build) is chosen, or if `routeClientElicitation` survives deletion for some other reason (confirm which functions are actually dead before assuming all of them are), extract the shared "call `Elicit`, translate to response" logic from `ElicitUserInput` and `routeClientElicitation` into one helper both call, with a single, explicit, deliberately-chosen `context.Canceled` contract — not two different ones. If the two callers genuinely need different contracts (per the untraced-downstream-consumer question above), document why in-code rather than leaving the difference implicit and inconsistent-looking.

### Non-goals
- Not a broader elicitation-subsystem redesign — server-side elicitation is confirmed live and healthy and is out of scope here.
- Not building `ClientElicitMiddleware` unless the architect explicitly chooses Option B — do not treat "the doc describes it" as license to build it opportunistically.

## Tests required

- If GO-MCPTOOL-004 resolves to Option A (delete): confirm via `deadcode` (or the equivalent tool the audit used) that the deleted functions leave no dangling references, and that the package doc no longer describes a nonexistent type. A `go build ./...` and `go vet ./...` pass is the direct proof.
- If GO-MCPTOOL-004 resolves to Option B (build): new tests covering the newly-live `ClientElicitMiddleware` wiring, proportionate to a real feature addition, not a cleanup — treat this the same as any other new-feature task's test bar.
- For GO-CHAT-004 (if still relevant after the above): a test asserting `context.Canceled` is now handled identically (whatever the single chosen contract is) by both call sites, so a future re-divergence is caught.

## Prevention

The remediation guide's "Semantic Duplication" standard for GO-CHAT-004: a shared helper with one explicit cancellation contract prevents this specific divergence from recurring. For GO-MCPTOOL-004: whichever option is chosen, the package doc should accurately describe only what actually exists in the codebase — a stale, aspirational doc is exactly the guide's §27 "trust comments over executable behavior" trap, and correcting it (or making it true) closes that gap.

## Verification

```bash
go build ./internal/mcp/...
go vet ./internal/mcp/...
go test ./internal/mcp/... -run 'Elicit' -v
grep -rn 'ClientElicitMiddleware' --include='*.go' .
```

Observable behavior required for PASS: `go build`/`go vet`/`go test` all pass; the package doc's description of `ClientElicitMiddleware` matches what actually exists in the codebase (either removed, or built and true); `ElicitUserInput` and (if it survives) `routeClientElicitation` have one consistent, documented `context.Canceled` contract.

## Risk / rollback

Low risk for Option A (deleting confirmed-dead code, re-verified via `deadcode` before deletion). Medium risk/scope for Option B (a real feature build, with its own regression surface proportionate to whatever `ClientElicitMiddleware` turns out to need to do). GO-CHAT-004's unification (if still applicable) carries the same low-to-medium risk as this batch's other "extract a shared helper from two near-duplicate call sites" tasks — the main risk is silently changing one caller's error contract in a way its actual consumer doesn't expect, mitigated by tracing the downstream consumer before changing behavior. Rollback is a single-file revert of `internal/mcp/elicitation.go` in all cases.

## Done means

- [ ] `ClientElicitMiddleware`'s existence (or non-existence) independently re-confirmed via `deadcode`/grep before deciding.
- [ ] Architect decision recorded for GO-MCPTOOL-004: delete or build.
- [ ] Chosen direction implemented; package doc accurately describes what exists.
- [ ] GO-CHAT-004 resolved: either closed as a side effect of deletion, or `ElicitUserInput`/`routeClientElicitation` unified onto one explicit `context.Canceled` contract.
- [ ] `go build`, `go vet`, `go test ./internal/mcp/...` all pass.

## Work log

<!-- Worker fills this in: what was actually done, any deviation from plan and why, anything escalated. Record explicitly whether GO-CHAT-004 was closed as a side effect of GO-MCPTOOL-004's resolution. -->

## Review notes

<!-- Reviewer fills this in: pass/fail, what was independently re-verified. -->
