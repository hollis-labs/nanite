# Make the HTTP middleware chain plugin-extensible (builtins only, priority-ordered)

**Phase:** 4 — Middleware plugin-extensibility (`TASKS/plugin-system`)
**Status:** not-started
**Depends on:** none directly. Coordinate with `04` — both add a new field to
`internal/plugin/config.go`'s manifest structs (different fields: `04`'s `capabilities:`,
this task's `registers.middleware[]`); sequence merges, don't run as truly concurrent commits
against the same file.
**Touches:** `internal/server/server.go` (the middleware chain — **two identical construction
sites**, `:152-162` and `:179-190`), `internal/plugin/config.go` (`ManifestRegisters`, a new
`Middleware []MiddlewareRegistration`-shaped field), `internal/plugin/host.go` (a new
`RegisterMiddleware` method, gated to builtins only), `internal/plugin/filter.go`
(reference-only — the priority-ordering pattern this task's registration mechanism follows).

## Context

**This task supersedes `TASKS/phase-5/06-make-http-middleware-plugin-extensible.md`**, which
was drafted, escalated (`TASKS/ESCALATIONS.md`, 2026-08-18 entry), and explicitly **skipped**
for Phase 5 pending a real operator decision on where plugin-contributed middleware may
legally sit relative to the chain's existing security-ordered constraints. That decision is
now settled: `docs/engineering/architecture/09-plugin-system.md`'s "Middleware" section —
**"Shape decision: builtins only, priority-ordered. This chain wraps every request including
auth, so it follows the capability model's trust split... only Tier 1 (builtin) plugins can
register into it, mirroring the filter chain's priority-ordered registration pattern.
Subprocess plugins keep using manifest-declared routes only; no chain injection for Tier 2,
ever — auth-wrapping middleware is too high-stakes a surface to extend to less-trusted code
via a capability grant."** Mark `phase-5/06` as superseded by this task (update its own status
line and `TASKS/INDEX.md`'s Phase 5 row) rather than leaving two open task files describing
the same unresolved work.

This planning session's own research independently re-confirmed the chain and the precedent
pattern against current code (not the escalation entry's possibly-drifted line numbers):

- **Current chain, exact order, both construction sites** — `internal/server/server.go:152-162`
  (`ListenAndServe`) and `:179-190` (`newHTTPServer`, the test-harness twin): outer to inner,
  `recoverMiddleware` (`server.go:374`) → `loggingMiddleware` (`server.go:358`) →
  `corsMiddleware` (`server.go:241`) → `basicAuthMiddleware` (`internal/server/auth.go:15`) →
  `callerIdentityMiddleware` (`internal/server/caller_identity.go:47`) →
  `bodyLimitMiddleware` (`server.go:325`) → `s.mux`. Independently confirmed matching
  `internal/server/server_test.go:58-61`'s own construction. (The escalation entry's cited
  line numbers, `139-156`/`179-184`, have drifted slightly from small unrelated edits since
  2026-08-18 — the order and the two-site duplication are both still exactly as it described.)
- **Why each position matters, from the code's own comments**: CORS must sit *outside* auth
  (unauthenticated preflight `OPTIONS` must succeed — browsers can't attach credentials to a
  preflight request); body-limit must sit *inside* auth (don't spend the size cap on traffic
  about to be rejected, and don't let unauthenticated large-body traffic consume resources
  pre-rejection); caller-identity must sit strictly between auth and body-limit (only
  authenticated requests get an identity stamped on context, per the `server.go:147-150`
  comment's own G-6.3 header-contract reference).
- **The filter-chain precedent this task mirrors, confirmed working, including cleanup**:
  `internal/plugin/filter.go`'s `filterEntry{PluginID, Priority, View, Fn}` (`filter.go:76-81`,
  "lower priority values execute earlier"), `RegisterWithView` re-sorts with
  `sort.SliceStable` on every registration (`filter.go:108-128`), `Apply` runs the sorted
  chain feeding each handler's output to the next (`filter.go:134-168`, each call wrapped in
  `safego.Call` for panic recovery), and `RemoveByPlugin(pluginID string) int`
  (`filter.go:179-196`) is a real, working per-plugin cleanup mechanism — confirmed still
  present and correct. Model the new middleware registration on this exact shape (priority
  field, stable sort, `RemoveByPlugin`-equivalent cleanup on unload), not a new pattern.
- **No existing hook into the chain today.** Grep of `internal/plugin/*.go` for
  "[Mm]iddleware" returns zero hits — `RegisterHTTPHandler`/`RegisterCRUDHandler` both funnel
  through `registerRoute`/`installForwarderLocked` (`host.go:284-328`) into a *leaf* handler
  mounted on `s.mux` (via `MutablePluginMux`) — this runs **inside** the entire existing chain
  (the chain wraps `s.mux`, per `server.go:157`/`185`) and cannot see or affect requests to any
  other route or wrap/observe/short-circuit the chain itself. A genuinely new registration
  path is needed; there's nothing to extend.
