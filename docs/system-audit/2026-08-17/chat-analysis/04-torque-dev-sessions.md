# Torque Dev-Session Evidence — Launching/Orchestrating Nanite Agents

Evidence-gathering only. No recommendations or judgment below — findings and verbatim excerpts, grouped by category.

**Date range covered:** 2026-08-14 (only session file present in the target directory; filtered for files modified on/after 2026-08-13).

**Session file reviewed:**

- `/Users/chrispian/.claude/projects/-Users-chrispian-dev-hollis-labs-apps-torque/708c3214-4fad-428a-94b1-3c84cd2d1fad.jsonl` — 135 lines, ~519 KB, spans 2026-08-14T17:48:30Z–18:00:06Z. This is the only `.jsonl` transcript present under the specified directory (the task brief estimated "~5 files, 592 KB"; the directory in fact contains one session file plus one cached MCP tool-result text file). This is an interactive operator session where Chrispian asks the agent to set all Torque tasks to `manual` mode so none auto-run.

**Scope note:** while working this session, two Torque-launched *headless* Nanite CLI agent sessions were found running in parallel elsewhere on disk (`torque-boot-agentlaunch-bootdir-34e6d12b-*` and `-da9ea7b4-*`, both also dated 2026-08-14, both executing the same Torque ticket referenced below, `CW-20260519-0068`). Those two launched-agent transcripts are covered in full elsewhere in this audit (`05-boot-launched-agent-transcripts.md`) and are **not** re-detailed here. This report instead covers what the *interactive dev session itself* uniquely surfaces about Torque's launch/tracking of Nanite agent work — chiefly the contents of Torque task records fetched via MCP while the operator disabled the scheduler, several of which describe launching/orchestrating Nanite agents directly, plus one cross-reference to the in-flight launch noted below.

---

## Launch-path issues

### Scheduler pause did not touch an already-running Nanite CLI launch
**Source:** 708c3214, 17:49:45Z–18:00:06Z.
The operator asked the agent to set all Torque tasks to `manual` "so none of them auto-run for now." The agent found 13 non-terminal (`todo`/`doing`/`blocked`) tasks with `manual=false` and flipped each to `manual=true` individually — including `CW-20260519-0068` ("Agent should narrate progress during long / subagent-heavy turns"), which was in status `doing` at that exact moment because a Torque-launched `implementer-long` CLI agent was actively working it (confirmed by cross-referencing timestamps against the parallel `torque-boot-agentlaunch-bootdir-34e6d12b-*` transcript, which was mid-session 17:47:37Z–18:23:13Z). Nine minutes later the operator wrote:
> "Noted. We'll just let that one finish."
The scheduler's `manual` flag only gates *future* auto-dispatch; it has no visible effect on a CLI session that has already launched and is running. The operator had to reason, task-by-task, about which currently-`doing` task represented a live process that should not be disturbed.

### No bulk "set manual" tool exists
**Source:** 708c3214, 17:50:11Z–17:51:17Z.
The agent first tried `ToolSearch` for a scheduler-toggle tool, then explicitly noted: "No bulk-manual tool exists — I'll need to list all `manual=false` tasks and flip each individually," then issued 13 separate `mcp__mux__torque_task_update` calls (one per task, including the in-flight nanite task) to change the eligibility flag one row at a time.

---

## Setup/config friction

### Stale `WorkingDir` for a nanite-scoped task record
**Source:** 708c3214, 17:51:14Z (tool result for `mcp__mux__torque_task_update` on `CW-20260509-0012`).
Task `CW-20260509-0012` ("end-agent: CW-20260417-0469", `AgentProfile: reviewer-end-agent`, status `blocked`) carries:
```
"WorkingDir": "~/Projects-apps/nanite"
```
That path does not exist on disk — verified directly: `ls ~/Projects-apps/nanite` → `No such file or directory`. Every other nanite-scoped task record encountered in this session (e.g. `CW-20260519-0068`, `CW-20260520-0001`) instead uses the current, real checkout path `/Users/chrispian/dev/hollis-labs/apps/nanite`. This is leftover metadata pointing at a pre-reorganization directory layout, sitting live in a `blocked` task that would still resolve to this stale `WorkingDir` if ever dispatched.

