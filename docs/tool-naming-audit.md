# Tool Naming Audit

**Ticket:** CW-20260426-0012 — D-naming sweep  
**Date:** 2026-04-26  
**Precondition:** D-mcp wrap layer (ADR-002) is live; every tool already has a uniform
agent-facing name with no `mcp__server__` prefix. This audit records the verdict for
each owned tool and documents the hard-rename decision.

Convention doc: `docs/tool-naming-convention.md`

---

## Decisions

### Hard rename vs. compat alias

**Decision: hard rename, no compat aliases.**

Per `feedback_no_compat_shims` and the Phase 5 pre-launch latitude, any rename is a clean
break. There are no external consumers of the agent-facing tool surface. The two call paths
(`Manager.ExecuteTool` via uniform index, `Manager.ExecuteToolOnServer` via explicit server)
are both first-party. Aliases/redirects add permanent weight for zero benefit before launch.

### Disambiguator policy

**Decision: provider prefix only when a real collision exists.**

The agent description in CW-20260426-0010 (D2) is: *"tool names should reflect the concept,
not the provider."* The corollary is: when two providers share a concept, the provider
prefix becomes part of the concept (it disambiguates which task system, which health probe,
which scheduler).

Known real collisions in the current tool set: `health` (engine + hadron + cerberus),
`status` / `start` / `stop` / `restart` / `logs` / `build` (cerberus — generic enough that
future collision is near-certain). All of these keep their prefixes.

Purely speculative future collisions (no current second provider) do NOT justify prefixing.
If a second provider lands and a real collision appears, the runtime collision policy in
`Manager.assignUniformNameLocked` will disambiguate automatically.

---

## Audit table

Owned MCP servers audited: `mux` (Vanta Conduit), `engine` (Fragments Engine), `hadron`,
`cerberus`, `clockwork` (via mux), and the builtin servers (`dev`, `general`, `code`, `self`,
`memory`).

Third-party MCPs are out of scope: the wrap layer canonicalizes them, but naming authority
sits with their authors.

### Legend

| Column | Meaning |
|---|---|
| Current uniform name | Name the agent sees post-ADR-002 internalization |
| Source server | MCP server name (for `ToolAttribution` / audit) |
| Concept | What resource/domain the tool represents |
| Verdict | keep / rename |
| Notes | Rationale when non-trivial |

---

### Vanta Conduit (`mux` server)

| Current uniform name | Source server | Concept | Verdict | Notes |
|---|---|---|---|---|
| `memory_write` | mux | memory | **keep** | Bare concept name, no collision |
| `memory_get` | mux | memory | **keep** | Bare concept name |
| `memory_recall` | mux | memory | **keep** | Bare concept name |
| `memory_deprecate` | mux | memory | **keep** | Bare concept name |
| `memory_promote` | mux | memory | **keep** | Bare concept name |
| `memory_get_revision` | mux | memory | **keep** | Bare concept name |
| `memory_history` | mux | memory | **keep** | Bare concept name |
| `knowledge_write` | mux | knowledge | **keep** | Bare concept name |
| `knowledge_get` | mux | knowledge | **keep** | Bare concept name |
| `knowledge_history` | mux | knowledge | **keep** | Bare concept name |
| `context_write` | mux | context | **keep** | Bare concept name |
| `context_view` | mux | context | **keep** | Bare concept name |
| `context_pack` | mux | context | **keep** | Bare concept name |
| `context_head` | mux | context | **keep** | Bare concept name |
| `context_history` | mux | context | **keep** | Bare concept name |
| `context_namespaces_list` | mux | context | **keep** | Bare concept name |
| `context_namespace_register` | mux | context | **keep** | Bare concept name |
| `context_namespace_show` | mux | context | **keep** | Bare concept name |
| `context_broker_plan` | mux | context | **keep** | Bare concept name |
| `context_broker_fetch` | mux | context | **keep** | Bare concept name |
| `context_audit` | mux | context | **keep** | Bare concept name |
| `context_typed_write` | mux | context | **keep** | Bare concept name |
| `context_typed_view` | mux | context | **keep** | Bare concept name |
| `context_types_list` | mux | context | **keep** | Bare concept name |
| `context_views_list` | mux | context | **keep** | Bare concept name |
| `context_status_promote` | mux | context | **keep** | Bare concept name |
| `context_status_deprecate` | mux | context | **keep** | Bare concept name |
| `context_promote_request` | mux | context | **keep** | Bare concept name |
| `context_promote_approve` | mux | context | **keep** | Bare concept name |
| `context_promote_list` | mux | context | **keep** | Bare concept name |
| `context_search` | mux | context | **keep** | Bare concept name |
| `context_rag_query` | mux | context | **keep** | Bare concept name |
| `context_embed` | mux | context | **keep** | Bare concept name |
| `context_estimate` | mux | context | **keep** | Bare concept name |
| `context_bulk_ingest` | mux | context | **keep** | Bare concept name |
| `context_chunked_ingest` | mux | context | **keep** | Bare concept name |
| `context_session_snapshot` | mux | context | **keep** | Bare concept name |
| `context_packet` | mux | context | **keep** | Bare concept name |
| `conduit_lookup` | mux | conduit | **keep** | `conduit` IS the concept here (the conduit store lookup); not provider-flavored |
| `views_evaluate` | mux | views | **keep** | Bare concept name |
| `mux_health` | mux | mux | **keep** | Mux-specific health probe; would collide with future health tools if bare |
| `mux_discover` | mux | mux | **keep** | Mux-specific discovery |
| `mux_call` | mux | mux | **keep** | Mux routing primitive |
| `mux_boot_generate` | mux | mux | **keep** | Mux-specific |
| `mux_catalog_*` | mux | mux | **keep** | Mux catalog namespace |
| `mux_logical_agent_*` | mux | mux | **keep** | Mux logical-agent namespace |
| `mux_message_*` | mux | mux | **keep** | Mux inter-agent messaging |
| `mux_proxy_events` | mux | mux | **keep** | Mux-specific |
| `mux_session_*` | mux | mux | **keep** | Mux session management |
| `mux_events_tool_calls` | mux | mux | **keep** | Mux-specific |

