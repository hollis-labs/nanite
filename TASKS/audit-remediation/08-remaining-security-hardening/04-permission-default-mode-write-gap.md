# permission.Engine's ModeDefault may not prompt before non-destructive writes — ambiguous, needs architect call

**Phase:** Wave 3 — Remaining security hardening (guide §4; sequenced 2026-08-21 — see the sequencing block below)
**Status:** not-started
**Depends on:** none
**Touches:** `internal/permission/engine.go` (`Engine.Check`, `defaultDecision`), `internal/permission/engine_test.go`, `internal/service/tool.go` (name-heuristic classification consumed by `Check`)
**Requires architect decision:** true (matches `findings.json`)

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 3 — remaining security hardening · **Dispatch unit:** `W3`
> - **Depends on:** Wave 2 complete
> - **Blocks:** none
> - **Parallel-safe with:** `08/01`, `08/02`, `08/03`, `08/06`, `08/10`
> - **Gated on:** AD-16 (default file/directory permission policy)
> - **requires_security_review:** true · **requires_regression_test:** true

> ## ✅ AD-16 DECIDED (2026-08-22) — the comment is stale, not the code. But verify first.
>
> **`PathGrants` governs writability.** `ModeDefault` falling through to Allow
> for non-destructive, non-read-only operations is intended: writes are gated by
> `PathGrants`' session-scoped explicit-mention grants
> (`internal/permission/path_grants.go`) under a documented "no nag-again"
> philosophy. Per-call prompting would contradict that design, not complete it.
>
> **The remediation is fixing `ModeDefault`'s const comment** (`engine.go:25`),
> which promises *"prompt for destructive/write operations"* and is wrong.
> **Do not** add the `{false,false}` → Ask branch, and do not reclassify
> `dev_write`/`dev_edit` as destructive — both were considered and rejected.
>
> ### ⚠ Verify the premise before you write the comment
>
> This decision assumes `Check()` actually consults `PathGrants` on the write
> path. `Check()` (`engine.go:117-122`) reads `e.sessionGrants[sessionID]`,
> which is **not obviously the same mechanism**. Trace it first.
>
> **If `PathGrants` does not gate writes reached through `ModeDefault`, the
> premise is false — stop and re-open AD-16.** Writing a comment that describes
> a guarantee nothing provides would convert a code bug into a documentation
> lie, which is strictly worse than the stale comment you started with. Make
> this verification an explicit Done-means item with its result recorded either
> way.

## Findings addressed

- **GO-SEC4-003** (medium severity, **medium confidence** — needs architect confirmation of intent; category security + error-handling) — report §8.12.

## Context

`permission.Engine`'s `ModeDefault` doesn't actually ask before non-destructive writes, **contradicting its own doc comment** ("prompt for destructive/write operations"). Traced: `engine.go:156-180` — the name-based heuristic classifying tools splits into read-only vs. destructive pattern lists; `dev_write`/`dev_edit` match **neither** list, so both classification flags resolve `false`, and `ModeDefault`'s switch statement has **no `{false,false}` branch** — it falls through to `DecisionAllow`. The more-restrictive-sounding `ModeAcceptEdits` *does* have the missing branch (`!meta.IsReadOnly → Ask`) right next to `ModeDefault`'s switch. Since `ModeDefault` is the default engine construction, this is the harness's primary chat-loop authorization gate — a live-path finding, not a corner case.

**Untested gap:** the existing `engine_test.go` table doesn't cover `ToolMeta{}` (both flags false) under `ModeDefault` — this is the one combination missing from the table. Nobody wrote a test pinning down what *should* happen here, which itself suggests this may be an oversight rather than an intentional, known-but-untested behavior.

**The ambiguity — present directly, do not resolve it yourself:** a substantial mitigating consideration exists. `PathGrants`' own documented design philosophy is "explicit-mention auto-grant, no nag-again." It's entirely plausible the intended architecture is "`PathGrants` governs whether a path is writable at all; once a path is writable, don't prompt per-call" — which would make `ModeDefault`'s current allow-by-fallthrough behavior **correct as designed**, and the doc comment ("prompt for destructive/write operations") simply stale/imprecise language rather than a spec the code violates. The audit itself could not resolve this and reports it "as a fact (code contradicts its own doc, untested) with that ambiguity stated explicitly, not asserted as a confirmed bug." This task inherits that same posture.

