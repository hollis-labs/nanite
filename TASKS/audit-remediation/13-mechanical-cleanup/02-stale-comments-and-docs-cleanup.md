# Trim/correct stale comments and package docs across 6 findings — scoped trim, not blanket removal

**Phase:** Wave 8 — Mechanical cleanup (audit-remediation batch, sequenced 2026-08-21 — see the sequencing block below)
**Status:** not-started
**Depends on:** none within this batch.
**Touches:** `internal/store/*.go` (25+ files, comment-only), `internal/service/ingest.go`, `internal/service/known_tools_backfill.go`, `internal/service/cli_structured_input_fallback.go`, `internal/service/agent_deps.go`, `internal/service/chat_reflex_dispatch.go`, `internal/service/team_routing.go`, `cmd/nanite/main.go`, `internal/runtime/agent/agent.go`, `internal/chat/hint_catalog.go`, `internal/service/install/adapters.go`, `internal/service/install/adapter_cleanup.go`.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 8 — mechanical cleanup · **Dispatch unit:** `W8`
> - **Depends on:** Wave 7 complete
> - **Blocks:** none
> - **Parallel-safe with:** **none — broad touch across `internal/store` (25+ files), `internal/service`, and `cmd/nanite/main.go`.** Runs after everything except `13/03`.
> - **Gated on:** none
> - **requires_security_review:** false · **requires_regression_test:** false

## Context

**This batch is explicitly NOT "delete all history comments."** The audit repeats this caution at every relevant finding: `internal/store`'s task-ID references include things like `user_settings.go:262-264`'s default-value rationale, which REPORT.md §8.1 calls out by name as load-bearing, and the audit's own healthy-review notes for this package say several comments (`store.go:80-93`, `agent_runtime.go:227-244`) are "exactly the invariant/safety documentation the guide says to *keep*." The same caution applies to `internal/service`'s equivalent comments (`GO-SVCCORE-009`): "each explains a real invariant rather than being pure filler." The actual work here is a **scoped trim pass for pure task-ID/date citations that carry no added rationale** — a `CW-YYYYMMDD-NNNN` reference or a `TASKS/*.md` path sitting alone with nothing else useful attached — done by someone who reads each instance and judges case-by-case, not a script that strips every comment matching a pattern.

Two findings in this batch are a different shape entirely and don't belong in the "trim" bucket: `GO-SVCCORE-003` and `GO-CHAT-006` are both cases where a comment makes a **factually false claim about what the code does**, not merely a comment that's grown too long. Fixing those requires either changing the code to match what the comment promises, or changing the comment to match what the code actually does — a real decision, not a trim. `GO-SVCCORE-003` in particular deserves more weight in this batch than the others: its severity is **medium**, not low/informational like every other finding here, because the comment's false claim sits on top of a genuinely destructive operation (stripping content from the project's own `CLAUDE.md`/`AGENTS.md`) with no actual safety net behind it — this is the guide's own named "trust comments over executable behavior" trap, not a stylistic nit.

## What to do

### General trim pass (`requires_architect_decision: false` — implementer judgment call, case-by-case)

| Finding | File(s) | What |
|---|---|---|
| `GO-STORE-009` | `internal/store/*.go` (~40 `CW-YYYYMMDD-NNNN` references across 25 files; ~23 files with `TASKS/*.md` path references) | Read each citation in context. Trim bare task-ID/path citations that add no rationale beyond "see this task." **Do not touch** citations that carry real invariant/safety rationale alongside the reference (e.g. `user_settings.go:262-264`, `store.go:80-93`, `agent_runtime.go:227-244` — all explicitly named by the audit as healthy, keep-as-is). When in doubt, keep the comment; this pass should err toward under-trimming.
| `GO-SVCCORE-009` | `internal/service/ingest.go`, `internal/service/known_tools_backfill.go`, `internal/service/cli_structured_input_fallback.go`, `internal/service/agent_deps.go` | Same trim discipline as `GO-STORE-009`, applied to this package's 15-50 line task-ID/PR-review-history comments. The audit explicitly notes each one "explains a real invariant rather than being pure filler" — this is "worth a future trim pass, not urgent," so lean conservative: shorten to the invariant plus a single pointer, don't delete the invariant itself.
| `GO-SVCEXEC-007` | `internal/service/chat_reflex_dispatch.go` (99-line package header), `internal/service/team_routing.go` (43-line priority-clamp justification) | Both comment blocks duplicate content that (by their own admission) also lives in the referenced task file's Work Log. This is a consistent house style across this half of `internal/service`, not an isolated lapse — trim each toward "the invariant, stated once, plus a pointer to the task file for full history," rather than repeating the task file's content inline.
| `GO-RUNTIME-006` | `cmd/nanite/main.go` (`cmdServe`), `internal/runtime/agent/agent.go` | `cmdServe` is roughly half comment-lines to code-lines — a systemic pattern in this cluster, not an isolated file. Same caution as everywhere else in this task: some of this content is genuinely load-bearing invariant documentation (the audit specifically calls out inline dependency-ordering rationale in `cmdServe` as real, not filler) — this is a maintainability observation for a future documentation-consolidation pass, not a mandate to strip comments. Apply the same conservative trim discipline as the other rows in this table; do not attempt a full architect-level documentation restructure as part of this task (see Non-goals).