**Mux summary:** All bare concept names. No renames needed. The `conduit_lookup` and
`mux_*` tools carry a prefix that IS the concept (conduit store, mux routing) — not provider
branding. The `context_*` and `memory_*` families are textbook `<concept>_<verb>`.

---

### Clockwork (`clockwork_*` via mux server)

| Current uniform name | Source server | Concept | Verdict | Notes |
|---|---|---|---|---|
| `clockwork_task_create` | mux | clockwork task | **keep** | Prefix disambiguates from Engine tasks (real collision risk) |
| `clockwork_task_get` | mux | clockwork task | **keep** | Same |
| `clockwork_task_list` | mux | clockwork task | **keep** | Same |
| `clockwork_task_update` | mux | clockwork task | **keep** | Same |
| `clockwork_task_delete` | mux | clockwork task | **keep** | Same |
| `clockwork_task_search` | mux | clockwork task | **keep** | Same |
| `clockwork_task_transition` | mux | clockwork task | **keep** | Same |
| `clockwork_task_bulk_transition` | mux | clockwork task | **keep** | Same |
| `clockwork_task_checkpoint_*` | mux | clockwork task | **keep** | Same |
| `clockwork_task_subtodo_*` | mux | clockwork task | **keep** | Same |
| `clockwork_task_checkpoints_pending` | mux | clockwork task | **keep** | Same |
| `clockwork_task_create_from_template` | mux | clockwork task | **keep** | Same |
| `clockwork_sprint_*` | mux | clockwork sprint | **keep** | Disambiguates from Engine sprints |
| `clockwork_epic_*` | mux | clockwork epic | **keep** | No collision today; prefix retained for family consistency |
| `clockwork_plan_*` | mux | clockwork plan | **keep** | No collision today; prefix retained for family consistency |
| `clockwork_project_*` | mux | clockwork project | **keep** | No collision today; prefix retained for family consistency |
| `clockwork_template_*` | mux | clockwork template | **keep** | No collision today; prefix retained for family consistency |
| `clockwork_artifact_*` | mux | clockwork artifact | **keep** | No collision today; prefix retained for family consistency |
| `clockwork_comment_*` | mux | clockwork comment | **keep** | No collision today; prefix retained for family consistency |
| `clockwork_run_*` | mux | clockwork run | **keep** | Disambiguates from Hadron runs |
| `clockwork_scheduler_*` | mux | clockwork scheduler | **keep** | Clockwork-specific |
| `clockwork_settings_*` | mux | clockwork settings | **keep** | Clockwork-specific |
| `clockwork_health` | mux | clockwork health | **keep** | Real collision: engine/hadron/cerberus also have `health` |

**Clockwork summary:** All `clockwork_` prefixes are legitimate. Clockwork task/sprint/run
concepts collide with Engine and Hadron counterparts. No renames.

---

### Fragments Engine (`engine` server)

