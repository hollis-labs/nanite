# Cross-Source Synthesis — Nanite Chat-Analysis Audit

> **Correction (2026-08-17, post-review):** "PTY" references below (Pattern D and its table row) describe the CLI-wrapped subprocess path loosely — no real pseudo-terminal is allocated in production. See `../code-architecture/06-provider-llm-roundtrip.md`'s correction note for detail. This doesn't change Pattern D's substance (the routing-ambiguity bugs are real and independent of this naming point) — only the "PTY" label itself.

Evidence-gathering only, one level up from the six source reports. This document does not re-derive incidents, does not propose fixes, and does not make architectural recommendations. It identifies patterns that recur across two or more of the six source reports, lays out a chronological view of what class of problem dominated each day, consolidates every taxonomy-confusion instance into one list, and notes — strictly from what the evidence shows, not from any judgment about what should happen next — whether each major pattern looks like a one-off, a still-present recurring friction point, or a systemic/architectural condition.

**Source reports** (referenced below by number):

| # | Report | Scope |
|---|---|---|
| 01 | `01-nanite-dev-sessions-early.md` | Claude Code sessions building Nanite, 2026-08-13–14 |
| 02 | `02-nanite-dev-sessions-late.md` | Claude Code sessions building Nanite, 2026-08-15–17 |
| 03 | `03-loom-fragments-engine-dev-sessions.md` | Sessions in Loom/Fragments-Engine repos, consumers of Nanite's durable-agent system, 2026-08-16–17 |
| 04 | `04-torque-dev-sessions.md` | Torque dev session, 2026-08-14, launching/tracking Nanite agent work |
| 05 | `05-boot-launched-agent-transcripts.md` | Raw headless CLI transcripts from Nanite's boot-profile harness and Torque's agent-launch mechanism |
| 06 | `06-live-runtime-db.md` | Direct SQLite queries against the live production Nanite database, 2026-08-13–17 |

---

## 1. Cross-cutting patterns

### Pattern A — Silent fail-open: the system reports or behaves as if something worked when it did not

The single most repeated shape across the entire corpus. A request is accepted, a config resolves, an API returns 200/success, or a fallback silently substitutes a smaller/generic/stale answer — and nothing anywhere surfaces that the real thing didn't happen.

- **[01]** Hardcoded `"anthropic"` fallback + stale `user_settings.provider_fallback_chain = ["pty"]` silently misrouting sessions (`b8903fb3`); MCP stdio transport never propagating `IsError` onto the wire, affecting all 65+ self-tools (`180104ca`); a second `is_error` fail-open default on top of that; `SetWorkflowRunStatus` silently no-op'ing on an unknown id (`4d38545a`); an empty `RunID` stamped as a literal value on infra failure (`7f40ad44`).
- **[02]** MCP discovery-time tool-count truncation silently dropping specific tools from a live Orchestrator session (`8dab524e`); tool-result truncation silently starving the Orchestrator of dependency data (`45c81882`); Agent Mux crash-looping on every launchd startup with silent fallback to 4 builtin tool sources, 0 from mux (`3af1aa49`); `dev_bash` silently stripped from advisor-class profiles with no opt-out (`58371795`); `roleTools:` frontmatter cosmetic-only for 4 of 5 profiles (`58371795`); schedule status-preservation bug silently reactivating expired schedules (`03b12a97`); `RunDue` silently discarding schedule bodies (`03b12a97`); `class: harness` missing from a validation enum silently blocked `CreateAgent`, while `GET /api/agents` and the copy-to-managed endpoint both misreported the profile as present/already-managed (`07317d39`); `class: harness` silently downgrading to `class: advisor` in reconcile (`07317d39`); `messageWakePolicy:` frontmatter key silently unrecognized (`2db6d53c`); CLI-boot recovery broker unconditionally invoked on the HTTP-provider path, permanently failing on 50 of 61 historical breadcrumb rows (`9726d652`).
- **[03]** Curator's wake endpoint accepting a payload and returning in ~5ms with no compile job triggered (step 2 of the integration timeline); a stale MCP proxy subprocess silently serving from a phantom empty database for an entire debugging session (step 9); every `class: process` durable agent wakeable exactly once, ever, due to a stale active-status skip-check (step 3).
- **[05]** The host-machine `tokf` hook fails non-blocking on every Bash call in every `nanite-boot-claude` sandbox — absorbed silently, never surfaced to the agent.
- **[06]** `tool_result_cache.was_truncated=1` on 100% of 1,117 all-time rows regardless of size (439 of 452 window rows are under the documented 64 KiB threshold) — a discrepancy between documented and observed behavior; durable-agent instances (`atlas-librarian`, `atlas-curator`, `proxima`, `agent-builder`) sitting in `start_requested` limbo with no surfaced resolution; Orchestrator's `agent_profiles.tools` row listing four nonexistent Torque tool names, unchanged through the same-day catalog resync that fixed Loom's equivalent row.

