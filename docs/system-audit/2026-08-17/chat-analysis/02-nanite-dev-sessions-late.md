# Nanite Dev-Session Evidence: Agent-System Incidents (Aug 15–17)

> **Correction (2026-08-17, post-review):** "PTY" references below describe the CLI-wrapped subprocess path loosely — no real pseudo-terminal is allocated in production. See `code-architecture/06-provider-llm-roundtrip.md`'s correction note for detail.

Evidence-gathering only. Not a code review or a set of recommendations. All incidents below are drawn from Claude Code development-session transcripts recorded while working *inside* the Nanite repo itself (`/Users/chrispian/dev/hollis-labs/apps/nanite`) — i.e., an AI coding agent (sometimes spawning its own Task-tool sub-agents, sometimes acting on tasks pulled from the "Torque" task tracker) actively building, fixing, and debugging Nanite's own agent/tool system.

**Date range covered:** 2026-08-15 00:00 through 2026-08-17 23:59 (local), corresponding to session timestamps 2026-08-15T02:43 UTC through 2026-08-17T22:55 UTC.

**Note on scope:** these transcripts are *development* sessions, not end-user chat sessions with Nanite. The "agents" whose behavior is under audit here are (a) Nanite's own durable/database-backed agent profiles (Orchestrator, Curator, Weaver, Project-Manager, etc.) as discussed, tested, or fixed within these sessions, and (b) Nanite's own routing/reflex/tool-broker machinery. Where a transcript shows the *developer's own* Claude Code session using its own Task-tool sub-agents or "fork" mechanism, that is explicitly excluded as out-of-scope (unrelated to Nanite's own agent system) unless the transcript itself demonstrates confusion between the two.

## Session files reviewed (14 files, excluding the live audit session itself)

| File | Approx. UTC span | Subject |
|---|---|---|
| `58371795-e70b-46a7-8455-34795263a25e.jsonl` | 2026-08-15 15:32–16:53 | Closes 7 Torque tickets from a tool-visibility investigation into a live Orchestrator session |
| `1d8e1d33-fe99-4d40-889f-c7ac49d3d99e.jsonl` | 2026-08-15 05:33–06:05 | Closes gaps in the Worker→Reviewer→Gate Agent Workflow |
| `ed715b3e-9cf3-44ce-97ae-339a4144d452.jsonl` | 2026-08-15 02:43–05:32 | Agent Roles design: project-manager/planner/orchestrator recipe + profile builds |
| `79184fcd-2b0a-411b-b996-b602bc0a5cb4.jsonl` | 2026-08-15 22:03–23:31 | Closes a subagent→parent-session result-surfacing gap (agent→subagent orchestration) |
| `45c81882-eaae-4f55-b939-aa2f8ffa8fae.jsonl` | 2026-08-15 18:45–19:40 | Fixes tool-result truncation blocking a live Orchestrator dogfood run |
| `8dab524e-456c-4bbc-b188-f2bfdf3206cd.jsonl` | 2026-08-15 16:54–18:44 | Removes MCP discovery-time tool-count truncation |
| `9726d652-a145-439a-aadb-f9e6bdfa9e94.jsonl` | 2026-08-15 20:28–21:06 | Closes 4 Torque tickets from a live dispatch-stack dogfood investigation |
| `07317d39-6349-4d5e-a46c-45609db63510.jsonl` | 2026-08-15 06:18–07:xx | Fixes Orchestrator durable-agent launch failure (`class: harness` not in allowed enum) |
| `2db6d53c-9122-42c3-9a05-7f83373e0576.jsonl` | 2026-08-16 21:38–23:27 | Builds "Conductor Console" epic; reflex/reflexes naming-collision ticket filed and resolved |
| `e32a8bac-290b-4778-9715-24afa3d5f256.jsonl` | 2026-08-16 19:35–21:18 | "Conductor Console" design session; surfaces taxonomy confusion |
| `18f8f448-987d-4f04-9b56-6a2fb807e518.jsonl` | 2026-08-16 00:19–04:35 | Fixes the subagent reaper's wall-clock-only kill logic |
| `03b12a97-34a9-467d-9c1e-439fb4498345.jsonl` | 2026-08-17 00:46–03:49 | Loom Curator/Weaver/Orchestrator build; reflex/driftguard naming-collision rename |
| `d16afefb-9405-46e1-ae9c-b26f59e2f658.jsonl` | 2026-08-17 15:46–18:06 | Loom Curator wake fix; discovers and fixes the `subagent_runs` migration crash-loop |
| `3af1aa49-d491-455e-a07a-a785cd766c65.jsonl` | 2026-08-17 18:09–22:55 | Full-day investigation into Curator/Weaver MCP tool-visibility failures |

*(Excluded per instructions: `31bceb29-6814-4de3-860e-9086db7de2a2.jsonl`, the live in-progress session running this audit.)*

---

## Setup / config friction

### Nanite's own shipped agent workflow was unreachable by default
**File:** `1d8e1d33-fe99-4d40-889f-c7ac49d3d99e.jsonl` · **~2026-08-15T05:33 UTC**

`WorkflowDefinitionsPath` is empty by default and nothing in the repo's own config set it, so the real, non-placeholder `worker-reviewer-gate.yaml` WorkflowDefinition existed in the repo but never loaded into the registry at startup — the Orchestrator could not dispatch through it without manual, undocumented config setup.

### The fix for the above itself introduced a hardcoded, developer-specific path
**File:** `1d8e1d33-fe99-4d40-889f-c7ac49d3d99e.jsonl` · **~2026-08-15T05:45 UTC**

While wiring config to fix the gap above, Claude Code set `workflow_definitions_path` in `nanite.yaml` to a hardcoded absolute path (`~/dev/hollis-labs/apps/nanite/...`) specific to one developer's machine — silently no-op-ing the same feature (empty/inert registry, no crash) for any other developer or CI. Caught by a GitHub Copilot PR review, not by Claude Code itself.

> "the hardcoded `~/dev/hollis-labs/...` path is user-specific and silently breaks the whole point of this change (nothing loads) for any other developer or CI."

### Agent role file had per-developer/per-fork hardcoded paths baked in
**File:** `ed715b3e-9cf3-44ce-97ae-339a4144d452.jsonl` · **~2026-08-15T04:05 UTC**

Generalizing `agridd-project-manager.md` into a reusable `project-manager.md` recipe required manually stripping several hardcoded references that would have gone stale: a working-title note, a hardcoded `hollis-labs/agridd` repo reference, `docs/durable-agents/` doc paths that don't exist in the Nanite repo, and a hardcoded `agridd-keeper` escalation target.

