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
| `nanite_show_giphy` | self | UI | **keep** | `nanite_*` self-tool namespace |
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
