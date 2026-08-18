# Live Runtime DB Analysis

**DB path:** `/Users/chrispian/.local/share/nanite/workspaces/default/main.db` (SQLite, WAL mode, live process attached)
**Date range queried:** 2026-08-13 through 2026-08-17 (query time ~22:30 on 2026-08-17), with some all-time queries run for row counts / schema context / to establish "never happened" baselines.
**Method:** read-only `sqlite3 <db> "SELECT ...;"` and `.schema` calls only. No writes.

## Tables examined and actual schemas

Column names below are as observed via `.schema`, since several diverge from what the audit brief assumed:

- `sessions` — no `created_at`/`last_activity` surprises; has `compacted_at`, `compaction_summary`, `intent`, `halted_at/reason`.
- `session_agents` — composite PK `(session_id, agent_id)`, `agent_id` explicitly has **no FK** ("agents may be file-based" per inline comment).
- `session_events` — `created_at` is TEXT (RFC3339, `T`-separated), event payload lives in `envelope_pointer_json`.
- `session_handoffs`, `session_stats` — present, unused in window (see below).
- `subagent_runs` — `status` CHECK constraint includes `stalled`/`over_budget` (added by later migrations — this is the exact column the crash-loop bug involved). `created_at` uses `T` separator with sub-second precision; `provider` column exists but is empty string on every row observed.
- `durable_agent_instances` / `durable_agent_instance_sessions` / `durable_agent_events` — `durable_agent_instances.durable` boolean marks the 7 "durable" profiles; `launch_source_type`, `lifecycle_class`, `activation_mode`, `default_state` describe the durable-agent taxonomy.
- `agent_runtime`, `agent_cycles`, `agent_schedules` — present; `agent_schedules` has only 1 active row DB-wide (loom-curator's daily cron).
- `agent_broker_decisions`, `broker_decisions`, `strategy_decisions`, `grounding_consultations`, `grounding_outcomes`, `playbook_match_log` — all present; **`grounding_consultations`/`grounding_outcomes` are empty tables, 0 rows all-time** (see Patterns).
- `messages` — `created_at` uses `T` separator; `content` is a JSON envelope (`{"v":1,"text":...,"tool_calls":[...]}`) for assistant turns, plain text for user turns.
- `agent_messages`, `agent_messages_legacy_089` — inter-agent inbox messages; small volume (13 rows all-time in `agent_messages`).
- `event_log` — `created_at` uses **space-separated** datetime (not `T`) — this tripped up an initial ISO-format filter attempt; had to requery with `'YYYY-MM-DD HH:MM:SS'` format.
- `nanite_recovery_breadcrumbs` — `timestamp` column (not `created_at`), RFC3339Nano.
- `handoff_stashes`, `compaction_events` — present, both effectively unused in window/ever (see Patterns).
- `execution_metrics`, `token_usage` — per-message-call metrics; `execution_metrics.error` was empty on every window row (errors surface via `event_log`/`subagent_runs.error` instead).
- `agent_profiles`, `agent_known_tools`, `agent_known_skills` — `agent_profiles` has a rich v2 schema (`source`, `source_ref`, `format`, `durable`, `imported_at`, `origin_system`, `agent_hash`) that directly answers the file-based-vs-DB-backed question (see dedicated section below).
- Also consulted: `workflow_runs`, `workflow_run_steps`, `tool_result_cache` (not in the priority list but directly relevant to tool-failure and cache-pointer questions).

---

## Incidents / anomalies

### 1. The subagent_runs crash-loop (migration bug) — confirmed via table state, gap in event_log, and post-fix activity

**Tables:** `subagent_runs`, `event_log`, `nanite_recovery_breadcrumbs`
**Timestamp:** fix commits `e2273f8`/`b5d5c13` landed 2026-08-17 12:52–13:05 (per `git log`); DB evidence brackets the outage before that.

`SELECT count(*), status FROM subagent_runs GROUP BY status` returns exactly **174 total rows**, matching the commit message's claim ("all 174 subagent_runs rows survive intact"), including the one `stalled` row cited in the commit:

```
id: eb870141-c50b-4321-a686-ce174e658e20
role: worker, status: stalled
created_at: 2026-08-17T16:00:07.172716Z
error: "stalled: inactivity reaper (no activity observed within threshold)"
```

Independent corroboration of the outage: `event_log` (3,833 rows all-time) has a **hard gap from 2026-08-16 02:49:54 to 2026-08-17 16:00:07** — roughly 37 hours with zero rows written, spanning the crash-loop window and extending well past the 12:52–13:05 fix. The very next events after the gap are `agent_broker_decision`/`broker_decision`/`subagent_recursion_blocked` at 16:00:07–16:00:08, coincident with the `stalled` subagent_run above. `nanite_recovery_breadcrumbs` shows **zero rows on 2026-08-17** at all (last breadcrumb: 2026-08-15T19:27:05Z) — the crash-loop happened at the DB-boot layer, before any breadcrumb-writing code path could run, so this specific outage left no breadcrumb trail, only the event_log gap and the subagent_runs row count matching the commit's verification claim.

### 2. Stale/retired model string causing repeated subagent failures (`claude-sonnet-4-20250514`)

**Tables:** `event_log` (category=error, event_type=provider_error), `subagent_runs`, `nanite_recovery_breadcrumbs`, `agent_profiles`
**Timestamps:** 2026-08-15 07:10–21:07, recurring as recently as **2026-08-17 17:59:54** (today, after the crash-loop fix).

`SELECT count(*), count(DISTINCT session_id) FROM event_log WHERE detail LIKE '%not_found_error%'` → 24 rows / 10 distinct sessions all-time, 8 rows in the 4-day window. Sample raw detail (id 2400):

```
iteration 0: POST "https://api.anthropic.com/v1/messages": 404 Not Found
{"type":"error","error":{"type":"not_found_error","message":"model: claude-sonnet-4-20250514"}}
```

These 404s are tightly linked to the `subagent_runs.status='failed'` rows (8 of 22 in-window subagent_runs, 36%): joining child_session_id shows the `reviewer`/`file-backend`/`worker` role subagents spawned from parent session `66f0330a-…` all failed with `error: "subagent: child chat loop emitted error event\nProvider streaming failed"`, and their child sessions (`cf40296e-…`, `0d43a00c-…`, `b9233425-…`, `bf25123a-…`) all carry `model = 'claude-sonnet-4-20250514'`. A repo grep (for context, not code review) shows this model ID is documented as retired by Anthropic (`pkg/models/registry.go`), and that **6 of 7** `.nanite/durable-agents/*.yaml` files still pin it — only `loom-curator.yaml` has been patched to omit it, per an in-file comment ("this file previously pinned … until this was cleared"). The most recent occurrence of this exact 404 in the DB is `event_log` id 3743, session `7e89dc42-…`, 2026-08-17 17:59:54 — i.e., this is not fully resolved as of the query.

### 3. Boot-dir-not-implemented for `pty`/anthropic provider combo — 6 failed boot attempts in 31 minutes

**Tables:** `session_events`, `nanite_recovery_breadcrumbs`, `sessions`
**Timestamp:** 2026-08-13 20:30:56 – 21:01:44

Six distinct sessions (`edd88c59…`, `f10603cf…`, `a40f03f6…`, `8d9058c8…`, `af9afb0f…`, `8d3f76b0…`), all `agent_id: "file-default"`, `provider: "pty"`, each fail within 0.8–3.3 seconds of `pty_turn_start`. The model string is different across attempts (`claude-sonnet-4-20250514`, then `claude-sonnet-5`, then `claude-sonnet-4-5-20250929`) as if something was being manually varied to work around the failure. `nanite_recovery_breadcrumbs` rows 35–40 record all six as `class: transient`, `outcome: transient_retry_succeeded`, but the underlying cause recorded later that same evening (breadcrumb id 41, same root cause class, session `cf40296e…`) spells out the actual error:

```
retry dispatch failed: agent.Boot: bootdir setup: agent: bootdir for provider "anthropic"
is not yet implemented (awaiting go-providers BootDirSpec coverage)
```

### 4. Orchestrator session hallucinating Torque tool names across a ~20-hour, high-cost session

**Tables:** `messages`, `event_log`, `agent_broker_decisions`, `token_usage`, `agent_profiles`
**Session:** `66f0330a-7862-4584-83e5-0a74d59c7c98` ("Orchestrator", the durable `orchestrator` agent's own session), turns from 2026-08-15 07:10 through 2026-08-16 02:50.

This single session accounts for the highest concentration of friction in the window: 28 of 118 window `tool_error` events (24%), 18 of 86 `agent_broker_decision` rows, and the single highest token/cost session in the window (**$0.469** across 16 `token_usage` rows, 917,785 total tokens — more than the next 9 sessions combined). Root cause is directly visible at the DB row level: `agent_profiles.tools` for `slug='orchestrator'` (id `d500a7c4-2053-4a30-b49f-831d1d780bb1`) lists:

```json
["torque_task_list","torque_task_get","torque_task_update","torque_task_search",
 "subagent_spawn","subagent_cancel","workflow_run","dev_read", ...]
```

None of `torque_task_list`, `torque_task_get`, `torque_task_update`, `torque_task_search` exist as real MCP tools — every call fails `unknown MCP tool: torque_task_get` (6 occurrences) or, via `tool_describe`, returns `tool_not_found` with `closest_matches` suggestions (e.g. `agent_list`, `todo_list`, `tool_list` for a guessed `torque_task_list`... `tool_describe`). The assistant's own message content shows it repeatedly self-diagnosing this:

> "The required Torque task management tools are **declared in my system prompt allowlist but not actually loaded** in my execution environment... When I attempt to call these tools, I get 'unknown MCP tool' errors, despite them being listed in my tool allowlist." (msg, 2026-08-15 17:25:34)

The recorded `tool_calls` array for that same message shows the agent retrying the identical failing call `torque_task_get` **5 times in a row**, with the harness itself injecting a repeat-suppression note after the 2nd/4th attempt ("this tool has returned the same result 2 times in a row"), then falling back to guessing filesystem/DB paths that don't exist (`~/.tether/torque.db`, `~/.agridd/torque.db`, `~/dev/hollis-labs/apps/nanite/.torque/tasks.json`) before giving up for that turn. `agent_broker_decisions` for this session shows the `reflex:reviewer-mention` reflex re-firing at confidence 0.15 across 13 of 18 decisions spanning nearly 20 hours of wall-clock time (07:10:14 → 02:47:43 next day), repeatedly routing back to a `reviewer` subagent role that itself was failing on the stale-model 404 (Incident 2). This same `agent_profiles.tools` row was still present unfixed as of the 2026-08-17T18:52:00Z catalog-wide profile resync (see file-based-vs-DB-backed section) — i.e., it was not part of the same-day fix that touched `loom-curator`/`loom-weaver`.

Despite this, the session's later turns (2026-08-16 00:48 onward) show the orchestrator successfully dispatching workers, tracking Torque status via the working `torque_issue_get` tool, and reporting real completions across 5+ task dispatches — the confusion was front-loaded, not sustained.

### 5. loom-curator tool-name/reference mismatch, live in the DB right up to a same-day fix

**Tables:** `messages`, `agent_profiles`, `agent_known_tools`, `durable_agent_instance_sessions`, `event_log`
**Timestamps:** failures 2026-08-17 16:09:23 – 21:19:57; DB-level fix reflected at 21:47:33 (loom-curator) / 19:34:17 (loom-weaver); first clean success 22:30:38.

Five loom-curator wake sessions on 2026-08-17 (`6ebfb0b8…` 16:09, `7a08d2e3…` 16:47, `2cf9136c…` 18:32, `99aa81e9…` 19:28, `93af1250…` 21:19) all hit tool errors, e.g.:

> "The tools listed in `skill_list` show as available but return 'unknown MCP tool' errors when I try to call them." (`2cf9136c-…`, 2026-08-17 18:33:51)
> "The issue is that the MCP tools (`get_fragment_detail`, `loom_*`) refer[ence]..." (`93af1250-…`, 21:19:57)

This matches `git show a21c936` (committed 17:54:56, same day): "roleTools/tools allowlist previously listed `fragments_*`/`wiki_*`/`relay_*` patterns that never matched any real MCP tool name... populated `agent_profiles.tools` (previously empty for both)." `agent_known_tools` for loom-curator shows exactly 3 rows, all `reason='role_seed'`, `added_at='2026-08-17 21:47:33'` (the deploy/reload lag between the 17:54:56 commit and the profile re-sync landing in the DB): `loom_*`, `get_fragment_detail`, `message_*`. The next 3 loom-curator wake sessions after that resync (`3b2661ea…` 21:42, `42f00077…` 22:18, `069545a3…` 22:30) no longer show tool-name errors — the first two fail instead on a **different** issue ("fragment retrieval failed — fragment ID ... not found in the Fragments Engine database"), and the third — the very last message row in the entire `messages` table (`created_at 2026-08-17T22:30:38Z`) — succeeds cleanly:

> "**Fragment compiled and stored** ... Title: Loom Pilot Smoke Test 8 ... Compile job: #5"

### 6. `subagent_recursion_blocked` burst — same session, 4 rejected spawns in 5 seconds

**Table:** `event_log`
**Timestamp:** 2026-08-15 19:27:00 – 19:27:05, session `7ab4a515-5520-4366-adde-e36a2e58838a`

```
2026-08-15 19:27:00  rejected spawn of role=reviewer: caller is itself a subagent
2026-08-15 19:27:02  rejected spawn of role=reviewer: caller is itself a subagent
2026-08-15 19:27:03  rejected spawn of role=reviewer: caller is itself a subagent
2026-08-15 19:27:05  rejected spawn of role=reviewer: caller is itself a subagent
```

All 4 attempts in this session are co-timestamped with `nanite_recovery_breadcrumbs` rows 58–61 (same session, `permanent`/`bootdir for provider "anthropic"` cause) and with a `provider_error` model-404 at 19:27:01–03 — i.e., a subagent stuck retrying a blocked recursive spawn while also hitting the stale-model 404 from Incident 2, in the same ~5-second span. 13 of 33 all-time `subagent_recursion_blocked` events fall in the 4-day window (39%), all `role=reviewer` or `role=worker`, all "caller is itself a subagent."

### 7. `workflow_runs` — only one row ever recorded, and it's `failed`

**Tables:** `workflow_runs`, `workflow_run_steps`
**Timestamp:** 2026-08-15T19:26:39Z

The entire DB (all-time) has exactly one `workflow_runs` row: `definition_name='worker-reviewer-gate'`, `status='failed'`, `error: 'step "approve" failed: skipped: an upstream dependency failed or was skipped'`. The step-level record shows the actual failure:

```
step_id=worker  kind=llm  status=failed
error: config resolution: agentworkflow: reference to unknown input "task"
```

i.e., a workflow-definition config bug (an unresolved `{{task}}`-style input reference) killed the `worker` step, which cascaded to skip the `approve` gate. This matches the `durable_agent_instances` row `workflow-worker-reviewer-gate-01m03e7ym5s9c6yn5vv89g5112`, status `stopped`, last touched 2026-08-15T19:26:39Z — never retried since.

### 8. `tool_result_cache` truncation flag doesn't line up with the documented 64 KiB threshold

**Table:** `tool_result_cache`

Per `CLAUDE.md`, results should only be cached-and-truncated when they exceed 64 KiB. In the window, 452 rows exist; **all 452 (100%) have `was_truncated=1`**, but **439 of those 452 (97%) are under 65,536 bytes** — the smallest is 2,051 bytes (`dev_glob`). Across the table's entire history (1,117 rows), `was_truncated` is `1` on every single row, min `byte_size` 2,051, no row ever has `was_truncated=0`. This is a purely descriptive discrepancy between the documented 64 KiB size gate and what the live table actually contains — every cache row observed is flagged truncated regardless of size, which either means truncation triggers on something other than the documented byte threshold, or the table only ever receives already-truncated rows by construction. Largest single cached result in-window: a `dev_grep` call at 1,354,360 bytes (~1.3 MB); largest all-time single-tool average was `skill_list` at 374,864 bytes on its one occurrence.

### 9. Durable agents stuck in `start_requested` limbo through the same-day catalog resync

**Table:** `durable_agent_instances`
**Timestamp:** all show `updated_at = 2026-08-17T18:52:00Z` (the mass profile resync, see next section)

Two durable agent instances sit in `status='start_requested'` as of the most recent snapshot: `atlas-librarian` (no `failure_reason` recorded) and `atlas-curator` (`failure_reason: "durable agent start requires workspace_id when creating a session"`). This exact failure string also appears repeatedly in `durable_agent_events` from 2026-05-25 (7 occurrences across `atlas-curator`, `ideation-partner`, `proxima`, `system-architect` that day) — i.e., a recurring, not-yet-resolved failure mode for durable-agent auto-start that survived a 3-month gap and re-appeared verbatim in the current row state after today's resync. `proxima` and `agent-builder` are also stuck in `start_requested`, both stale since 2026-05-25 with no activity since.

---

## Does this DB show evidence of file-based vs DB-backed agent definitions coexisting?

**Yes, unambiguously — every agent_profiles row is a DB-side mirror of a file, not an independently-authored DB entity.**

`SELECT slug, source, source_ref, format, durable FROM agent_profiles` (28 rows total) shows every row has `format='markdown'` and a `source_ref` pointing at a real file path:

- 9 rows: `source='internal'`, `source_ref='embedded:profiles/<slug>.md'` — compiled into the Go binary (e.g. `worker`, `reviewer`, `planner`, `researcher`, `hint-selector`, `default`, `backend`, `background-job`, `system-architect`).
- 19 rows: `source='project'`, `source_ref='.nanite/agents/<slug>.md'` — project-tree markdown files (e.g. `file-backend` → `.nanite/agents/file-backend.md`, `code-auditor`, `agent-builder`, `agridd-project-manager`, etc.).
- The 7 `durable=1` rows (`loom-curator`, `loom-weaver`, `atlas-curator`, `atlas-librarian`, `content-strategist`, `content-writer`, `ideation-partner`) are **also** `source='project'` / `.nanite/agents/<slug>.md`, and separately correspond 1:1 to `.nanite/durable-agents/<slug>.yaml` files on disk (confirmed via repo grep for the retired-model finding in Incident 2) — the durable-agent YAML and the markdown profile appear to be two file inputs feeding the same DB row.

Every row also carries `agent_hash != ''`, `origin_system='nanite'`, and `imported_at`. **26 of 28 rows share the identical `imported_at = '2026-08-17T18:52:00Z'`** — a single bulk re-import event, consistent with the nanite-api-service restart that followed the crash-loop fix deploy (Incident 1). The two exceptions, `loom-weaver` (`imported_at 2026-08-17T19:34:17Z`) and `loom-curator` (`2026-08-17T21:47:33Z`), got individually re-imported later that evening — exactly matching the `a21c936` fix commit (Incident 5) being deployed and reloaded after the mass resync.

`agent_known_tools` (321 rows) — the per-agent *learned/pinned* tool state — is **100% `reason='role_seed'`**, i.e. every row was seeded from the profile's `tools`/`role_tools` file-derived JSON, not accumulated from live usage/pinning (no other `reason` value exists anywhere in the table). Concretely: `loom-curator`'s 3 `agent_known_tools` rows (`loom_*`, `get_fragment_detail`, `message_*`) were inserted at `2026-08-17 21:47:33` — same instant as its `agent_profiles.imported_at` — confirming the known-tools table is entirely re-derived from the file-sourced profile on each (re)import, not independently DB-managed.

**Net:** there is no evidence in this DB of a DB-authored agent definition that lacks a backing file. The DB functions as a compiled/synced runtime cache of file-based agent definitions (both embedded-in-binary and project-tree markdown/YAML), rebuilt wholesale on service restart and per-file on individual redeploys.

---

## Patterns observed

- **subagent_runs failure rate:** 66/174 = **38%** failed all-time; in the 4-day window, 8/22 = **36%** failed, 1/22 (4.5%) stalled, 13/22 (59%) completed. The two dominant failure signatures in-window are `"subagent: child chat loop emitted error event\nProvider streaming failed"` (5 of 8, tied to the retired-model 404) and `"timeout: orphan, no child session"` / `"timeout: runner reaper"` (3 of 8).
- **event_log outage gap:** a 37-hour silent gap (2026-08-16 02:49:54 → 2026-08-17 16:00:07) with zero rows, bracketing the crash-loop fix window.
- **event_log category breakdown, 4-day window (3,833 rows all-time; window subset shown):** `tool_call` 1,188, `tool_error` 118, `agent_broker_decision` 86, `provider_error` 30, `broker_decision` 19, `subagent_recursion_blocked` 13, `envelope_error` 1. Tool errors concentrate heavily: 2 sessions (`66f0330a…`, `cb567c4d…`) account for 42 of 118 (36%) of all window `tool_error` rows.
- **provider_error root causes in window:** two distinct clusters — retired-model 404s (Incident 2, 8 window rows) and boot-dir-not-implemented for the pty/anthropic combo (Incident 3, 6 window rows via `nanite_recovery_breadcrumbs` class `permanent`).
- **`nanite_recovery_breadcrumbs` outcome mix in window (21 rows):** 6 `transient_retry_succeeded` (class `transient`, cause `http_stream`), 15 `permanent` (class `permanent`, cause `http_stream`/`http_stream_timeout`/`http_stream_rate_budget`, all reason text `bootdir for provider "anthropic" is not yet implemented`). Zero breadcrumbs recorded on 2026-08-17 despite that being the day of the crash-loop fix and the loom-curator fix — the DB-boot-level crash and the loom tool-mismatch failures both fell outside what this table captures.
- **broker/strategy decisions:** `agent_broker_decisions` window decisions: `reviewer` 34, `worker` 9, `researcher` 1, `planner` 1, and 41 rows with an empty `decision` (routed to `default-chat-handle` per the paired `reason` field). `strategy_decisions` window approach split: `direct_chain` 52 (61%), `subagent_delegation` 33 (39%). `broker_decisions` window outcome: `selected` 172 (89%), `loaded` 7, `empty` 3, `halted` 1, `reflected` 1.
- **grounding machinery unused:** `grounding_consultations` and `grounding_outcomes` are both **0 rows, all-time** — schema present, never populated in this DB's history.
- **compaction/handoff machinery largely unused in-window:** `compaction_events` is 0 rows all-time; no `sessions.compacted_at` is ever set (0 of 343 sessions); `session_handoffs` is 0 rows all-time; `handoff_stashes` has 26 rows all-time but none newer than 2026-05-19 — no compaction or handoff activity anywhere in the 4-day window.
- **token/cost concentration:** the top session by cost in-window (`66f0330a…`, the confused orchestrator session) spent $0.469 across 917,785 tokens — more than the next 9 highest-cost sessions combined (~$0.42 total). Several `worker`-role token_usage rows show `cache_read_tokens` in the millions (e.g. 4,402,466 and 2,944,366) against `input_tokens` of only 337–544 for the same row — a scale mismatch worth flagging descriptively (these values are far beyond any plausible context-window size), though whether it reflects a metrics-recording artifact vs. real cumulative cache reads was not determined from the DB alone.
- **session status mix in window:** of 51 sessions created 2026-08-13 or later, 39 `active`, 12 `archived`, 0 `paused`/`sleeping`/`halted`/`terminated`.
- **agent_profiles / agent_known_tools:** 28 agent profiles, all file-sourced (see above); 321 `agent_known_tools` rows, 13 `agent_known_skills` rows, 100% of the former `reason='role_seed'`.
- **durable_agent_events window distribution (14 days-worth shown, window-filtered):** `session_attached` 14, `start_requested` 14, `start_succeeded` 14 (i.e. every start in-window succeeded), `wake_requested`/`wake_started` 11 each, `wake_skipped` 2 ("wake already active", for `loom-curator` and `orchestrator`), plus single `created`/`updated`/`stop_requested`/`stop_succeeded`/`runtime_stop_succeeded` events.