> "the source file has several 'agridd' references (working-title note, `hollis-labs/agridd` repo, `docs/durable-agents/` paths that don't exist in this repo, `agridd-keeper` escalation target) that would go stale once agridd retires."

### Adding a new inter-agent message kind required a manual schema migration
**File:** `79184fcd-2b0a-411b-b996-b602bc0a5cb4.jsonl` · **~2026-08-15T22:18 UTC**

`agent_messages.kind` is a CHECK-constrained enum (`request/reply/notification/handoff`) with no extensibility — adding the new `subagent_result` kind needed to close the orchestration gap (see "Other," below) required a hand-written migration to widen the constraint, even though the consumption side (inbox filtering, MCP `nanite_message_inbox`) already worked with zero code changes.

### Agent Mux MCP proxy silently crash-looped on every launchd startup
**File:** `3af1aa49-d491-455e-a07a-a785cd766c65.jsonl` · **~2026-08-17T18:23 UTC**

Nanite's only MCP path to `loom`/`fragments-engine` tools is a stdio proxy ("Agent Mux") that crash-looped on every single launchd startup, silently falling back to 4 builtin tool sources (85 tools, 0 from mux) for every agent — with nothing surfacing this as an error anywhere. Three stacked root causes were reproduced directly: (1) a missing `--catalog` flag made mux resolve a CWD-relative path that doesn't exist under launchd; (2) even once fixed, mux's subprocess had no `HOME` env var passed through Nanite's deny-by-default env allowlist, breaking tilde-expansion and hitting `mkdir /tether: read-only file system`; (3) even once fixed, mux eagerly validated the entire shared Tether catalog, and three unrelated launch profiles referenced agent files in the wrong directory, fatally blocking any mux startup.

> "nanite's logs show `mcp: failed to discover tools server=Agent Mux err=\"tools/list: read from stdout: EOF\"` on every discovery cycle, so nanite silently falls back to its 4 builtin tool sources (85 tools total, 0 from mux) for every agent, not just Curator/Weaver."

### Config source-of-truth sprawl across DB rows, `.md` files, and durable-agent YAML
**File:** `3af1aa49-d491-455e-a07a-a785cd766c65.jsonl` · **~2026-08-17T21:43–21:48 UTC**

Fixing Curator/Weaver's config required synchronized manual edits across at least three separate surfaces — live DB rows (`agent_profiles`/`agent_known_tools` via API), `.nanite/agents/*.md` source-of-truth files, and durable-agent YAML — with no single edit propagating to the others. Stale `agent_known_tools` DB rows were additive-only and had to be manually deleted since the API update path didn't touch them.

> "The write persisted through to `.nanite/agents/loom-curator.md` too, so it survives a restart." / "No other Ion mentions existed in Curator's config, Weaver's config, or either durable-agent YAML (checked all four files)."

### `roleTools:` frontmatter is dead config for 4 of 5 agent-role profiles
**File:** `58371795-e70b-46a7-8455-34795263a25e.jsonl` · **~2026-08-15T15:32–16:53 UTC**

`roleTools:` frontmatter, used by 4 of 5 Agent Roles profiles (Architect, Orchestrator, Project-Manager, Task-Planner) to declare each role's intended tool surface, has zero runtime effect — it only seeds a DB display table that nothing else reads. Only Reviewer's separate `tools:`/`toolPermissions.allow_list` mechanism actually gates tool selection. Two parallel "declare your tools" mechanisms existed side by side; one was silently inert.

> "A repo-wide grep found zero call sites reading `agent_known_tools`/`ListAgentKnownTools`/`AgentKnownTool` anywhere in `internal/toolclient/`, `internal/service/tool.go`, or `internal/chat/` — the only consumers are read-only REST handlers ... UI display only. It's write-only bookkeeping."

### `agent_schedules` seeding mechanism had never been built
**File:** `03b12a97-34a9-467d-9c1e-439fb4498345.jsonl` · **~2026-08-17T02:07–03:25 UTC**

While wiring Curator's scheduled lint+export tick, the session found "schedule-seeding infrastructure that didn't exist before" had to be built from scratch — durable agents that needed recurring schedules had no existing path to get them seeded.

### No single documented location for "the real runtime database"
**File:** `9726d652-a145-439a-aadb-f9e6bdfa9e94.jsonl` · **~2026-08-15T20:30–20:31 UTC**

While verifying a fix, the session had to guess three different SQLite DB file locations before finding the one the live service actually reads: `nanite.db` in cwd ("no such table"), `~/.nanite/nanite.db` ("no such table"), and finally `/Users/chrispian/.local/share/nanite/workspaces/default/main.db`, which worked — except the column it expected was named `default_model`, not `model` as assumed.

> `Exit code 1 / Error: in prepare, no such table: agent_profiles` (twice, different guessed paths), then `Error: in prepare, no such column: model`.

### CLI-boot recovery mechanism is unconditionally invoked on a path where it can never work
**File:** `9726d652-a145-439a-aadb-f9e6bdfa9e94.jsonl` · **~2026-08-15T20:58–21:06 UTC**

`chat_http_broker_notify.go` unconditionally invoked the CLI-boot recovery broker on every HTTP-provider chat-stream error, which always failed (`bootdir for provider "anthropic" is not yet implemented`) because that recovery mechanism only applies to CLI sessions. Confirmed live: 50 of 61 historical `nanite_recovery_breadcrumbs` rows were this exact permanent failure — the recovery-broker mechanism had been structurally non-functional for the entire HTTP-provider chat path.

> "confirmed live: 50 of 61 historical breadcrumbs were this exact failure."

### Orchestrator durable agent could not be launched at all — lifecycle-class value missing from a validation enum
**File:** `07317d39-6349-4d5e-a46c-45609db63510.jsonl` · **~2026-08-15T06:18–06:48 UTC**

