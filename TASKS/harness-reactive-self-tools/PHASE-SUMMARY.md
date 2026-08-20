# Phase Summary — Harness-Reactive Self-Tools batch

Implements `docs/engineering/architecture/11-harness-reactive-self-tools.md`, the design produced by the dedicated design session `TASKS/reflex-taxonomy/07-harness-reactive-self-tools-design-session.md` (2026-08-20, operator-signed-off, no code changed). Not part of the Phase 0-9 sequence — a sibling to `TASKS/reflex-taxonomy/`, tracked in its own `TASKS/harness-reactive-self-tools/` folder, kept separate because this mechanism is deliberately *adjacent* to Reflexes rather than a Reflex action kind: a self-tool call is its own trigger, with no predicate/event/interval for a reflex evaluation loop to watch.

## What shipped, by subsystem

**Package reorganization — `internal/selftools`.** Self-tool definitions and dispatch moved out of `internal/mcp` in full, into a new top-level package. This was a pure move (no behavior change, verified by the reviewer via a byte-for-byte diff against a normalized reversal of every claimed rename/export), but touched far more surface than originally scoped — 47 files, not the 16 the task was estimated at, once a more thorough sweep found free-function files and a stray test file the original inventory grep missed. A handful of general MCP-transport helpers had to be exported to keep the split compiling; nothing was duplicated.

**New reactive-layer schema.** Two new database tables (`selftool_reaction_kinds`, a 4-row lookup; `selftool_reactions`, per-tool config rows) let a self-tool declare, in the database, what should happen when it's called — render a card, call an internal endpoint, or (reserved for later) call an external endpoint or invoke a callback. This is schema for the *reactive layer only* — the underlying ~70 self-tool definitions themselves stay exactly where they are, in Go.

