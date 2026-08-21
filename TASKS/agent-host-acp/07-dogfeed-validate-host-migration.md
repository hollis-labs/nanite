# Dogfeed-validate the full host migration

**Phase:** 2 — Nanite host migration (`TASKS/agent-host-acp`)
**Status:** not-started
**Depends on:** `04`, `05`, `06`
**Touches:** none (validation-only task — no code changes expected unless it finds a real
bug, in which case follow the fix-as-new-worker-task discipline per `EXECUTION-PROCESS.md`
rather than patching inline). Repo: Nanite.

## Context

16-agent-host.md names "zero production mileage" as a real, named risk of adopting
go-agent-wrapper: "`Wrapper.Run`'s dispatch path, the activity bridge, and TurnID tracking are
unvalidated against real session volume or against `internal/recovery/broker`'s existing
agentkit-based classifier/dispatch logic." Tasks `04`-`06` migrate the code; this task is the
Orchestrator-level "actually exercise the feature" checkpoint `EXECUTION-PROCESS.md`'s
Validation checkpoints section requires before a section can be marked `reviewed` — this
batch is the **first real production exercise of `go-agent-wrapper` anywhere in the
portfolio** (confirmed zero adopters at planning time), so this checkpoint carries more
weight than the same step does in most other batches.

Per `docs/engineering/standards/testing.md`'s "dogfeed it, don't just trust a green test
suite" discipline (the same discipline every other batch in `TASKS/` has followed at its own
Phase-boundary validation step).

## What to do

1. Launch a real session for each of Claude, Codex, and OpenCode through the migrated
   `wrapper.Wrapper`-based path (task `06`) — a real subprocess, not a mock — and confirm: the
   boot directory is planted correctly (task `04`'s migration — diff the actual planted files
   against pre-migration output for at least one provider), the sandbox profile applies
   correctly (task `05`'s migration), a real turn completes and activity/streaming events
   reach the chat surface correctly, and `Session.Stop()` cleanly terminates the process
   (confirming the carried-forward SIGTERM/SIGKILL-only behavior still works, per task `06`'s
   Context — not attempting to prove mid-turn interrupt, which doesn't exist).
2. Force a real session failure (e.g. an idle timeout, or a deliberate process kill) and
   confirm `internal/recovery/broker`'s classifier/remediator/dispatch chain still correctly
   classifies the exit and (if configured to) dispatches a replacement session — this is the
   direct check on 16-agent-host.md's named broker risk.
3. Confirm no regression against pre-migration behavior for anything not in scope for this
   batch — a full `go test ./...` pass plus at least one full chat session exercised through
   the running app (not just an isolated unit test), matching this project's own
   `cerberus_resource_deploy`/`cerberus_resource_reload` real-deployment verification pattern
   used by other batches' Validation checkpoints.
4. If this surfaces a real bug: do not patch it inline. Write it up as a new task file (fix-
   as-new-worker-task discipline, matching every other batch's precedent — e.g.
   `TASKS/phase-4/09`, `TASKS/reflex-taxonomy/08`/`09`) and dispatch it before continuing to
   Phase 3.

## Done means

- Real (not mocked) end-to-end session launch/activity/stop verified for all three providers.
- Real forced-failure classification/remediation verified against `internal/recovery/broker`.
- Full `go test ./...` clean; no unexplained regression against pre-migration behavior.
- Any real bug found is written up as its own task file, not silently patched.
- This section (Phase 2) is marked `validated` in `TASKS/INDEX.md` once this passes, ready for
  a fresh Reviewer dispatch before Phase 3 starts.
