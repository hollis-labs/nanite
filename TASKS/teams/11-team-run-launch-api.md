# TeamRun launch surface — launch by name, with invocation-time overrides

**Phase:** 4 — Definition & launch API surface (`TASKS/teams`)
**Status:** not-started
**Depends on:** `08`, `10`
**Touches:** `internal/api/teams.go` or a new `internal/api/team_runs.go` — exact file is this task's own research call, see below.

## Context

`docs/engineering/architecture/15-teams.md`'s "Runtime overrides follow the existing cascade" section states the requirement directly: *"A caller (Loom, or any Nanite consumer) should be able to launch against a saved Team by name with invocation-time overrides (`engineers.max: 3`) without creating a new persistent Team definition per call, matching how `WorkflowLaunchRequest.Params` already works today."*

**This planning session's research did not conclusively establish how the existing `WorkflowLauncher`/`WorkflowLaunchRequest` mechanism is actually invoked end-to-end today** (a REST route, a self-tool, an MCP-only surface for external consumers like Loom, or some combination) — confirmed real and reusable is the Go-level `WorkflowLaunchRequest` struct and `WorkflowLauncher` itself (`internal/service/workflow_launch.go`), but not the calling convention above it. **Before building anything, grep for the real existing entry point(s)** (`WorkflowLaunchRequest{` construction sites, any `/api/workflows`-shaped route, any self-tool that constructs one) and match whatever precedent already exists — do not invent a new pattern if one already exists for launching a plain (non-Team) workflow. If genuinely nothing external-facing exists yet for launching a workflow directly, treat that as a real finding worth flagging rather than silently working around it, and build the most consistent-with-this-codebase surface (a REST route, following `10`'s CRUD-endpoint conventions, is the safe default absent contrary evidence).

**Required prerequisite, found by task `08`'s own fresh reviewer (2026-08-20), not optional polish — read before building anything:** task `08`'s `internal/agentworkflow/registry.go`'s `Registry.Register` has no corresponding `Unregister`/eviction. `LaunchTeamRun` registers a permanently-unique-named compiled `WorkflowDefinition` (`team-run:<team-name>:<ulid>`) on every call and never removes it. This is not just an in-memory leak: `cmd/nanite/main.go` wires the *same* shared `*Registry` instance into `AgentCardGenerator`, whose `Generate()` (`internal/service/a2a_agent_card.go`) iterates `registry.Names()` and emits one `a2a.Skill` per entry into the **public** `/.well-known/agent-card.json` A2A discovery response. The moment this task wires `LaunchTeamRun` into real, callable traffic, **every single TeamRun launch — not eventually, from the first one — permanently pollutes an externally-visible discovery API with a stale one-off skill entry**, in addition to the unbounded memory growth. This task must not ship without addressing it: either (a) an eviction/TTL mechanism on `Registry` so a completed/stale TeamRun's compiled definition is removed after some bound, or (b) excluding per-launch-compiled TeamRun definitions from `AgentCardGenerator`'s skill enumeration (e.g. a naming-convention or explicit-flag filter). Pick one, implement it, document your reasoning — this is now a required "Done means" item for this task, not a follow-up to defer further.

## What to do

1. Confirm the real existing workflow-launch entry-point pattern (see above) and document what you found.
2. Build a launch surface — `POST /api/teams/{id}/launch` is the illustrative shape if no stronger existing precedent overrides it — accepting invocation-time overrides (a `TeamRunOverrides`-shaped body, per task `08`'s own type: per-slot `min`/`max` overrides, `WorkflowLaunchRequest.Params`-equivalent passthrough), calling task `08`'s `LaunchTeamRun`.
3. Response should surface enough to let a caller track the launched run — at minimum the resulting `workflow_runs.id`.
4. Resolve the registry-growth/agent-card-pollution requirement above.

## Done means

- An end-to-end launch through the real, chosen surface (not by calling `LaunchTeamRun` directly in a test) reaches a real `workflow_runs` row and correct `team_run_members` rows — the same outcome task `08`'s own done-means test verifies, but exercised through this task's API/self-tool boundary.
- The existing-entry-point research finding (step 1) is documented in the Work Log, whichever way it resolved.
- The registry-growth/agent-card-pollution requirement above is resolved (eviction/TTL or exclusion from skill enumeration), implemented and tested — not merely documented as a known issue.
- `go build`/`vet`/`test` clean.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