- **`plugins.kind` is readable at exactly the point a builtins-only gate needs it.**
  `LoadDiscovered` computes `kind` from `dp.IsSubprocess()` (`loader.go:171-174`,
  `DiscoveredPlugin.IsSubprocess()` at `loader.go:35-37`, reading `PluginManifest.Runtime`,
  `config.go:95`) *before* calling `host.LoadPlugin`/`applyManifestRegistrations`, and
  `applyManifestRegistrations` itself receives the full `*PluginManifest` — `manifest.Runtime`
  is directly readable at the registration call site. Caveat confirmed by the research: `Host`
  itself only tracks `activePlugin` (an ID string) during the synchronous `Load()` window, not
  the calling plugin's *kind* — so the builtins-only gate must be enforced either before
  `LoadPlugin`/`applyManifestRegistrations` is invoked (where `manifest.Runtime` is available),
  or `Host` needs a kind value threaded alongside `activePlugin`. Choose whichever is simpler
  given `01`'s pre-flight-pass work, which already needs to read manifest state before spawn —
  there may be a natural shared seam; check `01`'s landed shape before duplicating logic.

## What to do

1. Refactor `server.go`'s hardcoded nested-call chain into a composable structure (a slice of
   `func(http.Handler) http.Handler` or equivalent) preserving the exact current fixed
   ordering of the six built-in middlewares, with a well-defined insertion point for
   plugin-contributed middleware. Recommended insertion point, following the reasoning that
   dissolved most of the original escalation's stakes (builtins are full-trust, same tier as
   core code — this is not a less-trusted-code boundary question anymore): **inside
   `callerIdentityMiddleware`, i.e. strictly post-auth** — a plugin middleware never sees
   unauthenticated traffic or interferes with the CORS preflight path, consistent with the
   architecture doc's own framing that this chain "wraps every request including auth." If
   implementation surfaces a reason to place it differently, that's a real, worth-noting
   design call — record it in the Work Log with the reasoning, don't silently pick a different
   spot without saying why.
2. Fix the two-construction-site duplication (`server.go:152-162` and `:179-190`) as part of
   this refactor — both server-instance constructions must share one chain-building function,
   not two hand-copies that can drift, per the original escalation's own flagged risk.
3. Add `registers.middleware[]` to `ManifestRegisters` (`internal/plugin/config.go`) and the
   embedded JSON Schema. Add a new `Host.RegisterMiddleware(priority int, fn MiddlewareFunc)`
   method (naming to match existing `Register*` conventions), gated to builtins only — reject
   (with a clear error, not a silent no-op) if called during a subprocess plugin's
   registration. Priority-ordered, stable-sorted, following `filter.go`'s exact pattern
   (`filterEntry`/`RegisterWithView`/`RemoveByPlugin` shape) — including a
   `RemoveMiddlewareByPlugin`-equivalent cleanup on unload, so this doesn't reintroduce
   finding 06's stale-hook problem for a new registration category.
4. Update `TASKS/phase-5/06-make-http-middleware-plugin-extensible.md`'s own status/banner to
   point at this task as its resolution, and update `TASKS/INDEX.md`'s Phase 5 row for `06`
   accordingly — don't leave two files describing the same unresolved work once this lands.
5. Build a real test plugin demonstrating a middleware contribution, verifying it runs at the
   chosen insertion point and cannot affect the CORS/auth/body-limit ordering guarantees
   (a request through the modified chain still exhibits: unauthenticated CORS preflight
   succeeds; unauthenticated non-preflight requests are still rejected by auth before reaching
   any plugin middleware; the body-size cap still applies before a plugin middleware that
   might read the body).

## Done means

- The operator's settled shape (builtins only, priority-ordered) is implemented exactly — a
  subprocess plugin attempting to register middleware is rejected with a clear error, verified
  by a negative test.
- A builtin plugin can register middleware that runs at the chosen insertion point, verified
  with a real test plugin.
- The six built-in middlewares' relative ordering and security guarantees (CORS-outside-auth,
  body-limit-inside-auth, caller-identity-between) are unchanged and verified by a real test
  asserting the ordering — same requirement `phase-5/06` originally specified, now actually met.
- The two duplicated construction sites are unified into one shared chain-builder.
- Unloading a builtin plugin (if ever exercised — builtins are typically not unloaded at
  runtime, but the mechanism should be correct regardless) removes its middleware contribution
  cleanly, matching the filter-chain precedent.
- `phase-5/06` and `TASKS/INDEX.md` are updated to point at this task as the resolution, not
  left as a stale open item.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
