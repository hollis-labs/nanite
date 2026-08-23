# REVIEW NOTE (not a work task) — `Container` and `internal/store`: do not schedule a god-object refactor

**Phase:** Audit remediation — Wave 5 (architectural concentration)
**Status:** implemented
**Depends on:** none
**Touches:** nothing. This file recommends **no code changes** to `internal/service/container.go`, `internal/store/*.go`, or `internal/plugin/host.go`. It exists purely so the underlying findings have a documented disposition instead of silently disappearing from tracking.
**requires_architect_decision:** true — **satisfied by the operator on 2026-08-23**. The remediation guide requires every finding to reach an explicit disposition (§4 Wave 5, §7 output C), and "confirmed, no action needed" is itself a disposition an architect/operator must actually make, not one a planning pass can assume.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 5 — architectural concentration · **Dispatch unit:** `W5`
> - **Depends on:** none
> - **Blocks:** none
> - **Parallel-safe with:** anything — this task writes no code
> - **Gated on:** AD-14
> - **requires_security_review:** false · **requires_regression_test:** false

## Context

This file is deliberately **not shaped like a work task**. Files `01` and `02` in this same folder (`01-chatserviceimpl-generateresponse-decomposition.md`, `02-selftoolstransport-decomposition.md`) are real decomposition-planning work with concrete deliverables (a responsibility map, a phase-boundary proposal, characterization tests). This file is the opposite: a **documented decision not to do a blanket category of work**, covering five findings whose own audit evidence and the remediation guide's explicit instructions say metric-driven decomposition would be the wrong call. It exists in this folder — rather than being silently dropped — specifically because this batch's own README and `FINDING-INDEX.md` require every one of the audit's 113 findings to map to exactly one task file with a real disposition; a god-object-shaped finding that turns out not to warrant blanket action still needs a place to live where a future reader can see it was actually reviewed, not missed.

### Findings addressed

- **GO-DEP-001** (informational, god-object/architecture, confidence high) — `Container` (`internal/service/container.go`): the audit baseline was a 60-field composition root, 31.6K production LOC across 83 files in the surrounding package, fan-out 64. The 2026-08-23 recheck finds **64 named fields** (58 exported composition/configuration fields plus 6 unexported lifecycle fields), **4 production receiver methods** (2 exported plus 2 private shutdown helpers), and 33,055 production LOC across 85 files in `internal/service`; the package has 66 direct imports of other packages in this module. The structural conclusion is unchanged: `Container` is wiring/lifecycle orchestration, while the package's concentrated behavior lives in `chatServiceImpl` (see `GO-SVCEXEC-002`, covered in `01-chatserviceimpl-generateresponse-decomposition.md`).
- **GO-DEP-002** (medium, dependency/architecture, confidence high) — `internal/store`: the audit baseline fan-in was 30. The 2026-08-23 recheck finds **32 production package directories importing the exact root `internal/store` package**. The dominant caller pattern remains injection of the whole concrete `*store.Store`: **61 textual occurrences across 25 non-test files in `internal/service`**. Consumer-defined narrowing is now more widespread than the audit note recorded (for example `dispatch.TrustResolver`, the domain slices in `internal/service/store.go`, `selftools/reactions.Store`, and `scheduler.ScheduleRunStore`); the cited `grounding.ConsultationLogger` no longer exists because Wave 4 retired `internal/grounding`.
- **GO-STORE-001** (medium, package-cohesion/dependency/architecture, confidence high) — nearly all callers depend on the concrete `*store.Store` handle rather than narrower domain interfaces; same underlying evidence as GO-DEP-002, filed separately because it originates from the §8.1 cluster review rather than the mechanical §5 dependency-hotspot pass.
- **GO-STORE-002** (informational, god-object, confidence high) — `*Store` now has **372 production receiver methods** (367 exported, 5 unexported) across 62 method-bearing production files; the package has 66 production Go files in total. The surface still spans ~25+ domain concepts (sessions, agents, teams, plugins, schedules, workflows, a2a tasks, trust, roles, skills, todos, plans, reminders, durable agents, reflexes, user settings, etc.). Grounding persistence was removed in Wave 4.
- **GO-PLUGIN-006** (informational, god-object/package-cohesion, confidence high) — `Host` (`internal/plugin/host.go`) has 38 fields/123 methods across 9 files, above both the guide's thresholds, but is a single coherent domain ("everything a plugin can register"); roughly a quarter of the registration categories are already extracted into dedicated sub-registry types with their own locks (`cardRulesRegistry`, `panelRegistry`, `FilterRegistry`, `MutablePluginMux`), the rest remain raw maps.

