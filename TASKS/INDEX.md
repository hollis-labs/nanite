# Execution Index

Live tracker for `docs/engineering/TASKS.md`'s execution, per `docs/engineering/EXECUTION-PROCESS.md`. Phase 0 is planned in full below (34 task files). Phases 1-6 are index-level summaries only, per the operator's scope decision for this planning pass — they get their own detailed task-file breakdown in a follow-up planning session, once Phase 0 lands and (for Phases 3-6 especially) their still-open design questions resolve.

**Process note**: 30 of Phase 0's task files were drafted by a research fork that exceeded its authorized scope during planning — see `TASKS/ESCALATIONS.md`'s first entry for full disclosure. Every file was individually read and audited against this session's own independent research before being included here.

**Update, 2026-08-18**: the operator resolved every open escalation from the initial plan presentation (full detail in `TASKS/ESCALATIONS.md`) and sharpened the escalation rule in `EXECUTION-PROCESS.md`/`ORCHESTRATOR-KICKOFF-PROMPT.md` — `TASKS.md`'s decided action stands even when a decision-log rationale turns out to be wrong; default to cut on genuinely undocumented items; real stop-and-escalate is reserved for security/trust/data-integrity-sensitive or hard-to-reverse cases. Several task files were updated accordingly: `05` inverted from a build to a removal, `15` unblocked and split into three concrete removal tasks (`15a`/`15b`/`15c`), and `18b`/`20`/`22`/`33` had their conditional/escalation language replaced with final, direct instructions. Phase 0 is now fully unblocked and in execution (Phase B).

---

## Phase 0 — task table

| Task | Status | Depends on | Wave |
|---|---|---|---|
| 01-fix-model-pinning | not-started | none | 1 |
| 02-fix-request-start | not-started | none | 1 |
| 03-fix-callertype-mistagging | not-started | none | 1 |
| 04-build-http-provider-retry | not-started | none | 1 |
| 05-remove-ollama-routing | not-started | none | 1 |
| 06-enable-tools-lazy-load | not-started | none | 1 |
| 07-harden-builtin-server-check | not-started | none | 1 |
| 08-a2a-conformance | not-started | none | 1 |
| 09-adopt-goose-migrations | implemented | none | 0 |
| 10-seed-builtin-agent-profiles | not-started | none | 1 |
| 11-cut-strategy-planner | not-started | none | 3 (chain pos. 1) |
| 12-cut-agentconstraints-maxturns | not-started | 11 | 3 (chain pos. 2) |
| 13-cut-question-form | not-started | none | 1 |
| 14-verify-messaging-card-types | not-started | none | 1 |
| 15a-cut-giphy | not-started | none | 1 |
| 15b-cut-oembed | not-started | none | 1 |
| 15c-cut-support-ticket | not-started | none (coordinate w/ 15a on `giphy-modal` card type) | 1 |
| 16-cut-external-agent-import | not-started | none | 1 |
| 17-cut-role-skills-legacy | not-started | 10 | 2 |
| 18a-cut-dead-storage-and-config | not-started | none | 1 |
| 18b-cut-dead-messaging-and-plugin-tables | not-started | none | 1 |
| 19-cut-legacy-rename-tables | not-started | 09 | 1 |
| 20-retire-workspaces-and-instance-mechanism | not-started | 28 (orchestrator-added, see below) | 3 (chain pos. 8) |
| 21-cut-modes | not-started | 28 (orchestrator-added); coordinate w/ 18a on `agents.go` | 3 (chain pos. 7) |
| 22-remove-skill-and-tool-broker-abstractions | not-started | (sequenced after 29, see below) | 3 (chain pos. 10) |
| 23-export-and-drop-decision-tables | not-started | 11, 22 | 3 (chain pos. 11) |
| 24-housekeeping-agent-profile-files | not-started | none | 1 |
| 25-drop-unused-session-status-enum | not-started | 26 | 3 (parallel with chain, after pos. 4) |
| 26-cut-session-compaction-summary-fields | not-started | (sequenced after 27, see below) | 3 (chain pos. 4) |
| 27-cut-p7-scratchpad-snapshot | not-started | none | 3 (chain pos. 3) |
| 28-cut-session-intent-classifier | not-started | (sequenced after 27, see below) | 3 (chain pos. 5) |
| 29-cut-prompt-templates | not-started | none (see Context; sequenced after 21) | 3 (chain pos. 9) |
| 30-cut-templates-table | not-started | none | 1 |
| 31-rename-pty-naming-scrub | not-started | (all of Phase 0, see below) | 4 |
| 32-rename-recovery-namespace | not-started | 04 | 3 (chain pos. 6) |
| 33-rename-volon-eradication | not-started | none | 1 |