Attempting to launch Nanite's own Orchestrator durable agent failed (`sql: no rows in result set` looking up its own profile ID) because `.nanite/agents/orchestrator.md`'s `class: harness` frontmatter value had never been added to the allowed enum in `validateAgentMultiAgentFields`, so `CreateAgent` silently failed on every service boot for that one profile while four sibling profiles from the same PR ingested fine. Deploy logs before the fix showed `discovered=33, ingested=32` with a swallowed `class "harness" invalid` warning; after the fix, `33/33`. Two separate endpoints actively misreported success on top of the underlying failure: `GET /api/agents` listed the profile as present (reading live from the filesystem, not the DB row that didn't exist), and `POST /api/agents/{id}/copy-to-managed` returned `409 "agent is already a managed config"` — falsely reporting a never-persisted profile as already managed. This blocked launching the Orchestrator through both the API and the GUI.

> "This failed completely silently... `GET /api/agents` lists `orchestrator`... it's clearly reading it live from the filesystem, not from the DB row that doesn't exist... incorrectly reports it as already persisted."

### Agent profile wired to a tool that isn't actually reachable at runtime
**File:** `2db6d53c-9122-42c3-9a05-7f83373e0576.jsonl` · **~2026-08-16T (mid-session)**

Conductor's tool allowlist included `nil_create_item` (for note-capture), but the tool is unreachable because "Nil" isn't registered as an MCP upstream in the Cerberus/mux catalog yet — an agent profile referencing a capability that doesn't actually work at runtime. Filed as follow-up `CW-20260816-0091`.

> "`nil_create_item` is wired into Conductor's allowlist but isn't reachable yet since Nil isn't registered as an MCP upstream in the Cerberus/mux catalog."

### An agent's own system prompt instructs it to call a tool missing from its own allowlist
**File:** `2db6d53c-9122-42c3-9a05-7f83373e0576.jsonl` · **~2026-08-16T23:07 UTC**

A `/code-review high` pass over the finished Conductor Console epic found that Conductor's own system prompt instructs it to relay completions via `card_show(type="report-card", ...)`, but `card_show` was missing from Conductor's own tool allowlist — the call would simply be unavailable, breaking the summary-output contract the epic had just built. The same pass found the `AgentConstraints` frontmatter parser doesn't recognize `MessageWakePolicy`, so a `messageWakePolicy:` override in any profile's YAML silently no-ops.

> "`card_show` missing from Conductor's own tool allowlist — Conductor's system prompt tells it to relay completions via `card_show(...)`, but that tool isn't in its allowlist, so the call would just be unavailable."

---

## Tool-calling issues

### Silent MCP tool-count truncation dropped specific tools from a live Orchestrator session
**File:** `8dab524e-456c-4bbc-b188-f2bfdf3206cd.jsonl` · **~2026-08-15T17:00–18:41 UTC**

Root-caused (originally filed as a bug, then escalated to an architecture decision): `internal/mcp/manager.go`'s `DiscoverTools()` applied a hard positional slice — `tools = tools[:limits.MaxToolsPerServer]` — per MCP connection, at discovery time, using a tier→cap table. Since `tools/list` responses are alphabetically sorted and `torque_task_delete` sat at exactly position 77 of Torque's 93-tool catalog, the cap silently dropped `torque_task_get`/`list`/`update`/`search` from a live session with no error surfaced anywhere. Fixed by removing the truncation entirely and keeping only an advisory warning.

> "The drop was entirely inside Nanite... using a tier→cap table (`internal/mcp/validate.go`'s `LimitsFor`: `builtin`=1000, `plugin_stdio`=200, `plugin_http`=100, `third_party_http`=50). Since `tools/list` responses are alphabetically sorted, and `torque_task_delete` sits at exactly position 77 of Torque's 93-tool catalog, any cap landing in the `task_delete`→`task_get` gap produces exactly the observed symptom."

### Tool-result truncation silently starved a live Orchestrator of task dependency data
**File:** `45c81882-eaae-4f55-b939-aa2f8ffa8fae.jsonl` · **~2026-08-15T18:45 UTC**

Discovered during a live Orchestrator dogfood run: `torque_task_get` calls succeeded and returned results, but responses were silently truncated to unretrievable `tool_result://` pointer URIs because the two meta-tools needed to expand them (`fetch_tool_result`, `search_tool_result`) were `denied` — not present in the Orchestrator's `tools:` allowlist. The Orchestrator got each task's ID/title but lost `depends_on`, and correctly stopped rather than guessing at dispatch order.

> "The Orchestrator got each task's ID/title but not the full record, critically missing `depends_on` — it correctly refused to guess at dispatch order."

### Two agent-role profiles declared tool names that don't exist
**File:** `58371795-e70b-46a7-8455-34795263a25e.jsonl` · **~2026-08-15T15:32–16:53 UTC**

`project-manager.md` declared `bash_run` (real name: `dev_bash`), and `system-architect.md` declared `skill_get`/`skills_view_more` (real names differ). The same `bash_run` typo also existed in a pre-existing `agent-builder.md` profile, described in-session as "systemic doc/reality drift, not a one-off typo." Both profiles' documented boot steps were unconditionally broken as a result.

> "`project-manager.md` declares `bash_run` — does not exist. The real Nanite bash tool is `dev_bash` ... This unconditionally breaks the profile's own documented Step 7."

### `procedure_get`, called by two profiles' boot sequences, does not exist anywhere in the codebase
**File:** `58371795-e70b-46a7-8455-34795263a25e.jsonl` · **~2026-08-15T15:32–16:53 UTC**

The `agent_procedures` table has no read-back path into a running agent's context at all — only a write-side ingest/REST API. Both `project-manager.md`'s boot sequence and `system-architect.md` called a tool (`procedure_get`) that has never existed, unconditionally breaking their documented boot procedures.

> "no tool by this or any other name reads back from the `agent_procedures` table into a running agent's context — the entire 'procedures' feature is write-only."

### A live Orchestrator improvised the wrong tool and failed 5 times before giving up
**File:** `58371795-e70b-46a7-8455-34795263a25e.jsonl` · **~2026-08-15T15:32–16:53 UTC**

A live Orchestrator session needed `torque_task_get`, noticed it wasn't loaded, and — instead of using the available discovery tools (`request_tools`, `tool_list`, `tool_describe`) — improvised by calling `mux_call` (a different-layer tool for reaching servers outside Nanite's own selection) and failed 5 times before giving up. Root cause: no Agent Roles boot prompt documented that the discovery tools exist.

> "instead of reaching for `tool_list`/`tool_describe`/`request_tools` (all technically available to it), it improvised with Tether's `mux_call` ... and failed 5 times before giving up."

### Model-visible tool schema silently diverged from the schema actually enforced
**File:** `58371795-e70b-46a7-8455-34795263a25e.jsonl` · **~2026-08-15T15:32–16:53 UTC**

