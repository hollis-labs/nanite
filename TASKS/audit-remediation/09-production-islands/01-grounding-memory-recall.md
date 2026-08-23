# Decide the fate of `internal/grounding`'s pre-strategy memory-recall subsystem

**Phase:** Wave 4 — Production islands (per remediation guide §4)
**Status:** implemented
**Depends on:** none within this batch.
**Touches:** `internal/grounding/recall.go`, `internal/grounding/outcome.go`,
`internal/grounding/types.go`, `internal/selftools/self_tools_transport.go`
(`GroundingRecaller`/`GroundingLogger` fields), `internal/selftools/self_tools_dispatch.go`
(the E2 pre-strategy recall block), `internal/service/container.go` (the
composition root where wiring would happen if "wire" is chosen).

```yaml
requires_architect_decision: true
requires_security_review: false
requires_regression_test: true
```

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 4 — production islands · **Dispatch unit:** `W4`
> - **Depends on:** `04/01`, `07/04` (both edit `internal/service/container.go`), and `00/01`'s reachability report
> - **Blocks:** `10/02` (shares `self_tools_transport.go`), `11/11` (shares `self_tools_dispatch.go`)
> - **Parallel-safe with:** `09/03`–`09/06`. **Not** `09/02` — both wire into `container.go`.
> - **Gated on:** AD-06 (wire / defer / retire)
> - **requires_security_review:** false · **requires_regression_test:** true

> ## ✅ AD-06 DECIDED (2026-08-22) — RETIRE
>
> Delete `internal/grounding/` (534 prod / 361 test). **The deletion reaches
> outside that package** and the task is not done until these go too:
> - `internal/selftools/self_tools_transport.go:219-231` — the
>   `GroundingRecaller` / `GroundingLogger` fields and their comments.
> - `internal/selftools/self_tools_dispatch.go:109-118` — the E2 pre-strategy
>   recall block guarded by `if st.GroundingRecaller != nil`, plus the
>   explanatory comment at `:29`.
>
> Confirm nothing else references `grounding.` before deleting. Note `10/02`
> (Wave 5) decomposes `SelfToolsTransport` and `11/11` edits
> `self_tools_dispatch.go` — both surfaces shrink because of this task.

## Context

### Findings addressed

- **GO-MEM-001** (medium, confidence high) — `internal/grounding`'s entire
  pre-strategy memory-recall subsystem (recall, outcome-tracking,
  consultation-logging — ~530 LOC, extensively tested) is fully built but
  never wired in production: `GroundingRecaller`/`GroundingLogger` fields
  exist on the self-tools transport, but zero production code anywhere
  constructs and assigns a `*grounding.Recaller` to them (confirmed by
  `deadcode` + exhaustive grep). Also gated behind an env var defaulting off.
  Design intent ("prevent premature strategy dispatch without grounding in
  relevant memory") isn't happening today.

Source: `docs/audits/2026-08-21-go-quality/REPORT.md` §8.11 and
`docs/audits/2026-08-21-go-quality/findings.json`.

### Root cause

This is not a defect — it's a completed feature that was never given its
composition-root wiring step. The subsystem's consuming call site already
exists and is fully written defensively for the "not wired" case:
`internal/selftools/self_tools_dispatch.go`'s `callExecuteTask` (E2
pre-strategy recall block, `self_tools_dispatch.go:96-121`) checks
`if st.GroundingRecaller != nil` before doing anything, and the recall itself
is additionally gated by `grounding.IsGroundingEnabled()`
(`internal/grounding/recall.go:60-62`, reading
`NANITE_GROUNDING_ENABLED`, default off — `recall.go:89`). Two independent
gates (a nil check *and* an env var default-off) both have to be crossed
before this code does anything, and nothing in the codebase currently crosses
either.

### Current behavior

**The consuming call site (dead-safe, correctly nil-guarded):**