All five findings are evidenced in `docs/audits/2026-08-21-go-quality/REPORT.md` (`GO-DEP-001`/`GO-DEP-002` in §5, extended by §8.1/§8.3; `GO-STORE-001`/`GO-STORE-002` in §8.1; `GO-PLUGIN-006` in §8.6) and `docs/audits/2026-08-21-go-quality/findings.json`.

### Why this is a do-not-refactor note, not a work task

The remediation guide is explicit and direct on both `Container` and `internal/store`, and this task file quotes it verbatim rather than paraphrasing, because the instruction is the whole point:

> **On `Container`:** "Do not schedule a god-object refactor merely because it has 60 fields. The audit found it wiring-only with two methods. Only improve construction/lifecycle/grouping if it provides concrete value."

> **On `internal/store`:** "Treat as a gravitational-package review, not a mandatory split. Add narrow consumer-defined interfaces only where they solve demonstrated coupling/testability problems."

The guide's own top-level guardrails (§11) reinforce this directly: *"Do not: ... rewrite large code solely because metrics are high; split healthy composition roots; create repository interfaces everywhere because Store has high fan-in."* `Container`'s 64 current fields and `*Store`'s 372 current methods are exactly the shape those guardrails are written to prevent someone from reflexively "fixing."

`GO-PLUGIN-006` (`Host`) is included in this same note because it shares the same evidentiary shape — high field/method counts, informational severity, audit verdict of "single coherent domain," and an *optional* (not required) extension path already visible in the codebase — even though it comes from a different report section (§8.6, plugin cluster) than the other four (§8.1/§8.3/§5, store/service clusters). It is grouped here by disposition-shape (do-not-refactor), not by package family, matching this folder's own theme of "architectural concentration" findings that got individually judged rather than mechanically flagged.

### Current behavior — why each finding's own evidence argues against action

**`Container` (GO-DEP-001).** `internal/service/container.go:68-295`: `Container` now has **64 named fields**: 58 exported fields used for composition/configuration and 6 unexported lifecycle fields (`stopModelCatalog`, two reaper handles, two reaper cancel functions, and `shutdownOnce`). It has **4 production receiver methods**: exported `RefreshUtilitySettings` and `Shutdown`, plus private `shutdown` and `shutdownWithMaxWait`. The private methods are the decomposition of the same graceful-shutdown orchestration, not a new business capability. `NewContainer` is 1,093 lines (`:376-1468`), overwhelmingly constructor calls, wiring, cleanup, and error propagation. Only one production caller exists (`cmd/nanite/main.go:413`). The current evidence therefore preserves the audit's conclusion: the struct clears the guide's field-count threshold but still fails the *behavior* half of the god-object test. The actual behavior concentration remains `chatServiceImpl`, covered by `01-chatserviceimpl-generateresponse-decomposition.md` (`GO-SVCEXEC-002`).

**`internal/store` (GO-DEP-002 / GO-STORE-001 / GO-STORE-002).** `internal/store/store.go:22-25` remains one `*Store{DB, dbPath}` handle with only 2 fields. It now has **372 production receiver methods** (367 exported, 5 unexported) across 62 method-bearing files; `internal/store` contains 66 production Go files, 90 exported struct declarations, and 145 pure migration `.sql` files. The direct-dependency count is **32 production package directories importing the exact root package**. This definition deliberately excludes `internal/store/seedcatalog`: a prefix grep reports 33 directories only because it incorrectly counts `internal/store` itself as an importer of its subpackage. Within `internal/service`, the concrete-handle count is **61 `*store.Store` occurrences across 25 non-test files**. These are reproducible textual/Go-import counts, not estimates.

The audit's two cited narrow-interface examples have drifted: `dispatch.TrustResolver` still exists at `internal/dispatch/trust.go:37` and is implemented by `internal/store/trust.go:22`; `grounding.ConsultationLogger` and `internal/store/grounding_log.go` were deleted by Wave 4 (`cc019cff`). The pattern itself is not dependent on that deleted example and is now plainly established elsewhere: `internal/service/store.go` defines domain-scoped reader/writer interfaces and a composite `service.Store`, `internal/selftools/reactions/engine.go` accepts a two-method consumer-owned `Store`, and `internal/scheduler/retrying_runner.go` accepts the four-method `ScheduleRunStore`. This strengthens the factual claim that consumers know how to narrow dependencies when useful, while the remaining 61 concrete references show why the package is still gravitational. It does **not** establish a concrete coupling/testability problem that would override AD-14's no-new-interface decision. The audit's false-positive framing remains apt: a shared persistence facade is defensible at this scale, and file-per-domain-concept cohesion is the guide's own healthy-large-package example.