| Current uniform name | Source server | Concept | Verdict | Notes |
|---|---|---|---|---|
| `engine_task_create` | engine | task | **keep** | Collides with clockwork tasks if bare |
| `engine_task_get` | engine | task | **keep** | Same |
| `engine_task_update` | engine | task | **keep** | Same |
| `engine_task_transition` | engine | task | **keep** | Same |
| `engine_tasks_list` | engine | task | **keep** | Same |
| `engine_sprint_create` | engine | sprint | **keep** | Collides with clockwork sprints if bare |
| `engine_sprint_get` | engine | sprint | **keep** | Same |
| `engine_sprints_list` | engine | sprint | **keep** | Same |
| `engine_backlog_capture` | engine | backlog | **keep** | Unique concept; prefix retained for engine-family consistency |
| `engine_backlog_list` | engine | backlog | **keep** | Same |
| `engine_projects_list` | engine | project | **keep** | No collision today; prefix retained for family consistency |
| `engine_health` | engine | health | **keep** | Real collision: hadron and cerberus also export `health` |

**Engine summary:** Task and sprint concepts collide with Clockwork equivalents — prefixes
are required disambiguators. `engine_health` is required by the three-way `health` collision.
No renames.

---

### Hadron (`hadron` server)

| Current uniform name | Source server | Concept | Verdict | Notes |
|---|---|---|---|---|
| `hadron_run_enqueue` | hadron | run | **keep** | `run` could collide with Clockwork runs; prefix required |
| `hadron_run_get` | hadron | run | **keep** | Same |
| `hadron_runs_list` | hadron | run | **keep** | Same |
| `hadron_blueprint_get` | hadron | blueprint | **keep** | Unique today; prefix retained for family consistency |
| `hadron_blueprints_list` | hadron | blueprint | **keep** | Same |
| `hadron_pipeline_enqueue` | hadron | pipeline | **keep** | Unique today; prefix retained for family consistency |
| `hadron_schedule_create` | hadron | schedule | **keep** | `schedule` is generic enough to warrant prefix |
| `hadron_schedules_list` | hadron | schedule | **keep** | Same |
| `hadron_workspace_get` | hadron | workspace | **keep** | Unique today; prefix retained for family consistency |
| `hadron_workspaces_list` | hadron | workspace | **keep** | Same |
| `hadron_health` | hadron | health | **keep** | Real collision: engine and cerberus also export `health` |

**Hadron summary:** Run concepts collide with Clockwork runs. `hadron_health` required by
the three-way `health` collision. No renames.

---

### Cerberus (`cerberus` server)

| Current uniform name | Source server | Concept | Verdict | Notes |
|---|---|---|---|---|
| `cerberus_start` | cerberus | service start | **keep** | `start` alone is too generic; would collide with any future server |
| `cerberus_stop` | cerberus | service stop | **keep** | Same |
| `cerberus_restart` | cerberus | service restart | **keep** | Same |
| `cerberus_status` | cerberus | service status | **keep** | `status` is too generic |
| `cerberus_logs` | cerberus | service logs | **keep** | `logs` is too generic |
| `cerberus_build` | cerberus | service build | **keep** | `build` alone is too generic |
| `cerberus_rebuild` | cerberus | service rebuild | **keep** | Same |
| `cerberus_health` | cerberus | health | **keep** | Real collision: engine and hadron also export `health` |

**Cerberus summary:** Cerberus manages processes; its verbs (`start`, `stop`, `status`,
`build`, `logs`) are the most generic in the catalog. The prefix is essential. No renames.

---

### Builtin servers (`dev`, `general`, `code`, `self`, `memory`)

These are registered by the Nanite process itself, not via external MCP HTTP/stdio transports.

