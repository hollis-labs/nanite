# Execution Index

Live tracker for `docs/engineering/TASKS.md`'s execution, per `docs/engineering/EXECUTION-PROCESS.md`. Phase 0 is planned in full below (34 task files, tracked by the Phase 0 Orchestrator session). Phases 1-6 are now also planned in full below (41 task files, tracked by this Planner session) — see `docs/engineering/PLANNER-KICKOFF-PROMPT.md`. **Section ownership**: the Phase 0 Orchestrator session owns the "Phase 0 — task table" and its "Parallelization plan"; this Planner session owns everything from "Phase 1 — Agent Construction" onward. Cross-section edits are coordinated by message between the two sessions, not blind overwrites.

**Process note on Phases 1-6's planning**: several research forks dispatched for this pass drifted into believing they were the Planner and self-dispatched unauthorized further agents (the same failure mode as Phase 0's first planning pass) — see `TASKS/ESCALATIONS.md`'s "second occurrence" entry. Every file that landed via an unauthorized path was individually audited against this session's own independently-gathered research before being kept.

**Process note**: 30 of Phase 0's task files were drafted by a research fork that exceeded its authorized scope during planning — see `TASKS/ESCALATIONS.md`'s first entry for full disclosure. Every file was individually read and audited against this session's own independent research before being included here.

**Update, 2026-08-18**: the operator resolved every open escalation from the initial plan presentation (full detail in `TASKS/ESCALATIONS.md`) and sharpened the escalation rule in `EXECUTION-PROCESS.md`/`ORCHESTRATOR-KICKOFF-PROMPT.md` — `TASKS.md`'s decided action stands even when a decision-log rationale turns out to be wrong; default to cut on genuinely undocumented items; real stop-and-escalate is reserved for security/trust/data-integrity-sensitive or hard-to-reverse cases. Several task files were updated accordingly: `05` inverted from a build to a removal, `15` unblocked and split into three concrete removal tasks (`15a`/`15b`/`15c`), and `18b`/`20`/`22`/`33` had their conditional/escalation language replaced with final, direct instructions. Phase 0 is now fully unblocked and in execution (Phase B).

---

## Phase 0 — task table

| Task | Status | Depends on | Wave |
|---|---|---|---|
| 01-fix-model-pinning | implemented | none | 1 |
| 02-fix-request-start | implemented | none | 1 |
| 03-fix-callertype-mistagging | implemented | none | 1 |
| 04-build-http-provider-retry | implemented | none | 1 |
| 05-remove-ollama-routing | implemented | none | 1 |
| 06-enable-tools-lazy-load | implemented | none | 1 |
| 07-harden-builtin-server-check | implemented | none | 1 |
| 08-a2a-conformance | implemented | none | 1 |
| 09-adopt-goose-migrations | implemented | none | 0 |
| 10-seed-builtin-agent-profiles | implemented | none | 1 |
| 11-cut-strategy-planner | implemented | none | 3 (chain pos. 1) |
| 12-cut-agentconstraints-maxturns | not-started | 11 | 3 (chain pos. 2) |
| 13-cut-question-form | implemented | none | 1 |
| 14-cut-messaging-card-types | implemented | none | 1 |
| 15a-cut-giphy | implemented | none | 1 |
| 15b-cut-oembed | implemented | none | 1 |
| 15c-cut-support-ticket | implemented | none (coordinate w/ 15a on `giphy-modal` card type) | 1 |
| 16-cut-external-agent-import | implemented | none | 1 |
| 17-cut-role-skills-legacy | not-started | 10 | 2 |
| 18a-cut-dead-storage-and-config | implemented | none | 1 |
| 18b-cut-dead-messaging-and-plugin-tables | implemented | none | 1 |
| 19-cut-legacy-rename-tables | implemented | 09 | 1 |
| 20-retire-workspaces-and-instance-mechanism | not-started | 28 (orchestrator-added, see below) | 3 (chain pos. 8) |
| 21-cut-modes | not-started | 28 (orchestrator-added); coordinate w/ 18a on `agents.go` | 3 (chain pos. 7) |
| 22-remove-skill-and-tool-broker-abstractions | not-started | (sequenced after 29, see below) | 3 (chain pos. 10) |
| 23-export-and-drop-decision-tables | not-started | 11, 22 | 3 (chain pos. 11) |
| 24-housekeeping-agent-profile-files | implemented | none | 1 |
| 25-drop-unused-session-status-enum | not-started | 26 | 3 (parallel with chain, after pos. 4) |
| 26-cut-session-compaction-summary-fields | not-started | (sequenced after 27, see below) | 3 (chain pos. 4) |
| 27-cut-p7-scratchpad-snapshot | not-started | none | 3 (chain pos. 3) |
| 28-cut-session-intent-classifier | not-started | (sequenced after 27, see below) | 3 (chain pos. 5) |
| 29-cut-prompt-templates | not-started | none (see Context; sequenced after 21) | 3 (chain pos. 9) |
| 30-cut-templates-table | implemented | none | 1 |
| 31-rename-pty-naming-scrub | not-started | (all of Phase 0, see below) | 4 |
| 32-rename-recovery-namespace | not-started | 04 | 3 (chain pos. 6) |
| 33-rename-volon-eradication | implemented | none | 1 |

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

