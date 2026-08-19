# Run the CLI-vs-API experiment for durable agents (Curator wake)

**Phase:** 8
**Status:** not-started

## ⚠️ REQUIRES EXPLICIT OPERATOR SIGN-OFF AT DISPATCH TIME — SEPARATE FROM PLAN APPROVAL

**This task routes a real Curator wake — a live production action against a real external system (Loom's Curator agent, `consumer_id`-tagged, currently wired to `internal/api/loom_curator_wake.go`) — through a CLI-based subprocess instead of its current API path. Approving this plan (or the Phase 6 index) does NOT authorize dispatching this task. Do not dispatch this task under a batch "the plan looks good, proceed" — the operator must give a live, in-the-moment go-ahead immediately before this specific task is dispatched, every time it's dispatched. If a worker or the Orchestrator is ever tempted to fold this into a routine batch-execution pass, stop and get that explicit go-ahead first.**

**Depends on:** New Phase 2 in full (`TASKS/phase-2/`) and New Phase 3's `01-collapse-resolveprovider-into-cascade.md` — specifically `runtime_kind` actually wired as the real CLI/API routing mechanism. See Context: the experiment as described in TASKS.md ("made cheap by Phase 2's `runtime_kind` field") is not cheaply runnable before that lands; the pre-Phase-2 alternative (editing `loom-curator.yaml`'s `provider:` string directly) is a materially different, riskier action than the one this task is scoped to perform.
**Touches:** `.nanite/durable-agents/loom-curator.yaml` (or `atlas-curator.yaml` — pick one, see below), `internal/api/loom_curator_wake.go`, `internal/service/durable_wake.go` (`Wake`), `internal/service/durable_agents.go` (`durableAgentLaunchPolicyFor`, `selectOrCreateLaunchSession`), the Recovery Broker's telemetry (`nanite_recovery_breadcrumbs`) and `durable_agent_instances.status`.

## Context

TASKS.md Phase 6 item 1: *"Run the CLI-vs-API experiment for durable agents (made cheap by Phase 2's `runtime_kind` field) — route one real Curator wake through a CLI-based subprocess, measure whether anything in Nanite's retry/escalation policy is lost and whether session dormancy behaves sanely. This is the one genuinely unresolved architecture question from the whole review."* Architecture doc `00-overview.md`: *"Whether durable agents should eventually run CLI-based instead of API-based. The current two-substrate split... is the working default, never actually tested empirically. The `runtime_kind` typed field... is designed to make this experiment cheap to run, not to pre-decide the answer."*

### What Curator actually is, and why this is a live-system action

`internal/api/loom_curator_wake.go` is the real, ~30-line payload adapter Fragments Engine's callback destination POSTs to (`{generator, fragment}` shape), decoded and projected into a real user-turn prompt (`buildLoomCuratorWakePrompt`) that instructs Curator to run `classify_and_compile_fragment`, dispatched via `a.Services.DurableWake.Wake(ctx, inst.ID, ...)`. This is not a test harness or a simulated call — it is the live integration point an external, sibling app (Loom) depends on. A failed or degraded experiment here has a real chance of affecting Loom's actual fragment-classification pipeline, not just Nanite's own state.

### Current state — verified: neither Curator config has ever run CLI

Both `.nanite/durable-agents/loom-curator.yaml` and `atlas-curator.yaml` currently set `runtime_kind: api`, `provider: anthropic`. Neither has ever been routed CLI. **Critically, `runtime_kind` does not yet drive routing at all** — `durableAgentLaunchPolicyFor` (`durable_agents.go:679`) only reads it to validate `IsManagedAutomation`; the actual CLI-vs-API decision today flows entirely through `session.Provider` (a plain string copied from `inst.Provider` at `selectOrCreateLaunchSession`, `durable_agents.go:742`) into `chat.IsCLIProvider`/`NormalizeCLIProvider`. **This is exactly the mechanism Phase 2 replaces.** Before Phase 2 lands, the only way to test CLI-based Curator would be hand-editing `provider:` to a CLI-shaped alias (e.g. `pty-claude`) — a materially different, less-representative action than testing the real `runtime_kind`-driven mechanism this experiment is meant to validate. **Do not attempt the pre-Phase-2 workaround as a substitute for this task** — wait for Phase 2.

### What "retry/escalation policy" concretely means, and a real asymmetry this experiment will surface

The Recovery Broker's retry path (`DispatchRetry`, `internal/runtime/agent/recovery/broker.go:327`, `MaxBrokerRetries=3`, 10s remediation timeout) is CLI/bootdir-only by construction. `internal/service/chat_http_broker_notify.go:163` explicitly **skips notifying the broker at all** for any provider without a bootdir layout — meaning **Curator today, being API-based, gets zero Recovery Broker retry coverage**: no classification, no breadcrumb, no automatic replacement session on failure (Phase 0 item 4, `04-build-http-provider-retry`, is the tracked fix for this gap, independent of this experiment). This means the experiment's finding isn't purely "does CLI mode lose anything API has" — under a CLI-based run, Curator would for the first time **gain** the Recovery Broker's retry coverage it currently lacks entirely. Measure and report both directions explicitly: what CLI gains (real broker retry/breadcrumb coverage) and what changes about failure semantics (a fresh `agent.Boot` cold-start on each retry, vs. whatever Phase 0 #4's not-yet-built HTTP retry path does for the API-based baseline).

### Session dormancy — a real, pre-existing, CLI/API-independent gap this experiment will also surface, not cause

Don't confuse this with `sessions.status`'s dead CHECK values (Phase 0 #25) — that's a different table. `durable_agent_instances.status` is a separate, real enum where **nothing in the codebase ever transitions an instance back to `sleeping` after a wake completes** — confirmed by a repo-wide grep and by the code's own doc comment (`durable_wake.go:307-324`): *"nothing anywhere in this codebase ever transitions a `durable_agent_instances` row back out of 'active' once `Start()` sets it."* Curator's `lifecycle_class: process` maps to fresh-per-wake session policy (each wake creates an independent new session, not a long-held one) — so a CLI-based test cold-boots a fresh `agent.Boot` subprocess per wake, not a subprocess held alive between wakes for months. Expect this experiment to observe the *same* "stuck active forever" dormancy gap in both CLI and API modes — that's a pre-existing bug, not something this experiment causes or is responsible for fixing (note it, don't scope-creep into fixing it here unless it's trivially the same change).

## What to do

1. Confirm Phase 2 has landed (`runtime_kind` is real, driving routing) before starting anything else in this task.
2. **Get explicit, live operator sign-off immediately before dispatch** — re-read the banner above. Confirm with the operator: which Curator config (`loom-curator` vs. `atlas-curator` — prefer whichever has lower real-world consequence if a wake misfires; the operator should make this call), and what would constitute stopping the experiment early (e.g., a malformed classification result reaching Loom's real pipeline).
3. Flip `runtime_kind: cli` for exactly the one chosen agent's config — no other durable agent is touched.
4. Route one real wake through it (either wait for a real cron/callback trigger, or manually invoke the same path the operator approves for a controlled test).
5. Measure and record: (a) task correctness — did `classify_and_compile_fragment` complete correctly, same verification standard as the existing API-path check; (b) retry/escalation — compare `nanite_recovery_breadcrumbs` rows for a forced-failure wake under CLI vs. the API baseline (expect CLI to show broker coverage the API path currently lacks — report this explicitly, it's the real empirical finding, not a symmetric "did we lose anything"); (c) dormancy — confirm `durable_agent_instances.status` after a completed wake in both modes (expect: stuck `active` in both — a pre-existing gap, not new); (d) CLI-specific concerns — bootdir cleanup after the fresh-per-wake `agent.Boot`, any orphaned subprocess if `agent.Boot` fails mid-launch (covered generically by the Orphan Sweep reaper, not durable-agent-specific — confirm it actually catches this case if tested).
6. Revert `runtime_kind` back to `api` once the experiment concludes, unless the operator explicitly decides to keep it CLI going forward — don't leave a live production agent silently switched as a side effect of an experiment.
7. Write up the findings (in this file's Work Log) as the actual resolution to architecture doc `00-overview.md`'s "genuinely still open" question — this is empirical, not a document-analysis exercise.

## Done means

- Explicit, live operator sign-off was obtained immediately before dispatch (recorded in this file's Work Log — who approved, when, what scope was agreed).
- One real Curator wake completed via the CLI-based path, with the four measurement dimensions above recorded with real data, not inference.
- `runtime_kind` is reverted to `api` unless the operator explicitly decided otherwise (recorded either way).
- The genuinely-unresolved architecture question ("should durable agents eventually run CLI-based instead of API-based") has a real, evidence-based answer recorded in this file's Work Log, or a clear statement of what remains inconclusive and why.
- No Loom-visible production impact from a failed/degraded experiment run, or if one occurred, it's disclosed explicitly in the Work Log, not glossed over.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated. Record the operator sign-off explicitly here before any dispatch action.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
