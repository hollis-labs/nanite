# REVIEW NOTE (not a work task) — `Container` and `internal/store`: do not schedule a god-object refactor

**Phase:** Audit remediation — Wave 5 (architectural concentration)
**Status:** not-started
**Depends on:** none
**Touches:** nothing. This file recommends **no code changes** to `internal/service/container.go`, `internal/store/*.go`, or `internal/plugin/host.go`. It exists purely so the underlying findings have a documented disposition instead of silently disappearing from tracking.
**requires_architect_decision:** true — but the expected/likely outcome is **no action**. This is not a decision queued because the direction is unclear; it is queued because the remediation guide requires every finding to reach an explicit disposition (§4 Wave 5, §7 output C), and "confirmed, no action needed" is itself a disposition an architect must actually make, not one this planning pass can make unilaterally on the architect's behalf.

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

This file is deliberately **not shaped like a work task**. Files `01` and `02` in this same folder (`01-chatserviceimpl-generateresponse-decomposition.md`, `02-selftoolstransport-decomposition.md`) are real decomposition-planning work with concrete deliverables (a responsibility map, a phase-boundary proposal, characterization tests). This file is the opposite: a **documented decision not to do a category of work**, covering three findings whose own audit evidence and the remediation guide's own explicit instructions both say a refactor would be the wrong call. It exists in this folder — rather than being silently dropped — specifically because this batch's own README and `FINDING-INDEX.md` require every one of the audit's 113 findings to map to exactly one task file with a real disposition; a god-object-shaped finding that turns out not to warrant action still needs a place to live where a future reader can see it was actually reviewed, not missed.

### Findings addressed

- **GO-DEP-001** (informational, god-object/architecture, confidence high) — `Container` (`internal/service/container.go`): 60-field composition root, 31.6K production LOC across 83 files in the surrounding package, fan-out 64. Directly confirmed by the audit's §8.3 read: `Container` itself is genuinely wiring-only (2 methods), but the package's actual mass (real behavior) lives in `chatServiceImpl` (see `GO-SVCEXEC-002`, covered in `01-chatserviceimpl-generateresponse-decomposition.md`), not `Container`.
- **GO-DEP-002** (medium, dependency/architecture, confidence high) — `internal/store`: fan-in 30, **the single most gravitational package in the codebase**. §8.1 confirmed the dominant caller pattern is injecting the whole concrete `*store.Store` (76 direct references in `internal/service` alone) rather than a narrower domain interface, though two healthy consumer-defined-interface counter-examples already exist in the codebase (`dispatch.TrustResolver`, `grounding.ConsultationLogger`).
- **GO-STORE-001** (medium, package-cohesion/dependency/architecture, confidence high) — nearly all callers depend on the concrete `*store.Store` handle rather than narrower domain interfaces; same underlying evidence as GO-DEP-002, filed separately because it originates from the §8.1 cluster review rather than the mechanical §5 dependency-hotspot pass.
- **GO-STORE-002** (informational, god-object, confidence high) — `*Store`'s 349-method surface spans ~25+ unrelated domain concepts (sessions, agents, teams, plugins, schedules, workflows, a2a tasks, grounding, trust, roles, skills, todos, plans, reminders, durable agents, reflexes, user settings, etc.).
- **GO-PLUGIN-006** (informational, god-object/package-cohesion, confidence high) — `Host` (`internal/plugin/host.go`) has 38 fields/123 methods across 9 files, above both the guide's thresholds, but is a single coherent domain ("everything a plugin can register"); roughly a quarter of the registration categories are already extracted into dedicated sub-registry types with their own locks (`cardRulesRegistry`, `panelRegistry`, `FilterRegistry`, `MutablePluginMux`), the rest remain raw maps.

All five findings are evidenced in `docs/audits/2026-08-21-go-quality/REPORT.md` (`GO-DEP-001`/`GO-DEP-002` in §5, extended by §8.1/§8.3; `GO-STORE-001`/`GO-STORE-002` in §8.1; `GO-PLUGIN-006` in §8.6) and `docs/audits/2026-08-21-go-quality/findings.json`.

### Why this is a do-not-refactor note, not a work task

The remediation guide is explicit and direct on both `Container` and `internal/store`, and this task file quotes it verbatim rather than paraphrasing, because the instruction is the whole point:

> **On `Container`:** "Do not schedule a god-object refactor merely because it has 60 fields. The audit found it wiring-only with two methods. Only improve construction/lifecycle/grouping if it provides concrete value."