## Parallelization plan

**Wave 0 — solo.** `09-adopt-goose-migrations` rewrites the entire migration mechanism. Nothing else is strictly blocked on it (every migration-adding task is written to adapt its format either way), but it's high-blast-radius enough to isolate rather than run alongside a dozen other tasks touching the same migrations directory.

**Wave 1 — broad parallel batch, worktree-isolated.** `01, 02, 03, 04, 05, 06, 07, 08, 10, 13, 14, 15a, 15b, 15c, 16, 18a, 18b, 19, 24, 30, 33`. Independent subsystems (provider config, durable-agent lifecycle, MCP/A2A, Cards cuts, dead-plugin cuts, dead-code sweeps, PTY-adjacent renames). Merge-coordination notes, not hard blocks:
- `02` and `03` both touch `internal/service/durable_agents.go`, different functions (`RequestStart` vs. `deliverWakePrompt`) — merge sequentially, re-run tests after each.
- `19` needs `09` (Wave 0) done first — already reflected as a dependency, included here since Wave 0 precedes Wave 1.
- `15a` and `15c` both touch card-type registration (`giphy-modal` belongs to `15a`; `ticket-form`/`ticket-confirmation`/`resolution-capture`/`kb-result` to `15c`) — confirm the mapping doesn't overlap during merge, per each file's own coordination note.
- `15a` and `15b` both may touch `docs/audits/2026-04-11-telemetry-privacy-posture/07-low-oembed-and-giphy-outbound.md` — whichever merges first edits it; the second checks the first's Work Log rather than re-editing blind.