| Current uniform name | Server | Concept | Verdict | Notes |
|---|---|---|---|---|
| `dev_read` | dev | file read | **keep** | Unique |
| `dev_write` | dev | file write | **keep** | Unique |
| `dev_edit` | dev | file edit | **keep** | Unique |
| `dev_grep` | dev | text search | **keep** | Unique |
| `dev_glob` | dev | file glob | **keep** | Unique |
| `dev_bash` | dev | shell exec | **keep** | Unique |
| `web_fetch` | general | HTTP fetch | **keep** | Unique |
| `json_parse` | general | JSON | **keep** | Unique |
| `datetime` | general | time | **keep** | Unique |
| `base64_encode` | general | encoding | **keep** | Unique |
| `base64_decode` | general | encoding | **keep** | Unique |
| `url_encode` | general | encoding | **keep** | Unique |
| `url_decode` | general | encoding | **keep** | Unique |
| `hash` | general | hashing | **keep** | Unique |
| `math_eval` | general | math | **keep** | Unique |
| `think` | general | reasoning | **keep** | Unique |
| `nanite_code_execute` | code | code execution | **keep** | `nanite_*` self-tool namespace |
| `nanite_memory_save` | memory | memory | **keep** | `nanite_*` self-tool namespace |
| `nanite_memory_recall` | memory | memory | **keep** | `nanite_*` self-tool namespace |
| `nanite_create_skill` | self | skill mgmt | **keep** | `nanite_*` self-tool namespace |
| `nanite_list_skills` | self | skill mgmt | **keep** | `nanite_*` self-tool namespace |
| `nanite_update_skill` | self | skill mgmt | **keep** | `nanite_*` self-tool namespace |
| `nanite_delete_skill` | self | skill mgmt | **keep** | `nanite_*` self-tool namespace |
| `nanite_create_agent` | self | agent mgmt | **keep** | `nanite_*` self-tool namespace |
| `nanite_list_agents` | self | agent mgmt | **keep** | `nanite_*` self-tool namespace |
| `nanite_update_agent` | self | agent mgmt | **keep** | `nanite_*` self-tool namespace |
| `nanite_navigate_engine` | self | UI nav | **keep** | `nanite_*` self-tool namespace |
| `nanite_refresh_engine` | self | UI nav | **keep** | `nanite_*` self-tool namespace |
| `nanite_show_document` | self | UI | **keep** | `nanite_*` self-tool namespace |
| `nanite_show_report` | self | UI | **keep** | `nanite_*` self-tool namespace |
| `nanite_start_builder` | self | UI builder | **keep** | `nanite_*` self-tool namespace |
| `nanite_builder_step` | self | UI builder | **keep** | `nanite_*` self-tool namespace |
| `nanite_todo_create` | self | todo | **keep** | `nanite_*` self-tool namespace |
| `nanite_todo_update` | self | todo | **keep** | `nanite_*` self-tool namespace |
| `nanite_todo_list` | self | todo | **keep** | `nanite_*` self-tool namespace |
| `nanite_plan_create` | self | plan | **keep** | `nanite_*` self-tool namespace |
| `nanite_plan_update` | self | plan | **keep** | `nanite_*` self-tool namespace |
| `nanite_plan_list` | self | plan | **keep** | `nanite_*` self-tool namespace |
| `nanite_plan_get` | self | plan | **keep** | `nanite_*` self-tool namespace |
| `nanite_plan_delete` | self | plan | **keep** | `nanite_*` self-tool namespace |
| `nanite_install_home` | self | install | **keep** | `nanite_*` self-tool namespace |
| `nanite_install_project` | self | install | **keep** | `nanite_*` self-tool namespace |
| `nanite_install_rollback` | self | install | **keep** | `nanite_*` self-tool namespace |
| `nanite_install_diff` | self | install | **keep** | `nanite_*` self-tool namespace |
| `nanite_message_send` | self | messaging | **keep** | `nanite_*` self-tool namespace |
| `nanite_message_inbox` | self | messaging | **keep** | `nanite_*` self-tool namespace |
| `nanite_message_thread` | self | messaging | **keep** | `nanite_*` self-tool namespace |
| `nanite_message_ack` | self | messaging | **keep** | `nanite_*` self-tool namespace |
| `nanite_message_resolve` | self | messaging | **keep** | `nanite_*` self-tool namespace |
| `nanite_message_catch_up` | self | messaging | **keep** | `nanite_*` self-tool namespace |
| `nanite_handoff_request` | self | handoff | **keep** | `nanite_*` self-tool namespace |
| `nanite_handoff_approve` | self | handoff | **keep** | `nanite_*` self-tool namespace |
| `nanite_handoff_reject` | self | handoff | **keep** | `nanite_*` self-tool namespace |
| `nanite_spawn_subagent` | self | subagent | **keep** | `nanite_*` self-tool namespace |
| `nanite_subagent_status` | self | subagent | **keep** | `nanite_*` self-tool namespace |
| `nanite_subagent_cancel` | self | subagent | **keep** | `nanite_*` self-tool namespace |
| `nanite_scratchpad_write` | self | scratchpad | **keep** | `nanite_*` self-tool namespace |
| `nanite_scratchpad_read` | self | scratchpad | **keep** | `nanite_*` self-tool namespace |
| `nanite_scratchpad_clear` | self | scratchpad | **keep** | `nanite_*` self-tool namespace |
| `nanite_chat_search` | self | chat history | **keep** | `nanite_*` self-tool namespace |
| `nanite_execute_task` | self | task exec | **keep** | `nanite_*` self-tool namespace |

---

## Summary

| Server | Tools audited | Renamed | Kept |
|---|---|---|---|
| mux (Vanta Conduit) | ~46 | 0 | 46 |
| mux (Clockwork) | ~25 | 0 | 25 |
| engine | 12 | 0 | 12 |
| hadron | 11 | 0 | 11 |
| cerberus | 8 | 0 | 8 |
| dev (builtin) | 6 | 0 | 6 |
| general (builtin) | 10 | 0 | 10 |
| code (builtin) | 1 | 0 | 1 |
| self (builtin) | ~39 | 0 | 39 |
| memory (builtin) | 2 | 0 | 2 |
| **Total** | **~160** | **0** | **~160** |