`normalizeToolInputSchemas` forced `additionalProperties: false` onto property-less pass-through object schemas (e.g., `mux_call`'s free-form `arguments` field), making the schema shown to the model admit only `{}`, while the actual argument validator read a different, unmutated, permissive copy of the schema. Found while root-causing a live Orchestrator session's `mux_call` calls failing client-side with `ARG_VALIDATION_FAILED: got string, want object` on all 5 attempts. A follow-up audit found 9 tools affected beyond the one that surfaced it, including `card_show`, `dispatch_executor`, and `workflow_run`.

> "the model is shown one (broken, unsatisfiable) schema while the validator checks a different (correct, permissive) one — a real inconsistency."

### `dev_bash` silently disappears from advisor-class profiles with no way to opt out
**File:** `58371795-e70b-46a7-8455-34795263a25e.jsonl` · **~2026-08-15T15:32–16:53 UTC**

While fixing the `bash_run`→`dev_bash` rename, it surfaced that `dev_bash` is gated behind a global `developer_mode` setting, while Project Manager is documented in its own profile as "advisor-class, non-executing" — meaning the rename alone doesn't guarantee the tool is usable; it silently depends on an operator-wide setting the profile has no way to opt itself out of.

> "`developer_mode` is off, `dev_bash` is silently stripped from the tool list the LLM ever sees — no error, it just isn't there ... there's no way for PM's profile to opt itself out of the gate."

### A same-named upstream MCP server collided with Nanite's own native tools, non-deterministically
**File:** `3af1aa49-d491-455e-a07a-a785cd766c65.jsonl` · **~2026-08-17T18:37–18:54 UTC**

Once the Agent Mux fix (above) increased the live tool catalog, a separate upstream MCP server literally named `nanite` re-exposed the same tool names (`dev_bash`, `dev_read`, `skill_list`, etc.) that `nanite-api-service` already has natively. Nanite's existing collision defense only protected the `self` server by name, not `dev`/`code`/`general`, so the proxy won the bare `dev_bash` slot and silently evicted Nanite's real tool to `dev_dev_bash`. This produced live `"unknown MCP tool: dev_bash"` errors inside Curator's actual session, and the rename outcome was non-deterministic across discovery cycles, so a tool name the LLM was told about in one turn could point at nothing the next. A second, adjacent bug was found while verifying: a pointer-aliasing issue where slice-growth `append` silently orphaned in-place collision renames, corrupting the tool listing/broker-registration view. Fixed via commit `5144590`.

> "one of mux's 8 upstream servers is literally named `nanite`... The collision resolver renames the loser to `dev_dev_bash` etc. — but which side wins is unstable across discovery cycles... so a name the LLM was told about in one turn can point at nothing by the next turn."

### Hard 15-tool alphabetical cap was the root cause of an agent never seeing its own tools
**File:** `3af1aa49-d491-455e-a07a-a785cd766c65.jsonl` · **~2026-08-17T21:29 UTC**

`FinalizeToolSelection` (`internal/toolclient/broker.go:808`) caps offered tools at `MaxSelectedTools = 15` via a plain `tools[:15]` slice — a positional truncation, not relevance-ranked. The candidate list is sorted alphabetically, so the same fixed 15 tools (`base64_decode` … `web_fetch`) were returned for every intent regardless of context. Once the earlier mux fix grew the catalog to 300+ tools, none of Curator's actual `loom_*`/`get_fragment_detail`/`search_fragments` tools sorted early enough to make the cut — Curator wasn't ignoring instructions, it had never been given definitions for the tools it needed. Worked around (not fixed generally) by populating Curator's previously-empty `agent_profiles.tools` allowlist, which runs before the 15-cap.

> "I tested this directly: for *any* intent/hints, the selection always returns the exact same 15 tools... literally alphabetical order, b → w... Curator was never shown those tools at all; it wasn't ignoring instructions, it had no way to call something it was never given a definition for."

### Curator's seeded tool-name patterns were entirely fictional, but the config surface was cosmetic-only
**File:** `3af1aa49-d491-455e-a07a-a785cd766c65.jsonl` · **~2026-08-17T18:30 UTC**

Curator's seeded `role_tools` config contained pattern entries like `"fragments_*"` and `"wiki_*"` that never matched anything real — fragments-engine's real tools are verb-first with no server prefix (`search_fragments`, `get_fragment_detail`), and "wiki" functionality actually lives under `loom_page_*`/`loom_bundle_*`/`loom_template_*`. This field turned out to feed only the agent-management UI, never actual tool-selection — a config surface that looks load-bearing but isn't. Weaver's equivalent list was already accurate.

> "Curator's seeded `role_tools` pattern `\"fragments_*\"` will never literal-match any real tool name. This is currently harmless... it's cosmetic, just feeds the agent-management UI."

### Schedule-firing path silently discarded schedule bodies
**File:** `03b12a97-34a9-467d-9c1e-439fb4498345.jsonl` · **~2026-08-17T02:07–03:25 UTC**

Alongside the never-built `agent_schedules` seeding mechanism (above), `RunDue`'s prompt-delivery path was found to be "silently discarding schedule bodies" — fixed in the same PR, described as affecting Atlas Curator too.

### Orchestrator dispatched the same task through two different mechanisms simultaneously
**File:** `9726d652-a145-439a-aadb-f9e6bdfa9e94.jsonl` · **~2026-08-15T20:50–20:54 UTC**

The live incident that triggered a 4-ticket fix batch: Nanite's own Orchestrator agent dispatched the *same task* via both `workflow_run` and `subagent_spawn` in the same turn, despite its own boot prompt instructing it to pick exactly one based on whether a shipped `WorkflowDefinition` matches. Separately, `workflow_run`'s tool schema didn't surface a named workflow's required inputs, so a call omitting `params.task` failed deep inside engine config resolution with an opaque error instead of a clear one.

> "the Orchestrator dispatched the same task via both `workflow_run` and `subagent_spawn` in the same turn — its own recipe/boot prompt says pick one based on whether a shipped `WorkflowDefinition` matches, not call both."

### Subagent reply-delivery has been silently failing for every real dispatch since at least May
**File:** `9726d652-a145-439a-aadb-f9e6bdfa9e94.jsonl` · **~2026-08-15T20:38–20:47 UTC**

Subagent reply-delivery unconditionally passed the bare role string (e.g., `"worker"`) as `FromAgentID`, which always collided with the UNIQUE constraint on the real `agent_profiles` row for that role (whose real `id` is never equal to its `slug`), because the auto-registration lookup on a bare role string always misses and falls through to an INSERT. This fires on every single reply for any role with an existing profile — i.e., every real dispatch — and had been silently firing since at least 2026-05-19 (three months of history present in the logs), swallowed only as a `WARN` that never flips `run.Status`, so a parent session could complete work and never have its completion surface. A pre-existing regression test had been passing even with the bug reproduced, because the failure path is swallowed rather than surfaced. A related sibling bug (`to_agent_id: agent not found`) was found in the same area but deliberately left as a follow-up rather than fixed.

> "This fires on every single reply for any role whose profile already exists (i.e., every real dispatch)... the test passed even with the bug reproduced, because reply-delivery failures are swallowed as a warning and don't flip run.Status."

### Data race and unbounded goroutine spawn in the new wake-reactor path
**File:** `2db6d53c-9122-42c3-9a05-7f83373e0576.jsonl` · **~2026-08-16T23:27 UTC**

GitHub Copilot's automated review of the Conductor Console PR (#258) found a real data race in the new wake-reactor goroutine (a shared `*Message` pointer passed to a goroutine instead of a copy) and an unbounded/untracked goroutine spawn per message send (no backpressure, no shutdown draining) — both fixed before merge, not caught by Claude Code itself.

> "a data race in the wake-reactor goroutine (shared `*Message` pointer), the unbounded/untracked goroutine spawn... a TS type mismatch."

---

## Hallucinations

### Curator's system prompt asserted false facts about its own target codebase, sourced from a stale doc
**File:** `3af1aa49-d491-455e-a07a-a785cd766c65.jsonl` · **~2026-08-17T21:43 UTC**

Curator's own system prompt stated it maintains a wiki bundle "in Loom (**Ion, rebranded**)," and Curator's procedures pointed it at a canonical architecture doc that exists only inside the old `apps/ion` Python tree and says Loom "stays Python for now; a Go rewrite is deliberately out of scope." In reality `apps/loom` is a from-scratch Go module with no Python, running on port 8092. Curator, following its instructions faithfully, searched the wrong codebase and reported confusion about port/codebase identity — behavior that was actually correct given the stale grounding docs it was pointed at, i.e., the false belief originated in Nanite's own config/doc layer rather than in the model's reasoning.

> "Curator did exactly what it was told to: go read the canonical architecture doc for its scope. That doc told it (accurately, once) that Loom is Ion's Python codebase in-place-rebranded... The model's reasoning was sound — the doc it was pointed at is stale."

### Every dispatched worker/reviewer/researcher was silently failing and self-reporting a generic placeholder, masked by the dispatching session looking healthy
**File:** `9726d652-a145-439a-aadb-f9e6bdfa9e94.jsonl` · **~2026-08-15T20:28 UTC**

Ten agent-role profile files (`internal/agent/builtin/profiles/worker.md`, `reviewer.md`, plus 8 more including `.nanite/agents/*.md`) hardcoded a model ID (`claude-sonnet-4-20250514`) that the live Anthropic API now 404s on, even though Nanite already has a working system-wide default-model resolver (`ResolveProviderAndModel`) that these profiles bypassed. Because `subagent_spawn` is Nanite's most basic dispatch primitive, every dispatched worker/reviewer/researcher/planner/background-job/file-backend/code-auditor/analyst session was silently failing at the first turn, retrying 3 times against the same broken model in about 4 seconds, then persisting a `[generation interrupted]` placeholder as its result — while the Orchestrator's own session (which used the correct model) looked healthy throughout, masking that everything it had dispatched was actually broken.

> "Nanite already has a working system-wide default-model mechanism... that these profiles are bypassing by hardcoding a value... 10 profile files bypassing an existing SSOT by hardcoding a specific value, not a missing architecture."

---

## Steering issues

### The subagent reaper killed genuinely productive, in-flight work purely on elapsed wall-clock time
**File:** `18f8f448-987d-4f04-9b56-6a2fb807e518.jsonl` · **~2026-08-16T00:19 UTC**

`internal/subagent/reaper.go`'s `SweepOnce` kills a subagent run purely on `started_at + timeout_seconds < now` (default 1800s), with zero awareness of whether the run is actually still doing anything. Direct evidence cited in the fixing ticket: a sync `subagent_spawn` run was genuinely productive the entire time — the harness's own 30s heartbeat ticker fired 9 consecutive pings on schedule proving live progress — and the reaper still killed it at exactly the 30-minute mark, discarding real, in-flight work. An explicit operator quote captured verbatim from an earlier design session shaped the intended (but unshipped) fix: hard kills should be the last resort, and any timeout should reset on detected activity.

> "a `subagent_spawn` (sync, role worker) was genuinely productive the entire time — the harness's own 30s heartbeat ticker ... fired 9 consecutive pings on schedule (30/60/.../270s) proving live progress — and the reaper still killed it at exactly the 30-minute mark with `error: \"timeout: runner reaper\"`."
>
> "Philosophy: soft errors, nudges, steering, recovery, reaping zombies — hard kills are the last resort." (operator, quoted from a prior session)

### A keyword in a test-fragment title mis-routed a wake message to the wrong agent identity
**File:** `d16afefb-9405-46e1-ae9c-b26f59e2f658.jsonl` · **~2026-08-17T16:02 UTC**

Running the task's own prescribed smoke test with a fragment titled `"Curator Fix Verification 2"` caused the wake's message to be mis-routed to a generic "worker" sub-agent instead of running as Loom Curator. Root cause: an unrelated, pre-existing "worker-execute" reflex in `internal/promptrouter/catalog.go` triggers on the substring "Fix" appearing anywhere in a message, regardless of surrounding context — coincidentally matching the test's own required fragment title.

> "my test fragment title (`\"Curator Fix Verification 2\"`, from the task's own prescribed smoke test command) contains the word \"Fix\", which happens to be a trigger phrase for an unrelated pre-existing \"worker-execute\" reflex in `internal/promptrouter/catalog.go` that misroutes the wake's message to a generic worker sub-agent instead of running it as Loom Curator."

### A "read-only" policy check silently mutates state on every eligible agent-to-agent message
**File:** `2db6d53c-9122-42c3-9a05-7f83373e0576.jsonl` · **~2026-08-16T23:07 UTC**

A `/code-review high` pass over the Conductor Console epic flagged, as a design-level question requiring a human decision, that `resolveMessageWakePolicy` — meant to be a read-only "does this session want a wake" check — calls `ResolveForSession`, which has a write side effect: it can auto-assign an agent binding and emit an event as an incidental side effect of every eligible agent-to-agent message send.

> "a write side effect... on *every* eligible A2A message send."

### Wake-on-message behavior silently expanded from notifications-only to interrupting request/reply protocols
**File:** `2db6d53c-9122-42c3-9a05-7f83373e0576.jsonl` · **~2026-08-16T23:07 UTC**

The same review pass found that wake now fires by default for `KindRequest`/`KindReply`/`KindHandoff` messages, not just notifications — meaning a request/reply protocol that expected the recipient to poll on its own schedule now gets proactively interrupted with a synthetic turn by default, with no dedicated test coverage added for those specific kinds.

> "a real behavior change with no dedicated test coverage for those specific kinds."

---

## Taxonomy confusion

### An AgentProfile slug collision would have silently overwritten an unrelated agent identity
**File:** `ed715b3e-9cf3-44ce-97ae-339a4144d452.jsonl` · **~2026-08-15T04:40 UTC**

While building a new `planner` recipe, the session found the `planner` AgentProfile slug was already claimed by a deliberately tool-less Phase-6 cognition-arc stub tied to reflex dispatch's `PlannerRoleSlug`; reusing the slug would have silently overwritten that unrelated, migration-tracked identity. Caught before it happened; a distinct `task-planner` profile was created instead.

> "the existing `planner` AgentProfile slug is already claimed by a deliberately tool-less Phase-6 cognition-arc stub (tied to reflex dispatch's `PlannerRoleSlug`) — reusing it would silently overwrite that unrelated, migration-tracked identity."

### A stale reflex-catalog silently misrouted keyword mentions to a generic "Worker" fallback
**File:** `ed715b3e-9cf3-44ce-97ae-339a4144d452.jsonl` · **~2026-08-15T04:33 UTC**

`researcher-mention` and `reviewer-mention` reflexes were, before this session's fix, silently falling back to the generic `Worker` profile instead of routing to the real `researcher`/`reviewer` profiles that already existed — a stale-slug bug in the reflex catalog. Separately, `documentor`/`strategist` reflexes were re-audited and confirmed to have genuinely no matching profile (left on Worker fallback, but with corrected comments so the gap is now documented rather than silently wrong).

> "`researcher-mention` and `reviewer-mention` reflexes now set `Profile: \"researcher\"`/`\"reviewer\"` (confirmed exact slugs from the profile frontmatter, not filenames) instead of silently falling back to Worker."

### The user's own mental model of an existing "relay agent" didn't match anything in the codebase
**File:** `e32a8bac-290b-4778-9715-24afa3d5f256.jsonl` · **~2026-08-16T19:35 UTC**

The user opened a design session by referring to "we already have a relay agent" as a design starting point. Investigation found no `internal/relay` package anywhere in Nanite — "relay" only appears as a doc comment in `internal/dispatch/execute.go`. Claude Code had to stop and confirm what was actually meant before proceeding, concluding it was almost certainly `.nanite/agents/orchestrator.md` (a Torque-task-status-only durable agent), materially narrower than what "relay agent" implies.

> "the user specifically said 'we already have a relay agent,' and the audit found no `internal/relay` package in Nanite, so I want to confirm what they're actually referring to ... This is almost certainly the 'relay agent' you meant — there's no other candidate in the codebase."

### Two unrelated systems sharing the name "reflex" caused live confusion across three separate sessions over two days
**File:** `e32a8bac-290b-4778-9715-24afa3d5f256.jsonl` · **~2026-08-16T19:35–21:18 UTC** (discovered, filed as `CW-20260816-0062`) → `2db6d53c-9122-42c3-9a05-7f83373e0576.jsonl` · **~2026-08-16T21:38 UTC** (first rename implemented) → `03b12a97-34a9-467d-9c1e-439fb4498345.jsonl` · **~2026-08-17T02:09–02:42 UTC** (second rename, after developer pushback)

`internal/reflex/` (a live, wired deterministic phrase-match router used at dispatch time) and `internal/agent/reflexes/` (an unrelated in-flight predicate/event/interval steering engine for drift/runaway-loop monitoring) shared the same root term and doc-naming convention with no shared code or lifecycle. Claude Code itself initially conflated the two mid-session before checking actual file contents, then filed a ticket (`CW-20260816-0062`). Later the same evening, the ticket was resolved by renaming `internal/agent/reflexes` → `internal/agent/driftguard`. The next day, a different session picking up related work encountered developer pushback questioning whether that first rename had ever actually been approved ("I can't tell from the repo alone whether that was actually reviewed/approved by you or just done unilaterally in a prior session"), and ended up renaming it a *second* time back to `internal/agent/reflexes`, while renaming the original `internal/reflex` to `internal/promptrouter` instead — two live renames across two sessions to fully disambiguate a collision that took three separate sessions to notice, decide on, and settle.

> "Same root term, same 'docs/agent-reflex-catalog.md'-style naming, no shared code or lifecycle, both tagged `reflexes` in Torque — pure naming collision. Flagged live during design work on the new Conductor console (2026-08-16) when research initially conflated the two before checking file contents." (e32a8bac)
>
> "They share the word \"reflex\" and nothing else — no shared code, no shared lifecycle... I can't tell from the repo alone whether that was actually reviewed/approved by you or just done unilaterally in a prior session." (03b12a97)

### Dead legacy code shared a near-identical name with the real, live subsystem
**File:** `e32a8bac-290b-4778-9715-24afa3d5f256.jsonl` · **~2026-08-16T20:xx UTC**

`internal/workflow/` is dead legacy code still present in the tree, distinct from the real, live `internal/agentworkflow` — the same "old path vs. real path" naming-collision shape as the reflex/reflexes issue above. Flagged live but left as an open question rather than filed.

> "`internal/workflow/` (the old path) is dead legacy code still sitting in the tree, separate from the real `internal/agentworkflow`. Want a cleanup task for that too, or fold it into general hygiene later?"

### A durable-agent lifecycle class silently downgraded to a different class during reconcile
**File:** `07317d39-6349-4d5e-a46c-45609db63510.jsonl` · **~2026-08-15T07:xx UTC**

Fixing the missing-enum-value bug above (which blocked launching Orchestrator entirely) exposed a second, distinct bug found by an automated PR review: `class: harness` was silently downgrading to `class: advisor` in the durable-agent reconcile path — a real correctness regression in Nanite's own lifecycle-class handling, not caught by Claude Code's own session, fixed with a dedicated regression test in the same PR.

> "one real correctness bug my own fix exposed — `class: harness` silently downgrading to `advisor` in the durable-agent reconcile path."

### An agent-profile frontmatter override key is silently unrecognized
**File:** `2db6d53c-9122-42c3-9a05-7f83373e0576.jsonl` · **~2026-08-16T23:07 UTC**

The same `/code-review high` pass that found Conductor's missing `card_show` allowlist entry (above) also found that the `AgentConstraints` frontmatter parser doesn't recognize the `MessageWakePolicy` key, so a `messageWakePolicy:` override present in any profile's YAML silently no-ops rather than taking effect or erroring.

---

## Other

### `subagent_runs` migration recreate-pattern caused a full production crash-loop
**File:** `d16afefb-9405-46e1-ae9c-b26f59e2f658.jsonl` · **~2026-08-17T15:46–17:53 UTC**

Live incident: Nanite's daemon was crash-looping (`runs: 250` restart count observed) because this codebase has no `schema_migrations` table by design — every migration re-runs on every boot. Migrations `019`, `065`, and `067` each recreate `subagent_runs` from scratch (SQLite can't `ALTER` a `CHECK` constraint), but each rebuild only knows its own historical column/CHECK set. Migration `019`'s CHECK predates the `'stalled'`/`'over_budget'` statuses added by `065`. The first time any real row got one of those statuses — traced to a spawned "worker" subagent session from this same session's own earlier verification testing (see the reflex-misroute incident above), which the reaper marked `stalled` after 30 minutes idle — the next restart's re-run of `019` tried to copy that row into a table with the old narrow CHECK and crashed outright. Independent of the crash, the same recreate-pattern was also found to be silently dropping and resetting six other columns (`provider`, `retry_count`, `max_retries`, `on_fail`, `attempts_json`, `last_activity_at`) back to defaults on *every single restart*. Fixed with a `migrate:skip-if-column-exists` directive; verified against a backed-up copy of the actual crashed production database (174 rows recovered intact). Shipped as PR #261 / commit `e2273f8`. A follow-up audit ticket (`CW-20260817-0004`) was filed to check for the same anti-pattern elsewhere and other masked historical data loss.

