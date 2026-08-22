# Consider a single shared holder for the `*envelopes.Registry` pointer instead of 3 independent copies

**Phase:** Wave 6 — Semantic duplication / migration drift
**Status:** not-started
**Depends on:** none
**Touches:** `internal/chat/envelope.go`, `internal/envelope/validator.go`, `internal/plugin/host.go`, and `cmd/nanite/main.go` (the 3 manual setter calls).

`requires_architect_decision: false` — low-risk, low-priority, no urgent behavioral concern today; recorded for architect awareness rather than requiring sign-off before proceeding, but see "Risk / rollback" for why this is still worth deliberate handling rather than a purely mechanical pass.

## Context

### Findings addressed
- `GO-CHAT-001` — severity low, confidence high. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.9; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-CHAT-001`.

### Root cause

Three independent packages — `internal/chat`, `internal/envelope`, and `internal/plugin` (specifically `host.go`) — each hold their **own separate copy** of the shared `*envelopes.Registry` pointer. `main.go` has to remember to make **3 manual setter calls**, one per package, to keep all three copies pointing at the same registry instance. Nothing enforces they stay in sync — if a future code path constructs any of the three holders without going through the same `main.go` wiring sequence (a test harness, a new composition root, a future hot-reload path), that holder's copy could silently diverge from the other two, or be left `nil`.

This is classification **(1) textual-only boilerplate / plumbing duplication** — each of the three holders serves a genuinely distinct real consumer (this is confirmed **not** dead duplication; each package has its own legitimate reason to need access to the registry), so the fix is not "delete two of the three," it's "stop requiring three independently-set copies of the same pointer to begin with."

### Current behavior

The audit's evidence array for this finding is empty; no specific file:line citations were captured beyond the three file names and the general shape ("main.go's 3 manual setter calls"). **Locate the registry field/setter in each of the three files, and the three corresponding setter calls in `cmd/nanite/main.go`, via `grep -n` before starting** — search for `envelopes.Registry` and the setter method names across all three packages and `main.go`.

### Desired invariant

There is exactly one place a `*envelopes.Registry` pointer is stored at composition-root time; `internal/chat`, `internal/envelope`, and `internal/plugin.Host` each obtain their reference to it through a mechanism that cannot silently diverge (either they all read from the same shared holder, or their individual copies are set through one shared setter call rather than three independent ones).

## What to do

### Scope
- `internal/chat/envelope.go` — its own copy of the registry pointer and setter.
- `internal/envelope/validator.go` — its own copy and setter.
- `internal/plugin/host.go` — its own copy and setter.
- `cmd/nanite/main.go` — the 3 manual setter calls at the composition root.

### All production callers
The audit confirms this is currently **low risk** specifically because there is only one composition-root call site (`main.go`) and no reload path exists yet — enumerate whether any test harness or alternate entry point constructs any of the three holders independently of `main.go`'s wiring sequence (a likely place for silent divergence to already exist in test code, even if not in production). If found, note this explicitly, since it would mean the "low risk today" framing needs revisiting.

### Proposed direction

Per the audit's own recommendation: "Consider a single shared registry holder rather than three independently-set copies, especially before any future hot-reload/test-isolation feature is added." Two viable mechanical approaches, either is acceptable — pick whichever fits the existing dependency-injection style in this codebase best (check how other genuinely-shared, composition-root-provided values are threaded through `internal/chat`/`internal/envelope`/`internal/plugin` today, and follow that convention rather than introducing a new one):

1. **One shared holder type**, constructed once in `main.go` and passed by reference into all three packages' constructors, replacing each package's own field with a reference to the shared holder.
2. **One shared setter function** (rather than three independent ones) that `main.go` calls once, which internally propagates the pointer to all three packages — a smaller change if the three packages' internal storage can't easily be unified into one holder type without a larger refactor.

Either way, the goal is that `main.go` makes **one** call (or constructs **one** shared object), not three independent calls that each have to be remembered and kept in sync by a human.

### Non-goals
- Not building the hot-reload or test-isolation feature this finding's risk framing anticipates — this task only removes the *shape* of the bug that feature would otherwise trip over; it does not implement that feature.
- Not touching `*envelopes.Registry`'s own internal implementation — this task is about how the pointer to it is distributed, not the registry's own behavior.

## Tests required

- Existing tests for `internal/chat`, `internal/envelope`, and `internal/plugin.Host`'s envelope-registry-dependent behavior must pass unchanged after the consolidation.
- A test (new or adapted from existing composition-root tests, if any exist) confirming all three packages observe the same registry instance after `main.go`'s wiring completes — this is the direct regression guard against the "three independently-set copies can silently diverge" risk this finding names.

## Prevention

A single shared holder is self-enforcing by construction — there is no longer a "remember to call setter #3" step to forget. This directly addresses the audit's own framing of the risk ("shaped like a bug waiting for a future hot-reload/test-isolation feature") before that feature is ever built, which is exactly the guide's "remediation includes prevention" principle applied proactively rather than reactively.

## Verification

```bash
go build ./...
go vet ./internal/chat/... ./internal/envelope/... ./internal/plugin/...
go test ./internal/chat/... ./internal/envelope/... ./internal/plugin/... -run 'Envelope|Registry' -v
```

Observable behavior required for PASS: `main.go` makes one wiring call (or constructs one shared holder) instead of three independent setter calls; all three packages' existing envelope-registry-dependent tests pass unchanged; a new or adapted test confirms all three observe the same registry instance.

## Risk / rollback

Low risk today per the audit's own assessment (one composition-root call site, no reload path yet) — but the fix itself touches three packages' internal storage plus the composition root, so review carefully for any package that reads its own copy of the registry pointer in a way that assumes it is a private field rather than a shared reference (e.g. if any package currently mutates its own copy independently, which would be a bug in its own right if the three are supposed to stay in sync). Rollback is a revert across the touched files; no data or schema is involved.

## Done means

- [ ] All current call sites obtaining the registry pointer in each of the three packages enumerated.
- [ ] Single shared holder or single shared setter mechanism implemented, replacing the three independent copies.
- [ ] `main.go` wires the registry through one call/construction instead of three.
- [ ] Existing tests for all three packages pass unchanged.
- [ ] New/adapted test confirms all three packages observe the same registry instance.
- [ ] `go build ./...`, `go vet`, and the targeted `go test` runs above all pass.

## Work log

<!-- Worker fills this in. -->

## Review notes

<!-- Reviewer fills this in. -->