**Wave 2.** `17-cut-role-skills-legacy` — needs `10-seed-builtin-agent-profiles` done first (both touch `internal/service/ingest.go`'s ingest pipeline). Runs alone or alongside anything else that becomes unblocked; nothing else currently depends solely on Wave 1.

**Wave 3 — Steering & Session-Lifecycle Core. Strict serial chain, not parallelized internally.** This is the most tangled cluster in Phase 0 — `chat_generate.go`, `internal/api/sessions.go`, `internal/chat/context_client.go`, and `internal/store/agents.go`'s `DeleteAgent` cleanups slice are each touched by four or more tasks in this group. Rather than attempt fine-grained pairwise parallelization, these run in one dependency-respecting sequence:

```
11 → 12 → 27 → 26 → 28 → 21 → 20 → 32 → 29 → 22 → 23
              ↳ 25 branches off after 26 (needs 26's sessions-table
                rebuild done first; touches no Go code the rest of
                the chain modifies, safe to run alongside 28 onward)
```

Rationale for the additions beyond each file's own stated "Depends on":
- `27` before `26`: both edit `handleCompactSession` in `internal/api/sessions.go` (different fields, ~10 lines apart) — sequencing avoids a needless merge conflict.
- `28` before `21` and `20` (**orchestrator-added dependency, not stated in either task file**): `21` deletes `internal/store/modes.go` (the `store.Mode` type) and `20` drops `sessions.workspace_id`; `28`'s `internal/service/session_intent.go` references both (`*store.Mode` parameter, `sess.WorkspaceID`) until it's deleted. If `21`/`20` land first, `session_intent.go` fails to compile before `28` gets a chance to delete it. `28`'s own task file argues it doesn't *need* `21`/`20` first — true, but the reverse risk (them needing to wait for it) wasn't addressed in any of the three files, so this ordering is added here per `EXECUTION-PROCESS.md`'s "if there's any doubt, serialize" instruction.
- `21` before `20`: both plausibly touch `internal/api/sessions.go`'s mode/workspace-adjacent routes; `21`'s file explicitly requires not running parallel with `28`, and keeping `20` after `21` avoids a three-way pile-up on the same file.
- `32` after `20`: needs `04` (Wave 1, already satisfied) and touches `internal/api/sessions.go`'s interrupted-turn detection — slotted after the sessions.go-heavy portion of the chain clears.
- `29` after `21`: both touch `internal/chat/context_client.go` (`SlotMode` rendering vs. `assembleAgentSlotContent`) — different functions, sequenced to avoid repeated churn on the same file.
- `22` after `29`: both touch `internal/chat/context_client.go` again (skill-selection call site) — same reasoning.
- `23` last: explicitly depends on `11` and `22` per its own task file (and excludes `agent_broker_decisions`, deferred to Phase 3 — see `ESCALATIONS.md`).
- `18a` (Wave 1) and `21` (this chain) both edit the same `DeleteAgent` cleanups slice in `internal/store/agents.go` — since `18a` runs in Wave 1 (before this chain starts), this resolves itself by ordering; no action needed beyond noting it.

**Wave 4 — solo, final cosmetic sweep.** `31-rename-pty-naming-scrub` touches ~90 files across nearly every subsystem this phase modifies (`chat_generate.go`, `container.go`, `chat.go`, `agent_deps.go`, durable-agent files). It's a pure identifier/comment rename with no logic change, so running it last — after every logic-changing task in Waves 0-3 has landed — avoids repeated rebasing against a fast-moving set of files. Nothing else depends on it landing first.

## Validation checkpoints

Per `EXECUTION-PROCESS.md`, real validation (not just build/vet/test) happens at logical-section boundaries, not after every task. Given Wave 3's tight coupling, the natural checkpoint boundaries are: **end of Wave 1** (dogfeed: durable-agent start/resume, a chat turn with lazy tool loading, an A2A task submit/cancel round-trip), **end of Wave 3** (dogfeed: a full chat session exercising compaction, Glass-4 handoff, and tool/skill selection — this is where the most invasive Phase 0 changes land together), and **end of Wave 4** (confirm nothing textually broke in the rename sweep).

---

## Phases 1-6 — index-level summary

Not broken into task files this pass, per the operator's scope decision (`Phase 0 in full, others as index-level summary`). Detailed planning for these happens in a follow-up session, closer to when each phase is actually ready to start — Phase 1 once Phase 0 lands (it doesn't strictly need Phase 0 complete, but the sequencing principle in `TASKS.md` is to stage all of Phase 0 first to reduce downstream surface area), and Phases 3+ once their noted open design questions resolve.