> "this codebase has no `schema_migrations` table by design — every migration re-runs on every boot. Migrations `019`, `065`, and `067` each recreate `subagent_runs` from scratch... but each rebuild only knows about its *own* historical column/CHECK set... the next restart's re-run of `019` tried to copy that row into a table with the old narrow CHECK and crashed outright."
>
> "**Nanite is back up.** Same pid (89540), `runs: 3` (stable, no longer crash-looping from `runs: 250`), health check returns 200."

### A third documented occurrence: async subagent completions never reach the parent session
**File:** `79184fcd-2b0a-411b-b996-b602bc0a5cb4.jsonl` · **~2026-08-15T22:12 UTC**

Root-cause ticket text (dated 2026-08-15, describing a "third documented occurrence, now blocking a new real consumer"): async subagents complete after the parent's chat turn has already ended, and their `result_json` lands cleanly in `subagent_runs.result_json`, but nothing surfaces it back to the session — the data sits in the DB unreachable. While dogfooding the newly-built "Orchestrator" durable-agent role, a real worker subagent completed ~1,300 lines of genuine work and replied with a completion summary; the Orchestrator session never received any envelope or message reflecting the completion, and the operator had to manually transition the Torque task and manually re-prompt the Orchestrator to keep it moving. A prior, independent occurrence (2026-05-12) was table-documented in the same ticket: a parent session rendered its response 1 minute 27 seconds *before* an async subagent actually completed, telling the user the analysis was "still in progress" after it had, in fact, already finished. The ticket also records an explicit operator decision to reject a polling/reminder-based workaround in favor of a real emit path.