```go
// internal/selftools/self_tools_dispatch.go:109-121
if st.GroundingRecaller != nil {
    groundingResult := st.GroundingRecaller.Recall(ctx, grounding.RecallInput{
        UserInput: message,
        SessionID: sessionID,
        UserID:    userID,
        TurnID:    turnID,
    })
    if groundingResult.Enabled {
        groundingConsultationIDs = grounding.LogConsultations(st.GroundingLogger, groundingResult, turnID)
        if block := grounding.SystemPromptBlock(groundingResult); block != "" {
            dispatchMessage = block + "\n" + message
        }
    }
}
```

**The fields that would need assignment:**

```go
// internal/selftools/self_tools_transport.go:210-222
// GroundingRecaller is the pre-strategy memory recall step ...
GroundingRecaller *grounding.Recaller
// GroundingLogger persists consultation and outcome rows.
// ...
GroundingLogger grounding.ConsultationLogger
```

`grounding.NewRecaller` (`internal/grounding/recall.go:46`) has exactly two
call sites in the entire tree, both in `internal/grounding/recall_test.go`
(lines 35 and 53). There is no `GroundingRecaller:` struct-literal assignment
anywhere in production code — confirmed by grep across `internal/` and
`cmd/`.

**The env-var gate defaults off:**

```go
// internal/grounding/recall.go:60-62
func IsGroundingEnabled() bool {
    v := strings.ToLower(strings.TrimSpace(os.Getenv("NANITE_GROUNDING_ENABLED")))
```

`recall.go:89` calls this and short-circuits to a disabled `RecallResult`
when unset — meaning even a hypothetical operator who somehow got a
`*grounding.Recaller` constructed would still see it do nothing without also
setting this env var explicitly.

### Desired invariant

Not applicable in the usual "must always be true" sense this template
otherwise uses — the invariant this task actually establishes is
**disposition clarity**: after this task closes, `internal/grounding`'s
pre-strategy recall subsystem must be in exactly one of three states, with no
ambiguity between "looks live" and "is live":

- genuinely wired into the composition root and reachable from a real
  `task_execute` production call, with its env-var gate's default state a
  deliberate, documented choice; or
- explicitly marked deferred, with a trigger/owner recorded and nothing in
  code comments/docs implying it currently runs; or
- removed, along with its now-orphaned tests and doc references.

### Scope

- `internal/grounding/recall.go`, `outcome.go`, `types.go` — the subsystem
  itself (~530 LOC total: `recall.go` 235, `outcome.go` 132, `types.go` 163).
- `internal/grounding/recall_test.go`, `outcome_test.go` — its tests (~360
  LOC total).
- `internal/selftools/self_tools_transport.go:210-222` — the
  `GroundingRecaller`/`GroundingLogger` fields.
- `internal/selftools/self_tools_dispatch.go:96-121` — the E2 pre-strategy
  recall consumption block (comment references task `CW-20260419-0028`).
- `internal/service/container.go` — the composition root, if "wire" is
  chosen (this is where `SelfToolsTransport` is constructed and its fields
  populated for other, live dependencies — the natural wiring point, subject
  to re-verification per "What to do" below).

### All production callers

None. This is the finding — zero production callers exist. Confirming that
remains true against current source is this task's first job (see "What to
do").

## What to do

1. **Re-verify against current source that this is still unreachable.** The
   remediation guide's own Wave 4 section warns explicitly: *"check current
   source first; some were completed after the audited commit."* Development
   on `main` did not stop when the audit ran (audited commit `8feeee5c`).
   Before doing anything else:
   - Run `deadcode -test ./internal/grounding/...` and confirm `Recaller`,
     `NewRecaller`, and the other exported symbols are still flagged
     unreachable (or note if they're not — that would mean this finding is
     already resolved).
   - Grep for `GroundingRecaller:` and `grounding.NewRecaller(` across
     `internal/` and `cmd/` and confirm no production struct-literal
     assignment exists yet.
   - Check `internal/service/container.go` directly for any new wiring block
     touching `SelfToolsTransport`'s grounding fields.
   - If any of this has changed, stop and correct this task's disposition
     rather than proceeding on stale evidence — note the correction in Work
     log.

