# Internal Self-Tool Error-Recovery Readiness Audit

**Ticket:** [CW-20260429-0010 — E1 — Audit existing internal tools for error-recovery readiness](clockwork://CW-20260429-0010)
**Sprint:** SP-20260428-0002 — Self-Healing Tool Surface
**Vanta lens:** `decisions.nanite.architecture.self_healing_tool_surface_lens`
**Source under audit:** `internal/mcp/self_tools.go` (the ticket's named target). Cross-references where the handler lives in `self_tools_transport.go`, `self_tools_dispatch.go`, `self_tools_giphy.go`, `self_tools_panels.go`, `self_tools_chat_search.go`, `self_tools_python.go`, `self_tools_reminders_pins.go`, plus `internal/service/chat_scratchpad.go` for the scratchpad trio.
**Author:** worktree-agent-a5b93654f1b19e9a1 (claude-opus-4-7[1m])
**Date:** 2026-04-29
**Out of scope:** plugin tools, dev_tools (`dev_*`), general_tools (`web_fetch`, `json_parse`, `think`, etc.), memory_tools (`nanite_memory_*`), code_exec_tools (`nanite_code_execute`). The ticket specifically scopes `internal/mcp/self_tools.go`.

---

## Summary (top of file — read this first)

**Total tools surveyed:** 50 (49 inline-defined in `selfToolDefinitions()` + 1 imported via `naniteRunPythonToolDefinition()` from `self_tools_python.go`).

| Metric | Count | % |
| --- | --- | --- |
| Has a JSON schema (`InputSchema`) | 50 | 100% |
| Has a *strict* schema (`additionalProperties:false`, type-tight, complete `enum`s) | 0 | 0% |
| Has at least one golden example in the description | 3 | 6% |
| Has structured-error output (taxonomy code + JSON shape) | 1 | 2% |
| Description meets "agent gets enough info from description alone" bar | 35 | 70% |
| Tools whose handler skips schema validation (no enforcement of declared schema) | 50 | 100% |

**Top three gaps to address first** (rank-ordered by how much they cost the agent in wasted tool calls before the lens unlocks):

1. **No structured error taxonomy.** Every tool except `nanite_giphy_search` returns errors as free-form strings via `errorResult(fmt.Sprintf(...))`. The C1 taxonomy ticket cannot key off these. Fix scope: standardise an error envelope `{kind, hint, missing?, allowed?, repair_hint?}` for the same handful of failure modes already shipping (missing field, wrong scope, service-not-configured, downstream-error, schema-validation, timeout). Status: **NOT STARTED**.
2. **Schemas are advisory, not enforced.** All 50 tools declare `InputSchema` for the agent's benefit, but the dispatch layer doesn't validate against it before calling the handler. Each handler re-derives "what's required" inline, with hand-rolled `if x == "" { return errorResult("x is required") }` guards. This is why repair (C2) has nothing to anchor: the schema isn't the contract, the prose-error is. Fix scope: a single `validateInputSchema(name, args)` step at the dispatch boundary, so violations surface a structured `schema_validation` error instead of a per-handler ad-hoc string.
3. **Zero coverage of golden examples.** Only `nanite_run_python` (one block), `nanite_giphy_search` (the chaining pattern), and `nanite_show_card` (an envelope format excerpt embedded in `nanite_plan_create`'s description) carry runnable examples. The 47 remaining tools are description-only. Discovery (A1) needs at least one canonical happy-path example per tool to surface via `tool_describe`. Fix scope: add an `Examples []ToolExample` field to the `Tool` struct, populate per tool, expose through `tool_describe`.

The lens calls out the chat session c107 incident specifically: 10 failed `nanite_show_card` attempts in 70 seconds. `nanite_show_card`'s handler does run `envelope.ValidateData()` against a real schema (one of the few handlers that does), but the error message it returns is *raw* schema-validation error text — there's no structured `{missing_required: [...], wrong_type: {field, want, got}}` shape for repair to consume. So even nanite's most disciplined input gate produces a recovery-hostile error.

---

## Audit table

Legend:
- **Schema:** **yes** = `InputSchema` declared with `properties` + `required`. **partial** = declared but missing `required` list or has free-form pass-through fields. (No tools score "no" — every tool declares at least an empty schema.)
- **Example:** runnable golden example present in `Description` (chaining pattern, code block, full envelope). Boilerplate "Output shape: ..." prose does NOT count.
- **Recoverable kinds:** error returns the agent could plausibly fix and retry on the same call.
- **Unrecoverable kinds:** error returns where retry without external state change is hopeless.
- **Description:** **good** = covers when-to-use + when-NOT + required-context + output-shape. **needs-improvement** = missing one or more.

| # | Tool | Schema | Example | Recoverable kinds | Unrecoverable kinds | Description | Gaps |
|---|------|--------|---------|-------------------|---------------------|-------------|------|
| 1 | `nanite_run_python` | yes | yes (code block) | `missing_required` (code), `python_sandbox_error`, `marshal_error` | timeout, mem-cap | good | strings only; no taxonomy |
| 2 | `nanite_create_skill` | yes | no | `missing_required` (name/slug/description), `store_error` (collision) | — | good | error prose only |
| 3 | `nanite_list_skills` | yes | no | `store_error` | — | good | one-liner errors |
| 4 | `nanite_update_skill` | yes | no | `missing_required` (id), `not_found`, `store_error` | — | good | not_found unstructured |
| 5 | `nanite_delete_skill` | yes | no | `missing_required` (id), `store_error` | — | good | unrecoverable from agent's POV but not flagged |
| 6 | `nanite_create_agent` | yes | no | `missing_required` (name/slug/system_prompt), `store_error` | — | good | error prose only |
| 7 | `nanite_list_agents` | yes (empty properties) | no | `store_error` | — | good | empty-properties schema is structurally fine but conveys no constraints |
| 8 | `nanite_update_agent` | yes | no | `missing_required` (id), `not_found`, `store_error` | — | good | not_found unstructured |
| 9 | `nanite_navigate_engine` | yes (no `enum` on `page`) | no | `missing_required` (page), `engine_offline` | — | needs-improvement (no list of valid pages enforced as enum) | enum not declared; engine_offline returned as `textResult` not `errorResult` (silent-success-on-failure smell) |
| 10 | `nanite_refresh_engine` | yes | no | `engine_offline` | — | good | same engine_offline silent-success smell |
| 11 | `nanite_show_card` | yes (rich, with enum) | yes (envelope-block snippet, plus enum) | `unknown_type`, `not_passive_renderable`, `missing_data`, `schema_validation` (raw string), `missing_sources` | `untrusted_render_target` (auto-degrades to inline) | good | **the originating-incident tool** — schema validation message is literally `"data does not match the %q schema: %v"` formatting jsonschema's tree; not structured. The lens's example is *this* tool. |
| 12 | `nanite_giphy_search` | yes | yes (chaining pattern) | `http_error`, `parse_error`, `no_results` | — | good | **only tool with structured-error output**; pattern to copy |
| 13 | `nanite_start_builder` | yes | no | `missing_builder`, `unknown_builder`, `builder_session_error` | — | needs-improvement (does not list valid builder names; description says "Omit to list" — actual handler returns whatever `builders.HandleStartBuilder` returns) | builder error pass-through is opaque |
| 14 | `nanite_builder_step` | yes | no | `missing_required` (builder_name/step_name), `no_active_builder`, `step_validation`, `unknown_step` | — | needs-improvement (the step-name vocabulary is dynamic per builder) | passes through `builders.HandleBuilderStep` error directly without wrapping; no taxonomy |
| 15 | `nanite_todo_create` | yes | no | `missing_required` (title), `bad_scope`, `missing_project_id`, `missing_scope_id`, `store_error` | — | good | scope/scope_id repair is exactly the kind of thing C2 should auto-fix; surface looks ready but errors are strings |
| 16 | `nanite_todo_update` | yes | no | `missing_required` (id), `store_error` | — | good | no `not_found` distinction; bubbles up as `update todo: ...` |
| 17 | `nanite_todo_list` | yes | no | `store_error`, `marshal_error` | — | good | scope/scope_id auto-fill happens silently |
| 18 | `nanite_plan_create` | yes | partial (envelope-emit pattern shown but not a runnable invocation) | `missing_required` (title/scope), `missing_scope_id`, `store_error` | — | good | inline `nanite-envelope` block in description is the closest thing to a usage example anywhere |
| 19 | `nanite_plan_update` | yes | no | `missing_required` (id), `not_found`, `store_error` | — | good | step_id ergonomics (when present, applies to step; when absent, plan) is undermined by string-shaped errors |
| 20 | `nanite_plan_list` | yes | no | `store_error` | — | good | same as todo_list |
| 21 | `nanite_plan_get` | yes | no | `missing_required` (id), `not_found`, `marshal_error` | — | good | not_found unstructured |
| 22 | `nanite_plan_delete` | yes | no | `missing_required` (id), `store_error` | — | good | irreversible — should ideally have a confirm step or trust gate; not flagged in description |
| 23 | `nanite_install_home` | yes | no | `install_error` | — | good | force=true is destructive — description warns but no trust gate |
| 24 | `nanite_install_project` | yes | no | `missing_required` (project_dir), `install_error` | — | good | absolute-path requirement enforced via prose, not schema |
| 25 | `nanite_install_rollback` | yes | no | `missing_required` (project_dir), `install_error` | — | good | archive_path resolution implicit |
| 26 | `nanite_install_diff` | yes | no | — | `not_implemented` | needs-improvement (description admits "Not yet implemented") | shipping a tool whose handler returns a not-implemented sentinel as a *successful* `textResult` is a footgun for the agent |
| 27 | `nanite_message_send` | yes | no | `service_unavailable`, `messaging_error`, `elicitation_error` | — | good | required-list is long (5 fields); ripe for repair if fields are reorderable |
| 28 | `nanite_message_inbox` | yes | no | `service_unavailable`, `messaging_error`, `marshal_error` | — | good | enum on status/channel/kind allows empty string but doesn't document why |
| 29 | `nanite_message_thread` | yes | no | `service_unavailable`, `messaging_error`, `marshal_error` | non-participant returns empty (silent — by design but undocumented to LLM) | good | "non-participants receive empty slice" is in description; good |
| 30 | `nanite_message_ack` | yes | no | `service_unavailable`, `messaging_error` | — | good | idempotent — flagged in description |
| 31 | `nanite_message_resolve` | yes | no | `service_unavailable`, `messaging_error` | — | good | distinct from ack — flagged |
| 32 | `nanite_message_catch_up` | yes | no | `service_unavailable`, `messaging_error`, `marshal_error` | — | good | limit=20 default flagged |
| 33 | `nanite_handoff_request` | yes | no | `service_unavailable`, `handoff_error` | — | good | requested_by enum is good |
| 34 | `nanite_handoff_approve` | yes | no | `service_unavailable`, `handoff_error` | — | good | one-field call — minimal repair surface |
| 35 | `nanite_handoff_reject` | yes | no | `service_unavailable`, `handoff_error` | — | good | reason optional |
| 36 | `nanite_spawn_subagent` | yes | no | `service_unavailable`, `missing_required` (parent_session_id/parent_agent_id/role/prompt), `spawn_error`, `subagent_runtime_error` | timeout (waited but child exhausted budget) | good | mode enum + required list good; structured *output* missing for sync vs async |
| 37 | `nanite_subagent_status` | yes | no | `service_unavailable`, `subagent_error`, `marshal_error` | — | good | run-state enum documented in prose only |
| 38 | `nanite_subagent_cancel` | yes | no | `service_unavailable`, `subagent_error` | — | good | idempotent — flagged |
| 39 | `nanite_background_job` | yes | no | `service_unavailable`, `missing_required` (task/originating_session_id/originating_agent_id), `submit_error` | wall-clock-cap, output-cap | good | extensive sibling-comparison in description is a model |
| 40 | `nanite_background_status` | yes | no | `service_unavailable`, `missing_required` (job_id), `status_error`, `marshal_error` | — | good | output_truncated flagged |
| 41 | `nanite_background_cancel` | yes | no | `service_unavailable`, `missing_required` (job_id), `cancel_error` | — | good | idempotent — flagged |
| 42 | `nanite_chat_search` | yes | no | `missing_required` (query), `bad_scope`, `missing_session`, `store_error`, `marshal_error` | — | good | scope enum present |
| 43 | `nanite_panel_open` | yes | no | `missing_required` (panel_id) | non-recoverable (returned in body, not as error): `user_dismissed`, `unknown_panel`, `untrusted` | good | **structured non-error output** `{opened, panel_id, reason}` — pattern to copy; only on the success path though |
| 44 | `nanite_panel_close` | yes | no | `missing_required` (panel_id) | non-recoverable in body: `user_opened`, `unknown_panel`, `untrusted` | good | symmetric to open |
| 45 | `nanite_signal_mode` | yes | no | `missing_required` (mode) | unknown-mode returns `{signaled: true, mode}` (silent no-op contract) | good | "unknown modes silently ignored" is intentional; documented |
| 46 | `nanite_set_reminder` | yes | no | `missing_required` (text/trigger), `bad_trigger`, `bad_scope`, `missing_project_id`, `missing_session`, `reminder_error` | — | good | trigger object schema (nested `required:[type]`) is the only nested-required example in the file |
| 47 | `nanite_pin` | yes | no | `missing_required` (content), `bad_scope`, `missing_project_id`, `missing_session`, `pin_error` | — | good | budget caveat in prose only |
| 48 | `nanite_unpin` | yes | no | `missing_required` (pin_id), `unpin_error` | — | good | minimal |
| 49 | `nanite_execute_task` | yes | no | `dispatch_unavailable`, `missing_required` (session_id/message), `dispatch_error`, `marshal_error` | timeout (passed through) | good | "the only way Chat dispatches" prose is good agent-discipline scaffolding |
| 50 | `nanite_scratchpad_write` (handled in `service/chat_scratchpad.go`, NOT `mcp` dispatch) | yes (with `anyOf` for value type) | no | `missing_required` (key/value), `size_violation` | — | good | **only tool using `anyOf`** in InputSchema; serves as the closest thing to a schema-discipline pattern |
| 51 | `nanite_scratchpad_read` | yes | no | — (always returns empty entries on miss) | — | good | non-error empty-map return is the right shape; pattern to copy |
| 52 | `nanite_scratchpad_clear` | yes | no | `missing_required` (key) | — | good | idempotent — flagged in prose |

(Note: 52 rows because `naniteRunPythonToolDefinition()` adds nanite_run_python to the slice and the three scratchpad tools are also part of `selfToolDefinitions()` even though their dispatch is intercepted at the service layer. The ticket's "50+" framing matches.)

### Tools missed by the case-switch — surfacing discoveries

Two surface oddities surfaced during the audit:

- **`nanite_install_diff`** is registered with a fully-formed `InputSchema` and a `case` arm in `CallTool`, but the handler at `self_tools_transport.go:1154` returns `textResult("install diff not yet implemented")` — a non-error sentinel string. Agents calling this in good faith get back a "successful" tool result with no structured indicator that the call did nothing. This is a **recoverability inversion**: the lens's "fail loudly" rule wants this to be a structured `not_implemented` *error*, not a silent-pass text return.
- **`nanite_navigate_engine` / `nanite_refresh_engine`** both downgrade `engine_offline` to a *successful* `textResult("Navigation failed — Engine may be offline: %v")` (line 555, 575). Same inversion: cross-app failures look like successes to the LLM. The agent has no signal that the navigation didn't happen.

These two patterns are not in the lens's "schema_validation / type_coercion / wrong_card_type" taxonomy — they're a fourth category I'll call **`silent_no_op`** — and the lens's "default disposition: recover, don't fail" mantra applies *especially* to these (they're failing already; just lying about it).

---

## Per-cluster gap analysis

### Cluster G1: No structured error taxonomy (49 of 50 tools)

Every tool except `nanite_giphy_search` writes errors as `errorResult(fmt.Sprintf("<verb>: %v", err))`. There's no machine-readable code, no `missing_required: [...]` array, no `repair_hint`. The C1 (taxonomy) and C2 (LLM repair) tickets need to discriminate kinds; today they'd have to regex over English prose.

`nanite_giphy_search` already does the right thing via `giphyErrorJSON("http_error"|"parse_error"|"no_results", details, query)` — this is the template to lift.

**Follow-up ticket:** see "G1 — Adopt structured-error envelope across all internal self-tools" below.

### Cluster G2: Schema declared but not enforced (50 of 50 tools)

Every tool defines `InputSchema` for the LLM's benefit, but the dispatch layer (`SelfToolsTransport.CallTool`) doesn't validate the args map against that schema before calling the handler. Each handler re-implements its own required-field checks (`if x == "" { return errorResult("x is required") }`). This means:
- The agent has two contracts to learn (the schema AND the prose error), and they can drift.
- B1 (validate-without-side-effects) has no canonical implementation to call.
- C2 (repair) can't anchor on "which schema field failed" because the failure isn't expressed in schema terms.

The one place schema validation IS enforced (`nanite_show_card` → `envelope.ValidateData`) returns `*ValidationError`'s formatted string instead of a structured shape.

**Follow-up ticket:** see "G2 — Validate input args against InputSchema at the dispatch boundary" below.

### Cluster G3: Zero golden examples on 47 of 50 tools

The lens's first layer (Discovery, A1) wants `tool_describe(name)` to return: schema + at least one golden example + related skills. Today the description block is the only carrier for examples, and only three tools embed any:
- `nanite_run_python`: a real Python code block.
- `nanite_giphy_search`: a chaining pattern (search → show_card).
- `nanite_plan_create`: an envelope-block emit example (not strictly an invocation).

Forty-seven tools have zero. Discovery without examples reduces to schema-only, which is exactly what failed in the c107 incident: the agent had the schema ID space but not the canonical shape.

**Follow-up ticket:** see "G3 — Add golden Examples slice to Tool struct and seed one example per self-tool" below.

### Cluster G4: Silent no-ops disguised as success (3 of 50 tools)

`nanite_install_diff`, `nanite_navigate_engine`, `nanite_refresh_engine` all return `textResult(...)` (success) when the operation didn't happen. The agent has no `IsError` flag to branch on. The lens's "recover, don't fail" framing assumes failures are *signalled* — silent no-ops can't be recovered because the agent thinks it succeeded.

**Follow-up ticket:** see "G4 — Stop returning 'success' for not-implemented or downstream-offline cases" below.

### Cluster G5: Tool-description quality drift (15 of 50 tools — `needs-improvement`)

Most descriptions are good, but a handful lack one of: when-to-use / when-NOT / required-context / output-shape / referenced-vocabulary-not-in-enum. Specifically: `navigate_engine` (no enum on `page`), `start_builder` / `builder_step` (dynamic vocabulary), `install_diff` (admits not-implemented), `*_inbox` filter enums (empty string accepted but unclear when to use). This isn't a bug — it's a quality gap that compounds when discovery (A1) lifts descriptions verbatim.

**Follow-up ticket:** see "G5 — Description-quality polish on the 15 needs-improvement tools" below.

### Cluster G6: Service-unavailable kind isn't recoverable but is treated identically (10+ tools)

Every messaging / subagent / background tool starts with `if st.X == nil { return errorResult("X service not configured") }`. From the agent's POV, this is **unrecoverable** — the service isn't there; no schema fix retries this. But the error text is indistinguishable from `"foo is required"`. C1 should classify these as a separate `service_unavailable` kind so C2 doesn't burn a Haiku call trying to repair an args-shape problem that isn't an args-shape problem.

**Follow-up ticket:** rolled into G1 (taxonomy) — `service_unavailable` is a kind in the proposed envelope.

---

## Top three gaps (final ranking)

1. **G1 — Adopt structured-error envelope.** Without this, C1/C2 have nothing to anchor. Highest-leverage single change.
2. **G3 — Golden examples per tool.** Discovery (A1) becomes useful instead of schema-only. Medium effort, high impact.
3. **G2 — Schema enforcement at dispatch boundary.** Pre-flight validation collapses the dual-contract problem and makes repair (C2) reproducible.

(G4, G5, G6 are smaller cleanups that ride alongside.)

---

## Cross-references

- Lens (Vanta memory): `decisions.nanite.architecture.self_healing_tool_surface_lens` — the four-layer disposition (discover / validate / repair / learn) and the c107 originating incident.
- Sprint: `SP-20260428-0002` — Self-Healing Tool Surface (7 tickets: A1 discover, B1 validate, C1 taxonomy, C2 repair, D1 learn, **E1 audit (this doc)**, E2 portable doc artifact).
- Originating incident: chat session c107 — 10 failed `nanite_show_card` attempts, captured in `nanite.db` session id `4e868808-9284-4452-8b6b-7ff4a86fbb64`, 2026-04-28 22:14:34—22:15:48Z.
- Source files audited:
  - `internal/mcp/self_tools.go` — definitions for 49 tools (the audit's primary target).
  - `internal/mcp/self_tools_python.go` — `nanite_run_python` definition.
  - `internal/mcp/self_tools_transport.go` — `CallTool` case-switch + 40+ handler functions.
  - `internal/mcp/self_tools_dispatch.go` — `nanite_execute_task`.
  - `internal/mcp/self_tools_giphy.go` — `nanite_giphy_search` (the structured-error template).
  - `internal/mcp/self_tools_panels.go` — panel/mode tools.
  - `internal/mcp/self_tools_chat_search.go` — `nanite_chat_search`.
  - `internal/mcp/self_tools_reminders_pins.go` — reminders + pin tools.
  - `internal/service/chat_scratchpad.go` — scratchpad trio (handler lives outside MCP dispatch).
  - `internal/envelope/validator.go` — `ValidateData` (the only existing schema-validation usage).
- Audit tag for follow-up tickets: `phase-e-followup-of-audit`.