**The reaction engine.** A new `internal/selftools/reactions` package resolves a fired self-tool's configured reactions. It executes internal API calls directly and synchronously; for card rendering, it can only *resolve* the card's data (it structurally cannot reach the live chat UI from where it sits — the same import-boundary constraint that shaped how the reflex system's `halt_session` fix worked), handing that back to the calling self-tool to actually deliver. This resolve-vs-deliver split, and the constraint driving it, held up under direct verification, not just assumption.

**Card delivery, unified.** The piece of code that turns a resolved card into the marker string the chat UI already knows how to render was built to match the existing mechanism byte-for-byte, so all three places that already watch for that marker pick up a self-tool's card with zero changes to any of them.

**Telemetry.** Every fired reaction — successful, failed, or a reserved-but-not-yet-executable kind someone enabled by hand — now writes one trace record, on a new, clearly labeled telemetry stream separate from the reflex system's own.

**A related cleanup.** Three separate, independently-written implementations of the same "find this marker in this text" logic (one each in the chat engine, the HTTP API layer, and the CLI-agent bridge) were collapsed into one shared implementation, with the two genuinely different jobs they were doing (read-the-first-marker vs. replace-every-marker) preserved as two thin, correctly-named entry points on top of the shared logic. Zero pre-existing tests needed to change.

**Worked example, now live.** `task_update_report(id, msg)` is a new self-tool — deliberately minimal, no logic of its own beyond validating its two inputs — that proves the whole mechanism end to end. Calling it fires a card and a real HTTP call to a small demo endpoint, both configured entirely in the database rather than hardcoded in the tool's own code. This is now wired into the running service (`cmd/nanite/main.go`), not just tested in isolation — the first real, live consumer of everything else this batch built.

## What got escalated, and how it resolved

One escalation from this batch, `TASKS/ESCALATIONS.md`'s final entry (2026-08-20, "Log integrity: Orchestrator's own commit split for a live security fix misdescribes its own diff"):

While verifying the worked example, the Orchestrator found a genuine, if narrow, security gap in a first draft — a new demo HTTP route's auth exemption didn't actually have the same loopback-only restriction its own code comment claimed to match. That was found and fixed before anything was committed; the fix landed in the very first commit for that work, and the route was never reachable without the protection at any point in git history. Separately — and this is what the escalation entry is actually about — when the Orchestrator later split that already-fixed work into two commits for a cleaner-looking history, it did the split by file rather than by chronology, which produced a misleading pair of commit messages: one commit silently contained the real fix without saying so, the other's message described adding the fix but its actual contents were just a test file. The fresh reviewer caught this discrepancy independently (by reading the actual commits, not trusting either message), and it was corrected directly in the task file rather than by rewriting git history. **Net effect: the running code was correct and safe the entire time; only the commit-message narrative about exactly when the fix landed was wrong, and that's now been corrected in the written record.**

No other escalations were raised during this batch. (`TASKS/ESCALATIONS.md`'s other 2026-08-20 entry, about `ApprovePendingReflex` bypassing a provenance gate, belongs to the separate `reflex-taxonomy` batch's Phase 2 review, not this one — noted here only to avoid confusion since the two entries are dated the same day and sit next to each other in the log.)

## Anything still flagged, deferred, or needing attention before further work

- **The design docs this batch (and its `scheduling` sibling) depend on are not yet committed to git.** `docs/engineering/architecture/11-harness-reactive-self-tools.md` (this batch's own spec), `12-scheduling.md`, the whole `TASKS/scheduling/` folder, and two orchestrator-kickoff docs exist only as untracked files in the main checkout. This wasn't a one-off inconvenience — at least three separate workers across this batch independently hit the same problem (an isolated git worktree can't see uncommitted files, even ones sitting right there in the same repo's main checkout) and each had to work around it the same way, by reading the file directly off disk rather than through git. **Recommend committing these design docs** before the next batch of work starts, so this stops recurring.
- **A real, cross-batch migration-number collision is already flagged and waiting.** This batch's schema migration claimed and landed as `126_selftool_reactions.sql`. The still-not-started `scheduling` batch's own first task independently claims the same number for a different migration — its own task file already anticipates this and instructs whoever dispatches it to renumber. Concretely, that task now needs to move to `127` when it's picked up; nothing needs fixing on this batch's side.
- **What was deliberately not built, per this batch's own scope fence** (see the batch's `README.md`, "What this batch does NOT do"): no real auth/retry/idempotency for the two reserved-but-unimplemented reaction kinds (external API calls, callbacks); no authority/provenance restrictions on who can register a reaction (nothing needs it yet, since no plugin registers a self-tool today); no real integration for the worked-example tool — it stays a proof-of-shape, not wired to any actual todo/plan system; and a documented naming-convention wording revisit that's explicitly the operator's to pick up separately, not built here.
- This batch's code itself is fully committed to local git history (unlike the `reflex-taxonomy` batch, which is still sitting uncommitted in the working tree) — it just hasn't been pushed to the remote yet (`git status` shows local `main` 19 commits ahead of the tracked remote). Worth pushing when convenient; not blocking anything.

## Current `TASKS/INDEX.md` state for this batch

Per `TASKS/INDEX.md`'s "Harness-Reactive Self-Tools" section (lines 333-349):

| Task | Phase | Status |
|---|---|---|
| `01-move-self-tools-to-internal-selftools` | 1 | reviewed |
| `02-reactive-layer-schema` | 1 | reviewed |
| `03-reaction-engine-core` | 1 | reviewed |
| `04-render-card-construction` | 1 | reviewed |
| `05-selftool-reaction-telemetry` | 2 | reviewed |
| `06-collapse-envelope-marker-consumers` | 2 | reviewed |
| `07-worked-example-task-update-report` | 2 | reviewed |

All 7 tasks `reviewed` and closed — none in-progress, none blocked, none parked. Confirmed by two independent fresh-reviewer passes (Phase 1: `01`-`04`; Phase 2: `05`-`07`), each with no shared context with the implementing workers, plus the Orchestrator's own build/vet/test verification and live dogfeeds at each checkpoint. Per `TASKS/INDEX.md`'s own summary line for this section: "Batch complete, all 7 tasks reviewed clean (2026-08-20)."
