# Verify the subagent reaper's real-world behavior before further idle-timeout tuning

**Phase:** 4
**Status:** not-started
**Depends on:** none
**Touches:** `internal/subagent/reaper.go` (`Reaper.SweepOnce`, the activity-reset inactivity branch), `internal/subagent/service.go` (`stampActivity`/`emitHeartbeat`, `startHeartbeat`), `internal/store/migrations/093_subagent_run_last_activity.sql`-added `subagent_runs.last_activity_at` column, `internal/service/container.go` (`NewBootRunner` wiring, `SetStreamSink`).

**Scoping note, read first**: "the reaper" in `TASKS.md`'s Phase 4 text means specifically `internal/subagent.Reaper` (`internal/subagent/reaper.go`) — the periodic 30s-tick sweep over `subagent_runs` that `d92d8cf` (CW-20260816-0004) added activity-reset behavior to. It does **not** mean `chat.AgentConstraints.IdleTimeoutSeconds` (the in-process per-turn wall-clock timeout in `internal/chat/engine.go:43`/`internal/service/chat_loop_state.go`) — that mechanism is untouched by Phase 4, already correctly scoped as a hard stopper that stays per architecture doc `04-harness.md`. Do not conflate the two "idle timeout" concepts; they are genuinely different mechanisms with genuinely different owners.

## Context

TASKS.md Phase 4: *"Verify the reaper's real-world behavior before further idle-timeout tuning."* Architecture doc `04-harness.md`: *"Idle-timeout/reaper aggressiveness is a real, still-open reliability question, not resolved by the current activity-reset fix. Real operator experience: more false reaps historically than genuine idle/runaway catches. The shipped fix... should help but needs verification against real behavior before being trusted as fully resolved."*

### The scoping conflict, and how this task resolves it — see also `TASKS/ESCALATIONS.md`

A planning-time landmine flagged this precisely: *"needs live traffic to observe, but no agents run in production during this whole effort."* `TASKS.md`'s own Phase 0 sequencing note and `EXECUTION-PROCESS.md`'s non-goals both state no live agent traffic runs during this whole execution effort — so "verify... real-world behavior" cannot literally mean "watch new production traffic," because none is expected to exist in the execution window. **This task is scoped around a concrete resolution, logged as a new entry in `TASKS/ESCALATIONS.md`: historical-log analysis against already-existing data, not live observation, is the primary verification method** — with full-confidence "false reap rate under real production load" validation explicitly deferred to whenever live traffic resumes (post-Phase-6, not blocking this phase).

### The reaper mechanism, verified against real code

`Reaper.SweepOnce` (`internal/subagent/reaper.go:341-421`) runs three branches per sweep, `WHERE status = 'running'`, severity-ordered:
1. **Hard ceiling** (`:354-368`) — `started_at + hardCeiling(12h) < now` → `status='failed'`, `error=ReasonTimeoutReaper`.
2. **Inactivity** (`:375-392`) — `COALESCE(NULLIF(last_activity_at,''), started_at) + timeout_seconds < now` → `status='stalled'`, `error=ReasonInactivityReaper`.
3. **Orphan** (`:404-416`) — empty `child_session_id`, `created_at` older than 60s grace → `status='failed'`, `error=ReasonOrphanReaper`.

`d92d8cf` added `last_activity_at` (migration `093_subagent_run_last_activity.sql`) specifically so branch 2 resets its clock on real activity instead of always measuring from `started_at` — the exact "activity-reset" fix the architecture doc references. Production defaults (`internal/service/container.go:1187,1207`, unconfigured `ReaperOptions{}`/`RuntimeReaperOptions{}`): 30s sweep interval, 60s orphan grace, 12h hard ceiling, 1800s inactivity threshold (per-row `timeout_seconds`, tunable via `NANITE_SUBAGENT_DEFAULT_TIMEOUT_SECONDS`, unset in production).

### Correction (2026-08-18, post-planning verification) — this is NOT a confirmed bug, don't treat it as one

