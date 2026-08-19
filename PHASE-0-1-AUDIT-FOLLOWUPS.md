# Phase 0/1 Audit Follow-ups — 2026-08-18/19

**Reconciled 2026-08-19**: the open items above were reviewed and resolved in a planning session — P0.18/20 confirmed no action needed (projects/agent_projects intact). P0.10's real gap and Phase 1 #13's adapter-cut decision are now tracked as `TASKS/phase-2/05-freeze-internal-agent-profiles-on-reingest.md` and `TASKS/phase-2/06-cut-nanite-native-adapter-agent-sync.md`. The remaining outstanding Phase 1 items (`09-build-assignment-ui-api`, `11-wire-select-for-agent-to-read-agent-tools`) are relocated to `TASKS/phase-5/01-build-assignment-api.md` and `TASKS/phase-4/05-wire-select-for-agent-to-read-agent-tools.md`. See `TASKS/INDEX.md`'s Phase 2-9 sections for the current sequence.

Fresh doc, deliberately separate from `TASKS/INDEX.md`/`TASKS/ESCALATIONS.md`/task files. Those are considered best-effort, not authoritative — where this doc conflicts with them, this doc wins until reconciled. Written after a full independent re-verification of all 38 Phase-0 tasks (on `main`) and all 13 Phase-1 tasks (in the orphaned `phase-1-execution` worktree) against the actual code, not against Work Log claims.

Status: audit paused here for operator review. No further execution until this doc's open items are resolved and the process fix (below) is agreed.

---

## Needs your review/decision

### P0.10 — seed-builtin-agent-profiles: FICTION, unresolved
Work Log describes a detailed, tested fix (freeze `source=internal` profile content on re-ingest) that was never merged — traced to a paperwork-reconciliation commit (`cf1ac193`) written from a worker's isolated-worktree completion *report*; the worker's real commit never landed on `main`. `internal/service/ingest.go` has zero trace of it today. Live bug: builtin-agent DB customizations (including via the `agent_update`/`agent_create` self-tool path, which task 34 correctly closed) get silently wiped on every restart. Interesting cross-reference: Phase 1 task 08 independently re-solved almost the same "don't re-overwrite on boot" problem in the orphaned worktree — a real fix for this exists, just not on `main`.
**Needs:** decide whether to re-implement fresh, or pull the equivalent logic over once Phase 1 merges. Flagged by you as "needs review and discussion," not decided here.

### P0.18 / P0.20 — "Projects should exist"
You flagged this as "p0.18." The closest match in the audit is task 20 (retire-workspaces-and-instance-mechanism), whose decided scope was: drop `workspaces` + `workspace_role_trust`, **keep `projects`/`agent_projects` intact** — and that's what the code does. If you meant something inside 18a/18b (the dead-storage/dead-messaging cuts) specifically, neither of those touches `projects` — let me know and I'll re-check.
**Needs:** clarify which task you meant, then discuss in detail (not litigated here).

---

## Process issue to fix before resuming work

**"Code/callers exist" was repeatedly used as proof of the wrong question.** Concrete pattern, several instances today:
- P0.18b: `trigger_rules`/`custom_actions` had a live, router-registered REST API — that was treated as reason to reconsider the cut, when the real question (is this *wanted*, not *reachable*) still pointed to cutting them. Decided correctly in the end, but the reasoning step conflated "exists and works" with "should stay."
- P0.15a/b/c (giphy/oembed/support-ticket): same conflation in reverse — "confirmed demos" was the original (wrong) characterization; they were real, working features. Existing-and-working ≠ wanted, in either direction.
- P0.18a: `agent_boot_plans` — the *absence* of an obvious caller path was read as proof of deadness, when a full CRUD+API+UI stack existed with just no real traffic through it.

All three ended at the right decision, but only after an escalation caught the reasoning error — the task files' own first-pass verification method doesn't reliably distinguish "does this code run" from "should this code exist." **Needs:** a sharper verification standard before Phase 2 resumes, discussed with you before any more tasks are dispatched.

---

## Resolved tonight (direct code verification, not Work Log claims)

- **P0.11 (cut-strategy-planner) — "reaper replacement," clarified.** Two unrelated things share the word "reaper" in this codebase: the **subagent reaper** (`internal/subagent/reaper.go`, governs dispatched `subagent_runs`) and the **chat engine's own `AgentConstraints.HardCeiling`/`IdleTimeoutSeconds`** (`internal/chat/engine.go`, governs one agent's turn loop — that's item 12, not 11). The "replacement" claim is about the *subagent reaper*: commit `d92d8cf` (Aug 15, three days before this review started, unrelated to it) genuinely shipped an activity-reset inactivity timer + a separate non-resetting hard ceiling for stalled subagent runs. Strategy planner's `MaxTurns` output was a cruder, turn-count-based version of the same "don't let this run forever" concern — since the reaper's wall-clock/activity-based mechanism already covers that concern independently, cutting strategy planner's redundant version doesn't leave a gap. Nobody proposed changing or replacing the reaper itself — it's pre-existing infrastructure the decision leans on, not a discussion that happened this week.

- **P0.21 (cut-modes) — Agent Broker was not removed and is not scheduled for Phase 0.** It's real, live code in `internal/service/chat_broker_dispatch.go` (`attemptBrokerDispatch`, `persistAgentBrokerDecision`) — distinct from Skill Broker and Tool Broker (both cut in P0.22) and from Recovery Broker (`internal/recovery/broker`, renamed in P0.32). P0.21's actual scope was narrower than "remove agent broker": stub its two Mode-dependent rules (now permanently no-op since `Mode` is always empty post-Modes-cut) so it keeps compiling. The decision log (§10) proposes consolidating Agent Broker into `internal/agent/reflexes` — but that's explicitly Phase 3, "agreed in principle, concrete design still to be worked out," not yet started. This also confirms P0.23's decision to keep `agent_broker_decisions` alive was correct — Agent Broker is still running and still writing to it (`persistAgentBrokerDecision`, verified live).