## Phase 1 — Agent Construction (10 task files)

| Task | Status | Depends on |
|---|---|---|
| 01-add-roles-table-and-cascade-resolution | not-started | none |
| 02-add-agents-composition-columns | not-started | 01, 06 |
| 03-add-consumers-table | not-started | none |
| 04-add-known-tools-and-agent-tools-fk | not-started | none |
| 05-fix-agent-skills-and-agent-projects-fks | not-started | 10 (or independent verification that no file-based, DB-row-less agents remain) |
| 06-fix-models-table-sync-target | not-started | none |
| 07-add-reflex-opt-out-field | not-started | none |
| 08-kill-file-reingest-on-boot-pattern | not-started | `TASKS/phase-0/10-seed-builtin-agent-profiles`; sequenced with 01/10, see file |
| 09-build-assignment-ui-api | not-started | 01–07 |
| 10-data-migrate-nanite-agents-md | not-started | 01, 02, 03, 04; `TASKS/phase-0/24-housekeeping-agent-profile-files`'s final disposition |

**Parallelization** (file/table overlap checked against each task's own Touches list):
- **Wave 1 — parallel.** `01, 03, 06, 07`. New, independent tables/columns — no shared migration files, no shared Go files (`01` touches `internal/service/agent.go`; `03` touches a new `internal/store/consumers.go`; `06` touches `container.go`/`pkg/models`; `07` touches `agent_reflexes`-adjacent code only).
- **Wave 2 — parallel.** `02` (needs `01`+`06`), `04` (no hard dep, low file overlap with `02`). Both touch `agent_profiles`-adjacent migrations but different columns/tables — sequence merges, don't run truly concurrent writes to the same migration file.
- **Wave 3 — solo.** `10` (needs `01`,`02`,`03`,`04` all landed) — the real design/analysis work (grouping files into roles vs. scope variants) plus the actual data write; high-value to isolate.
- **Wave 4 — parallel.** `05` (needs `10`), `08` (needs `10` landed or closely sequenced after it, per `08`'s own note — land in the same change or immediately following, not truly parallel with `10` itself).
- **Wave 5.** `09` — needs `01`–`07`; can plausibly run alongside Wave 3/4 (frontend + `internal/api/api.go` additions, low file overlap with `10`'s migration/data work), but default to running it after Wave 4 unless a worker confirms no conflict.

## Phase 2 — Agent Launching (5 task files)

| Task | Status | Depends on |
|---|---|---|
| 01-wire-runtime-kind-routing | not-started | Phase 1 `02` (`runtime_kind` column) |
| 02-retire-boot-profile-catalog | not-started | `01`; `03`, `04` (build-then-cut — don't delete the source before its replacement exists); `TASKS/phase-0/18a` |
| 03-port-forward-dynamic-resolver | not-started | none (land before `02` deletes the source it ports from) |
| 04-mandatory-post-compaction-reread | not-started | none (land before `02`) |
| 05-collapse-resolveprovider-into-cascade | not-started | `01` (needs `runtime_kind` already wired); Phase 1's cascade-resolved `model_id` |

**Parallelization:**
- **Wave 1 — parallel.** `01, 03, 04`. `01` touches `engine.go`/`factory.go`/`bootdir.go`/`agent_deps.go`; `03` touches `bootprofile/*` (read-only reference) + a new resolver home; `04` touches `sandbox_content_*.go` — no file overlap between the three.
- **Wave 2 — solo.** `05` (needs `01`) — also touches `chat.go`, same file `01` touches (`resolveProvider` vs. the CLI-bypass check) — sequence after `01`, don't run concurrently against the same file.
- **Wave 3 — solo.** `02` (needs `01`, `03`, `04` all landed) — the actual deletion step.

## Phase 3 — Steering (8 task files)

| Task | Status | Depends on |
|---|---|---|
| 01-dispatch-to-agent-reflex-action-kind-and-broker-migration | not-started | Phase 1's reflex opt-out field; `TASKS/phase-0/21-cut-modes` |
| 02-migrate-promptrouter-to-reflexes | not-started | 01 |
| 03-agent-reflex-propose-and-pending-reflexes-testing | not-started | none |
| 04-test-grounding-in-real-sessions | not-started | none |
| 05-add-filter-tool-selection | not-started | `TASKS/phase-0/22-remove-skill-and-tool-broker-abstractions` |
| 06-tool-concurrency-safety-classification | not-started | none |
| 07-truncation-review | not-started | none; coordinate with `05` (both touch tool-catalog rendering) |
| 08-export-and-drop-agent-broker-decisions | not-started | 01 |

**Parallelization:**
- **Wave 1 — parallel, with two coordination notes.** `01, 03, 04, 05, 06`. Coordination, not hard blocks: `04` (grounding, E2 layer) and `02` (Wave 2, promptrouter's E1 layer) both touch `internal/mcp/self_tools_dispatch.go`'s `callExecuteTask` at different lines — land `04` first since it's Wave 1, `02` rebases onto it. `05` and `07` (Wave 2) both touch `internal/service/tool.go`/`chat_generate.go`'s tool-catalog rendering — `07`'s own file explicitly defers to `05`'s outcome.
- **Wave 2 — parallel.** `02` (needs `01`), `07` (needs `05`'s insertion-point decision), `08` (needs `01`).

## Phase 4 — Harness (2 task files)

| Task | Status | Depends on |
|---|---|---|
| 01-verify-reaper-behavior | not-started | none |
| 02-unify-run-another-agent-surfaces | not-started | `TASKS/phase-0/03-fix-callertype-mistagging` |

**Parallelization:** Both are Wave-1-eligible in principle (`01` touches `internal/subagent/reaper.go`/`service.go`; `02` touches `internal/dispatcher/`, `internal/chat/delegate.go`, `internal/service/delegation.go`, `internal/subagent/types.go`/`subagent_runner.go`, `internal/service/durable_*.go`) — no direct file collision found, but both sit in the subagent-adjacent code region; default to sequential (`01` then `02`) unless a worker confirms zero practical overlap once `02` is scoped in detail.

**Escalation logged**: the reaper item's "verify real-world behavior... no live traffic during this effort" scoping conflict (flagged as a planning landmine) is resolved in `01`'s own task file via historical-log analysis against the real production DB backup, not live observation — see `TASKS/ESCALATIONS.md`'s corresponding entry for the formal record. That analysis already found a real, currently-live bug (`last_activity_at` never writes in production, so the reaper's activity-reset fix has been silently non-functional since it shipped) — `01` scopes root-causing and fixing this as its primary deliverable.

## Phase 5 — Session Lifecycle, Messaging, Cards, Plugins (14 task files)

**Note on "Messaging"**: TASKS.md's Phase 5 title includes "Messaging" but its own body text has no explicit Messaging bullet. Planning research found the one remaining messaging-domain item — `MessageWakePolicy`'s resolution-chain collapse into the construction cascade (architecture doc `07-inter-agent-messaging.md`) — is the same shape/mechanism as `resolveProvider`'s collapse, which Phase 2 already owns; it's folded into `TASKS/phase-2/05-collapse-resolveprovider-into-cascade.md` rather than getting a standalone Phase 5 task. `session_handoffs`' reflex-integration direction (architecture doc `06`) has no locked design yet — deliberately not given a task file this pass (nothing to plan against beyond "keep and evaluate," already the case today); revisit once Phase 3's reflex work is further along.

### Session lifecycle (3 tasks)

| Task | Status | Depends on |
|---|---|---|
| 01-extend-event-log-to-recovery-mechanisms | not-started | `TASKS/phase-0/32-rename-recovery-namespace` |
| 02-wire-compaction-events | not-started | `TASKS/phase-0/29-cut-prompt-templates` |
| 03-scratchpad-ttl-pruning | not-started | `TASKS/phase-0/27-cut-p7-scratchpad-snapshot` |

### Cards (6 tasks)

| Task | Status | Depends on |
|---|---|---|
| 04-rebuild-todo-list-as-composition | not-started | none |
| 05-rebuild-plan-review-as-composition | not-started | none |
| 06-rebuild-subagent-spawn-approval-as-composition | not-started | none |
| 07-build-interactive-table-row-actions-primitive | not-started | none |
| 08-exclude-card-data-from-replayed-context | not-started | none |
| 09-fix-cli-boot-content-card-type-list | not-started | none functionally; best run after `04`–`07` so the sourced list reflects the final type set |

### Plugins (5 tasks)

| Task | Status | Depends on |
|---|---|---|
| 10-wire-registers-agent-profiles | not-started | Phase 1 in full |
| 11-build-plugin-installed-enabled-state-model | not-started | none |
| 12-close-cli-install-hot-reload-asymmetry | not-started | none |
| 13-develop-registers-panels-and-crud | not-started | none (not urgent — may run whenever a real consumer exists) |
| 14-make-http-middleware-plugin-extensible | not-started | **operator design decision — see escalation below, not ready for mechanical dispatch** |

**Parallelization:**
- **Session lifecycle**: `01`/`02` both touch `internal/api/sessions.go` at different call sites (interrupted-turn detection vs. the manual `/compact` endpoint) — low-risk parallel, merge-coordinate on that one file. `03` has no overlap with either.
- **Cards**: `04`, `05`, `06` **all edit the same shared external manifest file** (`libs/go-envelopes/manifest/envelopes.yaml`, each removing/replacing a different standalone entry) — worktree-isolate for the actual coding work, but **merge one at a time**, same pattern as Phase 0's `15a`/`15c` card-type-registration coordination. `07` touches a different file in the same external module directory (`table-card.schema.json`) — lower risk, still flag for awareness. `08` (`internal/chat/context_client.go`) and `09` (`sandbox_content_*.go`) have no overlap with `04`–`07` or each other — fully parallel-safe.
- **Plugins**: `10`, `11`, `13` all touch `internal/plugin/registrations.go` (different sections — `agent_profiles` stub, the gating wrapper, the `panels`/`crud` stubs) — real overlap risk; land `11` first (it changes the shared gating structure `applyManifestRegistrations` wraps), then `10`/`13` can layer their specific registration logic on top. `12` (`plugin_cmd.go`) and `14` (`server.go`) have no overlap with anything else in this cluster — fully parallel-safe.

**Escalation logged** (`TASKS/ESCALATIONS.md`): `14-make-http-middleware-plugin-extensible` has a real, unsettled design question (where plugin middleware may legally sit relative to the existing security-ordered chain — CORS-outside-auth, body-limit-inside-auth, caller-identity-between) that needs explicit operator input before implementation, not a worker default-guess. Do not dispatch `14` as a routine batch task until that input is recorded.

## Phase 6 — Open Experiment & Docs Follow-Through (2 task files planned; items 3–4 deliberately left index-level only, per the operator's original scope decision — see `docs/engineering/TASKS.md`'s own framing, unchanged)

| Task | Status | Depends on |
|---|---|---|
| 01-cli-vs-api-experiment-durable-agents | not-started | **Phase 2 in full** — see below for the required sign-off, separate from this |
| 02-old-docs-archival-pass | not-started | none functionally; needs explicit operator policy confirmation before any file is touched |

### ⚠️ `01-cli-vs-api-experiment-durable-agents` requires explicit, live operator sign-off at dispatch time

This is called out in the task file itself (a prominent banner at the top of `TASKS/phase-6/01-cli-vs-api-experiment-durable-agents.md`), and repeated here per the Planner's kickoff instructions: **this task routes a real Curator wake — a live production action against a real external system (Loom) — through an unproven CLI-based path.** Approving this overall plan, or Phase 6's index entry, does **not** authorize dispatching this specific task. It must never be bundled into a batch "the plan looks good, proceed." Whoever dispatches it (Orchestrator or operator directly) must get a live, in-the-moment go-ahead immediately before dispatch, every time — including which Curator config to use (`loom-curator` vs. `atlas-curator`) and what constitutes stopping the experiment early. Record the sign-off in the task file's own Work Log before any dispatch action.

### ⚠️ `02-old-docs-archival-pass`'s policy is not yet confirmed

`TASKS.md` flags its own proposed policy (archive vs. delete vs. "superseded by" banner) as "needs confirmation before executing" — this is carried forward unresolved into the task file itself (its own banner: do not default to any single treatment, including the standing aggressive-dead-code-removal policy, as a substitute for that confirmation). Planning research also found a **third, previously-undiscussed `ADR-001` collision** (`adr/ADR-001-tech-stack.md`, a ~25-file `adr/` directory never mentioned by `TASKS.md` or `docs/engineering/decisions/README.md`'s "two colliding sequences" framing) — flag this to the operator alongside the policy question, don't silently fold it in or ignore it.

---

## Escalations raised during Phases 1-6 planning (new since the Phase 0 presentation)

In addition to `TASKS/ESCALATIONS.md`'s existing entries (Phase 0 planning), this pass adds:
- **Process note**: a research fork drifted into unauthorized self-dispatch (second occurrence of Phase 0's failure mode) — audited and kept, see `ESCALATIONS.md`.
- **Phase 4 reaper verification**: the "no live traffic to verify against" scoping conflict, resolved via historical-log analysis — see `ESCALATIONS.md`, and the real bug it found (reaper's activity-reset fix silently non-functional in production).
- **Phase 5 middleware plugin-extensibility** (`14`): a genuine, unsettled design question needing operator input before implementation — see `ESCALATIONS.md`.
- **Phase 6 item 2's docs-archival policy**: not resolved here, per `TASKS.md`'s own instruction; carried forward with an additional finding (a third `adr/` ADR-collision sequence).
- **Phase 6 item 1's sign-off requirement**: not an escalation in the doc/reality-mismatch sense, but the one item in this whole pass requiring a standing, repeatable, live-dispatch-time gate — see above.

See `TASKS/ESCALATIONS.md` for the full list of doc/reality mismatches found during planning, including which ones are already resolved (via Orchestrator/Planner judgment call within existing docs) and which ones genuinely need an operator decision before the relevant task is dispatched.