**Trust classification:** not directly applicable in the guide's filesystem/network-input sense — this is an authorization-*policy*-correctness finding, not an untrusted-input-reaching-a-path finding. Noted for completeness since the folder-wide instruction asks the classification to be applied "wherever relevant"; it isn't the operative lens here.

## What to do

**Step 1 (architect decision — do not skip):** present this exact ambiguity to the architect: is `ModeDefault`'s current allow-on-`{false,false}` behavior (a) a live authorization gap that should prompt before `dev_write`/`dev_edit`-shaped tools, or (b) working-as-intended because `PathGrants` already gates writability and `ModeDefault` is deliberately not meant to nag again? The architect's answer determines everything downstream.

**Step 2a (if real gap):** either (i) reclassify `dev_write`/`dev_edit` into the destructive name-heuristic pattern list, or (ii) add the missing `{false,false} → Ask` branch directly to `ModeDefault`'s switch (mirroring `ModeAcceptEdits`'s existing adjacent branch). Implementer's choice between these two — whichever is more consistent with how the rest of the heuristic table is organized.

**Step 2b (if stale-comment, not a gap):** correct `engine.go`'s doc comment to accurately describe the `PathGrants`-governs-writability design, so a future reader doesn't rediscover this same false alarm.

**Either way — add the missing test case:** add a `ToolMeta{}` (or equivalently `dev_write`/`dev_edit`-shaped) case under `ModeDefault` to `engine_test.go`'s table, asserting whichever behavior the architect confirms is correct. This is the concrete artifact that closes the coverage gap and prevents the ambiguity from recurring silently, regardless of which direction is chosen.

**All production callers:** `Engine.Check` is the harness's primary chat-loop authorization gate. Confirm — do not assume — whether any other call site constructs an `Engine` with `ModeDefault` and would be affected identically; the audit names `internal/service/tool.go` as a related touched file for the heuristic classification but did not enumerate every construction site.

## Non-goals

Do not redesign the mode system (`ModeDefault`/`ModeAcceptEdits`/etc.) or `PathGrants`' broader architecture as part of this task. Scope is the one missing branch/classification plus its test.

## Tests required

The `{false,false}`-under-`ModeDefault` test case in `engine_test.go` — **mandatory regardless of which direction the architect picks**. This is the specific regression that proves the ambiguity was actually resolved, not just discussed.

## Prevention

The audit's own lint-triage funnel (§3.1, cluster #6) names `exhaustive` (missing switch case over enum) as an existing, real, worklist-shaped analyzer class already in use elsewhere in the repo. Check whether pointing it at `ModeDefault`'s switch would catch a missing case mechanically going forward — worth adding as a low-cost prevention measure if not already scoped there.

## Verification

`go test ./internal/permission/...`; `go vet ./internal/permission/...`; if `exhaustive` is extended to this switch, `golangci-lint run` on the changed file.

## Risk / rollback

If Step 2a is chosen (real gap), the observable behavior change is that `dev_write`/`dev_edit` now prompt under `ModeDefault` where they previously didn't — a real UX change for any interactive session on `ModeDefault` today. Flag this explicitly to the architect and reviewers as a **behavior change**, not just a bugfix, since some users may have grown to depend on the current no-prompt behavior. If Step 2b is chosen (stale comment), the change is purely documentation — negligible risk.

## Done means

- [ ] Architect decision obtained and recorded in Work Log (real gap vs. stale comment)
- [ ] Corresponding code or doc-comment change landed
- [ ] `{false,false}`-under-`ModeDefault` test case added to `engine_test.go` and passing
- [ ] All production `ModeDefault`-construction call sites confirmed unaffected or updated consistently

## Work log

<!-- Worker fills this in. -->

## Review notes

<!-- Reviewer fills this in. -->