- **P0.24 (housekeeping-agent-profile-files), plain-English.** Two stale custom agent definition files (`.nanite/agents/agridd-project-manager.md`, `proxima.md`) get deleted; a third (`torque-task-writer.md`) is confirmed good and stays. No "extraction" involved — that word belongs to P0.27, not this one.

- **P0.25 (drop-unused-session-status-enum), plain-English.** `sessions.status`'s CHECK constraint allowed values (`sleeping`/`halted`/`terminated`) that no code path ever actually writes — leftover vocabulary copied from a different table's status enum. The fix narrows the CHECK constraint to only the values real code uses. The separate, real `halted_at`/`halted_reason` columns are untouched. No "extraction" here either.

- **P0.29 (cut-prompt-templates) — appears genuinely done, not just claimed.** Real commit on `main`: `475f0bb5`, "relocate compaction-disclosure content, cut prompt_templates/agent_prompt_templates." Migration `110_drop_prompt_templates.sql` exists. `internal/chat/universal_rules.go` is the real replacement (the single hardcoded universal disclosure message). The only remaining references to `prompt_templates`/`PromptTemplate` in the codebase are explanatory comments describing what was removed, not leftover functionality. Your recollection that this was pushed back for being larger is plausible as the *initial* Wave-1 scheduling call — the evidence says it was picked back up and finished later the same session, not skipped. Flag if this still doesn't match what you remember; the evidence here is concrete enough that I'd want to see something specific before treating it as suspect.

- **P0.32 (rename-recovery-namespace) — confirmed real, all four pieces, low risk.** Single commit `df08da8b`. All four recovery mechanisms named in the decision log (§25) are genuinely regrouped under `internal/recovery/*`: `internal/recovery/broker` (Recovery Broker), `internal/recovery/orphansweep` (Orphan Sweep), `internal/recovery/pack` (Recovery Pack), `internal/recovery/interrupted_turn.go` (interrupted-turn detection). This is exactly what TASKS.md #32 and decision log §25 called for — mechanical package regrouping, not a design change. The earlier audit disagreement (MATCH vs. "landed at a different path than documented") looks like it was checking the task file's own prose against itself, not against the decision log — the decision log never specified an exact target path, only "e.g. `internal/recovery/*`," which is exactly where it landed.

---

## Still unresolved from the original audit (not new tonight)

- **P0.7** — real fix landed via a different function name (`AddBuiltinServer`) than the task file documents (`IsFirstPartyBuiltinServerName`). Same outcome, cosmetic doc drift only.
- **P0.8 (a2a-conformance)** — method names + `CancelTask` done. The decision required *either* a real ticker for `A2APushNotifier.ProcessPendingDeliveries` *or* an explicit documented scope-out of push notifications for v1 — neither happened. Still silently half-wired, which is the specific state the decision said not to leave it in.
- **Migration numbers 106–111 collide** between `main` (Phase-0 drops) and `phase-1-execution` (Phase-1 adds) — real, confirmed twice independently, not yet triggered because nothing's merged.
- **Phase 1 #08 (kill-file-reingest-on-boot)** — shipped once on a scope that contradicted the architecture doc's actual text, caught by you twice, redone (Round 2). Final state matches now.
- **Phase 1 #13 (nanite-native adapter / `.nanite/config.yaml`)** — real contradiction with `GLOSSARY.md`'s stated model, logged, explicitly parked for your review — not fixed blind.

---

## Phase 0 / Phase 1 merge status (plan, not executed)

`phase-1-execution` (worktree) and `main` diverged at `f2d2114b`. Neither has the other's work:
- `phase-1-execution`: 41 unique commits — real, code-verified Phase 1 work (8 of 13 tasks implemented).
- `main`: 5 unique commits since the same point — Phase-0 cleanup that landed directly on `main` after the branches split.

This needs a real merge (not a fast-forward) plus manual migration renumbering for the 106–111 collision before it can land. Not attempted tonight — this is tomorrow's first real task once the open items above are settled.

`TASKS/phase-1/` on `main` (10 files, all stale "not-started") and the worktree's own expanded `TASKS/phase-1/` (13 files, real statuses) need to be reconciled into one source of truth as part of the same merge — don't hand-edit either copy separately before then.

---

## Worktree / stash cleanup

- **10 `agent-*` worktrees** under `.claude/worktrees/` — **done, 2026-08-19.** Confirmed fully merged into `phase-1-execution`, git-clean, no unique content. Worktrees removed (`git worktree remove`) and their 10 `worktree-agent-*` branches deleted (`git branch -D`).
- **`phase-1-execution` worktree** — kept, on purpose. Do not delete; it currently has no remote, so its 41 commits exist only in this one local worktree, and it hasn't merged into `main` yet (see "Phase 0 / Phase 1 merge status" above).
- **`stash@{0}` (cross-worktree collision entry) — recovered and dropped, 2026-08-19.** All other agent sessions were shut down before this audit, so there was no live owner to pop it back to. Investigated instead: it's a WIP attempt at Phase 0 task 16 (cut-external-agent-import) from a worktree (`worktree-agent-a3acbe819c9fafc10`) that no longer exists. Diffed its base commit (`25519f1a`, not an ancestor of `main`) against `main`'s current `adapter-{claude,codex,gemini,opencode}/plugin.go` — **byte-identical result**. Confirmed fully redundant with what already landed, not unique work. Dropped.
- Other stash entries (`stash@{1}` through `stash@{6}` after the drop) are older, unrelated WIP from before this session — not touched, not in scope of this audit.