**`Host` (GO-PLUGIN-006).** `internal/plugin/host.go:97-172` still has **38 fields** and **123 production receiver methods** (103 exported, 20 unexported) across 9 files. The four dedicated registry owners remain present and independently synchronized: `cardRulesRegistry` (`cardrules.go`), `panelRegistry` (`panels.go`), `FilterRegistry` (`filter.go`), and `MutablePluginMux` (`httpmux.go`). Hooks, CRUD handlers, UI components, connectors, keybindings, slots, services, configs, envelopes, manifests, and their ownership maps remain held directly by `Host`, so the audit's mixed extracted/raw-registry observation is still accurate. The count is unchanged and the responsibility remains one coherent domain: everything a plugin can register. A targeted sub-registry remains an available pattern if a concrete bug or testability problem appears; the count alone does not supply that driver. `GO-PLUGIN-004` remains the separately tracked teardown/TOCTOU concern and is not broadened here.

### Desired outcome

**"Done means" for this file is not a code change — it is an architect decision recorded against these five findings.** The trigger for any future work here must be a real, demonstrated pain point, not the metrics alone:

> Architect reviews this note and either (a) confirms no action needed, or (b) names a specific, concrete coupling/testability problem that justifies a narrow interface extraction for a **specific named consumer domain** of `Store` (or, separately, a specific named registration-category extraction off of `Host`, following its own existing sub-registry pattern) — not a general "let's improve Store's/Host's architecture" directive.

### Operator decision — 2026-08-23

The operator explicitly approved the following dispositions after reviewing the current-source evidence:

- **GO-DEP-001 / `Container`: confirmed false-positive as a decomposition target.** Its current size is accepted because it remains a wiring/lifecycle composition root. No decomposition is authorized.
- **GO-DEP-002 / GO-STORE-001 / GO-STORE-002 / `Store`: no blanket package/type split and no new interfaces from this review.** AD-14's accepted-as-is decision for the concrete dependency findings stands. Add a consumer-defined narrow interface only when a specific named consumer demonstrates concrete coupling or testability friction.
- **GO-PLUGIN-006 / `Host`: selective decomposition is warranted only when driven by the existing `UnloadPlugin` pain.** Extend the established sub-registry pattern for named registration categories where doing so localizes ownership and teardown invariants, coordinate that work with the existing `GO-PLUGIN-004` scope, and explicitly reject an all-category migration sweep.

The governing rationale is to address growing pains as they appear rather than decompose for its own sake. This decision creates no new task from `10/03`: the only approved Host driver is already tracked by `GO-PLUGIN-004`, and any category boundary must be named and justified in that work rather than selected here by metric.

## What to do