**Zero renames applied.** The post-D-mcp names are already concept-shaped correctly. The
provider prefixes that remain (`engine_`, `hadron_`, `cerberus_`, `clockwork_`, `mux_`) are
all legitimate disambiguators — either a real collision exists today, or the concept is
generic enough that a collision is near-certain in the evolved tool set.

The D-naming ticket's value is primarily **documentation**: establishing the convention so
future tool authors have a clear rule, and recording that the current names are intentional,
not accidental.

---

## Appendix A — `nanite_*` rename audit (CW-20260501-0005)

**Ticket:** CW-20260501-0005 (P2) — sub-ticket 1.
**Date:** 2026-05-01.
**Trigger:** c121 surfaced two coupled gaps:
1. The chat agent has no Vanta integration (`memory_*` / `context_*` / `knowledge_*` are
   off-surface) — fixed by sub-ticket 2 of the same ticket.
2. The `nanite_*` prefix on first-party self-tools doesn't help the agent reason. Once
   the chat surface gains bare-named Vanta tools (`memory_recall`), the residual
   `nanite_memory_recall` becomes a confusing duplicate.

**User decision (2026-05-01):**
> "I would like for the agent to have memory_*, context_*, and knowledge_* read/write.
> Also, I think we can remove nanite_ from all our internal tools."

This appendix is the **rename plan**. Execution is sub-ticket 3 (out of scope here).

### Scope

Every tool currently registered under the `nanite_*` namespace from the in-process
`self`, `code`, and `memory` builtin transports. The convention doc's "reserved
namespace" rule (§"The `nanite_*` reserved namespace") is being relaxed — the harness
no longer needs the prefix to disambiguate self-tools from MCP-origin tools because
the uniform-name index (`Manager.uniformIndex`) already resolves names per-server.

### Rename rule

`nanite_<concept>_<verb>` → `<concept>_<verb>` with these exceptions:

- **Generic-on-its-own concepts** keep a prefix: `nanite_set_reminder` → `reminder_set`
  (verb-suffix); `nanite_pin` → `pin` is too generic — propose `context_pin` (or keep the
  prefix as `nanite_pin`, see "Open questions" below).
- **`nanite_show_card` and panel signals** are a special class: they cross the
  harness/UI boundary and are part of the elicitation surface. Rename to
  `card_show` / `panel_open` / `panel_close` / `signal_mode` (still single-owner concepts).
- **Memory tools** collide head-on with Vanta. See "Collision matrix" below.

### Per-tool rename plan

Source files: `internal/mcp/self_tools.go`, `internal/mcp/self_tools_describe.go`,
`internal/mcp/self_tools_list.go`, `internal/mcp/self_tools_validate.go`,
`internal/mcp/self_tools_remember.go`, `internal/mcp/self_tools_python.go`,
`internal/mcp/self_tools_panels.go`, `internal/mcp/self_tools_reminders_pins.go`,
`internal/mcp/self_tools_chat_search.go`, `internal/mcp/code_exec_tools.go`,
`internal/mcp/memory_tools.go`. Tool surface is also referenced from
`internal/dispatch/role.go` (ChatToolSurface prefix list), `internal/toolclient/tool_knowledge.go`
(broker hints), and `internal/mcp/self_tools_describe.go` (describeRelations cross-references).