> "`CW-20260814-0014` was dispatched to a real worker subagent, which completed substantial, genuine work ... and replied with a completion summary. **The Orchestrator session never received it** — no envelope, no message, nothing in its own transcript reflecting the child's completion. The operator had to manually transition the Torque task and manually re-prompt the Orchestrator to keep the dogfood run moving."
>
> "we have reminders that agents can set and the Orchestrator could use those to check every 10 minutes, but I think the proper solution is to stop relying on the agents; they should simply emit status changes... and Nanite should know how to handle it." (operator, quoted verbatim in the ticket)

### A previously scoped fix for reaper behavior was marked "done" in the tracker despite never shipping
**File:** `18f8f448-987d-4f04-9b56-6a2fb807e518.jsonl` · **~2026-08-16T00:19 UTC**

The fix for the wall-clock-only reaper (see "Steering issues," above) had already been scoped three months earlier as `CW-20260519-0073` ("switch from fixed wall-clock deadline to heartbeat/inactivity liveness"). That ticket was marked `Status: done` in Torque, but its own `BlockedReason` field read "WOUND DOWN 2026-05-19 — Torque stabilization pause. Re-queue after the stabilization sprint lands." It had been parked, not implemented, and never re-queued; the live reap of productive work on 2026-08-16 was the direct proof.

