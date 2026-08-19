# Execution Index

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

**Parallelization:**
- **Wave 1 — parallel.** `01, 02, 03`. `01` touches `engine.go`/`factory.go`/`bootdir.go`/`agent_deps.go`; `02` touches `bootprofile/*` (read-only reference) + a new resolver home; `03` touches `sandbox_content_*.go` — no file overlap between the three.
- **Wave 2 — solo.** `04` (needs `01`, `02`, `03` all landed) — the actual deletion step.
- `05` and `06` are independent of the boot-profile-catalog cluster and of each other — both touch `internal/service/ingest.go`/adjacent code but different functions; can run in parallel with Wave 1 or after.

**Validation (2026-08-19, Orchestrator):** all 6 tasks merged to `main`; backend (`go build`/`vet`/`test`) and frontend (`tsc`/`vite build`/`vitest`, 180/180 passing) baselines green. Real dogfeed against the live `nanite-api-service` deployment: clean restart with new pid, no dangling `bootprofile` references in logs; a genuine DB-only `runtime_kind='cli'` agent (no backing file) correctly routed to the CLI runtime end-to-end (`"chat-service: CLI provider routed to agent runtime"`, real ~9s subprocess turn) — confirms `01`'s mechanism is correct. Finding logged in `TASKS/ESCALATIONS.md` (2026-08-19): file-backed agents (the overwhelming majority of agents in this deployment today) short-circuit `Get()`/`GetBySlug()` to an in-memory `agent.Definition` that carries no `runtime_kind`, so the legacy `chat.IsCLIProvider` fallback still decides routing for them in practice — not a bug, informational context for `phase-3/01`.

**Review (2026-08-19, fresh Reviewer, no shared context with workers):** 5/6 tasks (`02`-`06`) pass clean — no findings, every significant Work Log claim independently re-verified against code (not trusted). `01` passes on its own correctly-bounded scope but the reviewer independently confirmed (via its own empirical probe test) the same silent-misroute gap the Orchestrator's dogfeed run above had already hit by hand: `resolveProvider`'s untouched legacy cascade can resolve to a real HTTP provider before `runtime_kind` is ever consulted, so a `runtime_kind='cli'` agent with a bare (non-`pty-`-prefixed) `default_provider` would silently route to the API path today. Doesn't regress any live agent (all 28 real rows have `default_provider=""`/`runtime_kind='api'`); explicitly out of `01`'s scope and deferred to `phase-3/01` by design. Not a Phase 2 blocker — landed as a concrete requirement (Context finding + new "Done means" criterion + required test) directly in `TASKS/phase-3/01-collapse-resolveprovider-into-cascade.md`, and logged in `TASKS/ESCALATIONS.md`. **Phase 2 is `reviewed` and closed.**

## Phase 3 — Compaction & Recovery Events (3 task files)

| Task | Status | Depends on |
|---|---|---|
| 01-collapse-resolveprovider-into-cascade | reviewed | `TASKS/phase-2/01-wire-runtime-kind-routing.md` (needs `runtime_kind` already wired); Phase 1's cascade-resolved `model_id`; also now carries a required bug-fix (see 2026-08-19 addendum in the task file) from Phase 2 review |
| 02-wire-compaction-events | reviewed | `TASKS/phase-0/29-cut-prompt-templates` |
| 03-extend-event-log-to-recovery-mechanisms | reviewed | `TASKS/phase-0/32-rename-recovery-namespace` |

**Parallelization:** All three touch different files (`01`: `chat.go`/`engine.go`; `02`/`03`: `internal/api/sessions.go` at different call sites — the manual `/compact` endpoint vs. interrupted-turn detection) — low-risk parallel, merge-coordinate on `sessions.go` between `02`/`03`.

**Validation (2026-08-19, Orchestrator):** all 3 tasks merged to `main`; backend baseline (`go build ./...`, `go vet ./...` — same 2 pre-existing `container.go` findings, `go test ./...`) fully green. Real dogfeed against the live `nanite-api-service` deployment (redeployed, new pid, clean startup logs): `01`'s bug fix was already verified pre-merge by the worker against 343 real production sessions (0 mismatches) plus a direct reproduction test — not re-litigated live. `02`'s compaction-disclosure fix verified end-to-end against the running service: created a real session, sent a turn, forced a real compaction via `POST /sessions/{id}/compact`, confirmed a real `compaction_events` row landed, then sent a second turn and had the agent itself confirm — in its own reply — that it saw the compaction disclosure in its context. `03`'s three real-trigger tests (orphan sweep, recovery pack, interrupted-turn detection) were left to their own worker-authored real-store tests rather than re-triggered live (harder to safely reproduce against a shared service without real risk); reviewer should spot-check these. Test agents/sessions cleaned up after.