An earlier draft of this task asserted a confirmed bug here, bucketing rows by `d92d8cf`'s git-commit date (2026-08-15 23:06). That's the wrong cutover — independent verification found the actual deploy+reload cutover was **2026-08-17T18:51:49Z** (`~/.cerberus/apps/nanite/nanite-api-service/install-manifest.json`'s `synced_at`; confirmed `d92d8cf` is included via `git merge-base --is-ancestor`). Rebucketing the same 174 rows by the *real* cutover: **172 have a non-empty `started_at`, and every single one is before the deploy** (latest: `2026-08-17T16:00:07Z`, ~2h51m pre-deploy). The `eb870141-...` example this section previously cited as "proof the fix failed" is itself one of those pre-deploy rows — it ran on the binary that predates the fix entirely, which never had the write code in the first place.

**Net finding: zero `subagent_runs` have started since the fix actually went live. There is no data, in either direction, on whether `last_activity_at` writes correctly.** The write path itself (`emitHeartbeat`→`stampActivity`, a straightforward `UPDATE ... WHERE id = ? AND status = 'running'`, unconditionally reachable in production per the wiring traced below) looks structurally sound on inspection — nothing found broken by reading the code. Treat this as **unverified, not confirmed-broken.** The real local run (now step 2 below) is the only way to actually answer the question — do it first, not last; everything else in this task is secondary to that one data point.

Wiring traced so far (not fully root-caused — this task's real job is finishing that): `emitHeartbeat`/`stampActivity` (`internal/subagent/service.go:498-550`) is a no-op only when `svc.streamSink == nil` (`:563`), but production unconditionally calls `SetStreamSink` (`container.go:1142`) — so that specific documented escape hatch does not explain the gap. `stampActivity`'s `UPDATE ... WHERE id = ? AND status = 'running'` guard should match, since `status='running'` is written before `execute()`/`startHeartbeat()` runs (`service.go:1097,1113-1119`). `startHeartbeat` wraps any `Runner` generically (`service.go:1217`) — production actually wires `NewBootRunner(...)` with `ChatRunner` only as its fallback (`container.go:1136-1137`; correct any doc/assumption that treats `ChatRunner` as the live production runner).

**A secondary, lower-confidence observation worth checking early**: one 2026-08-16 row was reaped via the hard-ceiling branch after only ~30 minutes, not 12h — plausibly explained by the fix landing in git before the running `nanite-api-service` binary was actually rebuilt and `cerberus_resource_reload`'d (see this project's `CLAUDE.md` on the build-vs-deploy distinction — editing code doesn't affect the running service until an explicit deploy+reload cutover). If real, any historical analysis must bucket by actual deploy/reload time, not git-commit time — verify the deploy timeline before trusting timestamps close to the `d92d8cf` landing date.

## What to do

1. Independently reconfirm the real deploy+reload cutover (`install-manifest.json`'s `synced_at`, cross-checked against `git merge-base --is-ancestor d92d8cf <deployed-head>`) — it may have moved again since this task was written; don't just trust the timestamp cited above without a fresh check.
2. **Do this before anything else — it's the only real data point available.** Spawn a real local subagent run with a task that runs long enough to cross several 30s heartbeat intervals. Confirm directly, by querying the dev DB during the run, whether `last_activity_at` updates. This is genuine forward observation, available without needing production traffic — it does not depend on historical data at all.
3. **If step 2 shows `last_activity_at` failing to update:** root-cause why `stampActivity`'s write isn't landing (or isn't being read back by `SweepOnce`'s `COALESCE`) — trace the heartbeat ticker's write path end to end. Fix it, then re-verify with another local run showing the column updating live and the inactivity branch's clock genuinely resetting on activity.
4. **If step 2 shows `last_activity_at` updating correctly:** the premise was unconfirmed, not broken. Don't force a fix for something you can't reproduce — record this plainly in the Work Log, including that the earlier "confirmed bug" framing in this file's history was itself a bucketing error (see the Correction section above), and close this task without a code change to the write path.
5. Either way, separately: query the real backed-up production database (copy it first, don't mutate the original) for every `subagent_runs` row with a reaper-attributable `error` (`ReasonTimeoutReaper`/`ReasonInactivityReaper`/`ReasonOrphanReaper`), bucketed by the confirmed real deploy/reload cutover. Cross-reference `attempts_json`/`retry_count` (did a retry of the same work later succeed, suggesting the original reap was premature?) and `result_json` (partial-work evidence at time of reap) to characterize the historical false-reap rate as best the data allows. This dataset is entirely pre-deploy — treat it as historical context on past behavior, not as evidence about whether the current fix works.
6. Do not tune `timeout_seconds`/`hardCeiling`/`orphanGrace` values as part of this task regardless of outcome — the item is explicitly "verify... before further idle-timeout tuning." Any tuning recommendation goes in this file's Work Log as a finding for a later task, not an in-place change here.

## Done means

- Step 2's real local run gives a direct, recorded answer (in the Work Log) on whether `last_activity_at` updates correctly — this is the core deliverable, regardless of which way it comes out.
- If step 2 found a genuine failure: root-caused and fixed, verified by a re-run showing the column updating live, plus a new test exercising the fixed write path.
- If step 2 found it works correctly: recorded as such, no fix forced, no new test needed for a non-bug.
- Historical analysis of the full (pre-fix-only) `subagent_runs` dataset is recorded, characterizing the false-reap rate the operator's stated experience refers to, clearly labeled as historical context rather than evidence about the current fix.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass, including `internal/subagent/reaper_test.go`'s existing 14-test suite.
- No `timeout_seconds`/`hardCeiling`/`orphanGrace` values are changed as part of this task — any recommendation for a follow-up tuning task is recorded, not acted on here.
- `TASKS/ESCALATIONS.md`'s entry for this scoping conflict (added during planning) is updated with this task's actual resolution once done, so the record reflects what really happened, not just the plan.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