> "It's marked `Status: done` in Torque, but its own `BlockedReason` reads: 'WOUND DOWN 2026-05-19 — Torque stabilization pause. Re-queue after the stabilization sprint lands.' It was parked, not implemented, and never re-queued — today's live reap is direct proof."

### A durable agent's first wake would have silently failed workspace resolution
**File:** `03b12a97-34a9-467d-9c1e-439fb4498345.jsonl` · **~2026-08-17T01:13 UTC**

While wiring Loom Curator's wake endpoint, the session found and fixed a first-wake workspace-resolution bug in `DurableAgentWakeService.Wake` that "would've silently broken Curator's very first callback" — noted as also affecting Atlas Curator (a different app's durable agent using the same Nanite mechanism).

### Schedule status-preservation bug would silently reactivate expired schedules on every boot
**File:** `03b12a97-34a9-467d-9c1e-439fb4498345.jsonl` · **~2026-08-17T03:42 UTC**

Caught by a GitHub Copilot PR review rather than by Claude Code itself: `syncManagedDurableAgentSchedule` only preserved the `paused` status across re-sync, meaning `expired` schedules would silently reactivate on every boot. Fixed in the same PR along with a misleading comment about `wakeScheduleDue`'s fallback behavior, plus a new regression test.

> "Copilot flagged two issues in `syncManagedDurableAgentSchedule` — a status-preservation bug (only `paused` was preserved across re-sync, so `expired` schedules would silently reactivate on every boot) and a misleading comment about `wakeScheduleDue`'s fallback behavior."

### The Tool Broker fix itself introduced a new cross-request state-leak bug
**File:** `58371795-e70b-46a7-8455-34795263a25e.jsonl` · **~2026-08-15T15:32–16:53 UTC**

A GitHub Copilot PR review on the Tool Broker fix (closing out the tool-visibility investigation) caught a bug introduced by the fix itself: "a scoped call's agent/workspace override rules stayed loaded on the shared broker instance and silently applied to the next unscoped call." Fixed by reloading rules unconditionally on every call.

### A downstream app's silent DB-path bug blocked every Nanite agent's fragment-fetch calls
**File:** `3af1aa49-d491-455e-a07a-a785cd766c65.jsonl` · **~2026-08-17T21:53 UTC**

Not a Nanite-side bug, but directly blocking every Nanite agent's fragment-fetch calls: fragments-engine's MCP server resolved `database.path` relative to process CWD rather than to its own config file, and silently created/opened a fresh empty SQLite DB with no warning instead of erroring on a missing/wrong path — so `get_fragment_detail` always returned "no rows" for real, existing fragments whenever the subprocess inherited the wrong working directory. Traced to a phantom zero-row DB file materializing inside Nanite's own repo (`apps/nanite/data/fragments-engine.db`). No override knob existed anywhere in the stack (Agent Mux's catalog schema has no `cwd` field; fragments-engine has no `--db`/env override) to fix this from Nanite's side.

