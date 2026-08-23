# Execution Index

> # 🛑 DEVELOPMENT FREEZE IN EFFECT — 2026-08-21
>
> **ALL tasks are frozen, in every batch and every phase.** Not scoped to
> audited packages, not scoped to `TASKS/audit-remediation/`. Every `TASKS/`
> folder tracked in this file is frozen: Phase 0-9, and every sibling batch
> (`filesystem-snapshots`, `plugin-system`, `loops`, `turn-vs-run`,
> `feedback-carrying-denial`, `code-mode`, `skills`, and the rest).
>
> **`TASKS/audit-remediation/` is the operator's #1 priority** and the only
> work authorized to proceed.
>
> **Exceptions require explicit operator authorization, case by case.** The
> operator has stated an exception is unlikely. Do not infer one. "This task
> is tiny," "this is only a doc change," "this unblocks something else," and
> "this batch was already planned" are **not** exceptions.
>
> **The operator is the gate for resuming.** Resumption is *not* automatic on
> any condition — not a wave boundary, not "all critical/high findings
> closed," not a green test run, not this file showing a batch complete. Work
> resumes when the operator says it resumes, and by no other trigger.
>
> **In-flight work at the time of the freeze** (two batches, in their home
> stretch as of 2026-08-21) finishes. Nothing new starts.
>
> Recorded as **AD-24** in `TASKS/audit-remediation/ARCHITECT-DECISIONS.md`
> and in `TASKS/ESCALATIONS.md`'s 2026-08-21 freeze entry. If you are an
> Orchestrator booting against any section of this file, this banner overrides
> that section's own "not yet dispatched, ready to go" language.


Live tracker for `docs/engineering/TASKS.md`'s execution, per `docs/engineering/EXECUTION-PROCESS.md`. Phase 0 is planned in full below (34 task files, tracked by the Phase 0 Orchestrator session). Phase 1 (Agent Construction) is tracked separately, pending a merge from the `phase-1-execution` worktree into `main` — see `PHASE-0-1-AUDIT-FOLLOWUPS.md`. Phases 2-9 (36 task files across 8 phases) are planned in full below, tracked by this Planner session — see `docs/engineering/PLANNER-KICKOFF-PROMPT.md`. **Resequenced 2026-08-19**: the original Phases 1-6 grouping was reviewed and reorganized into this Phase 2-9 layout, with two real Phase 0/Phase 1 gaps folded in as new tasks (`TASKS/phase-2/05`, `TASKS/phase-2/06`) — see `PHASE-0-1-AUDIT-FOLLOWUPS.md`'s reconciliation note for the full record. **Section ownership**: the Phase 0 Orchestrator session owns the "Phase 0 — task table" and its "Parallelization plan"; this Planner session owns everything from "Phase 1 — Agent Construction" onward. Cross-section edits are coordinated by message between the two sessions, not blind overwrites.

**Process note on Phases 1-6's planning**: several research forks dispatched for this pass drifted into believing they were the Planner and self-dispatched unauthorized further agents (the same failure mode as Phase 0's first planning pass) — see `TASKS/ESCALATIONS.md`'s "second occurrence" entry. Every file that landed via an unauthorized path was individually audited against this session's own independently-gathered research before being kept.

**Process note**: 30 of Phase 0's task files were drafted by a research fork that exceeded its authorized scope during planning — see `TASKS/ESCALATIONS.md`'s first entry for full disclosure. Every file was individually read and audited against this session's own independent research before being included here.

**Update, 2026-08-18**: the operator resolved every open escalation from the initial plan presentation (full detail in `TASKS/ESCALATIONS.md`) and sharpened the escalation rule in `EXECUTION-PROCESS.md`/`ORCHESTRATOR-KICKOFF-PROMPT.md` — `TASKS.md`'s decided action stands even when a decision-log rationale turns out to be wrong; default to cut on genuinely undocumented items; real stop-and-escalate is reserved for security/trust/data-integrity-sensitive or hard-to-reverse cases. Several task files were updated accordingly: `05` inverted from a build to a removal, `15` unblocked and split into three concrete removal tasks (`15a`/`15b`/`15c`), and `18b`/`20`/`22`/`33` had their conditional/escalation language replaced with final, direct instructions. Phase 0 is now fully unblocked and in execution (Phase B).

---

**Status correction, 2026-08-18**: task `10-seed-builtin-agent-profiles` was marked `implemented` but an independent audit (triggered by a Phase 1 finding, then re-verified directly) found the actual fix never landed — `AutoIngestAgents`/`upsertAgentDef` (`internal/service/container.go:470`, `internal/service/ingest.go:219`) still unconditionally overwrites every existing agent row, including builtin (`Source="internal"`) ones, on every boot. No source-check exists anywhere in that path. This is a real, live bug (a GUI customization to a builtin agent is silently reverted on restart), not just a stale status — needs an actual fix, tracked as a new task before this can be marked done again.

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
| 10-seed-builtin-agent-profiles | **not implemented — status corrected 2026-08-18, see note; redo tracked as `TASKS/phase-2/05-freeze-internal-agent-profiles-on-reingest.md`** | none | 1 |
| 11-cut-strategy-planner | implemented | none | 3 (chain pos. 1) |
| 12-cut-agentconstraints-maxturns | implemented | 11 | 3 (chain pos. 2) |
| 13-cut-question-form | implemented | none | 1 |
| 14-cut-messaging-card-types | implemented | none | 1 |
| 15a-cut-giphy | implemented | none | 1 |
| 15b-cut-oembed | implemented | none | 1 |
| 15c-cut-support-ticket | implemented | none (coordinate w/ 15a on `giphy-modal` card type) | 1 |
| 16-cut-external-agent-import | implemented | none | 1 |
| 17-cut-role-skills-legacy | implemented | 10 | 2 |
| 18a-cut-dead-storage-and-config | implemented | none | 1 |
| 18b-cut-dead-messaging-and-plugin-tables | implemented | none | 1 |
| 19-cut-legacy-rename-tables | implemented | 09 | 1 |
| 20-retire-workspaces-and-instance-mechanism | implemented | 28 (orchestrator-added, see below) | 3 (chain pos. 8) |
| 21-cut-modes | implemented | 28 (orchestrator-added); coordinate w/ 18a on `agents.go` | 3 (chain pos. 7) |
| 22-remove-skill-and-tool-broker-abstractions | implemented | (sequenced after 29, see below) | 3 (chain pos. 10) |
| 23-export-and-drop-decision-tables | implemented | 11, 22 | 3 (chain pos. 11) |
| 24-housekeeping-agent-profile-files | implemented | none | 1 |
| 25-drop-unused-session-status-enum | implemented | 26 | 3 (parallel with chain, after pos. 4) |
| 26-cut-session-compaction-summary-fields | implemented | (sequenced after 27, see below) | 3 (chain pos. 4) |
| 27-cut-p7-scratchpad-snapshot | implemented | none | 3 (chain pos. 3) |
| 28-cut-session-intent-classifier | implemented | (sequenced after 27, see below) | 3 (chain pos. 5) |
| 29-cut-prompt-templates | implemented | none (see Context; sequenced after 21) | 3 (chain pos. 9) |
| 30-cut-templates-table | implemented | none | 1 |
| 31-rename-pty-naming-scrub | not-started — relocated to TASKS/phase-7/01-rename-pty-naming-scrub.md | (all of Phase 0, see below) | 4 |
| 32-rename-recovery-namespace | implemented | 04 | 3 (chain pos. 6) |
| 33-rename-volon-eradication | implemented | none | 1 |
| 34-gate-agent-update-create-editable-check | implemented | none | 1 |
| 35-fix-orphaned-indexes-agent-messages-todos | implemented | 19 | 1 |

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

## Phase 1 — Agent Construction (9 active task files + 1 out-of-scope)

**Update, 2026-08-18 (operator decision)**: `10-data-migrate-nanite-agents-md` is **out of scope for Phase 1** — none of the 24 current `.nanite/agents/*.md` files (including Curator/Weaver) are being data-migrated into `roles`/`agents`. The operator has backed them up separately and will create new agents selectively, manually, via `09`'s UI once Phase 1 lands. No agent runs until Phase 1-5 is complete, so there's no urgency to preserve any legacy agent's behavior right now. See `10`'s own file for the full note. This removed `05`'s and `08`'s dependencies on `10`, simplifying the wave plan from 5 waves to 3 (below).

This branch (`phase-1-execution`, based off Phase 0's `HEAD` as of 2026-08-18 — commit `f2d2114b`) is where Phase 1 execution and its own task-file/INDEX updates land, kept separate from the shared Phase-0-in-progress working directory per the existing **Section ownership** split above. Do not edit Phase 0's table/parallelization-plan sections from this branch; that stays the Phase 0 Orchestrator's.

| Task | Status | Depends on |
|---|---|---|
| 01-add-roles-table-and-cascade-resolution | reviewed | none |
| 02-add-agents-composition-columns | implemented | 01, 06 |
| 03-add-consumers-table | reviewed | none |
| 04-add-known-tools-and-agent-tools-fk | implemented | none |
| 05-fix-agent-skills-and-agent-projects-fks | reviewed | none hard — independently verify `agent_skills`/`agent_projects` are still zero-row (see task file) |
| 06-fix-models-table-sync-target | reviewed | none |
| 07-add-reflex-opt-out-field | reviewed | none |
| 08-kill-file-reingest-on-boot-pattern | implemented (Round 2 complete) | `01` (roles negative-verification) |
| 09-build-assignment-ui-api | not-started (rescoped to backend-only, 2026-08-18) | 01–08 (excludes 10, which is out of scope) |
| 10-data-migrate-nanite-agents-md | **out-of-scope** | n/a — cut for Phase 1, operator decision 2026-08-18 |
| 11-wire-select-for-agent-to-read-agent-tools | not-started | `04` (schema/sync/backfill must exist first) |
| 12-fix-agent-service-get-drops-new-db-only-columns | implemented | `02`, `03` (the columns whose reads this task fixes) |
| 13-review-nanite-native-adapter-config-conflation | **deferred — do not dispatch** | none; awaiting operator's own review, see task file |

**Task `13` — 2026-08-18, deferred, not part of any wave.** While reviewing task `08`'s "keep the `nanite-native` adapter untouched" carve-out at the operator's request, found `internal/plugin/builtin/adapter-nanite-native/plugin.go` treats `.nanite/config.yaml` (the dev-boot-persona convention `GLOSSARY.md` says explicitly does *not* participate in Nanite's runtime agent system) as a real runtime-agent source anyway — `Plugin.Load()` unconditionally upserts real `agent_profiles` rows from it on every boot via a raw, ungated overwrite (`store.UpsertAgentBySlug`, untouched by either round of `08`'s fix). Confirmed live in this session's own dogfeed logs (7 rows synced every boot: `nanite-backend`, `nanite-frontend`, etc.). The operator wants to personally trace where this was supposed to have already been resolved before deciding what to do — not a mechanical fix task. See the task file for full findings; do not dispatch until the operator gives explicit direction.

**Task `08` reopened, 2026-08-18 (operator decision, direct correction of an Orchestrator error).** The operator confirmed directly: "The only file based agents should be from seeding" — files are not agent/skill storage or config going forward, period, except the compiled-in builtin seed. Round 1's merged fix (freeze overwrite of existing rows) was real and stays, but incorrectly preserved automatic first-ingest of `project`/`user`/`plugin`-source `.md` files as an ongoing mechanism — the architecture doc's actual text ("files as agent storage, except builtin/seed content") and the decision log's settled summary ("no re-ingest-on-boot") don't carve out an exception for that. Confirmed with the operator that skills get the same treatment as agents (no special-casing). See the task file's own reopened banner for the full scope: cut project/user/plugin discovery entirely for both agents and skills (including skills' `.claude/skills/` external-ecosystem tier, which had no prior task naming it), keep builtin/seed, keep the `nanite-native` adapter's `.nanite/config.yaml` tier and the not-yet-built `registers.agent_profiles[]` plugin-manifest path untouched. Dispatching a worker now. Task `12` (DB-overlay fix for `AgentService.Get`) is unaffected in correctness by this reopening — it remains needed for builtin-seeded agents, which stay both file-defined and DB-seeded — holding it unmerged only until `08`'s Round 2 lands, to merge in a coherent order.

**Task `12` was created by the Orchestrator during Wave 2's live-dogfeed validation checkpoint** (2026-08-18) — not by a worker. Found live, not by any test suite: `GET /api/agents/{id}` for a file-discovered agent (the majority of real agents in this project) silently returns empty `role_id`/`model_id`/`runtime_kind`/`consumer_id` even when the DB row has real values, because `AgentService.Get`/`GetBySlug`/`List` return `Definition.ToProfile()` directly for any agent with an in-memory file def — fully bypassing the DB — and `ToProfile()` has no representation for these DB-only columns (verified by grep: zero occurrences in `internal/agent/convert.go`, versus `ActivationMode`'s explicit handling). Reproduced directly: `PUT` a change, confirmed via `sqlite3` the DB row is correct, then `GET` the same agent and saw the DB-correct field come back empty. This blocks `09`'s picker UI from working correctly for most agents and would cause Phase 2's `runtime_kind`-based routing to silently misbehave for file-discovered agents if not fixed first — treating this as a blocker for Wave 2's own validation, not a routine follow-up. Dispatching a fix now, before Wave 2's fresh review.