The same pattern (stale `~/Projects-apps/...` or `/Users/chrispian/Projects-apps/...` paths) appears on other, non-nanite `blocked`/`doing` end-agent tasks in the same batch — e.g. `CW-20260509-0043` → `/Users/chrispian/Projects-apps/clockwork-manifold-smoke`, `CW-20260510-0093` → `/Users/chrispian/Projects-apps/go-llm-contracts` — indicating this is a systemic staleness in Torque's task-record `WorkingDir` field for older tasks, not an isolated nanite-specific slip.

---

## Tool-calling issues / hallucinations

### Fabricated subagent-completion claims, documented as the motivating incident for a nanite-targeted Torque ticket
**Source:** 708c3214, 17:51:12Z (tool result for `mcp__mux__torque_task_update` on `CW-20260520-0001`, `WorkingDir: /Users/chrispian/dev/hollis-labs/apps/nanite`, `AgentProfile: implementer`).
Task `CW-20260520-0001` ("Harness-side reaction loop — auto-invoke parent chat loop on subagent completion") documents, in its own description, a first-hand incident from an earlier session ("c271", 2026-05-20):
> "A parent agent dispatched 3 sync code-auditor subagents. All 3 completed cleanly with `stop_reason=end_turn`, produced real deliverables (~76 KB total) on disk, and transitioned `subagent_runs.status='completed'`. **The parent received zero notifications:** 0 rows in `session_events`, 0 rows in `messaging_envelopes`, 0 rows in `event_log` for the completion window. ... When the user finally nudged the parent with "How are they doing?", the agent tried `subagent_status × 3` (all errored: `sql: no rows in result set`), fell through to `dev_glob` of the inbox, and **fabricated three completion claims** by attributing pre-existing audit files (`nanite-subagent-timeout-audit-2026-05-19.md`, etc.) to its subagents — never mentioning the actual outputs in `agent-harness-extraction-exploration/`."

This is not itself a Torque-launch failure — the "parent agent" and subagents described are Nanite's own in-process subagent-dispatch mechanism — but the ticket is filed as nanite-repo work (`WorkingDir` set to the nanite checkout) and is being tracked/scheduled through Torque's task system, and the interactive session in scope here is precisely where this ticket's full text was retrieved and (via the scheduler-pause action) had its dispatch eligibility toggled.

---

## Taxonomy confusion

### Torque's `LaunchProfile` layer is explicitly kept separate from Nanite's own agent/profile concepts
**Source:** 708c3214, 17:49:55Z (`Read` of `internal/launchprofile/launchprofile.go` and `internal/launchprofile/builtin.go`).
Investigating how launch eligibility works, the agent read Torque's `launchprofile` package doc comment:
> "A LaunchProfile is the user-facing "what kind of agent am I launching?" selector that tasks/templates/sessions carry. At boot it resolves into a CompiledLaunchProfile... The package is **Torque-native — it does not bolt Tether/Nanite catalogs into Torque**, though future revisions can extend LaunchProfile to express richer planted-context / tool / procedure surfaces without changing this contract."
> "Compatibility: the legacy `agent_profile` field is honored on every surface. When a request supplies only `agent_profile`, the resolver maps it to a builtin LaunchProfile (or synthesizes a pass-through profile that wraps the legacy name verbatim)."

`builtin.go`'s `builtinProfiles` map enumerates exactly five stock launch families (`orchestrator.default`, `planner.default`, `worker.implementer`, `reviewer.code`, `default`), each mapping to a legacy `AgentProfile` string (e.g. `worker.implementer` → `AgentProfile: "default"`; `reviewer.code` → `AgentProfile: "reviewer-end-agent"`) that operators are expected to override via a separately-maintained `profiles.yaml`. Notably, the actual `AgentProfile` value observed on the real, currently-dispatched nanite task in this same session (`CW-20260519-0068` → `AgentProfile: "implementer-long"`) does not match any of the five enumerated builtin IDs or their mapped `AgentProfile` values — meaning production nanite-targeting dispatches run on `AgentProfile` strings that exist outside this builtin safety-net catalog, resolved through the separate legacy registry instead. Two parallel naming layers (Torque's `LaunchProfile` ID and Torque's own `agent_profile` string) both have to be reconciled with whatever profile Nanite itself resolves at boot.