> "instead of erroring on a missing/wrong path, it silently creates a brand-new, fully-migrated, empty SQLite database and opens that. No warning, no error — just zero rows for every fragment."

---

## Patterns observed

*(Descriptive only — no recommendations.)*

- **"Silent fallback to a working-but-wrong default, or silent no-op, instead of an error" is the single most repeated shape.** Across well over a dozen distinct incidents in this window — MCP tool-count truncation, tool-result truncation, Agent Mux crash-loop → 4 builtin sources, tool-name collision → renamed tool, `dev_bash` developer-mode gating, reflex-catalog Worker fallback, `role_tools`/`roleTools:` cosmetic-only fields, schedule status-preservation, `RunDue` discarding bodies, hardcoded-model dispatch failures, subagent reply-delivery collisions, the Orchestrator `class: harness` enum gap, `class: harness`→`advisor` reconcile downgrade, `messageWakePolicy:` frontmatter no-op, `nil_create_item` unreachable tool, CLI-boot recovery broker on the HTTP path — the system did not error when something was missing, wrong, or unreachable; it silently substituted a smaller, generic, stale, or inert answer. Every one of these was found only through live dogfooding or a downstream Copilot/human review, never through the system's own error surface.
- **Several of the silent-failure incidents were not one-off — they had been silently firing for weeks to months before being caught.** Subagent reply-delivery's `FromAgentID` collision had been silently swallowed as a WARN since at least 2026-05-19 (three months) on *every* real dispatch, with a pre-existing regression test that kept passing throughout because the failure path itself is what's swallowed. The reaper's wall-clock-only kill logic had an already-scoped, already-designed fix (`CW-20260519-0073`) marked "done" in the tracker three months earlier while never actually shipping. The CLI-boot recovery broker had been permanently failing on 50 of 61 historical breadcrumb rows for the entire HTTP-provider chat path's lifetime.
- **A "healthy-looking parent session masking a fully broken dispatch layer" shape appeared twice, independently.** Hardcoded model IDs across 10 agent-role profile files caused every dispatched worker/reviewer/researcher/etc. to fail immediately and self-report a generic `[generation interrupted]` placeholder — while the dispatching Orchestrator session (on a different, correct model) looked completely healthy throughout, giving no visible signal that its dispatches were broken. Separately, the subagent reply-delivery collision (above) meant a parent session could complete real work and never have that completion surface, without the parent itself showing any error.
- **Every crash/data-loss/dispatch-broken-class incident found in this window was discovered via direct operator dogfooding of a new durable-agent role (Orchestrator, Curator), not through routine testing.** Tool-count truncation, tool-result truncation, subagent-result-never-surfaces, reaper wall-clock kill, subagent_runs crash-loop, hardcoded-model dispatch failure, reply-delivery collision, dual-dispatch (`workflow_run` + `subagent_spawn` together), and the `class: harness` launch failure were all first (or re-)discovered because a live durable-agent role was actually put to work and something broke live — several explicitly logged in their own tickets as "Nth documented occurrence" of the same underlying gap.
- **Naming/taxonomy collisions cluster specifically around the word "reflex."** At least 3 sessions across two days (`ed715b3e`'s stale reflex-catalog fallback; `e32a8bac`'s discovery of the `internal/reflex` vs `internal/agent/reflexes` collision and ticket-filing; `2db6d53c`'s first rename to `driftguard`; `03b12a97`'s second rename, prompted by developer uncertainty about whether the first rename had ever been approved) involve this one term, requiring two separate live renames across two different packages to fully settle. A parallel, structurally identical collision (`internal/workflow/` dead code vs. real `internal/agentworkflow`) was also flagged but left unresolved as of these transcripts.
- **"Read-only" checks with hidden write side effects, and default behaviors that quietly expanded scope, appeared in the same review pass.** `resolveMessageWakePolicy`, meant to be read-only, was found to call into a path with a real write side effect (auto-assigning an agent binding, emitting an event) on every eligible agent-to-agent message. In the same pass, wake-on-message was found to have started firing by default for request/reply/handoff message kinds, not just notifications, with no dedicated test coverage for that expanded scope.
- **GitHub Copilot's automated PR review caught real bugs in the fixes themselves at least 6 times in this window**, none caught by Claude Code's own session first: a hardcoded dev-specific path (`1d8e1d33`), SQL-identifier quoting (`d16afefb`), a Tool Broker cross-request state leak (`58371795`), schedule status-preservation (`03b12a97`), a `class: harness`→`advisor` silent downgrade (`07317d39`), and a data race plus unbounded goroutine spawn in a new wake-reactor path (`2db6d53c`).
- **Lifecycle-class handling for durable agents broke in two different, independently discovered ways on the same day** (`07317d39`, Aug 15): a new class value (`harness`) missing from a validation enum silently blocked that profile from ever being created, while a separate reconcile-path bug silently downgraded the same class value to a different one once created.
- **Config-file/database/DB-row drift recurs at multiple layers of the same system**, not just once: `roleTools:` vs `tools:` (two competing "declare your tools" surfaces, one dead), `role_tools` cosmetic-only patterns for Curator, `agent_known_tools` DB rows that are additive-only and never cleaned up automatically, `MessageWakePolicy` frontmatter silently unrecognized by its own parser, and multi-surface config (DB rows + `.md` files + durable-agent YAML) all needing synchronized manual edits.
- **At least three incidents involved a hardcoded or environment-specific value that only ever worked in the original author's context**, then failed generically elsewhere: the `~/dev/hollis-labs/...` workflow-definitions path (one developer's machine only), the hardcoded model ID across 10 profiles (worked until the model was deprecated upstream), and the "Fix"-keyword reflex trigger (only surfaced because a smoke test's own *prescribed* fragment title happened to contain the trigger word).
- **At least one incident (Curator's "Ion, rebranded" belief) was a case where the agent's stated behavior was internally consistent and correctly derived from its instructions/grounding docs, but the docs themselves were stale** — flagged in-session explicitly as not the model's fault.
- **Discovery/exploration tooling was itself a recurring point of friction, independent of any specific bug**: no single documented path for "the real runtime database" (three guesses needed), no boot-prompt documentation that tool-discovery meta-tools exist (leading to 5 failed improvised calls), and at least one tool-input schema (`mux_call`) that showed the model a stricter contract than the validator actually enforced.