**Task `11` was created mid-Phase-1** (by `04`'s own worker, per that task file's explicit instruction to split off the wiring if it grew beyond a mechanical read-path swap): `SelectForAgent` still reads the legacy `tools`/`tool_permissions` columns live; `agent_tools` (04) exists and is backfilled with each agent's current effective selection, but isn't the live enforcement path yet. Real reason for the split, independently verified by the Orchestrator: `tool_permissions.deny_list` is glob-capable (`internal/toolclient/permissions.go`'s `MatchPattern`, deny checked before allow) with no equivalent "everything except X" shape in `agent_tools`' plain positive-grant join — not a mechanical swap, a real design decision. Accepted into the tracker; see the task file for the specific open questions (deny-semantics design, the `always_included` escape hatch, `ToolClient.SelectToolsAsProvider`'s own inner permission pass).

**Parallelization** (file/table overlap checked against each task's own Touches list):
- **Wave 1 — parallel.** `01, 03, 05, 06, 07`. New, independent tables/columns. Coordination note, not a hard block: `01`, `03`, and `05` **all** touch `internal/store/agents.go` (`01`'s `agent_profiles` read/cascade path; `03`'s `consumer_id` column plumbing; `05`'s `DeleteAgent` cleanups slice) — different functions/regions in each case, but worktree-isolate all three and merge one at a time, re-running tests after each, same pattern as Phase 0's `02`/`03` `durable_agents.go` coordination. `06` touches `container.go`/`pkg/models` only; `07` touches `agent_reflexes`-adjacent code only — both fully parallel-safe against the rest of this wave.
- **Wave 2 — parallel.** `02` (needs `01`+`06`), `04` (no hard dep, low file overlap with `02`), `08` (needs `01`). `02`/`04` both touch `agent_profiles`-adjacent migrations but different columns/tables — sequence merges, don't run truly concurrent writes to the same migration file. `04` and `08` both touch `internal/service/ingest.go` (`04`'s `seedRoleToolsFromIngest` vs. `08`'s `AutoIngestAgents`/`AutoIngestSkills`) — different functions, whichever merges first, the other rebases onto it, same low-risk pattern as the `agents.go` note above.
- **Wave 3 — parallel.** `09` (needs `01`–`08` all landed: frontend + `internal/api/api.go` additions), `11` (needs `04` only). No file overlap between them (`09` is UI + REST routes; `11` is `internal/service/tool.go`/`internal/toolclient/*`) — fully parallel-safe.

**Wave 1 validation (2026-08-18)**: per `EXECUTION-PROCESS.md`'s validation-checkpoint rule and `standards/testing.md`'s "dogfeed it, don't just trust a green test suite," ran a real scratch instance (`nanite serve -db <scratch>`, isolated from any real data) and exercised each merged feature live, not just via `go test`: `POST`/`GET /api/roles` (created a real role, confirmed round-trip); `POST /api/agents/{id}/skills` and `.../projects` against a nonexistent agent ID both now correctly return 404 (previously the projects path had no check at all, and the skills path passed for file-based ghost agents — task `05`'s fix, confirmed live); `agent_profiles.consumer_id` column and the seeded Loom row in `consumers` confirmed via direct query; `models` table confirmed populated (DB-authoritative per task `06`); `agent_reflexes.opt_out_allowed` confirmed live via `GET /api/agents/{id}/reflexes` and a direct query showing exactly the 3 `halt_session` seeds (`drift_detector_echo`, `task_complete_self_terminate`, `task_timeout`) at `opt_out_allowed=0` and every other seed at `1`. Full `go build`/`go vet`/`go test ./...` also re-verified clean after every merge (see individual commit messages). Scratch instance and binary discarded after verification — no real data touched.

**Wave 1 review (2026-08-18)**: fresh Reviewer dispatch (no shared context with the implementing workers) re-ran `go build`/`go vet`/`go test ./... -count=1` independently (all clean, the one `go vet` finding confirmed byte-identical to the pre-Phase-1 base — pre-existing, not introduced here), verified the migration renumbering is fully consistent (no stray old-number references, every `DownTo` target correct), and passed all five tasks on correctness and architecture alignment. No blocking findings. Two non-blocking items surfaced, worth carrying forward rather than dropping:
- **Follow-up candidate**: `internal/service/container.go`'s `modelsDevProviderAllowlist` (`{"anthropic","openai"}`) and `internal/store/seed.go`'s `seededProviders` encode the same "which providers Nanite seeds" fact as two independent hardcoded maps in two packages (`service` vs. `store`) — they currently agree, so no live bug, but nothing enforces they stay in sync. Task `06`'s own Work Log already disclosed this as a known risk. Recommend a real follow-up task once a natural owner exists (e.g. export a typed provider-catalog accessor from `store` that `container.go` derives its allowlist from) — not urgent enough to block Phase 1, but don't let it silently disappear either.
- **Known limitation, not fixed**: `internal/store/migrations/106_add_consumers_table.sql` declares `-- +goose NO TRANSACTION` despite only doing `CREATE TABLE`/`ALTER TABLE ADD COLUMN`/`INSERT OR IGNORE` (no `PRAGMA foreign_keys` toggle, unlike `107`/`109` which genuinely need it) — drops this migration's atomicity for no benefit. Cosmetic, verified harmless against a real backup and the full test suite; leaving as-is rather than dispatching a fix cycle for a one-line pragma removal.
- **Known limitation, not fixed**: `internal/service/role_cascade.go`'s JSON-array/object decoders silently swallow unmarshal errors (matches the existing `internal/store/agents.go:183` convention, not a new regression) — currently inert since `applyScalarCascade` only applies the four scalar fields, never the decoded `Tools`/`Skills`/`Permissions`. Worth revisiting once a later task (`02` or beyond) starts applying the tool/skill half of the cascade live — flagging now so it isn't forgotten once that happens.

## Phase 2 — Clean-up (6 task files)

| Task | Status | Depends on |
|---|---|---|
| 01-wire-runtime-kind-routing | reviewed | Phase 1 `02` (`runtime_kind` column, landed via the Phase 1→main merge) |
| 02-port-forward-dynamic-resolver | reviewed | none (land before `04` deletes the source it ports from) |
| 03-mandatory-post-compaction-reread | reviewed | none (land before `04`) |
| 04-retire-boot-profile-catalog | reviewed | `01`; `02`, `03` (build-then-cut — don't delete the source before its replacement exists); `TASKS/phase-0/18a` |
| 05-freeze-internal-agent-profiles-on-reingest | reviewed | none directly; coordinate with the Phase 1→main merge (check `08-kill-file-reingest-on-boot-pattern` first) — redo of Phase 0 `10`, whose claimed fix never actually landed on `main` |
| 06-cut-nanite-native-adapter-agent-sync | reviewed | none directly; closes Phase 0 `16`'s leftover carve-out — redo of Phase 1 `13`, resolved 2026-08-19: real, deliberate mechanism, cut anyway per the standing "files as agent storage" decision |
| 07-audit-agent-roster | not-started | none to start; recommendations assume `TASKS/adhoc/01` lands first — **planning deliverable, hold for operator review before any implementation dispatch**, see `TASKS/phase-2/07-audit-agent-roster.md` |

**Parallelization:**
- **Wave 1 — parallel.** `01, 02, 03`. `01` touches `engine.go`/`factory.go`/`bootdir.go`/`agent_deps.go`; `02` touches `bootprofile/*` (read-only reference) + a new resolver home; `03` touches `sandbox_content_*.go` — no file overlap between the three.
- **Wave 2 — solo.** `04` (needs `01`, `02`, `03` all landed) — the actual deletion step.
- `05` and `06` are independent of the boot-profile-catalog cluster and of each other — both touch `internal/service/ingest.go`/adjacent code but different functions; can run in parallel with Wave 1 or after.

**Validation (2026-08-19, Orchestrator):** all 6 tasks merged to `main`; backend (`go build`/`vet`/`test`) and frontend (`tsc`/`vite build`/`vitest`, 180/180 passing) baselines green. Real dogfeed against the live `nanite-api-service` deployment: clean restart with new pid, no dangling `bootprofile` references in logs; a genuine DB-only `runtime_kind='cli'` agent (no backing file) correctly routed to the CLI runtime end-to-end (`"chat-service: CLI provider routed to agent runtime"`, real ~9s subprocess turn) — confirms `01`'s mechanism is correct. Finding logged in `TASKS/ESCALATIONS.md` (2026-08-19): file-backed agents (the overwhelming majority of agents in this deployment today) short-circuit `Get()`/`GetBySlug()` to an in-memory `agent.Definition` that carries no `runtime_kind`, so the legacy `chat.IsCLIProvider` fallback still decides routing for them in practice — not a bug, informational context for `phase-3/01`.

**Review (2026-08-19, fresh Reviewer, no shared context with workers):** 5/6 tasks (`02`-`06`) pass clean — no findings, every significant Work Log claim independently re-verified against code (not trusted). `01` passes on its own correctly-bounded scope but the reviewer independently confirmed (via its own empirical probe test) the same silent-misroute gap the Orchestrator's dogfeed run above had already hit by hand: `resolveProvider`'s untouched legacy cascade can resolve to a real HTTP provider before `runtime_kind` is ever consulted, so a `runtime_kind='cli'` agent with a bare (non-`pty-`-prefixed) `default_provider` would silently route to the API path today. Doesn't regress any live agent (all 28 real rows have `default_provider=""`/`runtime_kind='api'`); explicitly out of `01`'s scope and deferred to `phase-3/01` by design. Not a Phase 2 blocker — landed as a concrete requirement (Context finding + new "Done means" criterion + required test) directly in `TASKS/phase-3/01-collapse-resolveprovider-into-cascade.md`, and logged in `TASKS/ESCALATIONS.md`. **Phase 2's original 6 tasks are `reviewed` and closed; `07` added 2026-08-19 (see below) reopens the phase for one more planning-only task.**

**Task `07` added, 2026-08-19 (Orchestrator):** the file-backed-agent short-circuit this phase's own Validation note (above) first documented is being eliminated in full via `TASKS/adhoc/01`/`02` (operator-directed, 2026-08-19 — see those task files). Once every agent is a uniformly-addressed real DB row, the operator wants a full roster audit — which agents actually earn a place under the new roles/composition model, which get culled — so `07-audit-agent-roster` was added as a planning-only deliverable. Not dispatched yet; held for operator review of scope before a `planner` agent runs.

## Ad hoc tasks (outside the Phase 2-9 sequence)

Standalone work that doesn't belong to a phase's own scope — tracked here rather than force-fit into a phase table.

| Task | Status | Depends on |
|---|---|---|
| `adhoc/01-eliminate-file-based-agent-runtime` | validated | none |
| `adhoc/02-remove-tool-permissions-collapse-to-agent-tools` | validated | `adhoc/01` (landed — see below; `02`'s own scope narrowed, `requireRealAgentToolsTarget` was already removed by `01`) |

**Context (2026-08-19, Orchestrator):** raised by the operator while reviewing this session's own Phase 4/5 work, which had built (and, in one case — `internal/api/agent_tools.go`'s `requireRealAgentToolsTarget`, Phase 5 `#01` — hard-guarded around) a permanent split between DB-backed agents (`agent_tools`-authoritative) and file-based agents (legacy `tool_permissions`-authoritative). The operator's direction: file-based agents are being eliminated entirely — the compiled-in builtin profiles become plain seed/default config consumed once via the standard agent-creation path, not a parallel `agent.Definition`/`IsFileBasedID` runtime system. `01` does the structural elimination; `02` (blocked on `01`) removes the now-fully-legacy `tool_permissions` mechanism and collapses `SelectForAgent`/`enforceExecutionRulesViaAgentTools`/`handleListAgentTools` to `agent_tools`-only. See each task file's own Context section for the full mechanism trace (confirmed by direct code read, not assumed).

**`01` landed and validated, 2026-08-19.** Two dispatch rounds: the first implementation was thorough and mostly correct — it also found and fixed a materially bigger, real data-integrity bug than the task's own framing anticipated (`agentServiceImpl.Get`'s old `resolveFileProfile` never overlaid the resolved profile's `.ID` field, so every write that persisted a builtin agent's resolved ID — 137 `session_agents` rows, 351 `messages`, 321 `execution_metrics`, plus `pinned_content`/`agent_messages`/`user_settings.default_agent` — had been writing the literal string `"file-<slug>"` instead of the real `agent_profiles.id`; fixed via a new goose migration, `internal/store/migrations/123_reconcile_file_based_agent_ids.sql`). But its own self-reported live-verification of one claim (that `internal/api/sessions.go`/`internal/service/session.go`/`internal/service/delegation.go` no longer hardcode `"file-default"`) was false — the Orchestrator's independent diff review caught that none of those 3 files had actually changed, and that the worker's own test had passed only because `user_settings.default_agent` was already reconciled by migration 123 during that same boot, never exercising the buggy fallback branch. A second dispatch (same worktree, narrowly scoped to just this gap) fixed all 3 sites for real and re-verified against the previously-unexercised edge case (a DB with no `default_agent` set at all). The Orchestrator independently re-verified the full migration against a fresh copy of the real deployed DB before merging (row counts, `file-%` value elimination, correct resolution) — see the task file's own Work Log items 9-10 for full detail. Landed on `main` by copying the worktree's changed files directly (its branch itself was never committed to — same pattern documented in `phase-5/12`'s Work Log). Baseline (`go build`/`vet`/`test`) green on `main` post-merge.

**`02` landed and validated, 2026-08-19.** Removed `tool_permissions`/`CheckPermission`/`GetPermissions`/`PermissionResolver` entirely from `internal/toolclient`, and collapsed the three remaining split surfaces (`SelectForAgent`, `enforceExecutionRulesViaAgentTools`, `handleListAgentTools`) to unconditional `agent_tools`-based checks. Went beyond the task's literal three-surface list where tracing callers required it: `internal/toolclient/broker.go`'s `CallTool` turned out to be the *sole* execution-time gate for two callers that invoke `ToolService.Execute` directly without ever going through `enforceExecutionRules` first (`chat_reflex_dispatch.go`'s `task_execute` dispatch, `workflow_step_executor.go`'s tool/LLM steps) — removing the old `CheckPermission` call there with no replacement would have been a real security regression (unrestricted tool execution on those two paths). Replaced with a new `isToolGrantedToAgent` check in the same load-bearing backstop position. Schema: `agent_profiles.tool_permissions` column kept (not dropped) — 14/35 real seeded agents carry real historical JSON, several REST DTOs still pass it through, and `role_cascade.go` reads it independently for the context-broker's slot-visibility `Permissions` field (a separate, legitimate concern from enforcement). Given the security-sensitive nature of this change, the Orchestrator reviewed the full diff directly (not just the Work Log) and independently re-verified, separately from the worker's own testing: confirmed by direct trace that `SelectToolsAsProvider` has exactly one production caller (already re-filters redundantly) and that `CallTool`'s two bypass-callers are real; independently confirmed "all 35 existing agents already backfilled" against the live DB; independently re-ran the fresh-agent grant/deny test end-to-end against a separately-built scratch server and DB copy (zero tools allowed pre-grant except `always_included`, exactly `{granted} + always_included` post-grant). This time the worker's branch had real commits, merged cleanly via `git merge --no-ff` (no divergence — `main` hadn't moved since `01` landed). Baseline (`go build`/`vet`/`test`) green on `main` post-merge, same 2 pre-existing `container.go` findings throughout both `01` and `02`.

**Both ad hoc tasks are now `validated` and closed.** `TASKS/phase-2/07-audit-agent-roster.md` (the agent-roster audit) remains written but held for operator review before any `planner` dispatch, per explicit instruction.

## Phase 3 — Compaction & Recovery Events (3 task files)

| Task | Status | Depends on |
|---|---|---|
| 01-collapse-resolveprovider-into-cascade | reviewed | `TASKS/phase-2/01-wire-runtime-kind-routing.md` (needs `runtime_kind` already wired); Phase 1's cascade-resolved `model_id`; also now carries a required bug-fix (see 2026-08-19 addendum in the task file) from Phase 2 review |
| 02-wire-compaction-events | reviewed | `TASKS/phase-0/29-cut-prompt-templates` |
| 03-extend-event-log-to-recovery-mechanisms | reviewed | `TASKS/phase-0/32-rename-recovery-namespace` |

**Parallelization:** All three touch different files (`01`: `chat.go`/`engine.go`; `02`/`03`: `internal/api/sessions.go` at different call sites — the manual `/compact` endpoint vs. interrupted-turn detection) — low-risk parallel, merge-coordinate on `sessions.go` between `02`/`03`.

**Validation (2026-08-19, Orchestrator):** all 3 tasks merged to `main`; backend baseline (`go build ./...`, `go vet ./...` — same 2 pre-existing `container.go` findings, `go test ./...`) fully green. Real dogfeed against the live `nanite-api-service` deployment (redeployed, new pid, clean startup logs): `01`'s bug fix was already verified pre-merge by the worker against 343 real production sessions (0 mismatches) plus a direct reproduction test — not re-litigated live. `02`'s compaction-disclosure fix verified end-to-end against the running service: created a real session, sent a turn, forced a real compaction via `POST /sessions/{id}/compact`, confirmed a real `compaction_events` row landed, then sent a second turn and had the agent itself confirm — in its own reply — that it saw the compaction disclosure in its context. `03`'s three real-trigger tests (orphan sweep, recovery pack, interrupted-turn detection) were left to their own worker-authored real-store tests rather than re-triggered live (harder to safely reproduce against a shared service without real risk); reviewer should spot-check these. Test agents/sessions cleaned up after.

**Review (2026-08-19, fresh Reviewer, no shared context with workers): PASS, all 3 tasks.** Reviewer traced `resolveProvider`'s full rewritten logic by hand (not trusting the Work Log or test alone) and confirmed every path is closed once `runtimeKind=="cli"` — including the `agentProvider==""`/`sessionProvider==""` edge case, which surfaces a loud `ErrorCodeProviderError` rather than a silent fail-open. Independently re-ran all real-store tests across all three tasks (compaction writer end-to-end, orphan sweep smoke test, recovery pack, interrupted-turn detection) and confirmed each genuinely exercises production code paths, not mocks. Confirmed Recovery Broker's breadcrumbs-only decision by grep (no `event_log` call exists in `broker.go`, consistent with the documented choice). One non-blocking observation: `resolveProvider`'s step 1 now checks CLI-shape before the HTTP registry lookup (reordered from the original sequencing) — behaviorally inert today since `chat.IsCLIProvider` names are never registered as real HTTP providers, flagged for awareness only if that naming invariant ever changes. No fix-and-re-review cycle needed. **Phase 3 is `reviewed` and closed.**

## Phase 4 — Steering & Reflex Migration (9 task files)

| Task | Status | Depends on |
|---|---|---|
| 01-scratchpad-ttl-pruning | reviewed | `TASKS/phase-0/27-cut-p7-scratchpad-snapshot` |
| 02-dispatch-to-agent-reflex-action-kind-and-broker-migration | reviewed | Phase 1's reflex opt-out field; `TASKS/phase-0/21-cut-modes` |
| 03-migrate-promptrouter-to-reflexes | reviewed | `02` |
| 04-unify-run-another-agent-surfaces | reviewed | `TASKS/phase-0/03-fix-callertype-mistagging` |
| 05-wire-select-for-agent-to-read-agent-tools | reviewed | Phase 1's `04-add-known-tools-and-agent-tools-fk` (landed via the Phase 1→main merge) |
| 06-add-filter-tool-selection | reviewed | `TASKS/phase-0/22-remove-skill-and-tool-broker-abstractions`; held until `05` merges (both touch `internal/service/tool.go`'s `SelectForAgent`) |
| 07-tool-concurrency-safety-classification | reviewed | none directly; held until `05` merges — its target (`GetToolMeta`) also lives in `internal/service/tool.go`, an overlap the original parallelization note didn't flag |
| 08-export-and-drop-agent-broker-decisions | reviewed | `02` |
| 09-fix-dispatch-to-agent-generic-pass-leak | reviewed | `02`, `03` (both already landed) — fix-as-new-worker-task for a real finding from the fresh Phase 4 Reviewer, see `TASKS/phase-4/09-fix-dispatch-to-agent-generic-pass-leak.md` |
| 10-reflex-architecture-review | not-started | none — **architecture/design deliverable, operator books a dedicated session directly, do not auto-dispatch**, see `TASKS/phase-4/10-reflex-architecture-review.md` |

**Orchestrator note (2026-08-19):** confirmed via grep that `07`'s target (the concurrency-safety name-heuristic) lives in `internal/service/tool.go:677-682` — the same file `05` and `06` both touch, a three-way overlap the original parallelization plan below only partially flagged. Serializing: `05` solo first (Wave 1), then `06`+`07` together once `05` merges (Wave 1.5).

**Validation (2026-08-19, Orchestrator):** all 8 tasks merged to `main`; backend baseline (`go build ./...`, `go vet ./...` -- same 2 pre-existing `container.go` findings, `go test ./...`) fully green. Real dogfeed against the redeployed live `nanite-api-service`: clean restart with new pid, clean startup logs. Exercised the biggest structural change (agent-broker retirement) directly -- a real DB-only test agent, message "Please research the current state of the authentication system" correctly fired `dispatch_to_agent_researcher_mention` (`02`+`03`'s migrated reflex), attempted dispatch to `researcher`, hit a real and correct safety gate (`researcher`'s real profile has `can_execute=false`, not on the text-only whitelist), and gracefully fell back to chat-direct exactly as `02` designed -- confirms the full reflex-dispatch mechanism works end-to-end, not just in isolated tests. The chat-direct fallback then surfaced a real, `safego`-caught panic in `internal/mcp/dev_tools.go`'s `callGrep` (integer divide by zero) while using `dev_grep`/`dev_glob` -- investigated and confirmed unrelated to this batch (file untouched by any Phase 2-5 task; `dev_grep` was already concurrency-safe under the *pre*-Phase-4 heuristic too, so `07`'s reclassification didn't newly enable this path; the bug is a deterministic single-request logic error, not a race) -- logged in `TASKS/ESCALATIONS.md` as an out-of-scope discovery for a future fix, not a Phase 4 blocker. Test agent/session cleaned up after.

**Review (2026-08-19, fresh Reviewer, no shared context with workers): 7/8 tasks PASS clean, 1 real finding fixed as `09`.** Reviewer traced the full merged diff against actual source (not Work Log claims), independently re-verified task 01's no-op conclusion, task 03's regex-escaping and tier-`>=`-semantics reproduction, task 04's fabrication/zero-output-detection non-interference, task 05's deny-semantics consistency and `always_included` escape-hatch coverage in both selection paths, task 06's fail-safe filter behavior, task 07's classification-table accuracy (spot-checked against real handlers), and task 08's migration-ordering safety. One real, moderate-severity finding: `dispatch_to_agent` reflexes were also visible to and spuriously "fired" by the pre-existing generic per-turn reflex pass (`Engine.EvaluateState`), which has no live dispatch hook for that action kind -- inflating `fired_count`/`last_fired_at` telemetry, writing a redundant `event_log` row, and firing `EmitReflexFired`/`EmitReflexActionStaged` plugin hooks with no real dispatch having occurred (confirmed via direct source trace, not inference). Did not affect actual routing correctness. Fixed as `09` per `EXECUTION-PROCESS.md`'s fix-as-new-worker-task discipline (not an inline patch) -- `Engine.EvaluateState` now skips `dispatch_to_agent` rows entirely before any evaluation/debounce/apply/hook-emission step; the 5 other action kinds confirmed unaffected by a dedicated mixed-reflex test. Orchestrator independently re-verified the fix live post-merge: the exact `fired_count`-doubling behavior that occurred pre-fix (a single real turn bumped `fired_count` by 2, confirmed against real DB state from the earlier dogfeed run) was reproduced as exactly `+1` post-fix, with exactly one `dispatch_to_agent` `event_log` row and no duplicate. Also independently sanity-checked the Orchestrator's `dev_tools.go` out-of-scope claim via `git log` and confirmed it holds. **Phase 4 is `reviewed` and closed.**

**Parallelization:**
- **Wave 1 — parallel.** `01, 02, 04, 05, 06, 07`. Coordination, not hard blocks: `05` changes what `SelectForAgent` reads before `06`'s plugin filter hooks its output — land `05` first if both are in flight together. `06`/`07` both touch tool-catalog-rendering-adjacent territory — low risk, note for awareness.
- **Wave 2 — parallel.** `03` (needs `02`), `08` (needs `02`) — both depend on `02` landing but not on each other.

**Escalation logged**: the reaper item's real-world-behavior verification (originally scoped here as `01`, now `TASKS/phase-8/05-verify-reaper-behavior.md`) is resolved via historical-log analysis against the real production DB backup — see `TASKS/ESCALATIONS.md`'s corresponding entry and Phase 8 below.

## Phase 5 — Plugins & Registers (8 task files)

| Task | Status | Depends on |
|---|---|---|
| 01-build-assignment-api | reviewed | Phase 1 tasks 01-08 (landed via the Phase 1→main merge) |
| 02-build-plugin-installed-enabled-state-model | reviewed | none |
| 03-wire-registers-agent-profiles | reviewed | Phase 1 in full (landed via the Phase 1→main merge); held until `02` merges |
| 04-close-cli-install-hot-reload-asymmetry | reviewed | none |
| 05-develop-registers-panels-and-crud | reviewed | none; held until `02` merges — scope corrected 2026-08-19 (see task file), now `crud[]` only |
| 06-make-http-middleware-plugin-extensible | superseded, 2026-08-21 — see `TASKS/plugin-system/07-make-http-middleware-plugin-extensible.md` | **operator design decision now settled (builtins only, priority-ordered) — implementation tracked under `TASKS/plugin-system`, not this phase** |
| 10-fix-list-agent-tools-endpoint-stale-permissions-view | reviewed | `TASKS/phase-4/05`, `01` (both already landed) — fix-as-new-worker-task for a real gap found during Orchestrator live dogfeed validation, see `TASKS/phase-5/10-fix-list-agent-tools-endpoint-stale-permissions-view.md` |
| 11-fix-hot-reload-never-applies-manifest-registrations | reviewed | `02`, `03`, `04`, `05` (all already landed) — fix-as-new-worker-task for a real, pre-existing-but-newly-load-bearing gap found by the fresh Phase 5 Reviewer, see `TASKS/phase-5/11-fix-hot-reload-never-applies-manifest-registrations.md` |
| 12-fix-unload-plugin-wrong-identifier | reviewed | `11` (already landed) — fix-as-new-worker-task for a real bug the Orchestrator live-reproduced during `11`'s own post-merge dogfeed re-verification (reload of an already-loaded plugin always 500s; `11`'s own new rollback code shares the bug), see `TASKS/phase-5/12-fix-unload-plugin-wrong-identifier.md` |

**Parallelization:** `02`, `03`, `05` all touch `internal/plugin/registrations.go` (different sections — the gating wrapper, the `agent_profiles` stub, the `panels`/`crud` stubs) — real overlap risk; land `02` first (it changes the shared gating structure `applyManifestRegistrations` wraps), then `03`/`05` can layer their specific registration logic on top. `01` (new REST endpoints, `internal/api/*`) has low file overlap with this cluster. `04` (`plugin_cmd.go`) and `06` (`server.go`) have no overlap with anything else in this cluster — fully parallel-safe.

**Escalation logged** (`TASKS/ESCALATIONS.md`): `06-make-http-middleware-plugin-extensible` has a real, unsettled design question (where plugin middleware may legally sit relative to the existing security-ordered chain — CORS-outside-auth, body-limit-inside-auth, caller-identity-between) that needs explicit operator input before implementation, not a worker default-guess. Do not dispatch `06` as a routine batch task until that input is recorded. **Confirmed still unresolved as of 2026-08-19 — skipped for this batch, flagged to the operator at wrap-up.**

**Orchestrator scope correction (2026-08-19)**: `05`'s original scope asked for `registers.panels[]`'s frontend rendering half, contradicting the standing "no frontend in any phase" operator instruction and `docs/engineering/TASKS.md`'s own already-corrected Phase 5 text. Narrowed to `crud[]`-only before dispatch; `panels[]`'s backend/manifest half confirmed already fully wired, nothing left to build there.

**Validation (2026-08-19, Orchestrator):** all 6 tasks (01-05, 10) merged to `main`; backend baseline (`go build ./...`, `go vet ./...` -- same 2 pre-existing `container.go` findings, `go test ./...`) fully green. Real dogfeed against the redeployed live `nanite-api-service`: clean restart, all 11 real builtin plugins loaded correctly through `02`'s new DB-backed state model (`"plugins: loaded builtins","count":11`). Exercised `01`'s assignment API directly: created a real role, set `role_id` on a real agent at creation, granted/revoked a real `agent_tools` entry via the new endpoints -- all worked. That same live exercise surfaced a real gap: `GET /api/agents/{id}/tools` (a pre-existing Phase 1 endpoint neither `01` nor `phase-4/05` touched) still computed `allowed` from the legacy `tool_permissions` path, showing every tool as allowed regardless of real `agent_tools` grants -- the same "two systems of record" pattern already closed at two other surfaces, just never closed here. Fixed as `10`, verified live post-merge: a fresh agent showed only `always_included` tools allowed before any grant, and exactly `{granted tool} + always_included` after -- matching the bug reproduction exactly. Test agents/roles cleaned up after each check. Ready for fresh Reviewer dispatch.

**Orchestrator scope correction (2026-08-19):** `05-develop-registers-panels-and-crud.md` originally asked for `registers.panels[]`'s frontend rendering half (a "generic frontend component" + `npm run build` in its Done means) — this directly contradicts the standing operator instruction "no frontend work in any phase, ever" (already banner'd on `01`) and `docs/engineering/TASKS.md`'s own already-corrected Phase 5 text ("`registers.panels[]`'s rendering half is frontend, deferred to the separate frontend pass, not this phase"). Task file corrected before dispatch to `crud[]`-only scope; `panels[]`'s backend/manifest half is already fully wired per the task's own Context, so nothing remained to build there once the frontend half was correctly excluded.

**Review (2026-08-19, fresh Reviewer, no shared context with workers): 5/6 tasks (`02`-`06` minus the skipped `06`) PASS clean, 1 real finding fixed as `11`, which itself surfaced a second real finding fixed as `12` — two consecutive fix-as-new-worker-task cycles before the phase closed.** Reviewer traced the merged `2a7e15b4..HEAD` diff against actual source, confirmed by full-repo grep that `applyManifestRegistrations` — the single function every `registers.*` manifest category funnels through (envelopes, components, slots, keybindings, commands, events, `crud[]` from `05`, `http_routes`, `mcp_servers`, `agent_profiles[]` from `03`, card_rules, panels) — had exactly two callers, both boot-time-only (`loader.go`), never the API-driven install/enable/reload path `runPluginLoadIntoHost` that `04`'s own CLI hot-reload default and every plugin-manager UI action route through. Practical effect: a plugin installed via `04`'s new no-restart path spawned and "went live" per the CLI's own success message, but none of its manifest-declared registrations actually took effect until a real process restart — directly contradicting `04`'s own deliverable and silently stranding `03`/`05`'s new registration categories. Bundled with two smaller findings from the same pass (`pluginDisable`'s stale rename-based comment, `pluginList` silently dropping every disabled plugin by trying to read a `plugin.yaml.disabled` path the DB-backed model never creates). Fixed as `11`: exported `plugin.ApplyManifestRegistrations`, wired it into `runPluginLoadIntoHost` right after `LoadPlugin` succeeds with rollback-via-`UnloadPlugin` on failure, corrected both comment and silent-drop bugs. Orchestrator live-verified `11` post-merge with a real throwaway subprocess plugin declaring `registers.crud[]`, installed via the CLI's hot-reload default — confirmed genuinely live with no restart via both the HTTP route and a server-log line proving `ApplyManifestRegistrations` actually ran.

That same live re-verification surfaced a second, unrelated real bug: reloading the same plugin a second time 500'd with `"already loaded"`. Traced to `unloadPluginFromHost` (and `11`'s own new rollback branch, which had copied the identical pattern) calling `Host.UnloadPlugin(manifest.Name)` — the display name — when the host's registry is keyed by `p.ID()`/`manifest.Identifier()`; the bug itself pre-dates this whole Phases 2-5 batch (`2026-04-13`) but is newly load-bearing because `11` just made the rollback path reachable and because `04`'s "no restart needed" deliverable depends on repeat reloads actually working. Fixed as `12` (both call sites corrected, three new regression tests using a deliberate `id != name` fixture — the shape every existing test in the repo lacked, which is exactly how this shipped unnoticed). Orchestrator live-verified `12` post-merge: three consecutive reloads of the same plugin all succeeded, each cycle's server log showing a clean unload/load pair and a full, non-duplicated registration set. **Phase 5 is `reviewed` and closed** (`06` remains explicitly skipped pending operator input, tracked separately below).

## Phase 6 — Envelopes & Cards (6 task files)

| Task | Status | Depends on |
|---|---|---|
| 01-rebuild-todo-list-as-composition | reviewed | none |
| 02-rebuild-plan-review-as-composition | reviewed | none |
| 03-rebuild-subagent-spawn-approval-as-composition | reviewed | none |
| 04-build-interactive-table-row-actions-primitive | reviewed | none |
| 05-exclude-card-data-from-replayed-context | reviewed | none |
| 06-fix-cli-boot-content-card-type-list | reviewed | none functionally; best run after `01`–`04` so the sourced list reflects the final type set |

**Review (2026-08-21, fresh Reviewer, no shared context with the implementing worker): PASS, task 06.** Independently re-ran the full baseline fresh (not cached) — clean. Wrote and ran a throwaway test invoking `envelopeSchemaContent()`/`claudeMDBody()` against a real `envelopes.LoadCore()`-populated registry, confirming output is exactly the 18 real core types, sorted, `subagent-spawn-approval` present, no `todo-list`/`plan-review`/other Phase-0-cut types, no stale path references — matching the Orchestrator's own dogfeed log. Verified via `git log -S` that the task's own claimed bug fix (a doc comment citing a `BuildEnvelopeSchema` export that never existed) is real. Confirmed the five files outside the task's originally-cited list (`internal/mcp/elicitation.go`, `internal/service/chat_loop_state.go`, `internal/service/chat_loop_terminated.go`, `internal/selftools/self_tools.go`/`_describe_test.go`) are legitimate narrow doc-comment corrections, not scope creep. Two non-blocking, pre-existing observations logged (not introduced by this task, not blockers): a stale `"type": "nanite"` example in the planted boot-content header that would fail real validation if used verbatim, and `08-cards.md`'s "known live bug" section now being stale — consistent with 01-05 also not circling back to update that doc. **Task 06 is `reviewed` and closed. All 6 Phase 6 tasks are now `reviewed`.**

**Parallelization:** `01`, `02`, `03` **all edit the same shared external manifest file** (`libs/go-envelopes/manifest/envelopes.yaml`, each removing/replacing a different standalone entry) — worktree-isolate for the actual coding work, but **merge one at a time**, same pattern as Phase 0's `15a`/`15c` card-type-registration coordination. `04` touches a different file in the same external module directory (`table-card.schema.json`) — lower risk, still flag for awareness. `05` (`internal/chat/context_client.go`) and `06` (`sandbox_content_*.go`) have no overlap with `01`–`04` or each other — fully parallel-safe.

**Merge notes (2026-08-21, Orchestrator).** All five workers left their changes uncommitted in their own worktrees rather than committing to their branch — a recurring pattern in this project (see Phase 5 `12`'s Work Log for a prior instance). Each was asked to commit before merge; verified via `git log <branch> --oneline` and `git diff HEAD...<branch> --stat` before every merge, matching exactly what each worker's own report described. `01`+`02` both independently edited `internal/selftools/self_tools.go` and `ui/src/components/chat/envelopes/primitives/ListCard.tsx` (both add a `data_source`-driven live-rendering branch to `ListCard`) — `self_tools.go` auto-merged cleanly; `ListCard.tsx` required a real manual merge, combining `01`'s `LiveTodoList`/`ListRow` (`data_source.kind:"todos"`) with `02`'s `PlanStepsListCard`/`PlanStepRow` (`data_source.kind:"plans"`) as two distinct, intentional components kept side by side rather than force-unified — re-verified via a full frontend build (`tsc -b && vite build`, clean) and `vitest run` (187/187 pass) after resolution, not just absence of conflict markers.

**Shared `libs/go-envelopes` mirror (2026-08-21, Orchestrator).** Per the coordination hazard already logged in `TASKS/ESCALATIONS.md` (2026-08-18, "shared `.claude/libs/go-envelopes` replace-target"), this file is not worktree-isolated — all four tasks' (`01`/`02`/`03`/`04`) edits landed as one combined uncommitted diff in the single shared physical checkout (`/Users/chrispian/dev/hollis-labs/libs/go-envelopes`), outside the Nanite git repo and outside this session's own worktree-isolation reach. A dispatched worker (task 03's) initially attempted the checkpoint commit across a chain of escalating requests, raised a legitimate concern about task-scope authorization (being asked to vouch for four tasks' combined correctness) partway through and declined further action — a reasonable call, addressed by confirming provenance directly and re-routing to a fresh agent rather than pressing a worker that had flagged discomfort. That fresh agent found the prescribed test fixture fix (`registry_test.go`/`validator_test.go`, hardcoded to the now-removed `"todo-list"` as their "schema-less core type" fixture) did not actually work — `session-task`, the suggested substitute, turned out to already have a real schema, and empirical audit found **no real core type remains schema-less** after this phase's removals. Resolved as a synthetic-fixture rewrite (via `registry.go`'s existing `WithManifestFS` test hook) rather than coupling the test to real manifest content — a test-hygiene fix within Orchestrator judgment, not an operator escalation. Landed as go-envelopes commit `7978078c` (`manifest/envelopes.yaml`, three schema files, `CHANGELOG.md` backfill, both test files). Re-verified in this worktree post-commit: `go build`/`go vet` (2 pre-existing `container.go` findings only) / `go test ./...` clean, frontend build + `generate-plugin-imports.mjs --check` + `vitest run` (187/187) all clean against the final committed manifest state.

**Validation (2026-08-21, Orchestrator).** Real dogfeed, not just green tests: built a scratch binary from this branch, launched it against an isolated copy of the real production DB backup (`~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-132726`, copied to an absolute scratch path, never the tracked file) on a scratch port — did not touch the live Cerberus-deployed `nanite-api-service`, since other concurrent orchestrator sessions (loops/plugin-system/skills) may depend on it. Clean boot, zero errors: `"envelope registry loaded","count":18,"source":"go-envelopes v0.1.0"` (matching the frontend codegen's own count). Exercised real read paths (`/api/plans` returned real production plan data with the step shape `02`'s composition expects). Scratch server torn down after.

**Review (2026-08-21, fresh Reviewer, no shared context with any implementing worker): PASS, all 5 tasks, no fix-and-re-review cycle needed.** Independently re-ran the full baseline (backend + frontend) rather than trusting Work Log claims; also independently verified the external `go-envelopes` repo itself (`go build`/`go test ./...` clean at commit `7978078c`). Confirmed `02`'s Context-section correction (real emitter is `self_tools.go`'s `plan_create` prompt string, not `subagent/service.go`) by independent grep. Confirmed `03`'s zero-backend-Go-change claim by diffing against `main` directly. Confirmed the manual `ListCard.tsx` merge holds up — `LiveTodoList`/`ListRow` (01) and `PlanStepsListCard`/`PlanStepRow` (02) coexist correctly, `ListRow` genuinely shared, nothing dropped from either task; cross-checked every hook call against its real signature. Two non-blocking observations logged, not requiring a fix: (1) `04`'s interactive table-card primitive has zero real production emitter yet (fully wired, reachable, openly disclosed in its own Work Log — judged a legitimate primitive-before-first-consumer situation, not a "dead wired feature" regression); (2) `internal/api/envelopes.go`'s `handleEnvelopeRespond` sets `responded_at` before invoking the `ResponseHandler`, so a handler rejection (e.g. a stale table-card `action_id`) has no retry path — pre-existing generic framework behavior shared by every response handler, not introduced by `04`, flagged only because table-card's click-driven UX makes it more reachable day-to-day. **Tasks 01-05 are `reviewed` and closed.**

## Phase 7 — PTY Rename (1 task file)

| Task | Status | Depends on |
|---|---|---|
| 01-rename-pty-naming-scrub | not-started | all of Phase 0 (see task file) |

**Solo phase.** This task touches ~90 files across nearly every subsystem the earlier phases modify (`chat_generate.go`, `container.go`, `chat.go`, `agent_deps.go`, durable-agent files) — a pure identifier/comment rename, no logic change. Run after every logic-changing task in Phases 2-6 has landed, to avoid repeated rebasing against a fast-moving set of files.

## Phase 8 — Test, Review, Verify (5 task files)

| Task | Status | Depends on |
|---|---|---|
| 01-set-default-runtime-kind | not-started | none functionally (Phase 2's `runtime_kind` wiring and boot-profile-catalog retirement, both already landed) |
| 02-agent-reflex-propose-and-pending-reflexes-testing | not-started | none |
| 03-test-grounding-in-real-sessions | superseded | Wave 4 AD-06 retirement (`09/01`) removed the subsystem and its integration seams |
| 04-truncation-review | not-started | none; coordinate with `TASKS/phase-4/06-add-filter-tool-selection.md` (both touch tool-catalog rendering) |
| 05-verify-reaper-behavior | not-started | none |
| 06-build-test-harness-adversarial-suite | not-started | none — first piece of a broader test-harness effort, scoped to adversarial testing of the failure-footer + sources-gate anti-hallucination mechanisms, see `TASKS/phase-8/06-build-test-harness-adversarial-suite.md` |
| 07-telemetry-buildout | not-started | none — grounded in a 2026-08-19 research-auditor pass over agent launching/turns/tool-calls/reflexes/steering/subagent-dispatch, see `TASKS/phase-8/07-telemetry-buildout.md` |

### ✅ `01` re-scoped 2026-08-19 — no longer a live experiment, no sign-off required

**Superseded.** This task originally asked for a live CLI-vs-API experiment routing a real Curator wake through an unproven CLI path, and required explicit live operator sign-off before dispatch (that banner is now removed). A separate dedicated review of the CLI vs. API paths concluded: **keep both paths** — they're much closer in behavior post-Phase-2-5 than originally assumed, so there's no "which one wins" experiment left to run. The task is now an ordinary settings/config change: make the CLI-vs-API choice explicit at three tiers — app-level default (**CLI**), a system-wide override, and the existing per-agent override. See `TASKS/phase-8/01-set-default-runtime-kind.md` for the full re-scope. Renamed from `01-cli-vs-api-experiment-durable-agents.md`.

**Escalation logged**: `05-verify-reaper-behavior`'s "no live traffic during this effort" scoping conflict (flagged as a planning landmine) is resolved in its own task file via historical-log analysis against the real production DB backup, not live observation — see `TASKS/ESCALATIONS.md`'s corresponding entry for the formal record. That analysis already found a real, currently-live bug (`last_activity_at` never writes in production, so the reaper's activity-reset fix has been silently non-functional since it shipped) — `05` scopes root-causing and fixing this as its primary deliverable.

## Phase 9 — Final Clean-up (1 task file; items formerly numbered 3–4 in TASKS.md's original Phase 6 remain deliberately left index-level only, per the operator's original scope decision — see `docs/engineering/TASKS.md`'s own framing, unchanged)

| Task | Status | Depends on |
|---|---|---|
| 01-old-docs-archival-pass | not-started | none — policy confirmed 2026-08-19, see below |

### ✅ `01-old-docs-archival-pass`'s policy confirmed 2026-08-19

Operator decision, recorded in full in the task file: genuinely superseded docs with no remaining historical value (mainly `docs/architecture/` and top-level `docs/*.md`) are **deleted** per the standing dead-code policy. All ADRs — including the newly-found **third `ADR-001` collision** (`adr/ADR-001-tech-stack.md`, a 23-file `adr/` directory never mentioned by `TASKS.md` or `docs/engineering/decisions/README.md`'s "two colliding sequences" framing) — are **kept with a banner** noting the collision and pointing to `docs/engineering/decisions/` as canonical, not deleted. `docs/audits/` (325 files) is archived/bannered as **one historical-snapshot unit**, not per-file triage. `adr/` is explicitly **in scope** for this pass.

---

## Reflex Action Taxonomy (`TASKS/reflex-taxonomy/`, outside the Phase 0-9 sequence)

Implements `docs/engineering/architecture/10-reflex-action-taxonomy.md` — the design produced by `TASKS/phase-4/10-reflex-architecture-review.md`'s dedicated architecture-review session (2026-08-19, operator-signed-off, no code changed). Resolves `docs/engineering/architecture/03-steering.md`'s former "Watch item" (reflexes carrying six distinct jobs with no real internal structure). Kept in its own subfolder — same pattern as `TASKS/adhoc/` — since this work wasn't part of the original `docs/engineering/TASKS.md` plan. See `TASKS/reflex-taxonomy/README.md` for the full read-first list and scope boundaries.

| Task | Phase | Status | Depends on |
|---|---|---|---|
| `01-taxonomy-schema-foundation` | 1 | reviewed | none |
| `02-recurrence-cascade` | 1 | reviewed | `01` |
| `03-shared-decision-engine` | 1 | reviewed | `01`, `02` |
| `04-halt-turn-synchronicity` | 1 | reviewed | `03` |
| `05-provenance-tier-enforcement` | 2 | reviewed | `01` |
| `06-unified-reflex-telemetry` | 2 | reviewed | `03`, `04` |
| `07-harness-reactive-self-tools-design-session` | 2 (parked) | design complete, implementation deferred (superseded — see below) | none — see `docs/engineering/architecture/11-harness-reactive-self-tools.md` |
| `08-fix-resolve-fail-open-visibility` | 1 (fix, post-review) | reviewed | `03` — real finding from Phase 1's fresh Reviewer, see below |
| `09-fix-duplicate-kind-lookup-warn-logging` | 1 (fix, PR review) | implemented | `03`, `08` — real finding from GitHub Copilot's PR #263 review, see below |

**Sequencing.** Phase 1 (`01`-`04`) is a strict serial chain — each task's schema/behavior is a real prerequisite for the next, not just a merge-conflict-avoidance ordering. `05` only needs `01` and touches a largely disjoint file set (`internal/api/reflexes.go`, a new join table) from `02`-`04`'s engine-internal work — parallel-safe with those, but confirm no overlap on specific `internal/store/agent_reflexes.go` functions before running truly concurrently. `06` needs `03` (the single `Resolve()` entry point to hook telemetry into) and is sequenced after `04` specifically to avoid both tasks re-touching `chat_generate.go`/`chat_reflexes.go`'s reflex call site independently. `07` is independent of everything else and is not part of either phase's execution — it's a parked design-session placeholder, matching how `TASKS/phase-4/10` itself was tracked before this folder existed.

**Two concrete, confirmed bugs this closes**: `force_tool_choice` reflexes can currently co-fire with contradictory directives (fixed by `03`'s `first_applicable` combining algorithm); `halt_session` neither preempts other actions in the same evaluation pass nor stops the current turn synchronously — it only stamps a DB flag checked on a later request (fixed by `03`'s same-pass `deny_overrides` preemption + `04`'s turn-level abort in `chat_generate.go`).

**Phase 1 (`01`-`04`) validation (2026-08-19/20, Orchestrator).** All four implemented serially in strict order, each build/vet/test-clean at the time (only the two known pre-existing `container.go` go vet findings throughout). Real dogfeed, not just green tests: redeployed `nanite-api-service` (`cerberus_resource_deploy` + implicit reload — new `launchd_pid`, clean startup log, no errors/panics) so migration `124` ran for real against the live production DB (not just `01`'s own real-backup-copy test). Verified directly via `sqlite3` against the live DB: `reflex_action_kinds`/`reflex_action_categories`/`reflex_provenance_tiers` at exactly 6/2/3 rows, and all three real `halt_session` seeds (`drift_detector_echo`, `task_complete_self_terminate`, `task_timeout`) correctly backfilled to `provenance_tier='system'`, `recurrence_override_seconds` NULL. Cross-checked the live read path too — `GET /api/agents/{id}/reflexes` against a real agent correctly serializes the new `provenance_tier`/`recurrence_override_seconds` fields for real pre-existing rows, confirming the API/JSON surface isn't broken by the schema change. Did not additionally fire a live `halt_session`/`force_tool_choice` turn against production — `03`'s and `04`'s own regression tests already exercise both fixes through the real `EvaluateState`/`generateResponse` code paths (not mocks), and `04`'s worker mutation-tested its own abort branch. Independently re-read `03`'s `resolve.go` in full (the batch's core deliverable) and confirmed the combining-algorithm logic matches the design doc's Facet 2 table exactly, including the one flagged, low-risk behavior narrowing (an `Apply` failure on a selected `first_applicable` candidate no longer cascades to the next-priority one) — verified `validateReflexDefinition` (`internal/api/reflexes.go:297-314`) does reject malformed `action_spec` JSON at write time, so this is effectively unreachable for valid data. Independently confirmed `04`'s halt-abort code (`chat_generate.go`) is placed before the function's first `StreamChat` call. Marked `validated` above, pending fresh review.

**Phase 1 review (2026-08-19/20, fresh Reviewer, no shared context with the workers): PASS on all four tasks, one real finding, fixed as `08`.** Reviewer independently re-read every changed file (not just Work Logs), independently ran every named regression test plus `-race`, and independently diff-checked `container.go`/`frontend_readiness.go` against the pre-batch commit (zero diff, confirming `04` didn't touch either). Confirmed `03`'s `deny_overrides` short-circuit is a true union across the whole pass (not per-kind), confirmed `04`'s halt-abort sits before all three `StreamChat` call sites, confirmed the two named regression tests (`force_tool_choice` co-firing, same-pass halt preemption) both genuinely exercise the real `EvaluateState`/`Resolve()` path. **Real finding (moderate):** `Resolve()`'s per-kind combining-algorithm lookup fails open to `all_applicable` with zero logging anywhere in the chain — a transient `kindLookup` failure would silently regress `halt_session` back to "doesn't actually preempt," the exact bug this batch exists to fix, with no operator-visible signal. Not fixed inline — written up as `08-fix-resolve-fail-open-visibility.md` per this project's fix-as-new-worker-task discipline, dispatched to a worker. Two minor/very-minor findings noted (an undisclosed-but-safely-gated cascade-removal in `self_tools_dispatch.go`'s empty-`agent_slug` handling; a related silent-multi-selection edge in the `dispatch_to_agent` fail-open) — folded into `08`'s scope rather than separate tasks, per the reviewer's own recommendation.

**Phase 2 (`05`, `06`) + fix `08` validation (2026-08-20, Orchestrator).** All three build/vet/test-clean (only the two known pre-existing `container.go` findings). `05` (provenance-tier enforcement) and `06` (unified telemetry) are the two most invasive tasks in the batch — `06` deletes a whole write path (`internal/store/reflex_log.go`, the `playbook_match_log` writer) and touches `cmd/nanite/main.go`'s wiring — so reviewed both directly rather than only spot-checking. Read `internal/api/reflexes.go`'s new provenance gate in full: confirmed `handleCreateAgentReflex` hardcodes `CreatedBy:"operator"` (falls through to tier `operator` correctly) and `handlePatchAgentReflex` carries the existing row's real `ProvenanceTier` forward (not a patchable field), so there's no PATCH-based privilege-escalation gap. Read `internal/agent/reflexes/telemetry.go`'s `EmitFirings` in full: correctly built as a thin post-`Resolve()` wrapper (not folded into `Resolve()` itself, keeping decision logic and recording concerns separate), all three real callers (`Engine.EvaluateState`, `attemptReflexDispatch`, `matchDispatchToAgentReflex`) confirmed wired via grep. Independently re-grepped the whole tree (code, frontend, docs, API routes) for any live reader of the retired `playbook_match_log`/`LogReflexMatch`/`ReflexLogger` — found only historical/doc references and the untouched migration files (table intentionally kept, matching this project's own precedent), zero orphaned code paths. Read the `cmd/nanite/main.go` diff in full: `selfTools.Plugins = pluginHost` wiring is correctly placed after `pluginHost` is initialized and `selfTools` is confirmed to be the same real variable used by every other `selfTools.X = ...` wiring in that function. Live dogfeed: redeployed `nanite-api-service` again (new `launchd_pid`, clean startup log, no errors) so migration `125` ran for real against the live production DB — verified via `sqlite3`: `reflex_action_kind_provenance_allow` at exactly 16 rows, `(halt_session, plugin)` correctly absent.

**Phase 2 (`05`, `06`) + fix `08` review (2026-08-20, fresh Reviewer, no shared context with the workers): PASS on all three, no blocking findings.** Reviewer independently traced the PATCH gate-bypass scenario by hand (confirmed `handlePatchAgentReflex` carries the row's real, immutable `provenance_tier` forward and re-checks it against any `action_kind` change — no bypass), independently repo-wide-grepped for any surviving `playbook_match_log` reader beyond the two migration files (none found), confirmed no `event_log` double-write between `chat_reflexes.go`'s removed per-action loop and `telemetry.go`'s centralized write (via a dedicated regression test asserting exactly one row), and confirmed `main.go`'s `selfTools.Plugins = pluginHost` wiring is correctly ordered and points at the same live instance. Ran the full suite plus targeted tests directly, read their assertions rather than trusting names. **One non-blocking observation, not a defect** (explicitly within `05`'s own stated scope boundary): `ApprovePendingReflex` (`internal/store/agent_reflexes.go:658-671`) does its own raw INSERT, bypassing `ActionKindAllowsProvenanceTier` entirely — hardcodes `provenance_tier='operator'`, currently safe since every kind allows `operator` tier today, but a future tightening of the allow-list wouldn't be honored by this path without a separate fix. Logged as a follow-up candidate in `TASKS/ESCALATIONS.md` rather than dispatched as a fix task. All eight tasks (`01`-`06`, `08`) are now `reviewed` and closed. `07` was subsequently run as its own operator design session (2026-08-20) — design complete, see `docs/engineering/architecture/11-harness-reactive-self-tools.md`; implementation was deliberately deferred at the time pending a concrete consumer, no follow-up task filed by that session.

**Update, 2026-08-20 (later session):** the operator moved forward — implementation tasks filed at `TASKS/harness-reactive-self-tools/`, see that section below. `07`'s own "document now, build once a concrete consumer exists" call still stands as an accurate record of that design session's own reasoning at the time; a later decision superseded it.

**Task `09` added, 2026-08-20 (Orchestrator) — GitHub Copilot PR review finding, fixed same-day.** PR #263 (the whole batch) went out for automated review; Copilot flagged two identical, real findings: `attemptReflexDispatch` (`chat_reflex_dispatch.go`) and `matchDispatchToAgentReflex` (`self_tools_dispatch.go`) each logged their own `GetReflexActionKind` failure locally, then `Resolve()` (`08`'s addition) logged the same underlying failure again via the `kindLookup` closure — one degraded-lookup event producing two WARN lines, `Resolve()`'s own line strictly a superset of either call site's. Verified directly against the real code before dispatching (not taken on Copilot's word). Written up as `09-fix-duplicate-kind-lookup-warn-logging.md`, dispatched to a worker, independently re-verified by the Orchestrator (diff read directly, build/vet/test re-run clean) before pushing `308a829f` to the PR branch and replying to both review comments. Pure logging deduplication, no behavior change — not re-reviewed by a fresh Reviewer given the scope (6 lines removed, mechanical, already independently verified twice).

---

## Harness-Reactive Self-Tools (`TASKS/harness-reactive-self-tools/`, outside the Phase 0-9 sequence)

Implements `docs/engineering/architecture/11-harness-reactive-self-tools.md` — the design produced by `TASKS/reflex-taxonomy/07-harness-reactive-self-tools-design-session.md`'s dedicated design session (2026-08-20, operator-signed-off, no code changed). A sibling to `TASKS/reflex-taxonomy/`, not nested inside it — the mechanism was deliberately scoped as *adjacent* to Reflexes, not a Reflex action kind (a self-tool call is its own trigger; there's no predicate/event/interval for a reflex evaluation loop to watch). See `TASKS/harness-reactive-self-tools/README.md` for the full read-first list and scope boundaries.

| Task | Phase | Status | Depends on |
|---|---|---|---|
| `01-move-self-tools-to-internal-selftools` | 1 | reviewed | none |
| `02-reactive-layer-schema` | 1 | reviewed | none (parallel-safe with `01`) |
| `03-reaction-engine-core` | 1 | reviewed | `01`, `02` |
| `04-render-card-construction` | 1 | reviewed | `03` |
| `05-selftool-reaction-telemetry` | 2 | reviewed | `03` |
| `06-collapse-envelope-marker-consumers` | 2 | reviewed | none — separable DRY cleanup, not required for `04`'s render_card path |
| `07-worked-example-task-update-report` | 2 | reviewed | `01`, `03`, `04`, `05` (`06` not required) |

**Sequencing.** Phase 1 (`01`-`04`) builds the mechanism itself: the package move (`01`) and the new reactive-layer schema (`02`) are independent of each other; `03`'s reaction engine needs both; `04`'s render_card marker construction needs `03`'s resolved payload. Phase 2 layers telemetry (`05`) and the worked example (`07`) on top, plus one separable DRY cleanup (`06`, the three pre-existing `ENVELOPE_DATA` marker-extraction implementations collapsed into one — not on `07`'s critical path, since those three consumers are already marker-agnostic and pick up `04`'s output unchanged).

**Batch complete, all 7 tasks reviewed clean (2026-08-20).** Phase 1 (`01`-`04`, the core mechanism) and Phase 2 (`05`-`07`, telemetry + marker-consumer DRY cleanup + the `task_update_report` worked example) both implemented, independently build/vet/test-verified by the Orchestrator, and each passed its own fresh-reviewer review with no blocking findings. Import-cycle constraint holds throughout (`internal/selftools`/`internal/selftools/reactions` never import `internal/chat`/`internal/service`); the `07`-added `internal/mcpserver`→`internal/chat` edge doesn't cycle either. Migration `126_selftool_reactions.sql` landed first against the shared `125` baseline — the sibling `TASKS/scheduling/01`'s own `126` claim (see that section's own note below) will need to renumber to `127` when dispatched. One real, self-corrected finding along the way: a live security gap (a new HTTP route's auth exemption lacked the loopback check its own comment claimed) was found and fixed pre-merge, and a subsequent commit-message/Work-Log inaccuracy about *when* that fix landed was itself caught by the Phase 2 reviewer and corrected — see `TASKS/ESCALATIONS.md`'s two 2026-08-20 entries. Doc-writer handoff next.

---

## Scheduling (`TASKS/scheduling/`, outside the Phase 0-9 sequence)

Implements `docs/engineering/architecture/12-scheduling.md` — the design produced by a dedicated architecture design session (2026-08-20, operator-signed-off, no code changed) that reviewed `go-scheduler`, Hadron's live adoption of it, Torque's own scheduling engine, and Nanite's existing half-built scheduling substrate before proposing anything. A sibling to `TASKS/reflex-taxonomy/` and `TASKS/harness-reactive-self-tools/`. Resolves Torque tasks `CW-20260819-0004` and `CW-20260819-0006`, unblocks `CW-20260819-0005` (full text `HANDOFF.md:141-177`). See `TASKS/scheduling/README.md` for the full read-first list and scope boundaries.

| Task | Phase | Status | Depends on |
|---|---|---|---|
| `01-schema-schedule-kind-collapse-and-retry-columns` | 1 | reviewed (merged `04f05392`) — pass, no findings | none |
| `02-store-adapter` | 1 | reviewed (merged `f832cedc`) — pass, no findings | `01` |
| `03-runner-adapter-and-job-taxonomy` | 1 | reviewed (merged `becaa3d9`) — pass, no findings | none directly (parallel-safe with `01`/`02`) |
| `04-retry-backoff-on-fail-policy` | 1 | reviewed (merged `010d8975`) — pass; real correctness fix found+applied (`Job.RunID` unstable across retries; correlate by `ScheduleID`+open-row status instead), independently confirmed twice (Orchestrator, then fresh Reviewer) against `go-scheduler` source; one non-blocking heads-up logged in `ESCALATIONS.md` for `06` | `01`, `02`, `03` |
| `05-engine-wiring-and-full-replace` | 1 | reviewed (merged `0c8598c9`) — pass; restart-mid-cycle no-double-fire claim independently re-verified via the reviewer's own separate live re-run (real production code, harsher non-graceful kill than the original dogfeed); two minor non-blocking findings logged in `ESCALATIONS.md` as follow-up candidates | `02`, `04` |
| `06-schedule-fire-telemetry` | 2 | reviewed (merged `4946e64b`) — pass, no findings | `03` |
| `07-wire-add-schedule-reflex` | 2 | reviewed (merged `9559260e`; fix merged `cd9593f9`, fix re-reviewed pass) — real, reproduced bug found (malformed non-empty `schedule_spec` silently inserted a never-fires row), fixed with a `cron.ParseStandard` pre-check matching `08`/`09`'s pattern, independently re-verified | `02` |
| `08-agent-self-tool` | 2 | reviewed (merged `76ce8a72`) — pass; one minor non-blocking nit (hardcoded `MaxRetries=3` duplicates the column default instead of leaving it unset) | `02`; cross-batch on `TASKS/harness-reactive-self-tools/01` — confirmed already landed, targeted `internal/selftools` directly |
| `09-operator-http-api` | 2 | reviewed (merged `537e8bf2`) — pass, no findings | `02` |

**Batch complete, all 9 tasks reviewed clean (2026-08-20).** Phase 1 (`01`-`05`, the core `go-scheduler` engine adoption) and Phase 2 (`06`-`09`, telemetry + producers + operator API) both implemented, independently build/vet/test-verified by the Orchestrator after every merge, and each phase passed a dedicated fresh-reviewer section review (no shared context with the implementing workers). The production scheduling mechanism (2-minute ticker + 15-minute-lookback heuristic) is fully retired; `go-scheduler`'s `Engine` is now the live mechanism, exercised by real live dogfeeds at multiple points including an independent reviewer-run restart-mid-cycle no-double-fire re-verification against real production code. One real, reproduced bug was found in Phase 2 review (`07`'s missing cron-syntax pre-check), fixed via the standard fix-as-new-worker-task-then-re-review discipline, and independently re-verified clean. Three minor, non-blocking cosmetic/DRY findings remain logged in `ESCALATIONS.md` as follow-up candidates for a future cleanup task (not fixed, not blocking): the `Container.Engine` → `ScheduleEngine` naming rename, `ComputeAgentScheduleNextRun`'s duplicated cron-math vs. `gosched.NextRun`, and `managed_durable_configs.go`'s same missing-cron-validation gap as `07`'s (fixed) one — plus one micro-nit in `08` (a hardcoded `MaxRetries=3` literal). Doc-writer dispatched next for end-of-batch handoff and summary.

**Sequencing.** Phase 1 (`01`-`05`) adopts `go-scheduler` and retires the existing 2-minute-ticker/`wakeScheduleDue` mechanism in full — `05` is the highest-blast-radius task in the batch (deletes the one live production scheduling path in the same change that replaces it) and is sequenced last, needing both the Store adapter (`02`) and the complete retry-wrapped Runner (`04`) ready before the new `Engine` can start for real. Phase 2 (`06`-`09`) is four mutually parallel-safe tasks now dispatched in parallel (worktree-isolated) now that Phase 1 has landed: observability, the two remaining producers (`add_schedule` reflex wiring, a new agent self-tool), and the operator API.

**Cross-batch migration-numbering collision, real, flagged explicitly in both folders — RESOLVED.** `harness-reactive-self-tools` landed `126_selftool_reactions.sql` first; `01` was dispatched with, and landed, `127_schedule_runs_and_retry_policy.sql` instead of its provisional `126` claim.

**`03`'s payload-shape contract, fixed for `02` to encode against** (from `03`'s Work Log): `durable_agent_wake: {instance_id, project_id?, reason?, prompt?, facts?, metadata?}` (note: `instance_id` is a `durable_agent_instances.id`, not `agent_schedules.agent_id` — `02` must resolve profile→instance at Schedule-conversion time); `agent_workflow_run: {workflow_name, params?, project_id?, agent_profile_id, parent_session_id?, timeout_seconds?}`; `command_run: {agent_id, command, args?}`; `reflex_dispatch: {reflex_id, session_id?}`.

**Dispatched 2026-08-20.** Execution Orchestrator booted; `01` and `03` in progress (parallel, worktree-isolated — file-disjoint per README's dependency table). Migration-numbering collision confirmed real and resolved: latest migration on disk at dispatch time is `126_selftool_reactions.sql` (harness-reactive-self-tools batch landed first) — `01` is dispatched with `127_schedule_runs_and_retry_policy.sql`, not the provisionally-claimed `126`. `08`'s cross-batch dependency (`internal/selftools`) is also confirmed already landed on disk — `08` will target `internal/selftools` directly when dispatched, no fallback needed.

---

## Escalations raised during Phases 1-6 planning (new since the Phase 0 presentation; phase/task numbers below use the 2026-08-19 Phase 2-9 resequencing)

In addition to `TASKS/ESCALATIONS.md`'s existing entries (Phase 0 planning), this pass adds:
- **Process note**: a research fork drifted into unauthorized self-dispatch (second occurrence of Phase 0's failure mode) — audited and kept, see `ESCALATIONS.md`.
- **Phase 8 reaper verification**: the "no live traffic to verify against" scoping conflict, resolved via historical-log analysis — see `ESCALATIONS.md`, and the real bug it found (reaper's activity-reset fix silently non-functional in production).
- **Phase 5 middleware plugin-extensibility** (`06`): a genuine, unsettled design question needing operator input before implementation — see `ESCALATIONS.md`.
- **Phase 9 item 1's docs-archival policy**: resolved 2026-08-19 (operator decision — delete superseded, keep+banner all ADRs including the third `adr/` collision, archive `docs/audits/` as one unit) — see Phase 9's table above.
- **Phase 8 item 1's sign-off requirement**: not an escalation in the doc/reality-mismatch sense, but the one item in this whole pass requiring a standing, repeatable, live-dispatch-time gate — see above.

See `TASKS/ESCALATIONS.md` for the full list of doc/reality mismatches found during planning, including which ones are already resolved (via Orchestrator/Planner judgment call within existing docs) and which ones genuinely need an operator decision before the relevant task is dispatched.

---

## Teams (`TASKS/teams/`, outside the Phase 0-9 sequence)

Implements `docs/engineering/architecture/15-teams.md` — the design produced by a dedicated architecture-alignment session (2026-08-20, operator-signed-off, no code changed) that reviewed an external proposal against Nanite's real code (`internal/agentworkflow`, `internal/dispatch`, `internal/agent/reflexes`, `internal/messaging`, Agent Construction) before proposing anything, generalizing today's hardcoded three-role dispatch into a configurable, reusable N-slot organizational shape that compiles to an ordinary `agentworkflow` run rather than a new orchestration engine. A sibling to `TASKS/reflex-taxonomy/`, `TASKS/harness-reactive-self-tools/`, and `TASKS/scheduling/`. See `TASKS/teams/README.md` for the full read-first list and scope boundaries.

| Task | Phase | Status | Depends on |
|---|---|---|---|
| `01-team-definition-schema` | 1 | reviewed (migration `128`) | none |
| `02-team-run-members-table` | 1 | reviewed (migration `129`) | none directly (parallel-safe with `01`/`03`/`04`/`05`) |
| `03-stepkindflex-schema` | 1 | reviewed (migration `130`) | none directly (parallel-safe) |
| `04-team-authority-schema` | 1 | reviewed (migration `132`) | `01` |
| `05-agent-reflexes-run-scoping` | 1 | reviewed (migration `131`) | none directly (parallel-safe) |
| `06-stepkindflex-executor` | 2 | reviewed (migration `133`) | `03` |
| `07-team-compiler` | 2 | reviewed (no migration — pure Go) | `01`, `02`, `03`, `04`, `05`, `06` |
| `08-team-run-launcher` | 2 | reviewed (no migration — pure Go) | `07`, `04` |
| `09-team-routing` | 3 | reviewed (no migration — pure Go) | `05`, `08` |
| `10-team-crud-api` | 4 | reviewed (no migration — pure Go) | `01` |
| `11-team-run-launch-api` | 4 | reviewed | `08`, `10` |

**Sequencing.** Phase 1 (`01`-`05`) is schema/storage only — four of the five tasks are mutually parallel-safe (different tables/columns), worktree-isolated; only `04` has a real (if soft) dependency on `01`'s Team Slot vocabulary. All five touch `internal/store/migrations/` — coordinate numbering at dispatch time, same real cross-batch collision pattern the scheduling and harness-reactive-self-tools batches already hit each other with (see `README.md`). Phase 2 (`06`-`08`) is a strict-ish serial chain: the flex-step executor, then the compiler that depends on every Phase 1 primitive plus the executor's real config shape, then the launcher that calls the compiler and does slot resolution. Phase 3 (`09`) needs both run-scoped reflexes and a real launched run to route against. Phase 4 (`10`-`11`) is the operator/consumer-facing surface, dispatched last.

**Real, load-bearing corrections to the design doc, found during this planning session's own research against the live codebase** (not just doc-vs-doc inconsistencies — each is cited with file/line in its owning task's Context, and each changes what "generalize the existing mechanism" or "reuse the existing column" actually means in practice): `agent_parent_dispatch_allowlist` (migration 060) is a JSON role-slug column on `agent_profiles`, not a table, and is advisory-only today (rendered into an LLM-facing tool description, no hard enforcement) — `04` must build real enforcement, not copy this pattern. `agent_profiles.durable` is an unrelated eject-survival flag (migration 073), not a signal for whether a slot should wake an existing durable identity — `08` treats `resolution: durable|fresh` as new Team-Slot-level launch config instead. `workflow_run_steps.kind` has a hardcoded DB CHECK (migration 051) that blocks inserting a `flex`-kind step without a real table-rebuild migration — `03` exists specifically because of this. The real messaging self-tool is `message_send`, not `send_message` (that name belongs to the reflex `action_kind`) — `09`. "Slot" already has an established, unrelated, load-bearing meaning in this codebase (`internal/context/slot.go`'s `SlotOrder`, one of six invariants in `internal/context/INVARIANTS.md`) that `15-teams.md`'s own naming-collision review never checked against — `01` requires "Team Slot" always spelled out in new identifiers, and adds the disambiguating GLOSSARY.md entry.

**Two explicitly-required stress-test answers, not left as open design questions**: the design doc's own "Validating this design" section names the **phase-closure race** (flex-step exit trigger fires while other active members are still working, owned by `06`) and **routing-target resolution failure** (message routes to an unavailable/failed slot member, owned by `09`) as "likely the messiest runtime edges of the whole design," deserving concrete implemented-and-tested answers before implementation, not a passing mention — both are required "Done means" items on their owning tasks.

**Deliberately out of this batch's scope** (see `README.md`'s full list): mid-run elastic slot growth (a concrete, current blocker — `task_execute`'s real recursion-depth-0 cap — not just caution, since a Team orchestrator slot triggering it is itself typically a non-root session); the final authority verb set beyond the three the design doc names; a final locked default for multi-member `@slot` addressing; the eventual Team/Workflow definition split.

**Planned 2026-08-20, not yet dispatched.** Per `EXECUTION-PROCESS.md`'s Phase A discipline, this is the planning checkpoint — present to the operator for review before any worker is dispatched.

**Dispatched 2026-08-20, operator go-ahead given.** Phase 1 (`01`-`05`) implemented in four worktree-isolated workers (`02`/`03`/`05` parallel, `01` alongside them, `04` following once `01` landed) — each merged and reviewed for correctness by the Orchestrator at merge time, migrations `128`-`132` landed in that order with no numbering collisions (pre-assigned ahead of dispatch). One real merge-time conflict found and fixed: `05`'s own dispatch-site test files had stood up a test-local stand-in `team_run_members` table (that table didn't exist in `05`'s worktree at dispatch time); by the time `05` merged, `02`'s real migration-129 table already existed on `main`, so the stand-in collided (`table team_run_members already exists`) — fixed by rewiring both test files to insert through the real `Store.InsertTeamRunMember` against the real schema instead; full detail in `05`'s own task file Work Log under "Orchestrator merge note."

**Phase 1 validation (2026-08-20, Orchestrator).** Per `EXECUTION-PROCESS.md`'s validation-checkpoint rule — real validation, not just build/vet/test, though Phase 1 is schema/storage only with no runtime wiring yet (no compiler/launcher/enforcement calls this data), so there's no live feature flow to click through yet. Validated instead by: (1) copying the real production DB backup to an isolated scratch path and booting the actual `nanite serve` binary against it — clean startup, `"nanite listening"`, no panics, no migration errors (one unrelated pre-existing `ERROR` about `system-architect`'s stale tool-catalog references, confirmed unconnected to this batch); (2) confirmed via direct `sqlite3` inspection against that booted instance that all three new tables (`teams`, `team_run_members`, `team_authority_grants`) exist with schema matching each migration exactly, `workflow_run_steps.kind`'s CHECK includes `'flex'`, and `agent_reflexes.workflow_run_id` exists; (3) confirmed real pre-existing production data survived the full 95→132 migration chain intact (343 `sessions`, 28 `agent_profiles`, all 21 pre-existing `agent_reflexes` rows correctly `workflow_run_id IS NULL`, both pre-existing `workflow_run_steps` rows intact). Scratch server stopped and scratch DB copy deleted after; `git status --short` confirmed clean, no stray writes to any tracked file. Full repo `go build`/`go vet`/`go test ./...` also re-confirmed clean on `main` after every merge.

**Phase 1 (`01`-`05`) reviewed clean, 2026-08-20** — PASS across all five tasks, two non-blocking findings logged (migration `130`'s Down-section `NO TRANSACTION` gap; `AuthorizedForVerb`'s `to_slot='self'` sharp edge, threaded into task `09`). Full detail in `TASKS/ESCALATIONS.md`'s "Teams Phase 1 review" entry.

**Phase 2 (`06`-`08`) dispatched serially, each reviewed clean before the next started, per the kickoff's explicit gate.** `06` (StepKindFlex executor, migration `133`) — implements both required stress-test answers (phase-closure race: fire-immediately with explicit stand-down; exit-trigger authority: any active-slot member by default). Reviewed PASS; one pre-existing unrelated test flake surfaced (`TestDurableAgentStopRuntimeErrorMarksFailed`, logged as a heads-up). `07` (Team compiler, no migration) — reproduces the SME example exactly, every compiled flex step genuinely round-trips through `06`'s real `parseFlexStepConfig`. Reviewed PASS, no findings. `08` (TeamRun launcher, no migration) — the highest-risk task in this phase: real `may_spawn` enforcement, corrected `resolution: durable` semantics, the `team_run_members`-insertion-ordering solve. Reviewed PASS; two real findings actioned — registry growth pollutes the public A2A agent-card discovery API once wired to real traffic (added as a required Done-means item to task `11`), and `ResolveLazySlot`'s idempotency has a real check-then-act race under true concurrency (threaded into task `09`'s Context, since `09`'s routing layer is the most concrete intended caller). Full detail for all three in `TASKS/ESCALATIONS.md`'s corresponding 2026-08-20 entries. **Phase 2 is `reviewed` and closed.** Phase 3 (`09`) is next.

**Phase 3 (`09`) — one real bug found, fixed, and re-reviewed clean.** `09` implements the required routing-target-resolution-failure stress test (fail loudly via `ErrTeamRoutingTargetUnavailable`, no silent fallback) and the `@slot`-broadcast + coordinator-fallback reflex-installation machinery. First review: FAIL — `InstallTeamRunRouting`'s coordinator-fallback-priority floor guard only applied on the derived-default priority path, so an explicit `TeamRoutingRule.Priority` at or below the fallback floor bypassed the clamp entirely, making that semantic rule permanently unreachable against `reflexes.Resolve`'s `first_applicable` tie-break — reproduced directly by the reviewer. Fixed as a new scoped worker task per the standing fix-then-re-review discipline (floor guard now applies unconditionally to the final priority value regardless of source), committed `311f8950`, with a new regression test exercising the real `reflexes.Resolve` combining path end-to-end. Re-reviewed by a second fresh reviewer: independently reproduced both the original bug (red) and the fix (green) via a throwaway revert, confirmed no other call sites are affected, confirmed the silent-clamp-vs-hard-error judgment call is reasonable and honestly documented. **PASS**, with one small non-blocking follow-up (the clamp path logged nothing when it silently overrode an author's explicit value, in tension with `docs/engineering/standards/code-quality.md`'s silent-fail-open standard) — applied directly (a gated `slog.Warn`, mirroring an existing precedent a few lines below in the same function), re-verified clean. Full detail in `TASKS/ESCALATIONS.md`'s "Teams task 09 review" entry and `TASKS/teams/09-team-routing.md`'s Work Log. **Phase 3 is `reviewed` and closed.** Phase 4 (`10`-`11`) is next.

**Phase 4 (`10`-`11`) — both reviewed clean. Teams batch complete.** `10` (Team definition CRUD API, no migration) — thin REST CRUD over task `01`'s `teams` store, full typed write-time validation for `slots_json`/`phases_json`/`routing_json` (via `SetSlots`/`SetPhases`/`SetRouting`), JSON-shape-only for `authority_json` (task `04` built a separate normalized table, no typed shape exists for this column). Reviewed PASS, no code findings; one process-only finding (this task's own `TASKS/INDEX.md` row had gone stale after merge, corrected alongside this closure — see `TASKS/ESCALATIONS.md`).

`11` (TeamRun launch API, the batch's highest-risk task — shared production wiring) — adds `POST /api/teams/{id}/launch` calling task `08`'s `LaunchTeamRun`, after research confirmed no existing external-facing entry point could express a `TeamRunOverrides`-shaped launch. Resolves the required registry-growth/agent-card-pollution prerequisite from task `08`'s review with two complementary fixes: an unconditional naming-convention exclusion in `AgentCardGenerator.Generate()` (primary — holds regardless of registry state) plus terminal-status eviction via a new `Registry.Unregister` (secondary, narrower). Also wires `TeamRunLauncher` into `Container`/`main.go` for the first time — it had no prior production wiring. Reviewed PASS by a reviewer specifically asked to be rigorous given the stakes: independently confirmed `TeamRunLauncher` and `AgentCardGenerator` share the exact same registry instance in production (the single most important correctness question for the fix to actually work), confirmed the exclusion filter is unconditional with no bypass path, confirmed `Unregister`'s terminal-status gating is correctly scoped against `resumeWorkflowRun`'s real requirement, and independently re-verified every step-1 research claim against source. Two minor non-blocking findings (an unenforced `team-run:` naming-collision risk with a hypothetical operator-authored workflow; a doc-comment overstatement about empty-body handling) logged in `TASKS/ESCALATIONS.md`. Full detail in `TASKS/ESCALATIONS.md`'s "Teams task 11 review" entry and `TASKS/teams/11-team-run-launch-api.md`'s Work Log.

**Phase 4 is `reviewed` and closed. All 11 tasks in the Teams batch (`01`-`11`) are now `reviewed`. The batch is complete.**

---

## Agent Host + ACP (`TASKS/agent-host-acp/`, outside the Phase 0-9 sequence)

Implements `docs/engineering/architecture/16-agent-host.md` (adopt `go-agent-wrapper` — a
sibling repo, `libs/go-agent-wrapper`, with zero adopters today — as Nanite's shared
agent-launch host, replacing bespoke code in `internal/runtime/agent`) and
`docs/engineering/architecture/17-acp.md` (add ACP-as-client support — Nanite driving other
agents' CLIs over the Agent Client Protocol — layered on that host). Treated as one project
per operator direction, since 17 depends structurally on 16 (every ACP adapter is a new
`go-agent-wrapper` `Adapter`/`RuntimeAdapter` implementation). A sibling to `TASKS/teams/`,
`TASKS/reflex-taxonomy/`, `TASKS/harness-reactive-self-tools/`, and `TASKS/scheduling/`. See
`TASKS/agent-host-acp/README.md` for the full read-first list, the two-repo shape of this
batch (most of Phases 1 and part of 3-4 land in the sibling `libs/go-agent-wrapper` repo, not
Nanite itself), and scope boundaries.

| Task | Phase | Status | Depends on |
|---|---|---|---|
| `01-bump-agentkit-pin-and-cut-release` | 1 | reviewed | none |
| `02-split-descriptor-protocol-transport-and-interrupt` | 1 | reviewed | none directly (parallel-safe with `01`) |
| `03-add-go-agent-wrapper-dependency` | 2 | implemented | none |
| `04-migrate-bootdir-layout-to-planter` | 2 | implemented | `02`, `03` |
| `05-migrate-sandbox-profile-to-applier` | 2 | closed (no migration — see `ESCALATIONS.md`, finding folded into `06`) | `03` |
| `05a-extend-wrapper-config-for-real-adapters` | 2 (sibling repo, Phase-1-shaped) | reviewed | none |
| `06-migrate-session-lifecycle-to-wrapper` | 2 | reviewed (PASS, 2 non-blocking findings fixed and verified) | `02`, `04`, `05` (closed), `05a` |
| `07-dogfeed-validate-host-migration` | 2 | reviewed — whole-section Phase 2 review PASS (fresh reviewer ran its own independent live dogfeed incl. broker/kill-9) | `04`, `05`, `06` |
| `18-fix-opencode-workdir-hard-fail-regression` | 2 (found via `07`) | reviewed (Orchestrator-verified, confirmed live in final re-run) | none |
| `22-fix-opencode-turn-never-completes-chat-harness` | 2 (found via `18`, sibling repo `libs/agentkit`) | reviewed (Orchestrator-verified, confirmed live in final re-run) | none |
| `19-fix-codex-missing-skip-git-repo-check` | 2 (found via `07`, sibling repo `libs/go-providers`, tagged v0.24.0) | reviewed (Orchestrator-verified, confirmed live in final re-run) | none |
| `20-fix-agentkit-legacy-waiter-swallows-exit-errors` | 2 (found via `07`, sibling repo `libs/agentkit`, tagged v0.5.0 — operator-approved cross-portfolio fix) | reviewed PASS (fresh reviewer + Orchestrator, confirmed live in final re-run) | none |
| `21-fix-broker-replacement-session-orphans-old-process` | 2 (found via `07`) | reviewed (Orchestrator-verified, regression tests re-confirmed against final build) | none |
| `08-build-acp-client-abstraction` | 3 | reviewed (Orchestrator-verified; `go-agent-wrapper v0.5.0`, pushed+tagged) | `02`; recommended after `07` |
| `09-acp-native-adapter-opencode` | 3 | reviewed (Orchestrator-verified live against real `opencode` binary; `go-agent-wrapper v0.6.0`) | `08` |
| `10-acp-native-adapter-copilot-cli` | 3 | reviewed (Orchestrator-verified live against real `copilot` binary, both stdio+TCP; `go-agent-wrapper v0.7.0`) | `08` |
| `11-nanite-per-agent-protocol-transport-config` | 3 | reviewed PASS (fix landed for 2 real event-translation bugs found on first review, re-reviewed clean) | `09`, `10`, `07` |
| `12-pin-acp-bridge-library` | 4 | reviewed — resolved, operator sign-off recorded (per-provider bridges chosen, `beyond5959/acp-adapter` rejected) | `08` |
| `13-acp-bridge-adapter-claude` | 4 | reviewed (Orchestrator-verified live against real `claude` binary via `claude-agent-acp` bridge; interrupt=Turn confirmed) | `12` |
| `14-acp-bridge-adapter-codex` | 4 | reviewed (Orchestrator-verified live against real `codex` binary via `codex-acp` bridge; found+fixed real CODEX_PATH bundled-version bug; interrupt=Turn confirmed) | `12` |
| `15-acp-bridge-adapter-pi` | 4 | reviewed (Orchestrator-verified; `pi` CLI installed+configured via local Ollama since no cloud creds available, real live turn+cancel verified, interrupt=Turn confirmed) | `12` |
| `16-audit-fs-terminal-proxying-requirement` | 5 | reviewed (no in-process fs/terminal server needed, all 5 adapters self-handle; one minor heads-up logged) | `09`, `10`, `13`, `14`, `15` |
| `17-native-vs-acp-side-by-side-comparison` | 5 | reviewed (Orchestrator independently re-ran both live comparison tests, all four dimensions' numbers confirmed exactly) | `07`, `09`, `10`, `13`, `14`, `15` |
| `23-wire-claude-codex-pi-acp-bridge-dispatch` | post-handoff fix | implemented (Orchestrator-verified: `go.mod` pin, dispatch map, live Claude+Codex turns through Nanite's own API, `go build`/`vet`/`test` clean) | `13`, `14`, `15` |

**Sequencing.** Phase 1 (`01`-`02`) is small, mechanical, foundation work entirely inside the
sibling `libs/go-agent-wrapper` repo — both tasks are file-disjoint and parallel-safe. Phase 2
(`03`-`07`) is the real lift: migrating Nanite's bespoke `internal/runtime/agent` code (boot-
dir planting, sandbox profile construction, session lifecycle) onto `go-agent-wrapper`'s
`Planter`/`Applier`/`Wrapper` seams, closing with a real dogfeed validation — this is the
**first real production exercise of `go-agent-wrapper` anywhere in the portfolio** (confirmed
zero adopters at planning time), so `07`'s validation checkpoint carries more weight than
usual. Phase 3 (`08`-`11`) builds the ACP client abstraction and the two low-risk native ACP
adapters (OpenCode, Copilot CLI), then wires per-agent protocol/transport selection into
Nanite's existing `runtime_kind`-adjacent routing. Phase 4 (`12`-`15`) is escalation-gated —
`12` must get explicit operator sign-off on which ACP bridge library to pin for Claude/Codex/
Pi before `13`-`15` can be dispatched (see `TASKS/ESCALATIONS.md`). Phase 5 (`16`-`17`) closes
the batch: an audit of whether any ACP agent actually needs the host to proxy filesystem/
terminal operations (a real, flagged-but-unresolved "hidden cost" risk in 17-acp.md), and a
side-by-side native-vs-ACP comparison to ground any future per-agent default decision in real
evidence. A post-handoff fix, `23`, closes a real gap the end-of-batch doc-writer pass
surfaced: `11` (Phase 3) wired OpenCode/Copilot CLI into Nanite's own ACP dispatch table
before Phase 4's bridge adapters existed, and no later task ever revisited it — so Claude/
Codex/Pi's bridge adapters were built and library-verified but never reachable from Nanite
itself. `23` extends the same dispatch table to all three and live-verifies Claude/Codex
end-to-end (Pi's dispatch is correct but blocked by an unrelated, pre-existing gap — no `"pi"`
entry in `cmd/nanite/main.go`'s `cliAdapters` list).

**Real, load-bearing corrections to both docs, found during this planning session's own
research against the live code** (not just doc-vs-doc inconsistencies — each is cited with
file:line in its owning task's Context): bumping go-agent-wrapper's `agentkit` pin from
`v0.1.0` to `v0.3.0` requires **zero code changes**, not "a compat pass" — `agentsessions`
(the only agentkit subpackage go-agent-wrapper imports) is byte-identical across those tags,
and the renamed symbol 16-agent-host.md cites lives in `agentlaunch`, which go-agent-wrapper
never imports (`01`). **Neither Claude's nor Codex's `Stop()` today calls any native
wire-level interrupt** — contrary to what 17-acp.md's first-pass framing implied, both
go-agent-wrapper (via `agentkit/agentsessions`) and Nanite's own current bespoke code do
stdin-close + SIGTERM/SIGKILL only; only OpenCode calls a real native abort endpoint. This is
a carried-forward limitation, not a regression — Nanite's own `internal/service/
agent_deps.go:772-776` already has a TODO acknowledging the identical gap (`02`, `06`).
`internal/recovery/broker`'s coupling to `agentkit` is narrow, not deep — only
`*agentsessions.ExitError`'s shape and five `Cause*` constants, entirely mediated through
Nanite's own `agent.Options`/`agent.Session`/`agent.HasBootdirLayout` types — so the migration
blast radius on broker (16-agent-host.md's own named top risk) is small, not structural (`06`).

**Deliberately out of this batch's scope** (see `README.md`'s full list): sequencing Tether's
or Torque's own adoption of `go-agent-wrapper` (this is Nanite's own tracker; that's a
portfolio-level call made in those apps' own planning); migrating `Mode`/lifecycle-decision
logic out of Nanite (stays product-owned per both docs' explicit boundary); attach/detach to
externally-launched CLI processes; building an in-process fs/terminal server ahead of `16`'s
audit actually finding one is needed; locking a final ACP bridge library ahead of `12`'s
operator sign-off; a final locked default for which protocol/transport any agent runs on
(migration is additive, per-agent, opportunistic — not a flag-day).

**Planned 2026-08-21, dispatched and in progress** (this note is stale as of Phase 2 —
Phase 1 is reviewed, Phase 2 is in flight; kept here for historical record of the original
planning checkpoint). `12`'s bridge-library decision still needs its own explicit operator
sign-off before Phase 4 can proceed, independent of the batch-level go-ahead already given.

## Filesystem Snapshots (`TASKS/filesystem-snapshots/`, outside the Phase 0-9 sequence)

Implements `docs/engineering/architecture/18-filesystem-snapshots.md` — an undo/audit
primitive for the filesystem state an agent's granted paths hold (shadow-git capture/diff/
preview/selective-restore, per-model-step cadence, never conflated with session/conversation
state). Planned by a separate planning session on 2026-08-21 while `agent-host-acp` was
already executing; task files were handed to the Orchestrator pre-written
(`TASKS/filesystem-snapshots/01-03`), with sequencing left to the Orchestrator's judgment.
Nanite-first only (matches `agent-host-acp`'s own "not portfolio-wide" scoping) — whether
Tether/Torque adopt this is a separate, later portfolio-level call.

| Task | Phase | Status | Depends on |
|---|---|---|---|
| `01-filesystem-snapshot-host-mechanism` | 1 — host mechanism (sibling repo `libs/go-agent-wrapper`) | implemented | none — independent of `agent-host-acp`'s `wrapper.Wrapper` work |
| `02-nanite-capture-policy-and-wiring` | 2 — product policy & wiring (Nanite) | not-started | `01`; held pending `agent-host-acp/06` landing (same files: `internal/runtime/agent/agent.go`) |
| `03-snapshot-diff-preview-restore-api` | 3 — consumer surface (Nanite, backend-only) | not-started | `01`, `02` |

**Sequencing decision (Orchestrator, 2026-08-21):** Task `01` lands entirely in the sibling
`libs/go-agent-wrapper` repo and has zero file overlap with anything `agent-host-acp` has
in flight (confirmed: that repo's working tree is clean at commit `7c65601`/`v0.3.0`, nothing
else running there) — dispatching now, in parallel with `agent-host-acp`'s still-running task
`06`. Tasks `02`/`03` land in Nanite; `02` specifically touches "wherever Nanite's per-turn/
per-step execution loop lives (likely `chat_generate.go` or `agent.go`)" — `agent.go` is
exactly the file `agent-host-acp/06` is actively rewriting right now. Holding `02`/`03` until
`06` (and its section review) lands, to avoid a real merge collision on the same functions
mid-rewrite. Will re-evaluate exact dispatch timing for `02` once `06` merges — may not need
to wait for `07`'s dogfeed too, since `02` only needs `agent.go`'s *shape* to be stable, not
a fully validated migration; will decide based on `06`'s actual landed diff.

**Update (2026-08-21):** `01` landed (`libs/go-agent-wrapper` commit `5c1a343`), `06` landed
(`1f947c55`, under fresh review). `06`'s diff to `agent.go` is substantial (real restructuring
around `wrapper.Wrapper.Run`, not a no-op) — holding `02`'s dispatch until `06`'s fresh review
resolves, to dispatch `02` against a reviewed, not just implemented, `agent.go` shape. Given
`agent-host-acp` still has `07` (dogfeed) ahead of it and Phase 3-5, `02`/`03` will likely run
interleaved with that batch's later phases rather than immediately back-to-back — sequencing
each dispatch against whatever's actually in flight at the time.

**Scope fences carried forward from the architecture doc, not silently expanded**: no
universal filesystem rollback (only sandbox-`FS.Write`-derived targets are recoverable); no
real conflict/merge resolution beyond task `03`'s lightweight hash-check (deferred until
Teams sees real multi-agent concurrent usage); no GUI/frontend surface (backend-only, matches
standing no-frontend-in-any-phase discipline); Nanite-first, not portfolio-shared by default.

## Plugin System (`TASKS/plugin-system/`, outside the Phase 0-9 sequence)

Implements `docs/engineering/architecture/09-plugin-system.md`'s "Target design" sections —
the design produced by a dedicated planning session (2026-08-21) that reconciled the
2026-04-11 internal audit (`docs/audits/2026-04-11-plugin-capability-model/`) against the
plugin system's real, current state. A sibling to `TASKS/teams/`, `TASKS/reflex-taxonomy/`,
`TASKS/harness-reactive-self-tools/`, `TASKS/scheduling/`, and `TASKS/agent-host-acp/`. See
`TASKS/plugin-system/README.md` for the full read-first list and scope boundaries.

| Task | Phase | Status | Depends on |
|---|---|---|---|
| `01-registration-preflight-validation-and-nanitecompat` | 1 | not-started | none |
| `02-build-pluginstore-scoped-sql-proxy` | 2 | not-started | none |
| `03-build-pluginmcpclient-scoped-proxy` | 2 | not-started | `02` |
| `04-plugin-capabilities-manifest-schema-and-storage` | 3 | not-started | none |
| `05-capability-install-time-approval-gate` | 3 | not-started | `04` |
| `06-capability-enforcement-at-rpc-proxy-layer` | 3 | not-started | `04`; parallel-safe with `05` |
| `07-make-http-middleware-plugin-extensible` | 4 | not-started | none directly; coordinate with `04` on `internal/plugin/config.go` — supersedes `TASKS/phase-5/06-make-http-middleware-plugin-extensible.md` (now marked superseded, see that phase's row above) |

**Two audit findings this planning pass found already closed, not tasks in this batch**:
finding 04 (no panic recovery in event-hook dispatch) landed 2026-04-12 in commit `ce40fbcf7`
(the repo-wide `safego` adoption sweep), the day after the audit that flagged it; the
CLI-install/hot-reload asymmetry `09-plugin-system.md` used to describe as an open decision
was fully closed by `TASKS/phase-5/04`, `11`, and `12` (all `reviewed`). Both were independently
re-verified against current code (not assumed from either the audit's or the doc's age) before
this batch was scoped — `docs/engineering/architecture/09-plugin-system.md` was corrected in
place rather than carrying either forward as a phantom task. See `TASKS/ESCALATIONS.md`'s
2026-08-21 entry for the full record.

**Sequencing.** Phase 1 (`01`) is small and independent — registration pre-flight validation
plus `NaniteCompat` enforcement, no schema. Phase 2 (`02`-`03`) is Tier 1 hygiene: scoped
`PluginStore`/`PluginMCPClient` proxies replacing raw `GetService("store")`/`GetService("mcp")`
for builtins — hygiene, not enforcement, since builtins stay fully trusted; `03` depends on
`02`'s per-plugin-identity-at-`GetService`-time pattern and slug validator. Phase 3
(`04`-`06`) is the real Tier 2 enforcement lift for subprocess plugins — capability schema and
storage first (`04`), then the install-time approval gate (`05`) and live RPC-proxy
enforcement (`06`) in parallel, both depending only on `04`. Phase 4 (`07`) is independent of
Phases 2-3 and resolves the previously-open Phase 5 HTTP-middleware-extensibility escalation
now that the operator has settled its shape (builtins only, priority-ordered).

**Real, load-bearing corrections to the architecture doc and the 2026-04-11 audit, found
during this planning session's own research against the live code** (not just doc-vs-doc
inconsistencies — each is cited with file:line in its owning task's Context): the audit's
named unmanaged-schema offender (`internal/plugin/builtin/sessionstats/`) and both external
plugins it cited (`plugins/support-ticket/`, `plugins/fragments-engine/`) are all already gone
from this repo — `02`/`03` are genuinely new infrastructure, not a fix to a live offender.
`applyManifestRegistrations`'s conflict checking is real for envelopes/UI components/
keybindings but **does not exist at all** for commands/CRUD resources/HTTP routes (they
silently overwrite on conflict today) — `01`'s pre-flight pass needs new detection logic for
those three categories, not just relocation of an existing check. All four subprocess
RPC-proxy call sites the target design names (`NewCRUDHandler`, `CallTool`, `NewEventHook`,
`newSubprocessHTTPHandler`) are exact matches to real code and are confirmed fully
unconditional today — zero authorization checks anywhere, grounding `06`'s scope precisely.

**Planned 2026-08-21, not yet dispatched.** Per `EXECUTION-PROCESS.md`'s Phase A discipline,
this is the planning checkpoint — present to the operator for review before any worker is
dispatched.

---

## Skills (`TASKS/skills/`, outside the Phase 0-9 sequence)

Implements `docs/engineering/architecture/20-skills.md` in full — the output of a dedicated
2026-08-21 architecture-alignment session that found skills were never fully implemented in
Nanite: no `scripts:`/`references:`/`assets:` support ever existed, no composition, no real
parameterization, and no path for a skill's actual body content to ever reach a model through
any live mechanism, in any runtime. A sibling to `TASKS/teams/`, `TASKS/reflex-taxonomy/`,
`TASKS/harness-reactive-self-tools/`, `TASKS/scheduling/`, `TASKS/agent-host-acp/`, and
`TASKS/plugin-system/`. See `TASKS/skills/README.md` for the full read-first list, scope
boundaries, and reused-primitives inventory.

**This batch deliberately goes against the project's recently-established "DB over files"
default** — a real, load-bearing exception for `scripts:`/`references:`/`assets:` content that
has to be real files on disk at materialization time, not a silent regression to the pattern
`TASKS/phase-1/08` killed (silent, unscoped, every-boot re-ingest stays dead; this is an
explicit, single-target install/sync instead). See the README's opening section for the full
reasoning.

| Task | Phase | Status | Depends on |
|---|---|---|---|
| `01-cut-legacy-skill-discovery-autodiscover-and-adhoc-authoring` | 1 | reviewed | none |
| `02-redesign-skills-index-schema-and-extend-agent-known-skills` | 2 | reviewed | `01` |
| `03-build-content-addressed-vendored-skill-store` | 2 | reviewed | none |
| `04-build-skill-package-parser-and-install-sync-pipeline` | 3 | reviewed | `02`, `03` |
| `05-install-sync-rest-api-and-cli-command` | 3 | reviewed | `04` |
| `06-build-skill-resolver-and-parameter-binding` | 4 | reviewed | `02`, `03`, `04` |
| `07-implement-inline-fork-composition-semantics` | 4 | reviewed | `04`, `06` |
| `08-rebuild-inline-marker-and-scripts-execution` | 4 | reviewed | `06` |
| `09-sandbox-and-capability-policy-gate-for-skill-execution` | 5 | reviewed | `02`, `08` |
| `10-cli-hosted-native-skill-delivery-boot-dir-planting` | 6 | reviewed | `02`, `03` |
| `11-api-direct-skill-get-self-tool` | 6 | reviewed | `06`, `07`, `08`, `09` |
| `12-remaining-skills-rest-api-list-grants-preview-uninstall` | 7 | reviewed | `02`, `05`, `09` |

**Three real, load-bearing corrections/decisions this planning session's own research made,
each logged in full in `TASKS/ESCALATIONS.md`'s 2026-08-21 entry, not silently baked into a
task's Context alone**: (1) `docs/engineering/architecture/16-agent-host.md`'s description of
`go-agent-wrapper`'s `policy.Engine`/`policy.Store` is accurate about the library but the
mechanism is confirmed **dormant in Nanite today** — `wrapper.Config.Policy` is never set
anywhere — so `20-skills.md`'s plan to "plug into the same shape the host already runs" has
nothing live to plug into; task `09` builds narrow, direct capability enforcement instead,
matching what `TASKS/plugin-system/06` independently decided for the same reason. (2)
`agent_known_skills` is confirmed **not dead overall** (only dead for prompt assembly, per
`TASKS/phase-0/17`) — it has a live REST API and two live frontend surfaces (Agent Builder
Wizard, Agent Capabilities Panel) still writing real rows, and
`docs/engineering/architecture/13-memory-and-knowledge-tools.md`'s §4a cites it as the reference
catalog+attachment pattern; task `02` extends it in place (additive columns only) rather than
building a third assignment table, resolving one of `20-skills.md`'s own "genuinely still open"
questions. (3) The existing `skill_create`/`skill_update` self-tools are structurally
incompatible with "skills are authored packages only" (no way to represent a real
`scripts:`/`references:`/`assets:` package via flat CRUD fields) — task `01` cuts both; the new
content-retrieval self-tool is named `skill_get` (task `11`), not `skill_invoke` as the
architecture doc's placeholder phrasing suggested, matching `docs/tool-naming-convention.md`'s
own `get`-verb precedent and the already-renamed bare `skill_*` self-tool family.

**Migration numbering.** Highest existing goose migration on disk at this planning session's
authoring time (2026-08-21) is `134_agent_profiles_protocol_transport.sql`.
`TASKS/plugin-system/04` provisionally claims `135`. This batch provisionally claims `136`
(task `02`'s skills-index redesign) and `137` (task `02`'s `agent_known_skills` extension +
`agent_skills` drop) — both provisional, re-check the migrations directory immediately before
either lands; `TASKS/agent-host-acp`, `TASKS/plugin-system`, and `TASKS/filesystem-snapshots`
are all concurrently in flight and any may have claimed `135`-`137` first by dispatch time.

**Sequencing.** Phase 1 (`01`) is a clean-slate cut, landing first so later phases build on a
decluttered base — matches `20-skills.md`'s own explicit "no carried-forward content" operator
call (none of the 8 embedded builtin skills or DB-originated auto-discovered rows has ever been
observed in use). Phase 2 (`02`-`03`) is the DB-index + vendored-content-store foundation;
`03` is a brand-new package with zero file overlap and can run in Wave 1 alongside `01`, but `02`
needs `01` landed first (both redefine `internal/store/skills.go`'s `Skill` struct). Phase 3
(`04`-`05`) is the explicit, single-target install/sync mechanism — the only way an index row is
ever created or updated, never a directory sweep. Phase 4 (`06`-`08`) is the materialization
pipeline (Resolver, composition, rebuilt inline-marker/scripts execution — the old marker's real
code-fence-unaware accidental-execution flaw is fixed here, not carried forward). Phase 5 (`09`)
is the one sandbox/policy gate every script/materializer execution routes through, regardless of
caller. Phase 6 (`10`-`11`) is the two boundary-specific delivery adapters (CLI native boot-dir
planting; the API-direct `skill_get` self-tool) sharing one canonical vendored source. Phase 7
(`12`) rounds out the REST surface (list/get, assign/revoke, grants/policy view, invoke/preview,
uninstall) the architecture doc's "API surface" section names.

**What this batch does NOT do** (explicit scope fences, see the README for full reasoning):
frontend/admin-UI work (a separate stream per the architecture doc itself); ecosystem-format
package adaptation (installing a foreign `.claude/skills/`-authored directory that doesn't
already match the real Agent-Skills-spec shape); explicit skill *triggering* (predicate-based
automatic activation, a separate follow-up filed in `13-memory-and-knowledge-tools.md`'s §4a,
not designed by `20-skills.md`); wiring `wrapper.Config.Policy`/`policy.Engine` live in Nanite
for the first time; reviving `internal/skillbroker`'s rule-matching layer (cut in full,
`TASKS/phase-0/22`, not reproposed here).

**Executing.** Operator sign-off recorded in `docs/engineering/architecture/20-skills.md`'s
`## Status` section, 2026-08-21. **All 8 waves (tasks `01`-`12`) are implemented, validated,
and reviewed. The batch is fully complete.** Fresh independent review found and fixed seven real
bugs across `02`/`04`/`05`/`06`/`07`/`08`/`09`, one high-severity path-traversal bug in `10`
(fixed in one round, fuzz-verified against ~25 adversarial slugs), and two minor accuracy
findings in `12` (fixed in one round, mutation-tested) — every fix independently re-reviewed
PASS. Full detail in `TASKS/ESCALATIONS.md`'s 2026-08-21/22 entries. Two genuine follow-up
candidates remain filed, not fixed, as explicitly out of scope: a production-inert typed-nil
hazard in `11`'s fork-composition wiring, and `12`'s preview endpoint's inability to
materialize `fork`-composed skills (a structural "no live session" limitation, not a bug).

## Loops (`TASKS/loops/`, outside the Phase 0-9 sequence)

Implements `docs/engineering/architecture/21-loops.md` in full — the output of a dedicated
2026-08-21 architecture-alignment session that found Nanite has no control layer above
execution: agent turns, `agentworkflow`, reflexes, scheduling, and Teams all exist, but
nothing answers "keep working toward this target state, across multiple bounded executions,
until it's actually true, on a budget, with an escalation path." A sibling to
`TASKS/teams/`, `TASKS/reflex-taxonomy/`, `TASKS/harness-reactive-self-tools/`,
`TASKS/scheduling/`, `TASKS/agent-host-acp/`, `TASKS/plugin-system/`, and `TASKS/skills/`.
See `TASKS/loops/README.md` for the full read-first list, load-bearing corrections, and the
resolution of every item on the design doc's own "What this session did not decide" list.

| Task | Phase | Status | Depends on |
|---|---|---|---|
| `01-goals-schema` | 1 | reviewed (migration 138) | none |
| `02-goal-evidence-schema` | 1 | reviewed (migration 140) | `01` |
| `03-loop-runs-schema` | 1 | reviewed (migration 141) | `01` |
| `04-loop-run-iterations-schema` | 1 | reviewed (migration 142) | `03` |
| `05-workflow-runs-loop-scoping-columns` | 1 | reviewed (migration 143) | `03` |
| `06-stepkindloop-schema` | 1 | reviewed (migration 139) | none |
| `07-loop-continuation-policy` | 2 | reviewed | `01`, `02`, `03`, `04` |
| `08-loop-engine-core` | 2 | reviewed | `03`, `04`, `07` |
| `09-stepkindloop-executor-and-waiting-status` | 2 | reviewed (migration 144) | `06`, `08` |
| `10-loop-launcher-and-api` | 3 | reviewed | `08` |
| `11-loop-event-predicate-trigger` | 3 | reviewed (migration 145) | `08` |
| `12-loop-run-tick-scheduled-trigger` | 3 | reviewed (migration 146) | `08` |
| `13-loop-presets` | 4 | reviewed | `07`, `08`, `10` |

**Two real, load-bearing corrections this planning session's own research found against the
actual code, neither anticipated by the design doc, both logged in full in
`TASKS/ESCALATIONS.md`'s 2026-08-21 "Loops planning" entry**: (1) legacy
`internal/workflow.LoopStep` (an unrelated, older pipeline primitive the design doc
explicitly declines to retire) is **dynamically reachable today** via
`POST /api/workflows/runs` accepting arbitrary YAML — not just theoretically present, as the
design doc's own "the actual usage question wasn't checked" framing left open; no task in
this batch touches it, flagged for the record. (2) The design doc's claim that Loop's
event/predicate trigger can fully reuse the reflex trigger-spec AST "the same way" Team's
flex-step exit trigger already does is **incomplete** — flex's own reuse is a lazy re-check
piggybacked on an unrelated caller's `.Resume()`, not a real push, and a `WAIT`-status
`LoopRun` has nothing to piggyback on; task `11` builds a real, new, legible reflex action
kind (`resume_loop_run`) instead of assuming reuse alone solves it.

**Every item on `21-loops.md`'s own "What this session did not decide" list is resolved by
this planning session** — see `TASKS/loops/README.md`'s own section for the full list with
reasoning: `goal_evidence` stays always-structured (no free-text rows); REARCHITECT reuses
the same reasoning-fallback `ExecuteLLMStep` call as the no-progress case rather than a
separate architect-agent dispatch; concurrent `LoopRun`s against one `goal_id` are
disallowed for v1; `on_exhausted ∈ {"escalate","fail"}`; `loop_run_tick`'s payload is
`{loop_run_id}`, matching the other four job types' exact convention now that Scheduling has
landed; auth is standard operator auth, no new provenance tier; presets are Go-coded
constants, not a DB table, with `ralph` built fully and the rest named-but-stubbed.

**Migration numbering.** Highest existing goose migration on disk at this planning session's
authoring time (2026-08-21) is `134_agent_profiles_protocol_transport.sql`.
`TASKS/plugin-system/04` provisionally claims `135`; `TASKS/skills/02` claims `136`-`137`.
This batch provisionally claims `138`-`144` (one per Phase 1 schema task, `01` through `06`,
in order, plus `09`'s separate `RunStatusWaitingOnLoop` status-CHECK migration at `144`) —
all seven provisional, re-check the migrations directory immediately before any lands;
`TASKS/agent-host-acp` and `TASKS/filesystem-snapshots` may also be concurrently in flight.

**Sequencing.** Phase 1 (`01`-`06`) is schema/storage only, no runtime behavior — three
parallel waves (`01`+`06`, then `02`+`03`, then `04`+`05`), per the README's own
parallelization note. Phase 2 (`07`-`09`) is the runtime engine — continuation policy first
(pure logic, DB-only dependency), then the engine that drives `WorkflowLauncher` iterations
using it, then the contained-loop step executor that needs the engine to exist. Phase 3
(`10`-`12`) is three mutually parallel-safe trigger surfaces (manual/API + escalation
resolution; event/predicate via a new reflex action kind; scheduled tick via Scheduling's
`JobType` taxonomy) once `08` lands — same shape Scheduling's own Phase 2 producers took.
Phase 4 (`13`) is the preset registry, needing the launcher, engine, and continuation policy
all real.

**Planned 2026-08-21, not yet dispatched.** Per `EXECUTION-PROCESS.md`'s Phase A discipline,
this is the planning checkpoint — present to the operator for review before any worker is
dispatched.

## Turn vs. Run (`TASKS/turn-vs-run/`, outside the Phase 0-9 sequence)

Implements `docs/engineering/architecture/22-turn-vs-run.md`'s "Target design," approved for
implementation by the operator 2026-08-21 (following a dedicated planning pass) — resolves
`19-api-cli-runtime-parity.md`'s flagged conflation of one model-invocation-plus-tool-call
cycle (**Turn**) with the full tool-settling loop (**Run**) under one name, and closes the
Glossary's "recommended but not yet executed" `CancelActiveGeneration` → `Run.Cancel` rename.
A sibling to `TASKS/teams/`, `TASKS/reflex-taxonomy/`, `TASKS/harness-reactive-self-tools/`,
`TASKS/scheduling/`, `TASKS/agent-host-acp/`, `TASKS/filesystem-snapshots/`,
`TASKS/plugin-system/`, `TASKS/skills/`, and `TASKS/loops/`. See
`TASKS/turn-vs-run/README.md` for the full read-first list and scope boundaries.

| Task | Phase | Status | Depends on |
|---|---|---|---|
| `01-define-turn-primitive` | 1 | not-started | none |
| `02-rename-cancelactivegeneration-to-run-cancel` | 1 | not-started | none |
| `03-wire-generateresponse-onto-turn-primitive` | 2 | not-started | `01` |
| `04-consolidate-workflow-step-executor-onto-turn-primitive` | 2 | not-started | `01` |

**Scoping deviation from the original 3-task sketch, found by this planning pass's own
research.** `generateResponse`'s tool-settling loop (`chat_generate.go:757-1726`) is ~970
lines with five distinct mid-loop retry shapes and live mid-stream side effects (SSE deltas,
PTY presence, auto-artifact creation) — materially bigger and more state-entangled than doc
22's own description suggests. Split into 4 tasks instead of 3: `01` defines the `Turn`
primitive in isolation (unit-tested, not yet wired anywhere) so a reviewer can sign off on its
contract before `03`'s actual rewire of `generateResponse` is attempted; `04` consolidates
`workflow_step_executor.go`'s materially-simpler capability-restricted loop onto the same
primitive without collapsing its deliberately-different tool-settlement path into
`generateResponse`'s. `02` also found and scoped in three real stale-doc corrections beyond
`CancelActiveGeneration` itself (a substantively wrong `Turn.Cancel` claim in
`acp_session.go` and `17-acp.md`, plus two stale bullets in `00-overview.md`).

**No migration needed** — pure in-process Go refactor plus doc/comment corrections.

**What this batch explicitly does not do** (see README for full reasoning): rename
`MaxTurns`/`TerminationMaxTurns`/the `/turns` HTTP route (a separate, larger, breaking-change
question doc 22's approval doesn't cover); durable-agent Turn-level wakes; finer-grained
`Turn.Cancel` itself; anything in `internal/loopdetect` (a different, unrelated mechanism);
any change to tool execution/settlement/permission checking in either call site.

**Planned 2026-08-21, not yet dispatched.** Per `EXECUTION-PROCESS.md`'s Phase A discipline,
this is the planning checkpoint — present to the operator for review before any worker is
dispatched.

## Feedback-Carrying Denial (`TASKS/feedback-carrying-denial/`, outside the Phase 0-9 sequence)

Implements `docs/engineering/architecture/23-feedback-carrying-denial.md`, approved for
implementation by the operator 2026-08-21 (following a dedicated planning pass) — a denial
should carry a decision, a reason, and — where the denying subsystem can produce one — a
context-specific suggestion, with provenance, while enforcement itself stays hard. Extends
`internal/recover`'s existing `RecoverableError{Kind, ToolName, SentArgs, SchemaURI,
ErrorPath, ErrorReason, Suggestion}` taxonomy with new policy-class `Kind` values (each
explicitly ineligible for C2's automatic LLM-repair loop) across four surfaces that today
produce flat, hardcoded, or generic-template prose: the permission engine, human-reject
feedback, the plugin pre-hook contract, and MCP trust-tier rejection. A sibling to
`TASKS/teams/`, `TASKS/reflex-taxonomy/`, `TASKS/harness-reactive-self-tools/`,
`TASKS/scheduling/`, `TASKS/agent-host-acp/`, `TASKS/filesystem-snapshots/`,
`TASKS/plugin-system/`, `TASKS/skills/`, and `TASKS/loops/`. See
`TASKS/feedback-carrying-denial/README.md` for the full read-first list and scope boundaries.

| Task | Phase | Status | Depends on |
|---|---|---|---|
| `01-shared-kind-taxonomy-and-auto-repair-gate` | 1 | not-started | none |
| `02-permission-engine-structured-denial` | 2 | not-started | `01` |
| `03-human-reject-feedback` | 2 | not-started | `02` (same file, sequence not concurrent) |
| `04-plugin-prehook-structured-contract` | 2 | not-started | `01`; sequence after `03` (shared file, disjoint blocks) |
| `05-mcp-trust-tier-suggestion` | 2 | not-started | `01` |
| `06-halt-session-recovery-pack-replay` | 3 | not-started | none |

**Doc 23's three open questions, resolved by this planning pass with code evidence:**
extend `internal/recover` in place, not a sibling type (`buildAgentErrorEnvelope`,
`tool.go:573`, already takes `*RecoverableError` concretely); the four illustrative `Kind`
names map 1:1 onto the four real surfaces, confirmed by trace; the plugin-SDK backward-compat
question resolves asymmetrically — `EventHandleResult.Reason` already exists on the wire at
the pinned `plugin-sdk@v0.3.0` and Nanite's host code simply discards it today (zero SDK bump
needed), while `Suggestion` has no wire field yet and is a named follow-up for subprocess
parity, not this batch. Also found: only the MCP surface (`05`) flows through
`recover.Classify`'s repair-gating logic today — the other three decide and render inside
`chat_tool_executor.go` before ever reaching it, which narrows `02`/`03`/`04`'s real risk to
constructing the envelope correctly, not touching repair-eligibility logic.

**No migration needed** — verified against real schema: approval requests are purely
in-memory, and every other surface touched is an in-process Go type with no DB-backed state.

**Planned 2026-08-21, not yet dispatched.** Per `EXECUTION-PROCESS.md`'s Phase A discipline,
this is the planning checkpoint — present to the operator for review before any worker is
dispatched.

## Code Mode (`TASKS/code-mode/`, outside the Phase 0-9 sequence)

Implements `docs/engineering/architecture/27-code-mode.md`, approved for implementation by
the operator 2026-08-21 after two rounds of follow-up research (dispatched when the doc's
original "no current pressure" framing was questioned) found the real blocker isn't whether
to make Code Mode non-agentically reachable — it's that `PythonPermChecker`/
`PythonDispatcher`, the fields bridging `python_run`'s sandboxed `tool_call()` helper to the
real permission engine and tool dispatcher, are never assigned outside test code, so every
`tool_call()` today either silently bypasses permission enforcement or fails outright,
regardless of caller. A sibling to `TASKS/teams/`, `TASKS/reflex-taxonomy/`,
`TASKS/harness-reactive-self-tools/`, `TASKS/scheduling/`, `TASKS/agent-host-acp/`,
`TASKS/filesystem-snapshots/`, `TASKS/plugin-system/`, `TASKS/skills/`, and `TASKS/loops/`.
See `TASKS/code-mode/README.md` for the full read-first list and scope boundaries.

| Task | Phase | Status | Depends on |
|---|---|---|---|
| `01-fix-python-sandbox-permission-and-dispatcher-wiring` | 1 | not-started | none |
| `02-workflow-tool-step-session-stamping` | 2 | not-started | `01` (sequencing only — no file overlap) |
| `03-run-python-sandbox-reflex-action-kind` | 2 | not-started | `01` (real — calls `01`'s new dispatcher adapter) |

**Correction to the original scoping brief, found by this planning pass:** the brief assumed
no migration would be needed; `TASKS/reflex-taxonomy/` has since landed (`reviewed`) and
`agent_reflexes.action_kind` now carries both a widened CHECK and a real FK to
`reflex_action_kinds`, plus a `reflex_action_kind_provenance_allow` gate table — so task `03`'s
new `run_python_sandbox` action kind is a real, small migration (one `reflex_action_kinds`
row, three provenance-allow rows, a CHECK-widen rebuild), not pure Go wiring. Also found:
`01`'s permission-check half needs zero adapter code (`permission.Engine.Check` already
matches the interface structurally); its dispatch half needs a real small adapter
(`service.NewPythonToolDispatcher`) reconciling a different parameter/return shape; task `02`'s
session-id question resolves to reusing the `WorkflowRun`'s own ID rather than minting a
synthetic one (verified: `event_log.session_id` has no FK, and the codebase already tolerates
a non-real sentinel session in this exact code path); and `internal/api/reflexes.go`'s own
hardcoded action-kind switch needs the new constant too, or the migration alone doesn't make
the kind writable via the CRUD API.

**Migration numbering.** This batch's one migration (task `03`) provisionally claims `144` —
the next free slot after `TASKS/plugin-system`'s `135`, `TASKS/skills`'s `136`-`137`, and
`TASKS/loops`'s `138`-`143` claims. Re-list `internal/store/migrations/` immediately before
landing it and renumber if any sibling batch lands first.

**What this batch explicitly does not do** (see README for full reasoning): wire Code Mode
into `internal/scheduler`'s periodic-job surface directly (composable from what this batch
ships, via `add_schedule` or a `loop_run_tick` producer, not a fourth task here); feed a
`run_python_sandbox` reflex's result back into LLM context; the same session-stamping fix for
`ExecuteLLMStep`'s own internal tool loop (a related, real, deliberately out-of-scope gap,
flagged for a future task); any change to the sandbox's own resource/network limits; a
CRUD/admin UI for authoring these reflexes.

**Planned 2026-08-21, not yet dispatched.** Per `EXECUTION-PROCESS.md`'s Phase A discipline,
this is the planning checkpoint — present to the operator for review before any worker is
dispatched.

## Audit Remediation (`TASKS/audit-remediation/`, outside the Phase 0-9 sequence)

Implements the remediation program derived from `docs/audits/2026-08-21-go-quality/REPORT.md` — a
13-package-cluster Go quality/architecture audit run against commit `8feeee5c` (2026-08-21) — as
sequenced by `docs/audits/2026-08-21-go-quality/REMEDIATION-GUIDE.md` (the advisor's planning guide,
vendored into the repo by this batch's planning pass; it previously lived only at
`~/dev/chrispian/inbox/`, outside the repository). A sibling to `TASKS/reflex-taxonomy/`,
`TASKS/harness-reactive-self-tools/`, `TASKS/scheduling/`, `TASKS/teams/`, `TASKS/agent-host-acp/`,
`TASKS/filesystem-snapshots/`, `TASKS/plugin-system/`, `TASKS/skills/`, `TASKS/loops/`,
`TASKS/turn-vs-run/`, `TASKS/feedback-carrying-denial/`, and `TASKS/code-mode/`.

**63 task files, 113 findings, 9 waves.** Task inventory (61 files) created by a dedicated
task-creation pass, merged in `8258176e`; sequencing, dependency ordering, parallelization, the
Wave 0 gate (2 new task files), the architect-decision queue, and the prevention table added by a
planning pass on 2026-08-21.

**This batch is dispatched as eleven units, not one.** At 63 tasks it exceeds anything this process
has run (previous maximum: `TASKS/skills/` at 12). One batch, one INDEX section, one `findings.json`
tracker — but eleven kickoff prompts, each covering 2-10 tasks, written one at a time as each unit
becomes dispatchable. Task IDs are `<folder>/<file>`; folder-scoped numbering was retained rather
than flattened to `01`-`63` because `findings.json`'s `task_file` field and every row of
`FINDING-INDEX.md` already address tasks by folder path.

**Severity distribution:** 3 critical, 8 high, 33 medium, 45 low, 24 informational. **44 of 113
findings carry `requires_architect_decision: true`** — collected into 24 decisions in
`TASKS/audit-remediation/ARCHITECT-DECISIONS.md`. A task whose `Gated on` decision is still `open`
must not be dispatched.

### Wave 0 — Revalidate the baseline (gates the entire batch)

| Task | File | Status | Depends on | Gated on |
|---|---|---|---|---|
| `00/01` | `00-revalidate-baseline/01-revalidate-findings-against-head.md` | reviewed | dev freeze | — |
| `00/02` | `00-revalidate-baseline/02-refresh-tool-baseline-at-frozen-head.md` | reviewed | dev freeze (step 1: none) | AD-23 |

### Wave 1 — Release-blocking trust boundaries

| Task | File | Status | Depends on | Gated on |
|---|---|---|---|---|
| `01/01` | `01-plugin-install-convergence/01-unify-plugin-catalog-install-pipeline.md` | reviewed | `00/01`, `00/02` | AD-04 |
| `01/02` | `01-plugin-install-convergence/02-wire-allow-unsigned-plugins-setting.md` | reviewed | `01/01` | AD-25 |
| `02/01` | `02-linux-sandbox-fail-open/01-sandbox-fail-closed-without-bwrap.md` | reviewed | `00/01`, `00/02` | AD-01, AD-02 |
| `02/02` | `02-linux-sandbox-fail-open/02-macos-seatbelt-read-boundary-disclosure.md` | reviewed | `00/01` | AD-03 |
| `03/01` | `03-agent-slug-traversal/01-canonical-slug-path-validation.md` | reviewed | `00/01`, `00/02` | — |
| `12/02` | `12-quality-ratchet-and-standards/02-add-engineering-standards-docs.md` | reviewed | `00/01` | — |

### Wave 2a — Container/reaper lifecycle + subagent ordering

| Task | File | Status | Depends on | Gated on |
|---|---|---|---|---|
| `04/01` | `04-container-reaper-lifecycle/01-fix-container-constructor-partial-failure-cleanup.md` | reviewed | `00/01` | — |
| `04/02` | `04-container-reaper-lifecycle/02-fix-api-test-container-shutdown-leak.md` | reviewed | `00/01` | — |
| `04/03` | `04-container-reaper-lifecycle/03-investigate-internal-service-race-timeout.md` | reviewed | `04/02` | — |
| `04/04` | `04-container-reaper-lifecycle/04-track-untracked-goroutine-spawns.md` | reviewed | `00/01` | — |
| `04/05` | `04-container-reaper-lifecycle/05-close-untested-service-config-functions.md` | reviewed | `00/01` | — |
| `05/01` | `05-subagent-execution-ordering/01-fix-approve-concurrency-cap-and-queued-cancel.md` | reviewed | `00/01` | — |

### Wave 2b — Store correctness + runtime lifecycle

| Task | File | Status | Depends on | Gated on |
|---|---|---|---|---|
| `06/01` | `06-store-correctness/01-fix-deleteagentbyid-error-swallowing.md` | reviewed | `00/01` | — |
| `06/02` | `06-store-correctness/02-triage-store-context-and-transaction-gaps.md` | reviewed | **none** (was `06/01`; corrected 2026-08-22 — file-disjoint, parallel-safe) | — (re-scoped by AD-14, gate lifted) |
| `06/03` | `06-store-correctness/03-full-context-propagation-sweep.md` | validated | none technically — must not run concurrently with anything else in the batch | AD-14 (decided; sweep landed) |
| `06/04` | `06-store-correctness/04-cancellation-safety-for-terminal-writes.md` | validated | `06/03` | — (fix task for `06/03`) |
| `07/01` | `07-runtime-correctness-lifecycle/01-fix-worktree-orphan-branch-cleanup.md` | reviewed | `00/01` | — |
| `07/02` | `07-runtime-correctness-lifecycle/02-fix-cmdserve-fatal-cleanup-bypass.md` | reviewed | `00/01` | AD-17 |
| `07/03` | `07-runtime-correctness-lifecycle/03-bound-background-job-registry-growth.md` | reviewed | `00/01` | AD-18 |
| `07/04` | `07-runtime-correctness-lifecycle/04-container-shutdown-idempotency-guard.md` | reviewed | `04/01` | — |
| `07/05` | `07-runtime-correctness-lifecycle/05-fix-mcp-config-silent-decode-errors.md` | reviewed | `07/02` | — |

**`04/04` Part B is gated on AD-26**, added 2026-08-22 — `GO-SVCCORE-002`
carried `requires_architect_decision: true` with no queue entry, the same gap
AD-25 exposed in Wave 1. Part A (`GO-SVCCORE-001`) is unaffected.

**`06/03` has landed** (`fe16e138`, with companion fix `06/04` for a
behavioral regression it exposed — see `06-store-correctness/04-*.md`) —
`validated`, not yet deep-reviewed line-by-line (operator's call). Store SQL
oracles 0/0/0, 371/371 exported methods take `ctx`, `go test ./...` 0 FAIL,
`go vet` clean at the expected 4 pre-existing `container.go` findings. It was
out-of-wave and not part of the Wave 2b dispatch unit while in flight — that
constraint is now moot, `06/01`/`06/02`/`11/13`/`13/01`/`13/02` are unblocked
to proceed against the post-sweep signatures.

### Wave 3 — Remaining security hardening

| Task | File | Status | Depends on | Gated on |
|---|---|---|---|---|
| `08/01` | `08-remaining-security-hardening/01-a2a-webhook-url-validation.md` | reviewed | Wave 2 complete | operator-approved external-unauthenticated SSRF disposition |
| `08/02` | `08-remaining-security-hardening/02-mcp-dev-grep-symlink-toctou.md` | reviewed | Wave 2 complete | — |
| `08/03` | `08-remaining-security-hardening/03-triage-remaining-gosec-g304-sites.md` | reviewed | `00/02` | operator-approved four-site remediation |
| `08/04` | `08-remaining-security-hardening/04-permission-default-mode-write-gap.md` | reviewed | Wave 2 complete | AD-16 |
| `08/05` | `08-remaining-security-hardening/05-secret-key-heuristic-hardening.md` | reviewed | `02/01` | — |
| `08/06` | `08-remaining-security-hardening/06-sandbox-proxy-header-timeout.md` | reviewed | Wave 2 complete | — |
| `08/07` | `08-remaining-security-hardening/07-server-auth-bind-tls-posture.md` | reviewed | `07/02` | AD-15 |
| `08/08` | `08-remaining-security-hardening/08-dependency-toolchain-vuln-bumps.md` | implemented — dependency fix passes; full race verdict deferred after known migration-time suite timeouts | `00/02` | — |
| `08/09` | `08-remaining-security-hardening/09-autocomplete-and-artifact-path-hardening.md` | reviewed | `01/01` | AD-04, AD-27, AD-28 (all decided; also now carries `GO-SEC4-007`, reassigned from `11/07`) |
| `08/10` | `08-remaining-security-hardening/10-api-validation-duplication-and-pagination-bug.md` | reviewed | Wave 2 complete | settings scope corrected after producer trace |

### Wave 4 — Production islands

| Task | File | Status | Depends on | Gated on |
|---|---|---|---|---|
| `09/01` | `09-production-islands/01-grounding-memory-recall.md` | reviewed | `04/01`, `07/04`, and `00/01`'s reachability report | AD-06 |
| `09/02` | `09-production-islands/02-hadron-context-gate.md` | reviewed | `04/01`, `07/04`, and `00/01`'s reachability report | AD-07 |
| `09/03` | `09-production-islands/03-team-semantic-routing.md` | reviewed | `00/01`'s reachability report | AD-08 |
| `09/04` | `09-production-islands/04-tool-builder-yaml-architecture.md` | reviewed | `00/01`'s reachability report | AD-09 |
| `09/05` | `09-production-islands/05-reasoning-augmented-tool-selection.md` | reviewed | `00/01`'s reachability report | AD-10 |
| `09/06` | `09-production-islands/06-curated-tool-knowledge-matcher.md` | reviewed | `00/01`'s reachability report | AD-11 |

### Wave 5 — Architectural concentration

| Task | File | Status | Depends on | Gated on |
|---|---|---|---|---|
| `10/01` | `10-architectural-concentration/01-chatserviceimpl-generateresponse-decomposition.md` | not-started | Wave 4 complete | AD-12 |
| `10/02` | `10-architectural-concentration/02-selftoolstransport-decomposition.md` | not-started | `09/01` | AD-13 |
| `10/03` | `10-architectural-concentration/03-container-and-store-review-note.md` | not-started | none | AD-14 |

### Wave 6a — Semantic divergence and migration drift

| Task | File | Status | Depends on | Gated on |
|---|---|---|---|---|
| `11/01` | `11-semantic-duplication-migration-drift/01-subagent-completion-vs-message-wake-policy.md` | not-started | `10/01` | AD-19 |
| `11/02` | `11-semantic-duplication-migration-drift/02-harness-v1-vs-native-durable-agent-handlers.md` | not-started | `03/01` | AD-19 |
| `11/05` | `11-semantic-duplication-migration-drift/05-mcp-result-processing-tail-duplication.md` | not-started | `09/04` | AD-19 |
| `11/06` | `11-semantic-duplication-migration-drift/06-provider-streaming-error-handling-divergence.md` | not-started | Wave 5 complete | AD-19 |
| `11/07` | `11-semantic-duplication-migration-drift/07-ssrf-cidr-denylist-duplication.md` | not-started | `08/01` | AD-19 |
| `11/08` | `11-semantic-duplication-migration-drift/08-config-package-naming-collision.md` | not-started | Wave 5 complete | AD-20 |
| `11/09` | `11-semantic-duplication-migration-drift/09-elicitation-client-side-duplication-and-dead-doc.md` | not-started | Wave 5 complete | AD-19 |
| `11/10` | `11-semantic-duplication-migration-drift/10-envelope-registry-triplication.md` | not-started | `07/02`, `07/05`, `08/07` | AD-19 |
| `11/11` | `11-semantic-duplication-migration-drift/11-dispatch-reflex-double-evaluation.md` | not-started | `10/01`, `10/02`, `09/01` | AD-19 |

### Wave 6b — Mechanical and boilerplate duplication

| Task | File | Status | Depends on | Gated on |
|---|---|---|---|---|
| `11/03` | `11-semantic-duplication-migration-drift/03-structuredmessage-unwrap-duplication.md` | not-started | Wave 6a complete | — |
| `11/04` | `11-semantic-duplication-migration-drift/04-traffic-light-calculation-duplication.md` | not-started | `10/01` | — |
| `11/12` | `11-semantic-duplication-migration-drift/12-devservername-constant-duplication.md` | not-started | `09/05` | — |
| `11/13` | `11-semantic-duplication-migration-drift/13-store-scan-loop-duplication.md` | not-started | `06/01`, `06/02` | — |
| `11/14` | `11-semantic-duplication-migration-drift/14-adapter-plugin-boilerplate-duplication.md` | not-started | Wave 6a complete | — |
| `11/15` | `11-semantic-duplication-migration-drift/15-api-response-boilerplate-duplication.md` | not-started | `01/01`, `08/09`, `08/10` | — |
| `11/16` | `11-semantic-duplication-migration-drift/16-workflow-naming-and-dispatch-naming-collisions.md` | not-started | Wave 6a complete | — |

### Wave 7 — Quality ratchet

| Task | File | Status | Depends on | Gated on |
|---|---|---|---|---|
| `12/01` | `12-quality-ratchet-and-standards/01-full-repo-scheduled-lint-gate.md` | not-started | Wave 6 complete, `00/02` | AD-21 |
| `12/03` | `12-quality-ratchet-and-standards/03-goroutine-lint-coverage-gap.md` | not-started | `04/04` | — |

### Wave 9 — Follow-ups (`14-followups/`)

Added 2026-08-23, during the Wave 4 decision pass. Holds work that emerged from
Waves 0-3 as decisions or review findings with no task attached.

| Task | File | Status | Depends on | Gated on |
|---|---|---|---|---|
| `14/01` | `14-followups/01-remove-default-seeded-catalog-source.md` | not-started | `01/01` (landed) | AD-05 (decided) |
| `14/02` | `14-followups/02-error-handling-backlog-paydown.md` | not-started | `12/01` stage 1 | AD-21 (decided) |

**Sequencing: after Wave 7, before `13/03`.** The repo-wide `gofmt` sweep stays
the batch's final commit per AD-22, so this wave must land ahead of it.

`14/02` **blocks `12/01` stage 2** — zero-tolerance on `errcheck`/`errorlint`/
`nilerr` cannot activate until its 365-finding backlog reaches zero.

The folder README also carries a register of **seven follow-up candidates**
logged in `TASKS/ESCALATIONS.md` but not promoted to tasks — visible in one
place rather than only chronologically. Promotion is the operator's call.

### Wave 8 — Mechanical cleanup

| Task | File | Status | Depends on | Gated on |
|---|---|---|---|---|
| `13/01` | `13-mechanical-cleanup/01-confirmed-dead-code-removal.md` | not-started | `09/02`, `09/04`, `03/01`, `00/02` | AD-09, AD-07 — **scope-changing, not merely gating** |
| `13/02` | `13-mechanical-cleanup/02-stale-comments-and-docs-cleanup.md` | not-started | Wave 7 complete | — |
| `13/03` | `13-mechanical-cleanup/03-naming-and-formatting-fixes.md` | not-started | **every other task in the batch** | AD-22 |
| `13/04` | `13-mechanical-cleanup/04-low-risk-error-handling-batch.md` | not-started | Wave 7 complete | — |
| `13/05` | `13-mechanical-cleanup/05-low-risk-hygiene-and-lock-scope-batch.md` | not-started | `11/05`, `11/10`, `09/02` | — |

### Load-bearing corrections found by the planning pass — real findings, not assumed

**The audit's raw evidence never landed on `main`.** `REPORT.md:8` states *"Raw tool output backing
every finding below lives in `raw/`."* That directory does not exist on `main` — it exists only in
`.claude/worktrees/go-quality-audit/docs/audits/2026-08-21-go-quality/raw/` (29 files, 8.0 MB),
untracked, with `git check-ignore -v` confirming the cause: `/Users/chrispian/.gitignore:11:*.log`.
The merge commit `8258176e` brought `REPORT.md` and `findings.json` across; the evidence they cite
did not. It cannot be regenerated — it was measured against a working tree at `8feeee5c` that no
longer exists. **Time-sensitive:** task `00/02` step 1 is written to run *before* the dev freeze so a
routine `git worktree prune` cannot destroy the evidence chain for 113 findings. Logged as AD-23.

**The lint rule that would have caught the most severe finding exists, and was scoped away from the
package where the bug lived.** `.golangci.yml:88-103` forbids `filepath.Join` with the message *"use
internal/pathsafe.ResolveUnder to prevent path traversal"*; `.golangci.yml:180-184` silences it
outside `(internal/sandbox/|internal/mcp/|internal/service/install/)`. `GO-PLUGIN-002` (critical,
unconfined path-traversal write) is a bare `filepath.Join(cs.pluginsDir, entry.Name)` at
`internal/api/catalog.go:301` — a package not in that list. Widening that one `path-except` to
include `internal/api/` is the highest value-per-line change in the batch (`08/09`, `12/01`).

**`GO-STORE-003` was already in the lint output and nobody saw it.** `REPORT.md:693` attributes the
high-severity `DeleteAgentByID` finding to golangci's `nilerr` linter at
`raw/golangci-baseline.log:6421`. `nilerr` is enabled today; it never gated because the pre-commit
hook runs `golangci-lint run --new` (`lefthook.yml`) — changed code only, which structurally cannot
surface a pre-existing finding in untouched code. This is the concrete argument for `12/01`.

**Two of the six production islands aren't flagged as needing a decision.** `GO-MEM-002` (Hadron
context gate) and `GO-MCPTOOL-003` (curated tool-knowledge matcher) carry
`requires_architect_decision: false` in `findings.json` despite the guide requiring a
wire/defer/retire call for all six islands. Both are in the queue anyway (AD-07, AD-11); Wave 0
should correct the catalog.

### Deliberate deviations from the remediation guide's ordering

**`12/02` (engineering standards docs) pulled forward from Wave 7 to Wave 1** — doc-only, collides
with nothing, and its content is already specified by `TASKS/audit-remediation/PREVENTION.md`.
Landing it first makes the six named standards citable by the remediation tasks meant to be governed
by them, instead of written down after the work they were supposed to govern.

**`08/08` (dependency bumps) marked "runs alone"** despite the guide's *"independent dependency
upgrades and isolated tests can run in parallel."* It rewrites `go.mod`/`go.sum`, which every
concurrent worktree also carries. The guide's advice assumes branch-per-task, not worktree-per-task.

**`13/03` (repo-wide gofmt) must be the last thing that lands, alone, with no other worktree open.**
If AD-22 selects the full sweep it rewrites 122 files (audited-commit count; `00/02` refreshes it)
and conflicts with every outstanding branch in the repository.

### Scope fence

Does **not** re-audit (new defects go to `ESCALATIONS.md`, not `findings.json`); does not refactor
`Container` (wiring-only with two methods — 60 fields is not itself a defect); does not split
`internal/store` (gravitational-package review, not mandatory split); does not delete islands on
reachability evidence alone; does not chase metrics (no splitting a type to reduce field/method
counts, no duplication work justified by LOC reduction); does not require the historical lint
backlog to reach zero before the ratchet lands; touches no frontend — all 113 findings are Go.

**Migration numbering: this batch claims none and needs none.** All findings are Go-level. Highest
migration on disk at planning time is `137` (`TASKS/skills/`). `135` remains unclaimed by
`TASKS/plugin-system/`, which was never dispatched — that gap is not this batch's to fill. If an
architect decision turns a task schema-touching (`07/03`'s retention policy is the one plausible
candidate), re-list `internal/store/migrations/` at that moment and claim the next free number then.

**Planned 2026-08-21, not yet dispatched.** Per `EXECUTION-PROCESS.md`'s Phase A discipline, this is
the planning checkpoint. Three blocking prerequisites before any dispatch: (1) the dev freeze is in
effect (AD-24); (2) Wave 0 is closed; (3) the operator has checked the approval box in the batch
README's `## Status` block. AD-23 (rescue the audit evidence) is urgent and **independent of all
three** — it should be decided and acted on immediately.