| Phase | Scope | Depends on | Status / open questions |
|---|---|---|---|
| **1 — Agent Construction** | New schema: `roles`, `agents` composition columns (`role_id`, `consumer_id`, `model_id`, `instance_mode`, `runtime_kind`), `consumers`, `known_tools`, `agent_tools`, `agent_dispatch_allowlist`; fix `agent_skills`/`agent_projects` FKs; fix `models` table's `models.dev` sync target; add reflex opt-out field; kill file-reingest-on-boot generally; build assignment UI/API; data-migrate every `.nanite/agents/*.md` onto `roles`/`agents`. | Reduced surface area from Phase 0 (not a hard schema dependency) | Architecturally well-specified (`architecture/01-agent-construction.md`, decision log §4/§6) — foundational and large, likely 15-20 task files on its own once planned in detail. |
| **2 — Agent Launching** | Wire `runtime_kind` as the single CLI/API routing mechanism (naming already fixed by Phase 0's `31-rename-pty-naming-scrub`), replacing four string-matching call sites. Retire the boot-profile catalog as a standalone system; port forward the `cmd`/`http` dynamic-resolver capability and the mandatory post-compaction re-read. Collapse `resolveProvider` into the construction cascade. | Phase 1's `runtime_kind` column | Well-specified (`architecture/02-agent-launching.md`, decision log §7/§9a). |
| **3 — Steering** | Design and add the `dispatch_to_agent` reflex action kind; migrate the agent broker's remaining rules and `promptrouter`'s phrase catalog into reflex predicates (fixing two phantom-agent reflex entries per the design review, and doing the `promptrouter` naming cleanup as part of this migration — deliberately NOT done early, per `TASKS.md`'s own "deliberately not included" note). Build the missing `agent_reflex_propose` self-tool and test `pending_reflexes` in real sessions. Test grounding in real sessions. Add `FilterToolSelection`. | Phase 1's reflex opt-out field | **Genuinely open**: "exact design of the reflex `dispatch_to_agent` action kind... direction is set, specifics aren't" (architecture doc `03-steering.md`, decision log §10). Also picks up `agent_broker_decisions`' export+drop, deferred here from Phase 0 (see `ESCALATIONS.md`). |
| **4 — Harness** | Verify the reaper's real-world behavior before further idle-timeout tuning (operator's own words: "more false reaps than idle or runaway agents by far" — not yet resolved by the activity-reset fix). Unify the three run-another-agent surfaces (REST delegation, LLM-triggered subagent, durable-agent wake) into one shared request/result type. | — | Reliability question flagged as still-open in the decision log; needs real operational observation, not just code review, before scoping. |
| **5 — Session Lifecycle, Messaging, Cards, Plugins** | Session lifecycle: extend `event_log` postmortem logging to all four recovery mechanisms (Phase 0's `32-rename-recovery-namespace` sets up the namespace this lands in). Wire `compaction_events` (relocates the compaction-disclosure text off `prompt_templates` per Phase 0 item 29 — note: Phase 0's `29-cut-prompt-templates.md` already did the disclosure-content relocation as a *prerequisite* to cutting the table; this phase's job is wiring the actual `compaction_events` writer, a distinct, larger piece). Add TTL pruning to the scratchpad tool. Cards: rebuild `todo-list`/`plan-review`/`subagent-spawn-approval` as primitive compositions; build the interactive-table-with-row-actions primitive; build the context-replay exclusion (flagged as the highest-leverage single item in this phase); fix CLI-agent boot content to source the card-type list dynamically. Plugins: wire `registers.agent_profiles[]`; build the real installed/enabled state model; close the CLI-install-vs-hot-reload asymmetry; develop `registers.panels[]`/`registers.crud[]` (not urgent); make the HTTP middleware chain plugin-extensible. | Various — largely independent sub-tracks within the phase | Broad phase covering three subsystem docs (`06`, `07`/`08` implicitly, `09`). Will likely split into 3 separate planning passes (session lifecycle, Cards, plugins) rather than one. |
| **6 — Open experiment + docs follow-through** | (1) Run the CLI-vs-API experiment for durable agents (made cheap by Phase 2's `runtime_kind` field) — the one genuinely unresolved architecture question from the whole review. (2) Old-docs archival pass (`docs/architecture/`, `docs/decisions/`, `docs/audits/`, most top-level `docs/*.md`) — proposed policy needs operator confirmation before executing (archive vs. delete vs. "superseded by" banner). (3) Frontend architecture pass — deliberately deferred, planned to be driven by a browser-based agent session. (4) GUI start-surface reconciliation (Chat/Harness/Durable/Recipe tabs) — folds into (3). | Phase 2 for item (1) | Item (2)'s policy needs an explicit operator go-ahead before any doc gets moved or deleted — flag this when Phase 6 planning starts, don't assume the proposed policy is pre-approved. |

---

See `TASKS/ESCALATIONS.md` for the full list of doc/reality mismatches found during planning, including which ones are already resolved (via Orchestrator judgment call within existing docs) and which ones genuinely need an operator decision before the relevant task is dispatched.