**Review (2026-08-19, fresh Reviewer, no shared context with workers): PASS, all 3 tasks.** Reviewer traced `resolveProvider`'s full rewritten logic by hand (not trusting the Work Log or test alone) and confirmed every path is closed once `runtimeKind=="cli"` — including the `agentProvider==""`/`sessionProvider==""` edge case, which surfaces a loud `ErrorCodeProviderError` rather than a silent fail-open. Independently re-ran all real-store tests across all three tasks (compaction writer end-to-end, orphan sweep smoke test, recovery pack, interrupted-turn detection) and confirmed each genuinely exercises production code paths, not mocks. Confirmed Recovery Broker's breadcrumbs-only decision by grep (no `event_log` call exists in `broker.go`, consistent with the documented choice). One non-blocking observation: `resolveProvider`'s step 1 now checks CLI-shape before the HTTP registry lookup (reordered from the original sequencing) — behaviorally inert today since `chat.IsCLIProvider` names are never registered as real HTTP providers, flagged for awareness only if that naming invariant ever changes. No fix-and-re-review cycle needed. **Phase 3 is `reviewed` and closed.**

## Phase 4 — Steering & Reflex Migration (8 task files)

| Task | Status | Depends on |
|---|---|---|
| 01-scratchpad-ttl-pruning | validated | `TASKS/phase-0/27-cut-p7-scratchpad-snapshot` |
| 02-dispatch-to-agent-reflex-action-kind-and-broker-migration | validated | Phase 1's reflex opt-out field; `TASKS/phase-0/21-cut-modes` |
| 03-migrate-promptrouter-to-reflexes | validated | `02` |
| 04-unify-run-another-agent-surfaces | validated | `TASKS/phase-0/03-fix-callertype-mistagging` |
| 05-wire-select-for-agent-to-read-agent-tools | validated | Phase 1's `04-add-known-tools-and-agent-tools-fk` (landed via the Phase 1→main merge) |
| 06-add-filter-tool-selection | validated | `TASKS/phase-0/22-remove-skill-and-tool-broker-abstractions`; held until `05` merges (both touch `internal/service/tool.go`'s `SelectForAgent`) |
| 07-tool-concurrency-safety-classification | validated | none directly; held until `05` merges — its target (`GetToolMeta`) also lives in `internal/service/tool.go`, an overlap the original parallelization note didn't flag |
| 08-export-and-drop-agent-broker-decisions | validated | `02` |

**Orchestrator note (2026-08-19):** confirmed via grep that `07`'s target (the concurrency-safety name-heuristic) lives in `internal/service/tool.go:677-682` — the same file `05` and `06` both touch, a three-way overlap the original parallelization plan below only partially flagged. Serializing: `05` solo first (Wave 1), then `06`+`07` together once `05` merges (Wave 1.5).

**Validation (2026-08-19, Orchestrator):** all 8 tasks merged to `main`; backend baseline (`go build ./...`, `go vet ./...` -- same 2 pre-existing `container.go` findings, `go test ./...`) fully green. Real dogfeed against the redeployed live `nanite-api-service`: clean restart with new pid, clean startup logs. Exercised the biggest structural change (agent-broker retirement) directly -- a real DB-only test agent, message "Please research the current state of the authentication system" correctly fired `dispatch_to_agent_researcher_mention` (`02`+`03`'s migrated reflex), attempted dispatch to `researcher`, hit a real and correct safety gate (`researcher`'s real profile has `can_execute=false`, not on the text-only whitelist), and gracefully fell back to chat-direct exactly as `02` designed -- confirms the full reflex-dispatch mechanism works end-to-end, not just in isolated tests. The chat-direct fallback then surfaced a real, `safego`-caught panic in `internal/mcp/dev_tools.go`'s `callGrep` (integer divide by zero) while using `dev_grep`/`dev_glob` -- investigated and confirmed unrelated to this batch (file untouched by any Phase 2-5 task; `dev_grep` was already concurrency-safe under the *pre*-Phase-4 heuristic too, so `07`'s reclassification didn't newly enable this path; the bug is a deterministic single-request logic error, not a race) -- logged in `TASKS/ESCALATIONS.md` as an out-of-scope discovery for a future fix, not a Phase 4 blocker. Test agent/session cleaned up after. Ready for fresh Reviewer dispatch.

**Parallelization:**
- **Wave 1 — parallel.** `01, 02, 04, 05, 06, 07`. Coordination, not hard blocks: `05` changes what `SelectForAgent` reads before `06`'s plugin filter hooks its output — land `05` first if both are in flight together. `06`/`07` both touch tool-catalog-rendering-adjacent territory — low risk, note for awareness.
- **Wave 2 — parallel.** `03` (needs `02`), `08` (needs `02`) — both depend on `02` landing but not on each other.

**Escalation logged**: the reaper item's real-world-behavior verification (originally scoped here as `01`, now `TASKS/phase-8/05-verify-reaper-behavior.md`) is resolved via historical-log analysis against the real production DB backup — see `TASKS/ESCALATIONS.md`'s corresponding entry and Phase 8 below.

## Phase 5 — Plugins & Registers (6 task files)

| Task | Status | Depends on |
|---|---|---|
| 01-build-assignment-api | not-started | Phase 1 tasks 01-08 (landed via the Phase 1→main merge) |
| 02-build-plugin-installed-enabled-state-model | not-started | none |
| 03-wire-registers-agent-profiles | not-started | Phase 1 in full (landed via the Phase 1→main merge) |
| 04-close-cli-install-hot-reload-asymmetry | not-started | none |
| 05-develop-registers-panels-and-crud | not-started | none (not urgent — may run whenever a real consumer exists) |
| 06-make-http-middleware-plugin-extensible | not-started | **operator design decision — see escalation below, not ready for mechanical dispatch** |

**Parallelization:** `02`, `03`, `05` all touch `internal/plugin/registrations.go` (different sections — the gating wrapper, the `agent_profiles` stub, the `panels`/`crud` stubs) — real overlap risk; land `02` first (it changes the shared gating structure `applyManifestRegistrations` wraps), then `03`/`05` can layer their specific registration logic on top. `01` (new REST endpoints, `internal/api/*`) has low file overlap with this cluster. `04` (`plugin_cmd.go`) and `06` (`server.go`) have no overlap with anything else in this cluster — fully parallel-safe.

**Escalation logged** (`TASKS/ESCALATIONS.md`): `06-make-http-middleware-plugin-extensible` has a real, unsettled design question (where plugin middleware may legally sit relative to the existing security-ordered chain — CORS-outside-auth, body-limit-inside-auth, caller-identity-between) that needs explicit operator input before implementation, not a worker default-guess. Do not dispatch `06` as a routine batch task until that input is recorded.

## Phase 6 — Envelopes & Cards (6 task files)

| Task | Status | Depends on |
|---|---|---|
| 01-rebuild-todo-list-as-composition | not-started | none |
| 02-rebuild-plan-review-as-composition | not-started | none |
| 03-rebuild-subagent-spawn-approval-as-composition | not-started | none |
| 04-build-interactive-table-row-actions-primitive | not-started | none |
| 05-exclude-card-data-from-replayed-context | not-started | none |
| 06-fix-cli-boot-content-card-type-list | not-started | none functionally; best run after `01`–`04` so the sourced list reflects the final type set |

**Parallelization:** `01`, `02`, `03` **all edit the same shared external manifest file** (`libs/go-envelopes/manifest/envelopes.yaml`, each removing/replacing a different standalone entry) — worktree-isolate for the actual coding work, but **merge one at a time**, same pattern as Phase 0's `15a`/`15c` card-type-registration coordination. `04` touches a different file in the same external module directory (`table-card.schema.json`) — lower risk, still flag for awareness. `05` (`internal/chat/context_client.go`) and `06` (`sandbox_content_*.go`) have no overlap with `01`–`04` or each other — fully parallel-safe.

## Phase 7 — PTY Rename (1 task file)

| Task | Status | Depends on |
|---|---|---|
| 01-rename-pty-naming-scrub | not-started | all of Phase 0 (see task file) |

**Solo phase.** This task touches ~90 files across nearly every subsystem the earlier phases modify (`chat_generate.go`, `container.go`, `chat.go`, `agent_deps.go`, durable-agent files) — a pure identifier/comment rename, no logic change. Run after every logic-changing task in Phases 2-6 has landed, to avoid repeated rebasing against a fast-moving set of files.

## Phase 8 — Test, Review, Verify (5 task files)

| Task | Status | Depends on |
|---|---|---|
| 01-cli-vs-api-experiment-durable-agents | not-started | **New Phase 2 in full, and New Phase 3's `01-collapse-resolveprovider-into-cascade.md`** — see below for the required sign-off, separate from this |
| 02-agent-reflex-propose-and-pending-reflexes-testing | not-started | none |
| 03-test-grounding-in-real-sessions | not-started | none |
| 04-truncation-review | not-started | none; coordinate with `TASKS/phase-4/06-add-filter-tool-selection.md` (both touch tool-catalog rendering) |
| 05-verify-reaper-behavior | not-started | none |

### ⚠️ `01-cli-vs-api-experiment-durable-agents` requires explicit, live operator sign-off at dispatch time

This is called out in the task file itself (a prominent banner at the top of `TASKS/phase-8/01-cli-vs-api-experiment-durable-agents.md`), and repeated here per the Planner's kickoff instructions: **this task routes a real Curator wake — a live production action against a real external system (Loom) — through an unproven CLI-based path.** Approving this overall plan, or Phase 8's index entry, does **not** authorize dispatching this specific task. It must never be bundled into a batch "the plan looks good, proceed." Whoever dispatches it (Orchestrator or operator directly) must get a live, in-the-moment go-ahead immediately before dispatch, every time — including which Curator config to use (`loom-curator` vs. `atlas-curator`) and what constitutes stopping the experiment early. Record the sign-off in the task file's own Work Log before any dispatch action.

**Escalation logged**: `05-verify-reaper-behavior`'s "no live traffic during this effort" scoping conflict (flagged as a planning landmine) is resolved in its own task file via historical-log analysis against the real production DB backup, not live observation — see `TASKS/ESCALATIONS.md`'s corresponding entry for the formal record. That analysis already found a real, currently-live bug (`last_activity_at` never writes in production, so the reaper's activity-reset fix has been silently non-functional since it shipped) — `05` scopes root-causing and fixing this as its primary deliverable.

## Phase 9 — Final Clean-up (1 task file; items formerly numbered 3–4 in TASKS.md's original Phase 6 remain deliberately left index-level only, per the operator's original scope decision — see `docs/engineering/TASKS.md`'s own framing, unchanged)

| Task | Status | Depends on |
|---|---|---|
| 01-old-docs-archival-pass | not-started | none — policy confirmed 2026-08-19, see below |

### ✅ `01-old-docs-archival-pass`'s policy confirmed 2026-08-19

Operator decision, recorded in full in the task file: genuinely superseded docs with no remaining historical value (mainly `docs/architecture/` and top-level `docs/*.md`) are **deleted** per the standing dead-code policy. All ADRs — including the newly-found **third `ADR-001` collision** (`adr/ADR-001-tech-stack.md`, a 23-file `adr/` directory never mentioned by `TASKS.md` or `docs/engineering/decisions/README.md`'s "two colliding sequences" framing) — are **kept with a banner** noting the collision and pointing to `docs/engineering/decisions/` as canonical, not deleted. `docs/audits/` (325 files) is archived/bannered as **one historical-snapshot unit**, not per-file triage. `adr/` is explicitly **in scope** for this pass.

---

## Escalations raised during Phases 1-6 planning (new since the Phase 0 presentation; phase/task numbers below use the 2026-08-19 Phase 2-9 resequencing)

In addition to `TASKS/ESCALATIONS.md`'s existing entries (Phase 0 planning), this pass adds:
- **Process note**: a research fork drifted into unauthorized self-dispatch (second occurrence of Phase 0's failure mode) — audited and kept, see `ESCALATIONS.md`.
- **Phase 8 reaper verification**: the "no live traffic to verify against" scoping conflict, resolved via historical-log analysis — see `ESCALATIONS.md`, and the real bug it found (reaper's activity-reset fix silently non-functional in production).
- **Phase 5 middleware plugin-extensibility** (`06`): a genuine, unsettled design question needing operator input before implementation — see `ESCALATIONS.md`.
- **Phase 9 item 1's docs-archival policy**: resolved 2026-08-19 (operator decision — delete superseded, keep+banner all ADRs including the third `adr/` collision, archive `docs/audits/` as one unit) — see Phase 9's table above.
- **Phase 8 item 1's sign-off requirement**: not an escalation in the doc/reality-mismatch sense, but the one item in this whole pass requiring a standing, repeatable, live-dispatch-time gate — see above.

See `TASKS/ESCALATIONS.md` for the full list of doc/reality mismatches found during planning, including which ones are already resolved (via Orchestrator/Planner judgment call within existing docs) and which ones genuinely need an operator decision before the relevant task is dispatched.
