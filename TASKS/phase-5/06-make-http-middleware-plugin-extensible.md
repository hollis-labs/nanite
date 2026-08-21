# Make the HTTP middleware chain plugin-extensible

**Phase:** 5
**Status:** superseded — see `TASKS/plugin-system/07-make-http-middleware-plugin-extensible.md`
**Depends on:** none

## ⚠️ SUPERSEDED, 2026-08-21 — do not dispatch this file

The open design question this file was blocked on (where plugin-contributed middleware may
legally sit, one fixed slot vs. declared priority, install-time validation shape) was settled
by the operator during the `TASKS/plugin-system` planning pass: **builtins only,
priority-ordered**, mirroring the filter chain's existing priority pattern
(`docs/engineering/architecture/09-plugin-system.md`'s "Middleware" section). The mostly-moot
original security concern (a less-trusted plugin affecting the auth-wrapping chain) dissolves
under that decision — builtins are already full-trust, same tier as core code. Implementation
is tracked at `TASKS/plugin-system/07-make-http-middleware-plugin-extensible.md`, which
supersedes this file in full. Left in place for historical record of the original escalation;
do not dispatch this file as a separate task.

## ⚠️ DESIGN NOT YET SETTLED (historical — see supersede notice above) — read the escalation entry in `TASKS/ESCALATIONS.md` before dispatching this task as a mechanical build

**This item is flagged in this planning pass as a genuine design-decision escalation candidate, not a locked shape ready for mechanical implementation.** The design review that produced `TASKS.md` ended this specific item with "take a look and let me know," per the Planner's own kickoff instructions — same posture as Phase 3's `dispatch_to_agent` action kind, but with a real security dimension `dispatch_to_agent` doesn't have. Do not dispatch this task expecting a worker to just "add a `registers.middleware[]` field and splice it in" — the ordering constraints below are security-sensitive and a wrong default here is the kind of thing this project's escalation discipline exists to catch before it ships, not after. Get real operator input on the open questions in **What to do**, step 1, before implementation begins.

**Touches:** `internal/server/server.go:139-156,179-184` (the middleware chain — **two identical construction sites, not one**), `internal/plugin/config.go` (a new `registers.middleware[]` manifest field, shape TBD by the design decision below)

## Context

TASKS.md Phase 5: *"Make the HTTP middleware chain plugin-extensible."* Architecture doc `09-plugin-system.md`: *"Middleware exists today, but only at the HTTP layer, and it's not plugin-extensible... Decision: don't build a second, general 'middleware' concept. The turn-loop's `Filter*`/`Emit*` system already functionally serves that role for the chat-turn domain... Instead: make the existing HTTP middleware chain plugin-extensible — the one place true middleware exists today but has zero plugin reach."*

### The chain, exact current state — hardcoded, not composable, and duplicated

`internal/server/server.go:139-156` (and duplicated again at `:179-184` for a second server instance — **two identical middleware-chain construction sites to keep in sync, not one**):

```
recoverMiddleware -> loggingMiddleware -> corsMiddleware -> basicAuthMiddleware -> callerIdentityMiddleware -> bodyLimitMiddleware -> mux
```

This is hardcoded nested function calls, not a slice/composable chain builder — there is no data structure today to insert a plugin middleware into.

### Real, documented, security-sensitive ordering constraints — the actual reason this isn't mechanical

From the code's own comments (`server.go:142-148`): **CORS must sit *outside* auth** (unauthenticated preflight OPTIONS must succeed); **body-limit must sit *inside* auth** (don't spend the size cap on traffic that's about to be rejected anyway); **caller-identity must sit *between* auth and body-limit** (only authenticated requests get an identity stamped on context). This is not incidental ordering — each position has a stated, deliberate reason. A plugin middleware inserted at the wrong point could, for example, run before auth and see unauthenticated request bodies it shouldn't, or interfere with the CORS preflight path.

## What to do

1. **Before any implementation**: get explicit operator input on the open design questions — where can plugin middleware legally sit (most likely answer: only *inside* `callerIdentityMiddleware`, i.e. strictly post-auth, so a plugin can never affect the CORS/auth security boundary — but this is a real decision, not a default to assume silently); does a plugin get exactly one fixed insertion point, or a declared priority/ordering relative to other plugins' middleware; does `registers.middleware[]` need its own install-time validation (per the pattern every other `registers.*` field already has) given the security stakes are higher here than for routes/panels/CRUD. Log this as a real escalation in `TASKS/ESCALATIONS.md` if not already resolved by the time this task is dispatched — don't let a worker default-guess a security-boundary shape.
2. Once the insertion-point/ordering design is settled: refactor `server.go`'s hardcoded chain into a composable structure (a slice of `func(http.Handler) http.Handler` or equivalent) that preserves the exact current fixed ordering for the six built-in middlewares, with a well-defined, restricted slot for plugin-contributed middleware per the settled design.
3. Add `registers.middleware[]` to the plugin manifest schema, with install-time validation matching the pattern every other `registers.*` field uses.
4. Fix the duplication at `server.go:179-184` as part of this refactor — both server-instance constructions should share one chain-building function, not maintain two hand-copies that can drift.
5. Build a real test plugin demonstrating a middleware contribution, verifying it cannot affect the CORS/auth/body-limit ordering guarantees.

## Done means

- The operator's design decision on insertion point/ordering/validation is recorded (in this file's Work Log and cross-referenced in `TASKS/ESCALATIONS.md`) before implementation, not inferred by a worker.
- A plugin can register middleware that runs at the settled insertion point, verified with a real test plugin.
- The six built-in middlewares' relative ordering and security guarantees (CORS-outside-auth, body-limit-inside-auth, caller-identity-between) are unchanged and verified by a real test asserting the ordering.
- The two duplicated construction sites (`server.go:139-156` and `:179-184`) are unified into one shared chain-builder.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