1. Confirm each of the five findings' cited metrics and evidence against current source before treating this note as final — line numbers and counts may have drifted since the audited commit (`8feeee5c`); re-grep `internal/service/container.go` for `Container`'s field/method count, `internal/store/*.go` for `*Store`'s method count and the two consumer-defined-interface examples, and `internal/plugin/host.go` for `Host`'s field/method count and the existing sub-registry types.
2. Present this note to the architect (or the operator directly, if this batch's future planner pass is architect-and-operator-combined) as a **decision item, not an implementation item** — the expected outcome is confirmation of no action, per the guide's own instructions quoted above, but that confirmation must actually be given and recorded, not assumed by this planning pass.
3. If the architect instead identifies a concrete, named pain point (e.g. "package X's tests need to mock 12 unrelated `*Store` methods to test one domain, and that's slowing test authorship" or "`Host`'s remaining raw-map registration categories are causing a specific bug class beyond `UnloadPlugin`'s already-tracked TOCTOU gap"), that becomes the seed for a **new, separate task file** (not an edit to this one) scoped narrowly to that specific domain/consumer — mirroring `dispatch.TrustResolver`, `selftools/reactions.Store`, or `scheduler.ScheduleRunStore` for `Store`, or the existing `cardRulesRegistry`/`panelRegistry`/`FilterRegistry`/`MutablePluginMux` pattern for `Host`.
4. Do not, under any circumstance arising from this task alone, schedule a blanket `internal/store` interface-segregation pass, a `Container` restructuring, or a `Host` sub-registry migration sweep. Any of those would contradict the guide's explicit guardrails quoted above.

## Non-goals

- Splitting `Container` for its field count alone.
- Splitting or interface-wrapping `internal/store` repo-wide "because Store has high fan-in" (a guardrail the guide names by this exact phrase in §11).
- Migrating `Host`'s remaining raw-map registration categories into sub-registries as a blanket sweep, absent a named concrete driver.
- Treating `GO-STORE-002`'s current 372-method count, on its own, as evidence of a problem — the guide's own healthy-large-package example is this package's exact shape.

## Dependencies

None. This note does not block or get blocked by `01`/`02` in this folder — different types, different packages, independent dispositions.

## Tests required

None. No code changes.

## Prevention

- This note itself is the prevention mechanism against a future audit or reviewer re-flagging these same five findings as "newly discovered" god-objects without checking whether they were already reviewed and deliberately not actioned — the disposition is recorded here, with the reasoning, not just a checkbox.
- If a future narrow-interface extraction is ever justified per the "What to do" step 3 trigger, that task's own Prevention section should note this file as the review-note precedent establishing the "named concrete pain point required" bar, so a future narrow extraction doesn't itself creep back into a blanket sweep.

## Verification

No commands required — no code is touched by this task. The only verification is documentary: confirm this file's cited metrics against current source (per "What to do" step 1) before considering the disposition final.

## Risk / rollback

None — this task changes no code. If a future architect decision reverses the "no action" disposition, that reversal is scoped as a new task, not a rollback of this one.

## Done means

- [x] This file's cited metrics are re-confirmed against current source, with counting definitions and material drift recorded above and in the Work log.
- [x] The operator reviewed the current-source evidence and recorded explicit dispositions for all five findings on 2026-08-23. `Container` is accepted as a composition root; Store breadth is accepted subject to the named-consumer pain threshold; Host may decompose selectively only through the existing `GO-PLUGIN-004` driver, never as an all-category sweep.
- [x] No code in `internal/service/container.go`, `internal/store/*.go`, or `internal/plugin/host.go` is modified as a result of this task.
- [ ] `FINDING-INDEX.md`'s disposition for `GO-DEP-001`, `GO-DEP-002`, `GO-STORE-001`, `GO-STORE-002`, and `GO-PLUGIN-006` reflects this file's outcome once the architect decision lands (a future planner-pass action, not this task's own — but noted here so the link isn't lost).

## Work log

### 2026-08-23 — current-source reverification

- Recounted named struct fields from the complete struct declarations, and receiver methods from non-test `.go` files. `Container`: 64 fields (58 exported + 6 private lifecycle fields), 4 methods (2 exported + 2 private shutdown helpers). `Host`: 38 fields, 123 methods (103 exported + 20 private) across 9 files.
- Recounted `*Store` receiver declarations in non-test `.go` files: 372 total (367 exported + 5 private), spread across 62 of the package's 66 production Go files. `Store` itself remains a two-field database handle. The package now contains 90 exported struct declarations and 145 migration SQL files.
- Counted Store fan-in by exact root-package import, not prefix: 32 production package directories import `github.com/hollis-labs/nanite/internal/store`. A prefix match produces 33 only by misclassifying `internal/store` as an importer because `store.go` imports the distinct `internal/store/seedcatalog` subpackage.
- Counted concrete coupling in `internal/service` as literal `*store.Store` occurrences in non-test Go files: 61 occurrences across 25 files. This is a transparent current-source replacement for the audit's 76-reference figure; it is not a claim that every occurrence is a distinct runtime dependency.
- Confirmed `dispatch.TrustResolver` remains a one-method consumer-defined interface implemented structurally by `*store.Store`. Corrected the stale second example: Wave 4 commit `cc019cff` deleted `grounding.ConsultationLogger` and `internal/store/grounding_log.go`. Confirmed additional live consumer-defined slices, including the domain interfaces in `internal/service/store.go`, the two-method `selftools/reactions.Store`, and the four-method `scheduler.ScheduleRunStore`.
- Confirmed all four cited plugin sub-registries and their own locks remain in current source: `cardRulesRegistry`, `panelRegistry`, `FilterRegistry`, and `MutablePluginMux`.
- Read AD-14 as recorded on 2026-08-22. It explicitly accepts `GO-DEP-002` and `GO-STORE-001` as-is and requires no new consumer-defined Store interfaces. It did not name or decide `GO-DEP-001`, `GO-STORE-002`, or `GO-PLUGIN-006`; that documentation gap was reported rather than inferred.
- The operator closed the gap on 2026-08-23: no `Container` decomposition; no blanket Store split and narrow interfaces only for demonstrated named-consumer friction; selective `Host` sub-registry extraction only where the existing `UnloadPlugin`/`GO-PLUGIN-004` pain identifies and justifies a named category, with an all-category sweep rejected. The operator expressly approved this balance and authorized this task to proceed as implemented.
- No application code or shared tracker file was changed. The orchestrator must centrally update `TASKS/INDEX.md`, `TASKS/audit-remediation/FINDING-INDEX.md`, `TASKS/audit-remediation/findings.json`, and any durable decision record with the operator's disposition.
- Baseline verification passed despite the documentation-only scope: `go build ./cmd/nanite/`, `go vet ./...`, and `go test ./...` all exited 0.

## Review notes

<!-- Reviewer fills in: pass/fail — for this file, "review" means confirming the architect decision was actually obtained and recorded, and that no code was touched, not the usual code-correctness review. -->