> **On `internal/store`:** "Treat as a gravitational-package review, not a mandatory split. Add narrow consumer-defined interfaces only where they solve demonstrated coupling/testability problems."

The guide's own top-level guardrails (§11) reinforce this directly: *"Do not: ... rewrite large code solely because metrics are high; split healthy composition roots; create repository interfaces everywhere because Store has high fan-in."* `Container`'s 60 fields and `*Store`'s 349 methods are exactly the shape those guardrails are written to prevent someone from reflexively "fixing."

`GO-PLUGIN-006` (`Host`) is included in this same note because it shares the same evidentiary shape — high field/method counts, informational severity, audit verdict of "single coherent domain," and an *optional* (not required) extension path already visible in the codebase — even though it comes from a different report section (§8.6, plugin cluster) than the other four (§8.1/§8.3/§5, store/service clusters). It is grouped here by disposition-shape (do-not-refactor), not by package family, matching this folder's own theme of "architectural concentration" findings that got individually judged rather than mechanically flagged.

### Current behavior — why each finding's own evidence argues against action

**`Container` (GO-DEP-001).** `internal/service/container.go`: `Container` the struct has **60 fields** (55 dependency handles + 5 lifecycle fields, per §8.3's direct count) but only **2 methods** — `RefreshUtilitySettings` (a 6-line setter) and `Shutdown` (pure orchestration). `NewContainer` is ~1,065 lines, overwhelmingly constructor calls and error propagation — a textbook composition root, not a god object; it clears the guide's field-count threshold on structure alone but fails the *behavior* half of the god-object test outright, because it has almost no behavior. Only one production caller of `NewContainer` exists (`cmd/nanite/main.go:372`). The audit's own cross-cutting note (§8.3) makes the real signal explicit: *"`chatServiceImpl`... has 84 methods total, dwarfing `Container`'s 2 — it is very likely the real god-object candidate in this package."* That candidate is already covered, correctly, by `01-chatserviceimpl-generateresponse-decomposition.md` (`GO-SVCEXEC-002`) — this note exists specifically so `Container`'s superficially-alarming 60-field count doesn't get independently escalated into a second, unwarranted refactor task alongside it.

**`internal/store` (GO-DEP-002 / GO-STORE-001 / GO-STORE-002).** `internal/store/store.go:22`: one `*Store{DB, dbPath}` handle (only 2 fields) with 349 methods across 63 production files, backing ~84 exported row-shaped structs across ~25+ distinct domain areas. The audit's §8.1 verdict is explicit: *"Structurally not a classic god object — the struct itself carries no scattered cross-subsystem dependencies — but it is squarely the guide's §3.2 'gravitational package'"* — the dominant caller pattern (76 direct `*store.Store` references in `internal/service` alone; fan-in 30 overall, the highest in the codebase) is "inject the whole concrete `*Store`," not a narrower per-domain interface. Two healthy counter-examples already exist and prove the narrower pattern is known in this codebase when it's actually needed: `dispatch.TrustResolver` (implemented by `internal/store/trust.go:22`) and `grounding.ConsultationLogger` (implemented by `internal/store/grounding_log.go:18,61`) — both narrow, consumer-defined, satisfied structurally by `*Store` without `internal/store` importing either consumer package. The audit's own false-positive framing for GO-STORE-001 is direct: *"a single shared persistence facade is a defensible, normal choice at this scale; this is 'worth an architect look,' not a confirmed problem."* Method-set breadth (GO-STORE-002) is, per the audit, *"the guide's own example of a healthy large package"* when it's file-per-domain-concept internally cohesive, which `internal/store` is (migrations cleanly separated into 134 pure `.sql` files with zero Go logic; each domain file internally cohesive; the package was independently reviewed and found healthy on transaction safety, unsynchronized-state absence, gosec cleanliness, and shared-helper adoption — see REPORT.md §8.1 "Reviewed and found healthy").

**`Host` (GO-PLUGIN-006).** `internal/plugin/host.go`: 38 fields / 123 methods across 9 files — above both the guide's inspect thresholds, but the audit's verdict is *"a single coherent domain ('everything a plugin can register')."* Roughly a quarter of the registration categories are already factored into dedicated sub-registry types with their own locks (`cardRulesRegistry`, `panelRegistry`, `FilterRegistry`, `MutablePluginMux`) — proving the extraction pattern is known and already partially applied in this exact type — while the remaining ~12 categories stay as raw maps directly on `Host`. The audit connects this directly to a separate, already-documented finding: *"This inconsistency is why `UnloadPlugin` ends up hand-inlined (GO-PLUGIN-004) — the established in-repo pattern is the natural extension path if the architect wants to reduce `Host`'s footprint."* (`GO-PLUGIN-004` itself, the `UnloadPlugin` complexity/TOCTOU finding, is tracked separately in a different folder of this batch, not here — this note covers only the god-object shape of `Host` itself.)

### Desired outcome

**"Done means" for this file is not a code change — it is an architect decision recorded against these five findings.** The trigger for any future work here must be a real, demonstrated pain point, not the metrics alone:

> Architect reviews this note and either (a) confirms no action needed, or (b) names a specific, concrete coupling/testability problem that justifies a narrow interface extraction for a **specific named consumer domain** of `Store` (or, separately, a specific named registration-category extraction off of `Host`, following its own existing sub-registry pattern) — not a general "let's improve Store's/Host's architecture" directive.

## What to do

1. Confirm each of the five findings' cited metrics and evidence against current source before treating this note as final — line numbers and counts may have drifted since the audited commit (`8feeee5c`); re-grep `internal/service/container.go` for `Container`'s field/method count, `internal/store/*.go` for `*Store`'s method count and the two consumer-defined-interface examples, and `internal/plugin/host.go` for `Host`'s field/method count and the existing sub-registry types.
2. Present this note to the architect (or the operator directly, if this batch's future planner pass is architect-and-operator-combined) as a **decision item, not an implementation item** — the expected outcome is confirmation of no action, per the guide's own instructions quoted above, but that confirmation must actually be given and recorded, not assumed by this planning pass.
3. If the architect instead identifies a concrete, named pain point (e.g. "package X's tests need to mock 12 unrelated `*Store` methods to test one domain, and that's slowing test authorship" or "`Host`'s remaining raw-map registration categories are causing a specific bug class beyond `UnloadPlugin`'s already-tracked TOCTOU gap"), that becomes the seed for a **new, separate task file** (not an edit to this one) scoped narrowly to that specific domain/consumer — mirroring `dispatch.TrustResolver`/`grounding.ConsultationLogger` for `Store`, or the existing `cardRulesRegistry`/`panelRegistry`/`FilterRegistry`/`MutablePluginMux` pattern for `Host`.
4. Do not, under any circumstance arising from this task alone, schedule a blanket `internal/store` interface-segregation pass, a `Container` restructuring, or a `Host` sub-registry migration sweep. Any of those would contradict the guide's explicit guardrails quoted above.

## Non-goals

- Splitting `Container` for its field count alone.
- Splitting or interface-wrapping `internal/store` repo-wide "because Store has high fan-in" (a guardrail the guide names by this exact phrase in §11).
- Migrating `Host`'s remaining raw-map registration categories into sub-registries as a blanket sweep, absent a named concrete driver.
- Treating `GO-STORE-002`'s 349-method count, on its own, as evidence of a problem — the guide's own healthy-large-package example is this package's exact shape.

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

- [ ] This file's cited metrics (Container 60 fields/2 methods; Store 349 methods/30 fan-in/76 references; Host 38 fields/123 methods) are re-confirmed against current source, with any material drift noted.
- [ ] An architect has reviewed this note and recorded one of: (a) confirmed, no action needed for all five findings, or (b) a specific named coupling/testability problem for a specific named `Store` consumer domain or `Host` registration category, seeding a new, narrowly-scoped follow-on task.
- [ ] No code in `internal/service/container.go`, `internal/store/*.go`, or `internal/plugin/host.go` is modified as a result of this task.
- [ ] `FINDING-INDEX.md`'s disposition for `GO-DEP-001`, `GO-DEP-002`, `GO-STORE-001`, `GO-STORE-002`, and `GO-PLUGIN-006` reflects this file's outcome once the architect decision lands (a future planner-pass action, not this task's own — but noted here so the link isn't lost).

## Work log

<!-- Whoever carries this note to the architect fills in: what was confirmed against current source, the architect's actual decision and date, any follow-on task created as a result. -->

## Review notes

<!-- Reviewer fills in: pass/fail — for this file, "review" means confirming the architect decision was actually obtained and recorded, and that no code was touched, not the usual code-correctness review. -->