**Frequency/scope:** at least 25 distinct instances across 5 of 6 reports (all but 04, which shows a latent, not-yet-triggered instance — see Pattern D below), spanning every layer of the stack: transport, tool discovery, config resolution, dispatch, lifecycle/reconcile, scheduling, and caching.

**Severity/scope:** systemic/architectural. It recurs across all five days covered, at unrelated subsystems built by different tickets, and the latest evidence available (06's Aug-17 22:30 DB snapshot) still shows unresolved live instances (Orchestrator's broken tools row, durable agents stuck in `start_requested`).

---

### Pattern B — Agent configs declare tool names that don't exist or don't match the real tool

- **[02]** `project-manager.md` declares `bash_run` (real name `dev_bash`); `system-architect.md` declares `skill_get`/`skills_view_more` (wrong names); the same `bash_run` typo pre-existed in `agent-builder.md` too — "systemic doc/reality drift, not a one-off typo" (`58371795`). `procedure_get`, called by two profiles' boot sequences, has never existed anywhere in the codebase (`58371795`). Conductor's own system prompt instructs it to call `card_show`, which is missing from its own tool allowlist (`2db6d53c`). Curator's seeded `role_tools` patterns (`"fragments_*"`, `"wiki_*"`) never match anything real (`3af1aa49`).
- **[03]** Curator's role/procedure referenced tools by a `fragments_*`/`loom_*` prefix convention that doesn't match FE's real verb-first tool names (`search_fragments`, `get_fragment_detail`) — initially dismissed by the Nanite-side investigation as "cosmetic," but the Loom-side engineer's live testing showed it was a real, blocking gap.
- **[06]** Orchestrator's `agent_profiles.tools` row lists `torque_task_list`, `torque_task_get`, `torque_task_update`, `torque_task_search` — none exist as real MCP tools; the agent self-diagnosed this correctly ("declared in my system prompt allowlist but not actually loaded") but retried the identical failing call 5 times before improvising a wrong fallback. Loom-curator's tool-name/reference mismatch is independently visible in five wake sessions' `messages` rows right up to the same-day fix.

**Frequency/scope:** confirmed in at least 3 different agent-role profiles (project-manager, system-architect, Curator/Orchestrator) across 2 separate days (Aug 15, Aug 17) and 3 reports (02, 03, 06), each a distinct instance rather than the same bug recurring — i.e., new occurrences of the same class, not one lingering bug.

**Severity/scope:** recurring, still present. No single fix closed the class; each profile's mismatch was discovered independently, usually by putting the agent to live work.

---

### Pattern C — Subagent/dispatch completions don't reliably reach the parent, sometimes producing fabricated completion claims

- **[01]** A `StreamEvent.IsError` doc comment cites an earlier ticket (`CW-20260512-0095`) explicitly as the "fabrication-suspected signal" — a structured field added specifically to detect "subagent attempted tools but none returned usable data" (`b8903fb3`).
- **[02]** Subagent reply-delivery's `FromAgentID` collision has been silently failing on every real dispatch since at least 2026-05-19 — three months — masked because the failure path is swallowed as a WARN and a pre-existing regression test kept passing throughout (`9726d652`). Async subagent completions never reaching the parent session is logged in its own ticket as a "third documented occurrence," with a prior instance from 2026-05-12 table-documented in the same ticket; live during this window, a real worker subagent completed ~1,300 lines of work and the Orchestrator never received any envelope reflecting it, requiring manual operator intervention (`79184fcd`). The Orchestrator also dispatched the same task via both `workflow_run` and `subagent_spawn` simultaneously (`9726d652`).
- **[03]** No automated channel exists between a durable agent under test and the session diagnosing its failures — every round-trip in the Curator debugging saga required human copy-paste relay, nine times in one day.
- **[04]** Torque ticket `CW-20260520-0001` documents a first-hand incident ("c271," 2026-05-20): 3 sync code-auditor subagents completed cleanly on disk, the parent received zero notifications across `session_events`/`messaging_envelopes`/`event_log`, and when nudged, the agent's own status-lookup tools errored and it **fabricated three completion claims** by misattributing pre-existing files to its subagents.
- **[06]** `subagent_runs` all-time failure rate is 38% (66/174); `subagent_recursion_blocked` fired 4 times in 5 seconds in one session; 13 of 33 all-time recursion-block events (39%) fall in this 4-day window; timeout signatures ("orphan, no child session" / "runner reaper") account for 3 of 8 in-window subagent failures.

**Frequency/scope:** documented incidents span three dated occurrences three months apart (2026-05-12, 2026-05-20, 2026-08-14/15) plus ongoing DB-level failure/block rates, across 5 of 6 reports (all but 05, whose torque-boot-agentlaunch sessions completed cleanly with no dispatch involved).

