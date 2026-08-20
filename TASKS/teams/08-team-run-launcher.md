# TeamRun launcher — slot resolution, `resolution: durable`'s corrected meaning, and the real launch call

**Phase:** 2 — Runtime engine (`TASKS/teams`)
**Status:** not-started
**Depends on:** `07` (needs the compiler), `04` (`may_spawn` enforcement at resolution time)
**Touches:** `internal/service/team_run_launcher.go` (new).

## Context

**A real, load-bearing correction to the design doc — read before writing any resolution logic.** `docs/engineering/architecture/15-teams.md`'s illustrative slot config states: *"`resolution: durable | fresh` and `activation_mode: singleton | fresh-per-wake | concurrent` are not new vocabulary — they're `agents.durable`/`agents.activation_mode` as they exist today, referenced by a slot rather than redefined by one."*

This is **half right**. Confirmed directly against the real code (this planning session's research): `agent_profiles.activation_mode` (migration `074`, the file's own internal header comment mislabels itself `073` — a pre-existing, harmless drift, not something to fix here) genuinely has exactly the three claimed values — `singleton | fresh-per-wake | concurrent` — enforced in Go (`validateAgentMultiAgentFields`, `internal/store/agents.go:220-227`), not a DB CHECK. **This half of the claim holds.**

`agent_profiles.durable` (migration `073_agent_profiles_durable.sql`) is real, but its actual documented purpose is unrelated to what a Team slot's "durable" resolution needs: it **exempts a profile row from migration `061`'s reingest/eject predicate** (`source != 'internal' AND durable = 0`) — a data-retention/survival flag on a static profile record, not a runtime signal about whether an active `durable_agent_instances` row exists to wake. Treating `resolution: durable` as "read `agent_profiles.durable`" would be a category error: a slot needs to know *whether to call `DurableAgentService.Start`/`Resume` against a named instance versus construct a brand-new session via the ordinary cascade* — an operational choice about **this launch**, not a static property of the referenced profile.

**Concretely, this task must treat `resolution: durable|fresh` as new Team-Slot-level launch-time configuration** (task `01`'s `TeamSlotDefinition.Resolution` field), not a literal read of `agent_profiles.durable`. `DurableAgentService` (`internal/service/durable_agents.go:92-109`) is the real mechanism to call for a `durable` slot — no single "wake" method exists; the real methods are `Start(ctx, id, req DurableAgentStartRequest)` and `Resume(ctx, id, req DurableAgentStartRequest)` (confirm which applies given the target instance's current `status`), plus `RequestStart`/`RequestStop`/`RequestPause`/`RequestResume` for state-transition requests. Real confirmed callers of this same service today: `WorkflowLauncher` (constructor dependency, `internal/service/workflow_launch.go:86,94`) and A2A's `TaskManager.CancelTask` (`internal/service/a2a_task_manager.go:378-420`, `TargetKind == "instance"`) — exactly matching the design doc's claim that both already call it. `DurableAgentStore.EnsureSessionAgent(sessionID, agentID, mode string, isPrimary bool) error` is the real function that would back a resolved slot's `(agent_id, session_id)` tuple binding into `session_agents`-adjacent state.

For a `fresh` slot: construct via the ordinary Agent Construction cascade — `internal/service/role_cascade.go`'s `RoleOverrideConfig`/`AgentOverrideConfig`, merged into `internal/agent/override.OverrideConfig` (the real role → agent → task closest-wins cascade, `docs/engineering/architecture/01-agent-construction.md`).

**`min`/`max`/elastic resolution — deliberately narrowed for this batch, with a concrete, current reason, not just caution.** The design doc leaves open *"Whether `min`/`max`/elastic slot resolution... needs a Team-level self-tool, or reuses `nanite_execute_task`'s existing dispatch surface with a slot-name argument."* Confirmed: that self-tool's real name is `task_execute` (not `nanite_execute_task`; `internal/selftools/self_tools.go:1174`, dispatched via `internal/selftools/self_tools_dispatch.go:54`), and it is **hard-capped at recursion depth 0** — only a root/non-subagent session may call it. An orchestrator Team Slot is itself very often a dispatched (non-root) session, so the design doc's own speculated reuse path is **currently blocked** for exactly the caller who would need it to grow a slot mid-run. **This batch resolves `min` members eagerly at launch (or an invocation-time override count up to `max`, via the existing runtime-override cascade) and does not build mid-run elastic growth** — a deliberate v1 scope cut, not an oversight; a future batch revisits this once either `task_execute`'s recursion cap changes or a dedicated Team-level self-tool is built.