| Current name | Source file:line | Proposed bare name | Vanta collision? | Resolution |
|---|---|---|---|---|
| `nanite_code_execute` | code_exec_tools.go:36 | `code_execute` | no | rename |
| `nanite_memory_save` | memory_tools.go:49 | `memory_save` | **YES** — Vanta has `memory_write` (different verb, no clash); but `memory_save` is alias-shaped for write. | **rename + suffix:** `local_memory_save` (or drop the local store entirely — see "Open question" 1) |
| `nanite_memory_recall` | memory_tools.go:90 | `memory_recall` | **YES** — head-on with `memory_recall` from Vanta | **rename + suffix:** `local_memory_recall`, OR drop the local store and let Vanta own recall (preferred, see "Open question" 1) |
| `nanite_remember` | self_tools_remember.go:27 | `remember` (verb-only — too bare) → propose `lesson_capture` | partial — captures into Vanta indirectly via the LearningRecorder; concept is "lesson", not "memory" | rename to `lesson_capture` (verb-suffix matches the remembered-thing) |
| `nanite_tool_describe` | self_tools_describe.go:60 | `tool_describe` | no | rename |
| `nanite_tool_list` | self_tools_list.go:38 | `tool_list` | no | rename |
| `nanite_validate` | self_tools_validate.go:33 | `validate` (too bare) → propose `tool_validate` | no | rename to `tool_validate` (concept = "tool", verb = "validate") |
| `nanite_run_python` | self_tools_python.go:520 | `python_run` | no | rename |
| `nanite_create_skill` | self_tools.go:53 | `skill_create` | no | rename |
| `nanite_list_skills` | self_tools.go:72 | `skill_list` | no | rename (singular noun per convention) |
| `nanite_update_skill` | self_tools.go:84 | `skill_update` | no | rename |
| `nanite_delete_skill` | self_tools.go:104 | `skill_delete` | no | rename |
| `nanite_create_agent` | self_tools.go:118 | `agent_create` | no | rename |
| `nanite_list_agents` | self_tools.go:136 | `agent_list` | no | rename |
| `nanite_update_agent` | self_tools.go:146 | `agent_update` | no | rename |
| `nanite_navigate_engine` | self_tools.go:166 | `engine_navigate` | no | rename |
| `nanite_refresh_engine` | self_tools.go:200 | `engine_refresh` | no | rename |
| `nanite_show_card` | self_tools.go:227 | `card_show` | no | rename |
| `nanite_show_document` | (legacy) | `document_show` | no | rename |
| `nanite_show_report` | (legacy) | `report_show` | no | rename |
| `nanite_start_builder` | self_tools.go:283 | `builder_start` | no | rename |
| `nanite_builder_step` | self_tools.go:298 | `builder_step` | no | rename |
| `nanite_todo_create` | self_tools.go:315 | `todo_create` | no | rename |
| `nanite_todo_update` | self_tools.go:340 | `todo_update` | no | rename |
| `nanite_todo_list` | self_tools.go:359 | `todo_list` | no | rename |
| `nanite_plan_create` | self_tools.go:378 | `plan_create` | no | rename |
| `nanite_plan_update` | self_tools.go:400 | `plan_update` | no | rename |
| `nanite_plan_step_add` | self_tools.go:421 | `plan_step_add` | no | rename |
| `nanite_plan_list` | self_tools.go:446 | `plan_list` | no | rename |
| `nanite_plan_get` | self_tools.go:460 | `plan_get` | no | rename |
| `nanite_plan_delete` | self_tools.go:473 | `plan_delete` | no | rename |
| `nanite_install_home` | self_tools.go:488 | `install_home` | no | rename |
| `nanite_install_project` | self_tools.go:501 | `install_project` | no | rename |
| `nanite_install_rollback` | self_tools.go:517 | `install_rollback` | no | rename |
| `nanite_install_diff` | self_tools.go:532 | `install_diff` | no | rename |
| `nanite_message_send` | self_tools.go:551 | `message_send` | no | rename |
| `nanite_message_inbox` | self_tools.go:577 | `message_inbox` | no | rename |
| `nanite_message_thread` | self_tools.go:595 | `message_thread` | no | rename |
| `nanite_message_ack` | self_tools.go:611 | `message_ack` | no | rename |
| `nanite_message_resolve` | self_tools.go:626 | `message_resolve` | no | rename |
| `nanite_message_catch_up` | self_tools.go:641 | `message_catch_up` | no | rename |
| `nanite_handoff_request` | self_tools.go:656 | `handoff_request` | no | rename |
| `nanite_handoff_approve` | self_tools.go:673 | `handoff_approve` | no | rename |
| `nanite_handoff_reject` | self_tools.go:687 | `handoff_reject` | no | rename |
| `nanite_spawn_subagent` | self_tools.go:703 | `subagent_spawn` | no | rename |
| `nanite_subagent_status` | self_tools.go:729 | `subagent_status` | no | rename |
| `nanite_subagent_cancel` | self_tools.go:743 | `subagent_cancel` | no | rename |
| `nanite_background_job` | self_tools.go:758 | `background_job` | no | rename |
| `nanite_background_status` | self_tools.go:781 | `background_status` | no | rename |
| `nanite_background_cancel` | self_tools.go:795 | `background_cancel` | no | rename |
| `nanite_scratchpad_write` | self_tools.go:810 | `scratchpad_write` | no | rename |
| `nanite_scratchpad_read` | self_tools.go:849 | `scratchpad_read` | no | rename |
| `nanite_scratchpad_clear` | self_tools.go:874 | `scratchpad_clear` | no | rename |
| `nanite_chat_search` | self_tools.go:900 | `chat_search` | no | rename |
| `nanite_panel_open` | self_tools.go:932 | `panel_open` | no | rename |
| `nanite_panel_close` | self_tools.go:951 | `panel_close` | no | rename |
| `nanite_signal_mode` | self_tools.go:969 | `signal_mode` | no | rename |
| `nanite_set_reminder` | self_tools.go:988 | `reminder_set` | no | rename |
| `nanite_pin` | self_tools.go:1036 | `pin` (too bare) → `context_pin` | maybe — Vanta has no `pin` today but the concept overlaps with their pinning model | rename to `context_pin` (concept = pinned context entry) |
| `nanite_unpin` | self_tools.go:1069 | `context_unpin` | same | rename to `context_unpin` |
| `nanite_execute_task` | self_tools.go:1088 | `task_execute` | **YES** — Engine and Clockwork both have `*_task_*` families; bare `task_execute` is unclaimed but generic | rename to `task_execute` (single owner today; if Engine/Clockwork add an `_execute` verb later, runtime collision policy disambiguates) |
| `nanite_open_sprint_planning` (broker hint) | toolclient/tool_knowledge.go:355 | `sprint_planning_open` | no | rename |