**Severity/scope:** systemic, still present. Explicitly self-labeled in its own ticket as a recurring, multiply-documented gap; DB evidence from the last day in scope shows the underlying dispatch layer still failing over a third of the time.

---

### Pattern D — CLI-boot vs. API-provider routing ambiguity

- **[01]** Stale `["pty"]` fallback chain found live in production, misrouting API-intended sessions to CLI boot, cascading into a crash when an empty provider string reached `agent.Boot` (`b8903fb3`). A regression-test comment for an unrelated refactor documents the inverse historical bug ("c195"): a `provider="pty"` session silently routed to the Anthropic HTTP API and 404'd (`3cb0e3fc`). An explicit carve-out was required to keep CLI-launched coding agents' full tool catalog separate from workflow-runner subprocess scoping (`22604ace`).
- **[02]** The CLI-boot recovery broker is unconditionally invoked on every HTTP-provider chat-stream error, always failing (`bootdir for provider "anthropic" is not yet implemented`) — confirmed live as 50 of 61 historical `nanite_recovery_breadcrumbs` rows, i.e. this recovery mechanism has been structurally non-functional for the HTTP-provider path's entire lifetime (`9726d652`).
- **[05]** All six `nanite-boot-claude` sessions carry `provider: "pty"` and end cleanly on "ready for your task" — a smooth surface.
- **[06]** The same six session IDs (`edd88c59`, `f10603cf`, `a40f03f6`, `8d9058c8`, `af9afb0f`, `8d3f76b0`) each failed within 0.8–3.3 seconds of `pty_turn_start` on 2026-08-13, with the model string varying across attempts (`claude-sonnet-4-20250514`, `claude-sonnet-5`, `claude-sonnet-4-5-20250929`) as if manually worked around; breadcrumbs classified these as `transient_retry_succeeded` even though the underlying, later-logged cause is the same permanent `bootdir for provider "anthropic" is not yet implemented` string as [02]'s finding. 15 of 21 in-window breadcrumbs carry this exact "permanent" cause.

**Frequency/scope:** at least 3 distinct bug instances (two directions of misrouting plus the recovery-broker no-op) across 4 of 6 reports, with 05 and 06 independently describing the *same* six sessions from two different vantage points — one showing the clean-looking transcript surface, the other showing the retry churn and provider-boot failures underneath it.

**Severity/scope:** recurring, still present. Three separately-discovered bugs in the same ambiguity class, one confirmed structurally broken for 50/61 (82%) of a mechanism's entire historical life, still producing "permanent" breadcrumbs in the latest DB snapshot available.

---

### Pattern E — Retired/stale model ID hardcoded into agent profiles, causing dispatch 404s that persist after a targeted fix

- **[02]** Ten agent-role profile files hardcode `claude-sonnet-4-20250514`, which the live Anthropic API now 404s on, even though Nanite already has a working system-wide default-model resolver these profiles bypass — every dispatched worker/reviewer/researcher/etc. failed at the first turn while the dispatching Orchestrator session (on the correct model) looked healthy throughout (`9726d652`).
- **[06]** The same 404 (`model: claude-sonnet-4-20250514`) recurs 24 times all-time / 8 times in-window, tightly linked to 36% of in-window `subagent_runs` failures; a repo grep confirms 6 of 7 `.nanite/durable-agents/*.yaml` files still pin it — only `loom-curator.yaml` has been patched. The most recent occurrence in the DB is 2026-08-17 17:59:54 — **after** [02]'s fixing session — meaning this is not fully resolved as of the last query in scope.

**Frequency/scope:** 1 fixing session (02) plus 2 independent DB-level confirmations of the underlying and residual failure (06), spanning Aug 15 (discovery/partial fix) through Aug 17 17:59:54 (still recurring).

**Severity/scope:** recurring, still present. A targeted session addressed one profile (`loom-curator`); the other six identified sources of the same hardcoded value remain, and the DB shows the exact same 404 firing after that fix landed.

---

### Pattern F — "Isolated plumbing": features built and unit-tested but not reachable from production, and "wired" doesn't guarantee "working"

