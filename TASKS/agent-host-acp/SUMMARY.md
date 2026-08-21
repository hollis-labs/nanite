# Agent Host + ACP — Batch Summary

**For:** the operator. This batch is closed — all 23 tasks (`01`-`22`, including `05a` and the
five dogfeed-found bug fixes `18`-`22`, plus `23`) are `reviewed`/`implemented` in
`TASKS/INDEX.md`. Task `23` was added after this doc's first draft, same day, to close a real
gap the doc-writer pass itself surfaced (see below).
Implements `docs/engineering/architecture/16-agent-host.md` (adopt `go-agent-wrapper` as
Nanite's shared agent-launch host) and `docs/engineering/architecture/17-acp.md` (add
ACP-as-client support). See `TASKS/agent-host-acp/HANDOFF.md` for the technical
handoff to whoever picks up related follow-on work next; this doc is the plain-language,
what-shipped-and-what-needs-your-attention version.

---

## What shipped, by subsystem

### The host migration (Nanite's own agent-launch code)

Nanite's bespoke agent-launch code (`internal/runtime/agent`) now runs on top of a shared
library, `go-agent-wrapper`, instead of hand-rolled code duplicated across the portfolio. Boot
directory planting (CLAUDE.md/AGENTS.md/.mcp.json/provider settings), sandbox profile
construction, and session start/stop/input all route through the library now. This was
verified — not just built — via a real dogfeed against live Claude, Codex, and OpenCode
subprocesses, including deliberately killing a live process to confirm the crash-recovery
system still reacts correctly (it does).

Along the way, this dogfeed found and fixed **five real bugs**, three of which were 100%-
reproducible, hard failures for real users before this batch started:
- Every real OpenCode session crashed on its first turn (`Config.Workdir is required`).
- Every real Codex session failed on its first turn (missing a required CLI flag).
- The crash-recovery system silently never reacted to a killed CLI process — a direct hit on
  the single biggest named risk in the original architecture review for this work.

All five are fixed, live-verified, and closed. See `TASKS/ESCALATIONS.md`'s 2026-08-21 entries
("Task `07` real dogfeed found four real bugs...", "Task `06` re-attempt landed...", "Phase 2
whole-section fresh review") for the full record.

### ACP — Nanite as a client driving other agents' CLIs

New capability, built on top of the migrated host: Nanite can now drive an agent CLI over the
Agent Client Protocol (ACP) instead of that CLI's own bespoke wire format — for two providers
that speak ACP natively (OpenCode, GitHub Copilot CLI) today, and three more (Claude, Codex, Pi)
that don't natively but can be reached through a small bridge process. All five were built and
verified against real, live agent processes/APIs, not mocks.

This is additive, not a replacement — every existing agent keeps working exactly as before
unless someone explicitly configures it to use ACP.

Claude/Codex/Pi's ACP support is real and verified at the underlying library level, and — as of
task `23`, closed same-day — is now wired into Nanite's own dispatch too. Claude and Codex are
live-verified end-to-end through Nanite's own API today. Pi's wiring is done and unit-tested but
blocked from launching by one small, separate, pre-existing gap (see "Still flagged" below).

---

## Every escalation this batch raised, and how it resolved

All entries below are in `TASKS/ESCALATIONS.md`, dated 2026-08-21 unless noted. Cited by title
so the full detail can be looked up rather than re-explained here.

- **"Agent Host + ACP planning: which ACP bridge library to pin..."** — logged as genuinely
  open at planning time, per the architecture doc's own explicit framing. Resolved later by
  task `12`, see below.
- **"Agent Host + ACP Phase 1 review: `legacyRuntimeToken`'s empty-Descriptor zero-value
  mirrors a different string..."** — a narrow, non-blocking code-review finding in the sibling
  library. Judged not a real defect (affects no call site that has ever existed). Phase 1
  stands as reviewed, PASS.
- **"Task `05` (sandbox.Applier migration): `Applier.Apply(ctx, pid)` confirmed to be a true
  post-spawn attach mechanism..."** — the planned migration target didn't fit. Resolved: task
  `05` closed with zero code changes; an existing, different library seam already solved the
  problem, and the trivial call-site change was folded into task `06`.
- **"Task `06` (migrate session lifecycle to `wrapper.Wrapper`): `Wrapper.Run` is non-functional
  for all three of go-agent-wrapper's own real adapters..."** — a genuine blocker found by the
  worker, correctly stopped rather than forced. Presented to the operator with three options;
  operator chose to land a small prerequisite fix in the sibling library (task `05a`) rather
  than descoping. Resolved, both tasks closed clean.
- **"Task `06` re-attempt landed and reviewed PASS... two real, non-blocking findings"** — two
  small cleanup items found on review (a dead config field with a stale doc comment, one error
  path missing its failure reason). Both fixed as a small follow-up rather than left as debt.
- **"Task `07` real dogfeed found four real bugs (`18`-`21`) — operator sign-off on
  cross-portfolio `agentkit` fix scope"** — one of the four fixes (task `20`) changes behavior
  inside a library with six consumers across the whole portfolio (Nanite, Tether, Torque, and
  three others), not just this app. **Operator approved fixing it now, with real care**
  (real-subprocess regression tests, a version bump, an explicit changelog flag for other
  consumers) rather than deferring or scoping it down. A fifth bug (task `22`) was found while
  verifying the fourth and fixed under the same operator-approved terms. All five confirmed
  working end-to-end in a final re-run.
- **"Critical process finding, before this batch's fixes could be trusted..."** — not an
  escalation in the design-decision sense, but a real process gap: sibling-repo fixes that
  tested clean in isolation weren't actually reaching Nanite's compiled binary, because of how
  Go module `replace` directives work across this monorepo. Caught and fixed before Phase 2 was
  declared done, per the operator's own direction to ensure the sibling libraries were properly
  pushed, tagged, and released.
- **"Task 11 review: FAIL — two real, unflagged event-translation bugs..."** — a fresh review
  caught two real bugs the worker's own otherwise-diligent Work Log hadn't flagged (reasoning
  text leaking into visible answers; crashed ACP sessions not reaching crash-recovery). Fixed
  and re-reviewed PASS.
- **"Task 12 resolved: ACP bridge library decision, explicit operator sign-off recorded"** — the
  one design decision in this batch that genuinely needed your input, not just research. You
  reframed the deciding question mid-discussion (interrupt capability wasn't actually the
  blocker; protocol uniformity for future extensibility was the real goal) and chose three
  focused, per-provider bridges over one dormant multi-provider alternative, as an explicitly
  reversible choice. Recorded in full, including your reasoning, in that entry.
- **"Task 16 complete: no in-process fs/terminal server needed"** — a real audit, cleanly
  negative: none of the five ACP-driven agents need Nanite to serve filesystem/terminal work on
  their behalf. One small, optional, non-blocking robustness gap found in one adapter (Copilot
  CLI), not fixed (blocked by a real account quota limit during testing, and judged genuinely
  optional).
- **"Task 17 complete: real native-vs-ACP comparison — `TASKS/agent-host-acp` batch fully
  closed"** — the closing verification task. Found a real, useful difference (native Claude
  doesn't stream token-by-token; ACP-bridged Claude does) without making any decision about
  which should be the default going forward — that's explicitly left for later, per-agent, as
  the architecture doc always intended.

---

## Still flagged, deferred, or needing your attention

1. **Closed same-day: Claude/Codex/Pi are now wired into Nanite's own ACP dispatch (task `23`).**
   This was discovered while preparing this summary — a real, checkable gap not called out
   anywhere in this batch's own tracking. Rather than leave it as a footnote, it was closed
   immediately: Nanite's `go-agent-wrapper` pin bumped to `v0.8.1` (task `23`'s own fix also
   found and fixed a second bug — the `v0.8.0` tag itself was cut on the wrong branch and never
   actually contained the Claude/Codex adapters), and `newACPClient`/`acpSupportedProviders`
   extended to all five providers. Claude and Codex are live-verified end-to-end through
   Nanite's own harness API. **One small remaining follow-up**: Pi's dispatch wiring is correct
   but unreachable — `cmd/nanite/main.go`'s `cliAdapters` list has no `"pi"` entry (a separate,
   pre-existing gap unrelated to ACP), so a Pi agent fails before it ever reaches the ACP
   dispatch code. Full detail in `HANDOFF.md`'s "A real gap discovered..." section.