**Plugin-registered tools:** none currently use the `nanite_*` namespace. The three
plugins previously registered under their own server names (`giphy`, `oembed`,
`support-ticket`) were all cut in full — `TASKS/phase-0/15a-cut-giphy.md`,
`TASKS/phase-0/15b-cut-oembed.md`, `TASKS/phase-0/15c-cut-support-ticket.md`. No
cross-cutting plugin renames required.

### Collision matrix — `nanite_*` ↔ Vanta

The chat-surface integration in sub-ticket 2 brings the following Vanta tool families
into the chat agent's possible reach (subset shown — full list per `mcp__mux__*` in
the audited Vanta surface):

| Vanta bare name | Local `nanite_*` near-match | Collision shape | Recommended resolution |
|---|---|---|---|
| `memory_recall` | `nanite_memory_recall` | head-on (same name post-strip) | drop the local SQLite memory tool (Vanta is the durable substrate per `vanta-primary-since: 2026-04-19`); OR keep both behind `local_memory_*` prefix |
| `memory_write` | `nanite_memory_save` | alias-shape (same verb family) | as above |
| `memory_history`, `memory_get`, `memory_promote`, `memory_deprecate`, `memory_get_revision` | none | n/a | adopt Vanta tools directly |
| `knowledge_*` | none | n/a | adopt Vanta tools directly |
| `context_*` (~30 tools) | none | n/a | adopt Vanta tools directly |
| `conduit_lookup` | none | n/a | adopt directly |
| `mux_*` | `nanite_message_*` (different concept — Vanta `mux_message_*` is inter-agent messaging across the orchestrator, not the harness's per-session chat-side messaging) | conceptual but not lexical | keep both; revisit at sub-ticket 4 (default-agent profile) when surfacing to the chat agent |

**Recommendation (memory collision):** Drop the local `nanite_memory_save` /
`nanite_memory_recall` tools entirely. The local in-process `memory.Service` was
established before Vanta was wired; per `vanta-primary-since: 2026-04-19`, all new
durable captures route to Vanta. Keeping a parallel local store now creates a fork
in the agent's mental model ("which memory store?") with no clear win. If the
SQLite memory store has runtime callers beyond the chat agent (e.g. session-scoped
slots), keep the underlying service but un-register the agent-facing tools. **This
is open question 1 — orchestrator decision needed before sub-ticket 3.**

### Alias period

**Recommendation: NO alias.** Per `feedback_no_compat_shims` and the existing audit
decision (Decisions §"Hard rename vs. compat alias" above), there are no external
consumers of these tool names — the agent surface, harness wiring, and MCP discovery
are all first-party. An alias map at the dispatch boundary would add permanent
weight for zero benefit. Hard rename, ship, fix breakage in the same PR.

The one exception worth flagging: **prompt-cached descriptions**. Tool-name strings
appear in cached system prompts and in the SQLite `mcp_servers` rows (for plugin
load-checker filters). The rename PR must invalidate prompt caches and re-sync the
tool-load-type allow-list. This is a sub-ticket 3 implementation note, not a reason
for an alias.

### Cross-cutting touchpoints

These references will need updating as part of sub-ticket 3 (NOT this round):

1. **`internal/dispatch/role.go` ChatToolSurface** — every prefix entry. The list
   is prefix-based, so each rename collapses one entry to its bare equivalent
   (e.g. `"nanite_todo_"` → `"todo_"`).
2. **`internal/mcp/self_tools.go` selfToolDefinitions()** — the canonical name
   field on every Tool struct.
3. **`internal/mcp/self_tools_dispatch.go`** — the switch/dispatch keyed by tool
   name (`case "nanite_todo_create":`).
4. **`internal/mcp/self_tools_describe.go` describeRelations** — every key and
   every value in `relatedTools` slices.
5. **`internal/toolclient/tool_knowledge.go`** — broker tool hints carry tool
   names verbatim.
6. **`internal/mcp/manager.go` selfServerName + naming.go reserved-namespace
   logic** — the `IsReservedSelfToolName` predicate (currently keys on `nanite_`
   prefix) needs to be either removed or rebased onto a different signal
   (e.g. server == "self"). This is the most architecturally interesting bit
   of the rollout — the reserved-namespace defense exists so MCP-published
   tools can't masquerade as self-tools. After the rename it must defend by
   server attribution rather than by name prefix.
7. **`docs/tool-naming-convention.md`** — the entire `## The `nanite_*` reserved
   namespace` section is invalidated; replace with "self-tools register under
   server name `self` and bare concept names; the runtime defends by server
   attribution".
8. **DB envelope `tool_name` fields** — `internal/store` schemas log tool names
   in audit tables (`tool_calls`, `chat_events`). Existing rows continue to
   reference the old names; that's correct (history is history). New rows
   carry the new names. No data migration required — the rename is a hard
   break, not a re-key.
9. **Vanta memory entries keyed by tool name** — captured lessons like
   `subject:"nanite_show_card"` exist in the user's Vanta substrate. These do
   NOT need migration: a captured lesson about the old name remains accurate
   for the historical session it was captured in. Going forward,
   `nanite_remember` (renamed to `lesson_capture`) writes lessons keyed by
   the new tool name, and `nanite_memory_recall` (replaced by Vanta's
   `memory_recall`) finds them. Some recall queries that hardcoded the old
   name in their search-string will return stale results until the lesson
   re-captures — acceptable churn.
10. **Tool-result cache keys** — confirmed: `internal/toolclient/truncate.go`
    keys cache entries by tool-call ID (UUID), not by tool name. The rename
    is invisible to the cache.

### Open questions for sub-tickets 3-5

These are surfaced for the orchestrator (per the round-2 dispatch instructions).

1. **Local memory store: drop or namespace?** The local SQLite `memory.Service`
   predates Vanta primacy. Recommend dropping the agent-facing `nanite_memory_save`
   / `nanite_memory_recall` tools entirely (Vanta is canonical). If the underlying
   service has non-tool callers (e.g. session-scoped slot extensions), keep the
   service but unregister the tools. Confirm with orchestrator before sub-ticket 3.

2. **`nanite_pin` / `nanite_unpin` resolution.** The bare name `pin` is too
   generic. Propose `context_pin` / `context_unpin` to flag the pinned-thing
   as context (matches how Vanta's pinning model frames the same concept). If
   we'd rather lock these in a different concept (e.g. "thread"), let me know.

3. **`nanite_validate` resolution.** The verb-only `validate` is too bare and
   could collide with future plugin tools (a plugin could legitimately publish
   `validate` for its own subject). Propose `tool_validate` (concept = the thing
   being validated). Alternative: `args_validate`. Pick before sub-ticket 3.

4. **Reserved-namespace defense rebase.** Current logic in
   `internal/mcp/naming.go`'s `IsReservedSelfToolName` keys on the literal
   prefix `nanite_`. After the rename, the defense must rebase onto server
   attribution (server == `self` ⇒ reserved). This is a meaningful invariant
   change — getting it wrong lets a third-party MCP shadow `card_show` and
   spoof envelope rendering. **Mandatory: include a regression test in the
   rollout PR that registers a third-party MCP publishing `card_show` and
   asserts it gets disambiguated to `<thirdparty>_card_show`.**

5. **`ChatToolSurface` migration shape.** The current list is prefix-based.
   Two options for the rollout:
   - **Mechanical strip:** drop `nanite_` from each prefix. The list ends up
     with shorter prefixes (`"todo_"`, `"plan_"`, etc.) which silently widens
     the surface to any future plugin tool starting with those prefixes. Bad.
   - **Explicit list of bare tool names:** replace the prefix list with an
     exact-match allow-list. More verbose at definition site, but the surface
     becomes explicit and won't accidentally absorb plugin tools. **Preferred.**

6. **Default-agent profile (sub-ticket 4) ordering.** Adding `memory_recall`
   /`context_*` etc. to ChatToolSurface is sub-ticket 3, but the *which subset*
   question is sub-ticket 4 (default profile). Recommend sub-ticket 3 lands
   the rename + adds the read-side Vanta tools (`memory_recall`, `knowledge_get`,
   `context_view`, `context_search`) only; sub-ticket 4 layers in write-side
   (`memory_write`, `knowledge_write`) once the default-profile contract is
   defined. This sequencing reduces the round-1 surface delta.

### Summary

| Bucket | Count |
|---|---:|
| `nanite_*` tools surveyed | ~64 |
| Bare-rename, no collision | 56 |
| Bare-rename + suffix needed (memory collision with Vanta) | 2 |
| Concept-shift rename (e.g. `nanite_pin` → `context_pin`) | 4 |
| Drop entirely (proposed) | 2 (subject to open question 1) |
| Plugin-side renames | 0 (no plugin uses `nanite_*`) |

**No alias period** — hard rename per existing decision. Cross-cutting rebase work
(reserved-namespace defense, ChatToolSurface shape) is sub-ticket 3 implementation,
flagged here for the rollout planner.