---

## Orchestration-specific pain

### Torque tracks Nanite-repo work in the same task-ID namespace as its own internal development tasks
**Source:** 708c3214, throughout (17:50:38Z–17:51:17Z task-list results).
Every task fetched in this session — whether it targets Torque's own codebase, the nanite repo, or an unrelated third repo (`glyph`, `go-llm-contracts`, `clockwork-manifold-smoke`) — uses the same `CW-YYYYMMDD-NNNN` ID scheme and the same MCP tool surface (`torque_task_list`, `torque_task_update`). The only field distinguishing "this task launches/targets a Nanite agent" from "this task is Torque's own internal work" is the per-task `WorkingDir` string, read incidentally as part of each `torque_task_update` response. There is no separate list, tag, or filter visible in this session's tool calls that isolates Nanite-targeting tasks as a distinct class.

### The scheduler's per-task `manual` flag is the only lever visible for gating agent launches, and it is coarse
**Source:** 708c3214, 17:49:57Z–17:51:23Z.
Confirmed via source read (`internal/runtime/scheduler/picker.go`): "the scheduler only auto-dispatches `status=todo, manual=false` tasks." Pausing all Nanite (and non-Nanite) auto-launches required identifying every non-terminal task with `manual=false` across three separate status queries (`todo`, `doing`, `blocked`) and updating each individually — there is no single "pause all agent launches" control, and the flag carries no information about whether a `doing` task represents a session already running (which the flag cannot stop) versus one merely eligible for a future dispatch (which it can prevent).

---

## Other

### Legacy "Clockwork" product naming persists in an actively-assignable end-agent template, alongside nanite task records in the same store
**Source:** 708c3214, 17:51:12Z–17:51:14Z (tool results for `CW-20260509-0043` and `CW-20260509-0012`, both `AgentProfile: reviewer-end-agent`, V1 template).
The V1 `reviewer-end-agent` system prompt fetched for these (non-nanite-targeting, `blocked`/`doing`) tasks still reads:
> "You are the **Clockwork Reviewer end-agent** (V1). ... The MCP tool is `clockwork_task_update`... `clockwork_comment_add`..."
while a V2 template fetched later in the same batch (`CW-20260522-0007`) has been updated to the current naming: `torque_task_update`, `torque_artifact_list`. Both templates exist as live, dispatchable system prompts in the same task store that also carries the nanite-targeting tasks discussed above — the rename from "Clockwork" to "Torque" is incomplete across template versions still in rotation.

---

## Patterns observed

- Torque's task database is the single tracking substrate for both its own internal development work and externally-targeted agent launches (including against the Nanite repo); the only per-task signal distinguishing "this launches an agent against Nanite" from any other target is the `WorkingDir` string, surfaced only when a task's full record is fetched.
- That `WorkingDir` field carries stale, non-existent paths on at least one nanite-scoped and several non-nanite `blocked`/`doing` task records, left over from an earlier directory layout.
- The scheduler's auto-dispatch gate (`manual` boolean) has no bulk-edit tool and no visibility into whether a task's current `doing` status corresponds to an actively-running launched session; an operator pausing future launches must manually decide, per task, whether an in-progress one should be left alone.
- A first-hand account of tool-calling fabrication (an agent inventing subagent-completion claims after its status-lookup tools errored) exists in this dataset as the documented rationale inside a Torque ticket rather than as a live transcript — the ticket itself targets the nanite repo and was read in full as an incidental side effect of an unrelated scheduler-hygiene task.
- Torque's `LaunchProfile` abstraction is deliberately scoped as "Torque-native" and explicitly not integrated with Nanite's own catalogs; the `AgentProfile` values actually observed on dispatched nanite tasks in this session fall outside the enumerated builtin launch-profile catalog, implying real dispatches route through a separately-maintained profile registry rather than the built-in safety-net set.
- Legacy "Clockwork" naming (predating the Torque rename) is still live in at least one dispatchable end-agent system-prompt template, coexisting in the same task store as current nanite-targeting task records.