2. **If still unreachable, present the wire/defer/retire options below to the
   architect.** Do not pick one — that decision belongs to the architect, not
   to whoever executes this task file.

   **Option — wire.** Construct a `*grounding.Recaller` in
   `internal/service/container.go` (the natural analog to how other
   `SelfToolsTransport` dependencies are wired there) and assign it, along
   with a `grounding.ConsultationLogger` implementation, to the transport's
   `GroundingRecaller`/`GroundingLogger` fields. Decide whether
   `NANITE_GROUNDING_ENABLED` should flip to default-on once wired, or stay
   an explicit opt-in — the finding's own framing ("prevent premature
   strategy dispatch without grounding in relevant memory") suggests the
   intended end state is default-on, but that changes `task_execute`'s
   observable behavior for every existing caller and deserves its own
   explicit sign-off, not an incidental flip alongside the wiring change.
   - Tradeoff: this is exactly what the code was built for, and the
     consuming call site (`self_tools_dispatch.go`) is already written and
     tested for both the "recaller set" and "recaller nil" cases — the
     lowest-risk of the six islands to wire, since the integration seam is
     narrow (two struct fields, one already-defensive call site) rather than
     a new architectural integration.
   - Tradeoff: turning on a real memory-recall step in the hot path of every
     `task_execute` dispatch has a latency/cost implication (a `memory.Service`
     round-trip per dispatch) that hasn't been load-tested in production,
     since the path has never actually run there. Needs a performance check
     before flipping the env var default, if that's the direction chosen.

   **Option — defer.** Leave the code as-is, but make the "not currently
   active" state visible and intentional rather than an accident of missing
   wiring. Concretely, per the guide's explicit requirement that a deferred
   island **must not look production-live in docs and should not impose
   unnecessary boot/runtime cost**:
   - Confirm (or add, if missing) an explicit doc comment on
     `GroundingRecaller`/`GroundingLogger` in `self_tools_transport.go`
     stating the fields are currently unpopulated in production and why —
     today's comment at `self_tools_transport.go:210-222` describes what the
     fields *would* do if set, without stating that nothing sets them; a
     reader could reasonably assume it's live.
   - Because the subsystem imposes no boot cost today (it's simply never
     constructed — no init-time I/O, no goroutine, no background job), the
     "should not impose unnecessary boot/runtime cost" requirement is
     already satisfied by inaction; document that this was verified, not
     assumed.
   - Record a trigger/owner: what future condition or decision would cause
     someone to revisit "wire" — e.g., a specific Phase or milestone.
   - Ensure `NANITE_GROUNDING_ENABLED`'s default-off state is not
     independently documented anywhere as "the feature is on but you have to
     enable it" language that implies partial production activity.

   **Option — retire.** Remove `internal/grounding/recall.go`, `outcome.go`,
   their test files, the `GroundingRecaller`/`GroundingLogger` fields from
   `SelfToolsTransport`, and the E2 pre-strategy recall block in
   `self_tools_dispatch.go`. Check whether `types.go` and any other files in
   `internal/grounding/` are used by anything else in the package before
   deleting the whole directory versus just the recall-specific files —
   confirm package boundary with a fresh `deadcode` run post-removal, not
   assumed from this task's own scope statement.
   - Tradeoff: removes ~530 LOC + ~360 test LOC of maintained-but-unused
     surface area, and the "would this integrate cleanly if we revived it
     later" question goes away because there's nothing to keep in sync with
     the rest of the codebase as it evolves.
   - Tradeoff: the code quality here is high and the audit itself notes this
     "argu[es] for 'not yet wired' over 'abandoned'" — retiring a
     well-built, well-tested subsystem on the theory that nobody's gotten
     around to wiring it yet is a real loss if "wire" was always the intent
     and just hasn't been prioritized. This tradeoff is exactly why the
     decision needs an architect who knows the actual intent, not an
     implementer guessing from the code alone.

## Done means

- [x] Current-source reachability re-verified (deadcode + grep + composition
      root read) and confirmed still open, or the task's disposition
      corrected if it's been resolved since the audit.