2. **A minor, optional robustness gap** in Copilot CLI's ACP adapter (`copilotacp`) — it answers
   a specific server request type with a generic error instead of a graceful decline, unlike
   its four sibling adapters. Low risk, not live-verified due to a real account quota limit
   during testing, not blocking anything.
3. **Whether/how Tether or Torque adopt `go-agent-wrapper`** is a real, open, portfolio-level
   question — explicitly out of this batch's scope per its own plan. This batch only proves the
   library works well for Nanite; it doesn't decide anything about the other two apps.
4. **Two pre-existing, unrelated test-infrastructure issues**, confirmed not caused by this
   batch, never fixed (out of scope): a flaky test in the sibling `go-agent-wrapper` repo's own
   test suite, and a test-suite timeout in Nanite's `internal/service` package under combined
   `-race` testing. Both worth small standalone follow-up tickets at some point, neither
   urgent.
5. **The rejected bridge library** (`beyond5959/acp-adapter`) — you rejected it for real,
   documented reasons (4+ months dormant, a source-verified worse fallback behavior for Claude,
   weak Pi support). That decision is explicitly reversible if the library matures, per your own
   framing at the time — nothing further needed unless you want to revisit it.

---

## Current `TASKS/INDEX.md` state for this batch

All 23 tasks done — 0 in-progress, 0 blocked, 0 not-started. Breakdown by phase:

| Phase | Tasks | Status |
|---|---|---|
| 1 — Host foundation | `01`, `02` | both reviewed |
| 2 — Nanite host migration | `03`, `04`, `05` (closed, no migration), `05a`, `06`, `07`, `18`-`22` | all reviewed/implemented; Phase 2 marked `validated` after the full re-run dogfeed |
| 3 — ACP client abstraction & native adapters | `08`, `09`, `10`, `11` | all reviewed (task `11` reviewed PASS after one fix-and-re-review round) |
| 4 — ACP bridge adapters | `12`, `13`, `14`, `15` | all reviewed; `12` resolved with your explicit sign-off |
| 5 — Verification & hardening | `16`, `17` | both reviewed; task `17` closes the batch |
| — Post-handoff fix | `23` | implemented — wires Claude/Codex/Pi into Nanite's own ACP dispatch, closing the gap found while drafting this summary |

No task in this batch is open or blocked. One small, unrelated follow-up remains (Pi's
`cliAdapters` registration, see "Still flagged" above) — not a blocker for anything shipped.