**`agent_id` slot-config-to-instance mapping**: the design doc's YAML example (`agent_id: nanite-architect`) reads like a `durable_agent_instances.slug`, not a raw `agent_profiles.id` — confirm and document which this task resolves against.

**Launch call**: `WorkflowLauncher`/`WorkflowLaunchRequest` (`internal/service/workflow_launch.go`) — real struct: `{WorkflowName string; Params map[string]any; ProjectID string; AgentProfileID string /* required — durable_agent_instances.profile_id is NOT NULL FK */; ParentSessionID string; TimeoutSeconds int}`. Its own doc comment (`workflow_launch.go:63-79`) confirms the design doc's "TeamRun IS a WorkflowRun" claim exactly: *"a workflow run IS a template-class durable agent, reusing durable_agents.go's instance/event-log machinery... No dedicated FK links a `workflow_runs` row back to the durable_agent instance — the link lives in the instance's `metadata_json` (`workflow_run_id`), stamped after `Run()` returns."* **This link is metadata-JSON-based, not a foreign key** — there is no existing FK pattern for this task to piggyback on for linking `team_run_members` rows to the eventual `workflow_runs.id`. Since a `team_run_members` row (task `02`) requires a non-null `workflow_run_id`, and the run doesn't exist until `WorkflowLauncher` actually runs, **document your resolved ordering explicitly** — e.g. resolve slots into a provisional/local structure first, call the launcher, then backfill `team_run_members` rows once the real `workflow_runs.id` is known, rather than assuming an ID is available up front.

## What to do

1. `func LaunchTeamRun(ctx context.Context, teamID string, overrides TeamRunOverrides) (*agentworkflow.WorkflowResult, error)` in `internal/service/team_run_launcher.go`.

2. Load the Team (task `01`), resolve each `TeamSlotDefinition`:
   - `required: false` slots (the design doc's "architect, normally dormant" example) resolve **lazily** — not at launch, but the first time a flex step's `active_slots` (task `06`) actually names them. Document precisely how this lazy trigger is implemented (a callback the flex executor invokes, a check-and-resolve-on-read at flex-step entry, etc.).
   - `durable` slots: call `DurableAgentService.Start`/`Resume` per the corrected semantics above (not a literal `agent_profiles.durable` read).
   - `fresh` slots: construct via the Agent Construction cascade.
   - `concurrent`-activation-mode, `min`/`max` slots: resolve `min` members eagerly (or an invocation-time override count between `min` and `max`, via `overrides`) — **no mid-run elastic growth in this batch**, per the concrete `task_execute` recursion-cap constraint above.

3. Insert one `team_run_members` row (task `02`) per resolved member, once the real `workflow_run_id` is known (see the ordering note above).

4. **`may_spawn` enforcement**: before resolving any slot beyond a Team's `required: true` baseline (i.e., an elastic-but-still-launch-time resolution triggered implicitly by `min`/`max` configuration, if such a case exists at launch), check task `04`'s `AuthorizedForVerb` at this actual resolution call site — not merely documented as a future integration point.

5. Call task `07`'s compiler with the resolved phase sequence, then call `WorkflowLauncher`'s real launch method (confirm its exact name/signature) with `WorkflowLaunchRequest.Params` populated from the Team's own stored defaults merged with this call's `overrides` — the same closest-wins cascade shape already standardized elsewhere in this codebase (role → agent → task; Team defaults → saved config → invocation overrides).

## Done means

- An end-to-end test launching the SME example Team (against real or fixture-seeded fresh/durable-capable test agents — never against a real tracked `.nanite/agents/*.md` file or a real production `durable_agent_instances` row, per `EXECUTION-PROCESS.md`'s live-verification-write discipline) produces a real `workflow_runs` row, correct `team_run_members` rows for every resolved slot, and reaches the first flex step in a waiting state.
- A test covering `min`-eager multi-member resolution for a `concurrent` slot.
- The corrected `resolution: durable` semantics (and the `agent_id`-to-instance mapping you resolved) are documented explicitly in the Work Log — this is the task's central de-risking finding and must not be buried in code comments alone.
- `may_spawn` enforcement is exercised by a test (an unauthorized elastic resolution attempt is rejected).
- `go build`/`vet`/`test` clean.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