- [x] Architect decision recorded: wire, defer, or retire.
- [ ] If **wire**: `*grounding.Recaller`/`grounding.ConsultationLogger`
      constructed and assigned in `internal/service/container.go`;
      `NANITE_GROUNDING_ENABLED`'s target default state explicitly decided
      and documented (not silently left at today's default); a real
      integration test exercises `task_execute` with grounding enabled and
      asserts the recall block is prepended to the dispatch message; a
      latency check performed before any default-on flip.
- [ ] If **defer**: doc comments on the transport fields updated to state
      plainly that nothing populates them in production today and why;
      trigger/owner recorded; confirmed no boot/runtime cost is currently
      paid (and this stays true).
- [x] If **retire**: subsystem, its tests, and the transport fields/dispatch
      block removed; `deadcode -test ./internal/grounding/...` (or package
      removal) confirms nothing else in the package remains unreferenced;
      any doc/comment elsewhere describing this as a live or planned feature
      updated or removed.
- [x] `go build ./...` and `go test ./...` pass after whichever direction is
      implemented.

## Work log

- 2026-08-23 — Re-derived reachability and line ranges before editing. `deadcode -test ./internal/grounding/...` reported only unreachable symbols in dependencies (`internal/memory` and `internal/safego`), not a production root for grounding; because the package's own tests make its exports reachable under `-test`, constructor/reference greps supplied the decisive production check. `grounding.NewRecaller(` still had exactly two calls, both in `recall_test.go`; `GroundingRecaller:` had no production assignment; and `internal/service/container.go` had only the stale comment at line 1406, no wiring. The current transport fields were lines 219–231. The full E2 dispatch state was larger than the banner's stale 109–118 range: the function comment at 29–34 and the recall/enriched-message/outcome placeholder block at 96–130.
- 2026-08-23 — Implemented AD-06 RETIRE. Deleted all five files under `internal/grounding` (534 production and 361 test lines), removed the two `SelfToolsTransport` fields/import, and removed the complete E2 dispatch state rather than only the cited nil-guard. That included the now-single-valued `dispatchMessage`, unused grounding-derived `turnID`/`userID` locals, the raw-vs-sent metadata branch, and the corresponding `matchDispatchToAgentReflex` parameters/test arguments. `task_execute`, broker consultation, reflex matching, and dispatch now consistently receive the original `message`.
- 2026-08-23 — Fresh reference analysis found one required coupling omitted from the authored Touches list: `internal/store/grounding_log.go` directly imported the retiring package and existed solely as its consultation/outcome persistence adapter. Deleted that 173-line adapter as part of the subsystem retirement. Historical migrations/tables remain untouched so existing database history and migration ordering stay stable. The broader `internal/memory.Service` remains load-bearing; its container comment now names only the live context-broker and extraction consumers.
- 2026-08-23 — Removed the surviving `grounding.SystemPromptBlock` comparison from `internal/learnings`, updated the canonical steering and memory architecture inventories, and marked the superseded Phase 8 real-session evaluation brief accordingly. Dated audit reports, decision records, and migration comments remain as historical evidence rather than being rewritten.
- 2026-08-23 — Post-removal package-boundary check confirmed `internal/grounding` no longer exists and no Go source references the removed package, fields, env gate, or consultation state. Verification passed: `go test ./internal/selftools ./internal/store ./internal/learnings ./internal/service`; `git diff --check`; `go build ./cmd/nanite/`; `go vet ./...`; `go test ./...`.
- 2026-08-23 — Fresh review found two documentation/status gaps after implementation: this folder's README still presented grounding as built and awaiting an architect decision, and `TASKS/INDEX.md` still listed the superseded Phase 8 grounding evaluation as `not-started`. A bounded documentation-only fix updated the README's framing and grounding row to AD-06's implemented retirement, and synchronized the Phase 8 index row to `superseded` with the Wave 4 reason. No application code or unrelated task status changed. Reference greps, Markdown table-shape checks, link-target checks, and `git diff --check` passed.

## Review notes

- **Fresh-review finding (2026-08-23): fix required.** The production-islands README and global Phase 8 index contradicted AD-06's implemented retirement as described above.
- **Fix applied (worker pass, 2026-08-23):** corrected both tracking surfaces and recorded the change in the Work Log. Re-review remains pending; this entry does not claim the fix has passed review.