### Needs an explicit decision, not a trim (`requires_architect_decision: true`)

| Finding | File(s) | What |
|---|---|---|
| `GO-SVCCORE-003` (medium — heavier than the rest of this batch) | `internal/service/install/adapters.go` (`snapshotAdapterTargets`, dead code), `internal/service/install/adapter_cleanup.go:31-33` (`cleanupRemovedAdapters`'s doc comment) | `cleanupRemovedAdapters` performs a real destructive operation (stripping/deleting Nanite-managed content from the project's `CLAUDE.md`/`AGENTS.md`). Its doc comment falsely claims "*Snapshot for rollback is handled by the existing `snapshotAdapterTargets()` pass that runs before cleanup*" — the audit traced the only production call path (`freshScaffold`) and confirmed **no such call exists**, and **no `Rollback` function exists anywhere in the repo** to consume a snapshot even if one were taken. `snapshotAdapterTargets` itself is separately confirmed dead code (zero production callers). **Decision needed:** either (a) wire `snapshotAdapterTargets` back into the `freshScaffold` path before `cleanupRemovedAdapters` runs and build the promised `Rollback` function, giving the destructive operation the safety net the comment already claims exists, or (b) correct the comment to state plainly that no rollback exists today, so a future reader isn't misled about the actual blast-radius/recovery story. Blast radius is bounded today (only strips Nanite's own previously-written managed markers, not arbitrary user content) — this mitigates urgency but not the correctness of the comment.
| `GO-CHAT-006` | `internal/chat/hint_catalog.go` | The package doc claims the loader reads `config/think-hints/hints.yaml`; the actual `//go:embed` directive embeds a **different** file, `internal/chat/hints/hints.yaml`. The two are currently byte-identical, but nothing — no build step, no generator, no code path — copies one into the other. An operator editing the file the doc comment points them to today would see zero runtime effect, silently. **Decision needed:** either (a) fix the doc comment to point at the real embedded path (`internal/chat/hints/hints.yaml`), accepting that `config/think-hints/hints.yaml` is not an operator-editable source of truth, or (b) if `config/think-hints/hints.yaml` is meant to be the actual editable source, add a generate/build step that copies it into the embedded location and keep the doc comment as-is. Either resolution removes the functional-confusion risk; leaving the mismatch as-is is not an acceptable outcome for this task.

## Non-goals

- Do not attempt a full architect-level documentation-consolidation pass (e.g. migrating history content wholesale to `docs/decisions/`) as part of this task — `GO-RUNTIME-006`'s own recommendation frames that as separate, future, architect-level work, not something this mechanical trim task should absorb.
- Do not expand the trim pass to files/comments not named in the findings above just because they look similar — this task's scope is the specific findings cited, not a repo-wide comment audit.

## Done means

- [ ] `GO-STORE-009`, `GO-SVCCORE-009`, `GO-SVCEXEC-007`, `GO-RUNTIME-006`: bare task-ID/path citations trimmed where they add no rationale; every comment the audit explicitly named as load-bearing (`user_settings.go:262-264`, `store.go:80-93`, `agent_runtime.go:227-244`, and any equivalent found during the pass) is left untouched or only lightly tightened, never gutted.
- [ ] `GO-SVCCORE-003`: decision made and recorded (wire the rollback, or fix the comment); implemented; if the comment-fix path is chosen, the corrected comment states plainly that no rollback exists today.
- [ ] `GO-CHAT-006`: decision made and recorded (fix the doc comment, or add a generate step); implemented; the mismatch between documented and actual embedded file no longer exists in either form.
- [ ] `go build ./...` and `go vet ./...` pass (comment-only changes shouldn't break anything, but `GO-SVCCORE-003`'s "wire the rollback" path and `GO-CHAT-006`'s "add a generate step" path both involve real code/build changes if chosen).

## Work log

<!-- Worker fills this in as it goes: what was trimmed vs. kept and why, and the two architect decisions once made. -->

## Review notes

<!-- Reviewer fills this in: spot-check that no load-bearing rationale was accidentally trimmed; confirm the GO-SVCCORE-003/GO-CHAT-006 decisions were actually implemented, not just recorded. -->