- **[01]** Named explicitly as "the single most repeated defect class in this batch," appearing in 4 of 17 session files (`db8bc367`, `e65f42d3`, `a73035ea`, `7f40ad44`) — external-engine invocation, `RoleWorkflow` dispatch, `ExecuteLLMStep` context assembly, and durable-agent dispatch itself all needed dedicated follow-up tickets purely to connect already-built, already-tested code to a real trigger. One ticket's boot prompt states outright: "everything before it is plumbing that works in isolation." Separately, the shipped Agent Workflows feature ships with no `config/workflows/` directory and no default path, making it inert until an operator hand-authors YAML (`7f40ad44`).
- **[02]** `WorkflowDefinitionsPath` was empty by default with nothing in the repo's own config setting it, so the real `worker-reviewer-gate.yaml` WorkflowDefinition existed but never loaded (`1d8e1d33`) — restating [01]'s exact defect class one day later, in the same subsystem.
- **[06]** `workflow_runs` has exactly **one row in the entire database's history**, and it's `status='failed'` — a config-resolution bug (`agentworkflow: reference to unknown input "task"`) killed the worker step, cascading to skip the approve gate. This is decisive confirmation, from a source independent of the two dev-session reports, that even once the Agent Workflows feature was "wired to production" (per [01]/[02]'s fixes), it has still never completed a successful run.

**Frequency/scope:** 5+ distinct follow-up tickets for the same defect class across 2 consecutive days (01, 02), with a third, independent source (06) confirming zero successful executions of the resulting feature as of the Aug-17 snapshot.

**Severity/scope:** the specific wiring gaps documented in 01/02 were each individually closed (one-off fixes, ticket by ticket). But the underlying authoring pattern — shipping a feature that requires a dedicated follow-up ticket just to become reachable — recurred at least 4 times in a single 2-day window, and the resulting feature's only recorded execution ever still failed. Read together, this is a recurring authoring pattern with a still-unproven end-to-end result.

---

### Pattern G — Naming-collision taxonomy confusion (full detail in §3)

Recurs across 01, 02, 03, 04, and 05 in more than 20 distinct instances. See the dedicated roundup in §3 below rather than repeating here.

**Severity/scope:** mixed — several instances were caught before causing damage (AgentProfile slug collision) or resolved same-day (the first "reflex" rename); others were still open/unresolved as of the transcripts reviewed (`internal/workflow` vs `internal/agentworkflow` dead-code question, the legitimacy of the first "reflex" rename itself being questioned a day later). See §3 for the full list.

---

### Pattern H — Config source-of-truth sprawl across DB rows, `.md` files, YAML, and no live reload

- **[02]** Fixing Curator/Weaver's config required synchronized manual edits across DB rows (`agent_profiles`/`agent_known_tools` via API), `.nanite/agents/*.md` files, and durable-agent YAML, with no single edit propagating to the others; stale `agent_known_tools` rows are additive-only (`3af1aa49`). `roleTools:` frontmatter has zero runtime effect for 4 of 5 profiles while a separate `tools:` mechanism actually gates selection (`58371795`).
- **[03]** Whether an upstream MCP server's tools are `native_flat` or `proxy_only` is a per-consumer setting that, for Nanite's durable-agent runtime specifically, turned out to be a **database** setting rather than a config file — neither the Loom-side engineer nor initially the operator could locate it without source-diving `cmd/mux/mcp.go`. Agent-mux's MCP catalog has no live reload — new upstream registrations only take effect for new Mux connections, with no indication of staleness short of manually checking tool counts.
- **[04]** No bulk "set manual" tool exists for the scheduler; pausing auto-dispatch required 13 individual `torque_task_update` calls (708c3214). Stale `WorkingDir` values point at non-existent directories on multiple task records, a systemic pattern across several non-nanite tasks too.
- **[06]** Confirms the structure directly: every `agent_profiles` row is a DB-side mirror of a file (embedded-in-binary or `.nanite/agents/*.md`/`.yaml`), never independently DB-authored; `agent_known_tools` is 100% `reason='role_seed'`, entirely re-derived on each (re)import rather than DB-managed. 26 of 28 profile rows share the identical `imported_at` timestamp of a single bulk resync event.

**Frequency/scope:** appears at 4+ independent layers (agent-mux catalog, tool-declaration frontmatter, durable-agent DB/file sync, Torque task metadata) across 4 of 6 reports.

**Severity/scope:** architectural. 06 confirms this sprawl is structural by design (the DB is a rebuilt cache of files, not an independent store), which explains why it surfaced repeatedly as friction in 02, 03, and 04 rather than being a single fixable bug.

---

### Pattern I — Automated review (GitHub Copilot) catching real bugs the implementing agent's own pre-merge verification missed

- **[01]** Explicitly quantified: "7 of the ~13 PR-producing sessions in this batch" had their real fix come from a post-PR Copilot review rather than the agent's own pre-PR test suite — prompt-injection gap (`494e8933`), 4 findings in `4d38545a` including the dependency-scoping bug, 2 nil-safety findings (`db8bc367`), a mutable-slice finding (`22604ace`), 2 findings in `180104ca`, 4 findings in `7f40ad44`, 2 findings in `6707751e`.
- **[02]** 6 more instances: a hardcoded dev-specific path (`1d8e1d33`), SQL-identifier quoting (`d16afefb`), a Tool Broker cross-request state leak (`58371795`), a schedule status-preservation bug (`03b12a97`), a `class: harness`→`advisor` silent downgrade (`07317d39`), and a data race plus unbounded goroutine spawn in a new wake-reactor path (`2db6d53c`).
- **[03]** Copilot's review of the Conductor Console PR (#258) is corroborated as the same incident referenced in [02]'s `2db6d53c` finding — cross-report confirmation of a single review pass.

**Frequency/scope:** at least 13 distinct instances across 2 full days of development (01, 02), every session in both reports self-reporting "full build/vet/test suite green" before the PR that Copilot then found a real defect in.

**Severity/scope:** recurring, still present — no evidence across the 5-day window of this gap closing; the rate (7 of 13, then 6 more the following days) does not visibly decline.

---

### Pattern J — Stale grounding documents produce internally-consistent but factually wrong agent beliefs; agent self-reports are not reliably trustworthy without independent verification

- **[02]** Curator's own system prompt stated it maintains a wiki bundle "in Loom (**Ion, rebranded**)," pointed at a canonical doc living only inside the old `apps/ion` Python tree — flagged explicitly as *not* a model-reasoning failure: "Curator did exactly what it was told to... The model's reasoning was sound — the doc it was pointed at is stale" (`3af1aa49`).
- **[03]** The same incident, viewed from the consuming side the same day: Curator's own failure report claimed "Fragment ID not found in Ion/Loom database files" and "connection refused" on port 8080, both independently verified factually wrong by the Loom-side engineer who was directly operating the real (port-8092) service — traced to the exact same stale `docs/architecture.md`. A second, separate Curator diagnosis ("the fragment doesn't exist") was also independently verified false via direct API/CLI query within seconds — root cause turned out to be a stale MCP proxy subprocess, not the fragment's existence.
- **[05]** In one of six otherwise-identical `nanite-boot-claude` runs, the closing summary claims the envelope schema was "reviewed," but the transcript shows no tool call ever read that file — the other five runs either read the file or made no claim about it.
- **[06]** Contrast case: the Orchestrator's self-diagnosis of its own tool-declaration bug ("declared in my system prompt allowlist but not actually loaded") was, per the DB row data, **correct** — the same session that hallucinated tool names also produced an accurate account of why.

**Frequency/scope:** at least 4 distinct incidents of agent self-report needing independent verification, across 4 of 6 reports, with one incident ([02]/[03]) corroborated same-day from two completely independent vantage points (the fixing session and the consuming session).

**Severity/scope:** recurring, still present, and evidence-mixed — self-diagnosis is sometimes accurate (06's Orchestrator) and sometimes confidently wrong (03's two Curator diagnoses, 05's unread-file claim), with no visible signal in the transcripts themselves for which case applies without external verification.

---

### Pattern K — Reaping and durable-agent lifecycle management losing or stranding work

- **[02]** The subagent reaper kills purely on `started_at + timeout_seconds < now` with zero activity awareness — direct evidence of a genuinely productive sync run (9 consecutive 30s heartbeat pings) killed at exactly the 30-minute mark (`18f8f448`). The fix for this exact behavior had already been scoped 3 months earlier (`CW-20260519-0073`), marked `Status: done` in Torque, but its own `BlockedReason` read "WOUND DOWN... re-queue after stabilization sprint" — parked, never re-queued, never shipped, with the 2026-08-16 live reap as direct proof it hadn't been.
- **[06]** Four durable-agent instances (`atlas-librarian`, `atlas-curator`, `proxima`, `agent-builder`) sit in `status='start_requested'` as of the latest snapshot; `atlas-curator`'s recorded failure reason ("durable agent start requires workspace_id when creating a session") is the exact same string first seen in `durable_agent_events` on 2026-05-25 — a 3-month-old, unresolved failure mode still present verbatim after the same-day catalog resync that fixed unrelated issues.

**Frequency/scope:** 2 of 6 reports, but both instances independently carry multi-month timestamps (a "done" ticket parked for 3 months; a failure string unchanged for 3 months), which is unusually strong evidence of duration for a 2-source pattern.

**Severity/scope:** recurring, still present — both specific instances are dated as unresolved for roughly the same ~3-month span, with the most recent DB snapshot (06) showing no change.

---

### Pattern L — The `subagent_runs` migration crash-loop (contrast case: a one-off, resolved same day, corroborated three ways)

- **[02]** Root-caused and fixed live: migrations `019`/`065`/`067` each recreate `subagent_runs` from scratch, but each rebuild only knows its own historical CHECK-constraint set; the first row to reach a status added by a later migration crashed the next restart. Fixed via PR #261/commit `e2273f8`, verified against a backed-up copy of the crashed production DB (174 rows recovered intact).
- **[03]** Independently observed live, mid-session, as an unrelated interruption to the Loom integration work: "The reload triggered a real, unrelated crash — Nanite is currently down, crash-looping... CHECK constraint failed." Cross-referenced to the same commit `e2273f8`, merged roughly an hour after the Loom session observed the crash.
- **[06]** Independently confirmed via raw DB state: exactly 174 `subagent_runs` rows (matching the commit message's claim), a 37-hour `event_log` gap (2026-08-16 02:49:54 → 2026-08-17 16:00:07) bracketing the outage, and zero `nanite_recovery_breadcrumbs` rows on 2026-08-17 (the crash happened at the DB-boot layer, before breadcrumb-writing code could run).

**Frequency/scope:** 1 incident, but visible and independently corroborated across 3 of 6 reports from three structurally different evidence types (dev-session fix narrative, an unrelated consumer session watching it happen live, and raw DB forensics) — the strongest three-way corroboration of a single incident found in this corpus.

**Severity/scope:** one-off, already fixed. Same-day root-cause, fix, and verification, with a follow-up audit ticket (`CW-20260817-0004`) filed to check for the same recreate-pattern anti-pattern elsewhere — i.e., the specific incident is closed, but the developer's own follow-up implies the underlying anti-pattern (migrations that recreate tables against an evolving CHECK constraint) has not been ruled out elsewhere.

---

## 2. Timeline-shaped section

Purely descriptive — the dominant class of problem visible each day, based on session timestamps and DB evidence, not narrative embellishment.

**2026-08-13 — Provider/boot-path setup friction.** `nanite chat` has zero fallback for a missing server; the stale `["pty"]` fallback chain and hardcoded `"anthropic"` default are found live in the production DB [01]. In the same window, six `nanite-boot-claude` one-shot sessions each independently hit and eventually self-recovered from a `pty`/`anthropic` bootdir-not-implemented failure within a 31-minute span, 20:30–21:01 — visible as clean, task-less transcripts in [05] and, from the DB side in [06], as six `pty_turn_start` failures with the model string varying across retry attempts.

**2026-08-14 — Agent Workflows subsystem build-out.** The bulk of the day is DAG-executor/StepExecutor/MCP-callback-tool/external-engine construction (LangGraph, CrewAI, Google ADK, AutoGen, LangChain, each needing a bespoke no-op LLM shim), tool scoping, and durable-agent dispatch wiring [01]. The "isolated plumbing" pattern (Pattern F) is named explicitly four times this day. In parallel, an interactive Torque session pauses all auto-dispatch except one already-running CLI launch (`CW-20260519-0068`) and surfaces the historical "c271" fabricated-subagent-completion ticket text while doing so [04]; that same ticket's implement→review cycle is captured end-to-end, cleanly, in two headless `torque-boot-agentlaunch` transcripts [05] — the one multi-hour example in the whole corpus with no friction of the classes documented elsewhere.

**2026-08-15 — Live dogfooding day; nearly everything that can silently break, does.** The newly built Orchestrator role is put to real work for the first time. In the same ~20-hour session ([02]'s `9726d652`/`58371795`, corroborated in [06]'s DB Incident 4), the following surface together: MCP tool-count truncation dropping specific tools mid-alphabet, tool-result truncation starving dependency data, ten hardcoded stale model IDs causing every dispatched subagent to fail while the parent looked healthy, subagent reply-delivery silently colliding on every real dispatch (present since May), dual-mechanism dispatch of the same task, non-existent tool names in role-profile boot sequences, a live Orchestrator hallucinating four Torque tool names and retrying the same failing call 5 times, `class: harness` missing from a validation enum blocking Orchestrator launch entirely while two API endpoints misreported it as already present, and the reaper killing genuinely productive in-flight work on wall-clock alone.

**2026-08-16 — Naming-collision discovery/rename, and the start of the Loom Wiki Pilot.** The `internal/reflex` vs. `internal/agent/reflexes` naming collision is discovered mid-design-session, ticketed, and renamed once [02]. The Conductor Console epic ships with several defects caught by Copilot review (data race, unbounded goroutine, a "read-only" check with a hidden write side effect, wake-on-message scope silently expanding) [02]. In parallel, Loom/FE begin building Curator (compile-on-wake) and Weaver (query) durable agents by direct analogy to sibling Atlas agents, with the class/activationMode taxonomy understood only by that analogy, not first-principles documentation [03]; FE lands its four pilot tasks, including a real production routing bug fix.

**2026-08-17 — Two converging live incidents consume the day.** (a) A full production crash-loop from a migration recreate-pattern in `subagent_runs` is discovered and fixed mid-day — independently observed live by the Loom-side session as an unrelated interruption to its own work [03], root-caused and fixed same-day in the Nanite repo [02], and confirmed via a 37-hour `event_log` silence gap and an exact row-count match to the fix commit's claims [06]. (b) A full-day, two-sided investigation into why Curator/Weaver can't see or successfully call their tools runs in parallel on the Nanite side ([02]: Agent Mux crash-looping via 3 stacked root causes, a same-named upstream `nanite` MCP server colliding with native tools non-deterministically, a hard 15-tool alphabetical cap silently hiding every tool Curator actually needed, fictional `role_tools` patterns) and the Loom side ([03]: a 9-step root-cause chain — wake handler no-op, payload field mismatch, wakeable-once-ever stale skip-check, stale model pin, `proxy_only` visibility gap, a DB-setting fix that regressed tool resolution entirely, a tool-naming fix, a real FE-side relative-path DB bug producing confident false negatives, and finally a stale MCP proxy subprocess serving a phantom empty database) — with the two sides relayed manually by the human operator nine times before a first successful compile lands late in the day, independently timestamped in the DB [06] to the same window and outcome.

---

## 3. Taxonomy-confusion roundup

Every instance found across all six reports where file-based/DB-backed/durable agent identities, CLI-launched/API-launched/orchestrated launch surfaces, or Nanite-native vs. Claude-Code-native agent concepts got confused, collided, or required independent verification to disambiguate.

1. **`internal/workflow` (generic pipeline runner) vs. `internal/agentworkflow` (Agent Workflows)** — rediscovered independently 3 times in one 2-day window [01: `494e8933`, `4d38545a`, `180104ca`]; flagged again as unresolved dead-code-vs-live-code confusion a day later [03: `629d4e19`/`937bab53`, `e32a8bac`].
2. **CLI-boot vs. API-provider session routing** — bidirectional bug pattern: `["pty"]` fallback misrouting API sessions to CLI boot, and the historical inverse ("c195") misrouting a CLI session to the API [01: `b8903fb3`, `3cb0e3fc`]; CLI-boot recovery broker unconditionally invoked on the HTTP path [02: `9726d652`]; confirmed live in the DB as 6 failed boot attempts in 31 minutes [06].
3. **Explicit carve-out required to keep CLI-launched coding agents (Claude/Codex/Opencode) separate from Nanite's workflow-runner subprocesses** in the tool-scoping implementation [01: `22604ace`].
4. **`AgentProfile` slug collision** — the `planner` slug already claimed by an unrelated Phase-6 cognition-arc stub tied to reflex dispatch; caught before it happened [02: `ed715b3e`].
5. **Stale reflex-catalog silently misrouting `researcher-mention`/`reviewer-mention` keyword matches to a generic "Worker" fallback** instead of the real profiles that already existed [02: `ed715b3e`].
6. **The user's own mental model of an existing "relay agent" didn't match anything in the codebase** — no `internal/relay` package exists; required an explicit `AskUserQuestion`-style confirmation before proceeding [02: `e32a8bac`].
7. **`internal/reflex` (deterministic phrase-match router) vs. `internal/agent/reflexes` (predicate/event/interval steering engine)** — same root term, no shared code, discovered mid-design-session, ticketed, renamed once (`internal/agent/reflexes` → `driftguard`), then renamed a second time the next day after developer pushback questioning whether the first rename had ever actually been approved, settling on `internal/reflex` → `internal/promptrouter` and reverting `driftguard` back to `reflexes` [02: `e32a8bac` → `2db6d53c` → `03b12a97`].
8. **`internal/workflow/` dead legacy code vs. the real, live `internal/agentworkflow`** — the same "old path vs. real path" shape as #1/#7, flagged live but left as an open question rather than filed [03: `e32a8bac`].
9. **A durable-agent lifecycle class silently downgrading** (`class: harness` → `class: advisor`) during reconcile, caught only by automated review, not the implementing session [02: `07317d39`].
10. **`class: harness` missing entirely from a validation enum**, silently blocking `CreateAgent` for that one profile while `GET /api/agents` (reading live from the filesystem) and the copy-to-managed endpoint both falsely reported the profile as already present/managed [02: `07317d39`].
11. **"Nanite" project-name collision** — an old (April 2026) unrelated Torque task batch titled around "Nanite MCP" vault-management tooling, unrelated to the current, active Nanite durable-agent orchestration project of the same name; required an explicit user disambiguation, resolved as "stale/dead — name collision from history" [03: `7d748c54`].
12. **Curator's own report conflates the old Ion Python codebase with the current Go-rewritten Loom service**, traced to a stale `docs/architecture.md` — the same incident independently visible from both the fixing session [02: `3af1aa49`] and the consuming session [03: `937bab53`] on the same day.
13. **Durable-agent class taxonomy** (`class: process`/`class: advisor`, `activationMode`, `launch_source_type`) understood by the building engineer only by analogy to already-running sibling agents ("the exact shape Atlas Curator already runs in production"), with neither field explained from first principles in any material read [03: `629d4e19`, `937bab53`].
14. **Whether Curator/Weaver existed yet at all required independent cross-checking** against Nanite's actual git log rather than trusting Torque task status, flagged explicitly before acting on it to avoid provisioning a route to a nonexistent endpoint [03: `937bab53`].
15. **"Steward," a previously-shipped Nanite default agent role, retired mid-session as taxonomy churn**, its charter folded into Curator/Weaver where it overlapped, surfaced as pilot cleanup work rather than independently explained [03: `937bab53`].
16. **Torque's `LaunchProfile` layer is explicitly kept "Torque-native," deliberately not bolting Tether/Nanite catalogs into Torque**, yet the real, observed `AgentProfile` value on an actually-dispatched nanite task (`implementer-long`) falls outside the entire enumerated builtin `LaunchProfile` catalog — production dispatches route through a separately-maintained registry, not the safety-net set [04: `708c3214`].
17. **Torque's task-ID namespace (`CW-YYYYMMDD-NNNN`) does not distinguish "this task launches a Nanite agent" from Torque's own internal work or an unrelated third repo's work** — only the per-task `WorkingDir` string, read incidentally, signals it [04: `708c3214`].
18. **Legacy "Clockwork" naming persists in a still-live, dispatchable end-agent system-prompt template** (V1 `reviewer-end-agent`, referencing `clockwork_task_update`) alongside a V2 template already updated to `torque_task_update`, both live in the same task store as current nanite-targeting task records [04: `708c3214`].
19. **A single boot's session identity is tracked under three different, mutually inconsistent ID systems simultaneously** — the temp-dir folder UUID, the Claude Code `sessionId`, and a third key minted by `session-start-key.sh` — with one run's own closing message reporting yet a different form of the ID than either system uses elsewhere [05].
20. **Two launch mechanisms (`nanite-boot-claude` "boot-profile CLI harness" vs. `torque-boot-agentlaunch`) provision fundamentally different environments** with no visible taxonomy distinguishing which an operator/system should reach for — one loads the operator's full personal skill/agent catalog into a `Can Execute: false` sandboxed default profile and lacks the `tokf` binary its own hooks expect; the other provisions a git-worktree-per-run environment where `tokf` is present and actively used [05].
21. **Ticket-ID handoff mismatches** — a session's boot prompt naming a stale or simply wrong Torque ticket ID, twice, both self-corrected within-session (once by the agent re-searching, once by direct user correction) [01: `db8bc367`, `6707751e`].
22. **File-based vs. DB-backed vs. durable agent-profile identity** — raised as an open, hard-to-locate question in both [02] (multi-surface config sprawl requiring synchronized manual edits) and [03] (whether Curator/Weaver "existed" required checking git, not Torque or the DB) — given a definitive, evidence-based answer only by [06]'s direct DB query: every `agent_profiles` row is provably a DB-side mirror of a file (embedded-in-binary or project-tree `.md`/`.yaml`), never an independently DB-authored entity, rebuilt wholesale on service restart.

---

## 4. Severity/scope summary

Purely factual classification based on recurrence and latest-available evidence — not a judgment about priority or what to do next.

| Pattern | Reports | Distinct instances | Status per evidence |
|---|---|---|---|
| A — Silent fail-open | 01,02,03,05,06 (+ latent in 04) | 25+ | Systemic/architectural — still present in the latest DB snapshot |
| B — Tool-name/declaration drift | 02,03,06 | ≥5 across 3 profiles | Recurring, still present |
| C — Subagent completion not surfacing / fabrication | 01,02,03,04,06 | 3 dated incidents (May 12, May 20, Aug 14–15) + ongoing DB failure rate | Systemic, still present |
| D — CLI-boot vs. API routing | 01,02,05,06 | 3 distinct bugs | Recurring, still present |
| E — Retired model ID hardcoded | 02,06 | 1 fix + confirmed post-fix recurrence | Recurring, still present |
| F — Isolated plumbing / built-not-wired | 01,02,06 | 4+ tickets in one window; 0 successful executions ever recorded | Individual gaps fixed one-off; underlying authoring pattern and end-to-end result still unproven |
| G — Naming-collision taxonomy confusion | 01,02,03,04,05 | 22 instances (§3) | Mixed — several resolved same-day, others open/contested as of the transcripts |
| H — Config source-of-truth sprawl | 02,03,04,06 | 4+ independent layers | Architectural — confirmed structural by DB evidence |
| I — Copilot catching bugs the primary agent missed | 01,02,03 | ≥13 instances across 2 days | Recurring, still present, no visible decline |
| J — Stale-doc hallucination / self-report reliability | 02,03,05,06 | 4 incidents, 1 corroborated same-day from 2 angles | Recurring, still present, evidence-mixed (sometimes accurate, sometimes confidently wrong) |
| K — Reaper/lifecycle stranding work | 02,06 | 2 incidents, each ~3 months unresolved | Recurring, still present |
| L — subagent_runs migration crash-loop | 02,03,06 | 1 incident, 3-way corroborated | One-off, fixed same day (follow-up audit filed for the anti-pattern elsewhere) |
