# K3 Audit: Internal Tool Descriptions Audit

**Ticket:** CW-20260419-0022
**Date:** 2026-04-26
**Orchestrator:** CW-20260426-0022 (arch-seq Phase 2 quick-wins)
**Verdict:** COMPLETE — all in-scope tools brought to the 6-bullet template.

---

## Per-Tool Scorecard

All tools scored against the 6-bullet template:
1. What it does
2. When to use
3. When NOT to use / anti-patterns
4. Output shape
5. Required context
6. Chaining hints

### `internal/toolclient/meta_tools.go`

| Tool | Before | After | Note |
|------|--------|-------|------|
| `request_tools` | 1/6 | 6/6 | Added when/when-not, output shape, chaining hint (load schema → call tool immediately) |
| `fetch_tool_result` | 2/6 | 6/6 | **Critical fix:** explicit ULID-only warning; "NOT a file path, NOT a cache:// URI"; pointer footer source called out; chaining with search_tool_result |
| `search_tool_result` | 2/6 | 6/6 | **Critical fix:** same ULID anti-pattern warning; RE2 syntax note; chaining with fetch_tool_result and dev_grep |

### `internal/mcp/general_tools.go`

| Tool | Before | After | Note |
|------|--------|-------|------|
| `web_fetch` | 3/6 | 6/6 | Added 1 MiB body cap, SSRF blocked-range warning, truncation shape |
| `json_parse` | 2/6 | 5/6 | Added when/when-not, output shape; chaining N/A by design |
| `datetime` | 1/6 | 6/6 | Added "current date in system prompt" steering, supported units, output format |
| `base64_encode` | 1/6 | 4/6 | Added when-to-use, output shape; minimal by design |
| `base64_decode` | 1/6 | 4/6 | Added when-to-use, error shape |
| `url_encode` | 1/6 | 4/6 | Added when-to-use, output shape |
| `url_decode` | 1/6 | 4/6 | Added when-to-use, error shape |
| `hash` | 2/6 | 5/6 | Added md5 security warning, sha256 default rationale, output example |
| `math_eval` | 2/6 | 6/6 | **Called out in ticket:** 1 KiB cap, supported operators (+,-,*,/,^), no functions (sqrt etc.), output shape |
| `think` | 3/6 | 6/6 | **Called out in ticket:** "do not substitute for tool calls", distinction from scratchpad/memory |

### `internal/mcp/self_tools.go` — Skill/Agent management

| Tool | Before | After | Note |
|------|--------|-------|------|
| `nanite_create_skill` | 1/6 | 5/6 | Added when/when-not, output shape |
| `nanite_list_skills` | 1/6 | 4/6 | Added when-to-use, output shape |
| `nanite_update_skill` | 1/6 | 5/6 | Added when-to-use, required context (need ID), output shape |
| `nanite_delete_skill` | 2/6 | 5/6 | Added when-to-use, required context, output shape |
| `nanite_create_agent` | 1/6 | 5/6 | Added when/when-not, output shape |
| `nanite_list_agents` | 1/6 | 4/6 | Added when-to-use, output shape |
| `nanite_update_agent` | 1/6 | 5/6 | Added when-to-use, required context, output shape |

### `internal/mcp/self_tools.go` — Builder tools

| Tool | Before | After | Note |
|------|--------|-------|------|
| `nanite_start_builder` | 3/6 | 6/6 | Tightened anti-pattern (NOT for Q&A), required context (builder_name), output shape, chaining with builder_step |
| `nanite_builder_step` | 2/6 | 5/6 | Added when/when-not (must have active session), output shape |

### `internal/mcp/self_tools.go` — Display tools

| Tool | Before | After | Note |
|------|--------|-------|------|
| `nanite_show_giphy` | 2/6 | 5/6 | **Called out in ticket:** added cosmetic-only warning ("NOT for real data"), envelope type named (giphy-modal) |
| `nanite_show_document` | 6/6 | 6/6 | Template — no change |
| `nanite_show_report` | 6/6 | 6/6 | Template — no change |

### `internal/mcp/self_tools.go` — Todo tools

| Tool | Before | After | Note |
|------|--------|-------|------|
| `nanite_todo_create` | 2/6 | 6/6 | **Called out in ticket:** full scope semantics (workspace/project/session), auto-fill of scope_id from session context, when-not (use plans for dependencies) |
| `nanite_todo_update` | 2/6 | 5/6 | Added when-to-use, status transition table, required context |
| `nanite_todo_list` | 4/6 | 6/6 | **Called out in ticket:** auto-fill semantics named, card-is-live-data clarified, no-scope → no-card anti-pattern |

### `internal/mcp/self_tools.go` — Plan tools

| Tool | Before | After | Note |
|------|--------|-------|------|
| `nanite_plan_create` | 3/6 | 6/6 | **Called out in ticket:** when-to-use vs todos, scope auto-fill, plan-review envelope instruction retained, chaining with plan_update |
| `nanite_plan_update` | 2/6 | 6/6 | Added when-to-use, step_id vs plan-level semantics, both FSM tables, output shape |
| `nanite_plan_list` | 1/6 | 4/6 | Added when-to-use, auto-fill note, output shape |
| `nanite_plan_get` | 1/6 | 5/6 | Added when-to-use (get step IDs for update), full output shape |
| `nanite_plan_delete` | 2/6 | 5/6 | Added when-to-use, required context, output shape |

### `internal/mcp/self_tools.go` — Install tools

| Tool | Before | After | Note |
|------|--------|-------|------|
| `nanite_install_home` | 2/6 | 5/6 | Added when-to-use, force=true warning, output shape |
| `nanite_install_project` | 2/6 | 5/6 | Added when-to-use, required context (absolute path), output shape |
| `nanite_install_rollback` | 1/6 | 5/6 | Added when-to-use, required/optional context, output shape |
| `nanite_install_diff` | 2/6 | 4/6 | Added when-to-use, not-yet-implemented note |

### `internal/mcp/self_tools.go` — Messaging/Handoff tools

| Tool | Before | After | Note |
|------|--------|-------|------|
| `nanite_message_send` | 3/6 | 6/6 | Added channel policy prose, required context summary, output shape, chaining with inbox |
| `nanite_message_inbox` | 1/6 | 5/6 | Added when-to-use (filter=unread), required context, output shape, chaining with ack |
| `nanite_message_thread` | 3/6 | 5/6 | Added when-to-use, required context, output shape |
| `nanite_message_ack` | 1/6 | 4/6 | Added when-to-use, idempotent note |
| `nanite_message_resolve` | 1/6 | 4/6 | Added when-to-use, distinction from ack |
| `nanite_message_catch_up` | 2/6 | 5/6 | Added when-to-use (handoff), required context, output shape |
| `nanite_handoff_request` | 2/6 | 6/6 | Added when-to-use, requested_by semantics, output shape, chaining |
| `nanite_handoff_approve` | 2/6 | 5/6 | Added when-to-use, required context, output shape |
| `nanite_handoff_reject` | 1/6 | 5/6 | Added when-to-use, reason field purpose, output shape |

### `internal/mcp/self_tools.go` — Subagent tools

| Tool | Before | After | Note |
|------|--------|-------|------|
| `nanite_spawn_subagent` | 3/6 | 6/6 | Added mode semantics table (sync/async/api), required context, output shape variants, chaining |
| `nanite_subagent_status` | 1/6 | 5/6 | Added when-to-use, required context, output shape |
| `nanite_subagent_cancel` | 2/6 | 5/6 | Added when-to-use, idempotent note, output shape |

### `internal/mcp/self_tools.go` — Scratchpad tools (pre-existing template)

| Tool | Before | After | Note |
|------|--------|-------|------|
| `nanite_scratchpad_write` | 6/6 | 6/6 | Already canonical — no change |
| `nanite_scratchpad_read` | 6/6 | 6/6 | Already canonical — no change |
| `nanite_scratchpad_clear` | 6/6 | 6/6 | Already canonical — no change |

### Skipped (per ticket scope)

| Tool | Reason |
|------|--------|
| `nanite_navigate_engine` | Slated for removal via CW-20260419-0014 |
| `nanite_refresh_engine` | Slated for removal via CW-20260419-0014 |

---

## Per-MCP Standard Applied

### Standard: Progressive Discovery with 6-Bullet Template

The standard applied in this pass:

1. **Progressive discovery via description** — The description is the LLM's only signal for tool selection before the schema is loaded. Each description now opens with a one-sentence "what" that names the return value or action, followed by explicit `When to use` / `When NOT to use` guidance that triggers on the user's phrasing, not the tool's internal name.

2. **Anti-patterns as first-class content** — The most expensive mis-use is a false positive (calling the wrong tool). Anti-patterns are prefixed with "Do NOT" and placed immediately after the when-to-use guidance so they are encountered before the agent commits.

3. **Output shape as contract** — Every description names the response shape in prose or a minimal example. This prevents the LLM from inferring structure from pattern-completion when the real shape differs.

4. **Required context surfaced explicitly** — Parameters like `scope_id` (auto-filled from session context), `id` (must come from a prior list call), and `session_id` / `agent_id` pairs are called out in the description, not only in the parameter schema. This reduces "parameter not found" failures where the LLM omitted a required field because the schema didn't say why it was needed.

5. **Chaining hints** — Common tool chains (e.g. `nanite_plan_create` → `nanite_plan_get` to read step IDs → `nanite_plan_update` per step) are documented in the trailing "Chaining" line, enabling the LLM to plan multi-step flows without an extra round-trip.

6. **Convenience function pattern** — `nanite_show_report` and `nanite_show_document` are the reference implementations: they roll up metric rendering, grounding enforcement, and envelope emission into a single call. New display tools should follow this pattern.

---

## Recommended Convenience Functions per Owned MCP

### Nanite self-tools (`internal/mcp/self_tools.go`)

- **`nanite_todo_status_update` convenience wrapper** — The common pattern `nanite_todo_list(scope=session)` → pick ID → `nanite_todo_update(id=..., status=done)` is a 2-call chain used constantly. A convenience function `nanite_todo_check_off(title_or_id, scope)` that resolves by title and marks done in one call would eliminate the most frequent unnecessary round-trip.

- **`nanite_plan_advance_step` convenience wrapper** — `nanite_plan_get` (to read step IDs) → `nanite_plan_update(step_id=...)` is always paired. A convenience `nanite_plan_step_done(plan_id, step_title)` that resolves the step by title and transitions it to done would collapse a 2-call chain into 1.

- **`nanite_show_plan` display convenience** — Currently agents must manually assemble a `nanite_show_document` or `nanite_show_report` from `nanite_plan_get` output. A `nanite_show_plan(plan_id)` that fetches and renders the plan as a structured card (like `nanite_show_report` does for metrics) would make plan status visible in chat without manual composition.

### Plugin MCPs

The plugin catalog (`plugins/repos.yaml`) lists plugins at external repos (giphy, oembed, support-ticket, etc.). These are maintained separately and were not in scope for this pass. If any of those plugins expose MCP tools, a parallel description audit should be filed against their respective repos.

---

## Open Follow-ups

### Clockwork / Engine / Vanta / Hadron tool descriptions

Per ticket §MCP tool descriptions — these are the tools the LLM uses most frequently and a parallel audit pass would yield the highest per-tool ROI. Specifically:

- **Clockwork tools** (clockwork_task_*, clockwork_sprint_*, clockwork_plan_*): descriptions already have some anti-patterns but many lack when-to-use and output shape. The task_transition description is the closest to the template and should be used as the reference.
- **Vanta Conduit tools** (memory_write, memory_recall, knowledge_write, conduit_lookup): the distinction between memory and knowledge namespaces, recall ordering, and write restrictions (Vanta-primary since 2026-04-19) are not in the tool descriptions — this is a frequent source of writes going to the wrong namespace.
- **Context Broker tools** (context_write, context_view, context_search, context_pack): the trust tier / namespace / promotion model is opaque to the LLM from description alone.

**Recommendation:** File a single rollup ticket "External MCP tool descriptions audit — Clockwork / Vanta / Context Broker" scoped to those three services, using this audit's scorecard format and the same 6-bullet template.

---

## Test Results

```
ok  github.com/hollis-labs/nanite/internal/mcp         19.332s
ok  github.com/hollis-labs/nanite/internal/toolclient   7.001s
```

All existing tests pass. No snapshot tests for tool descriptions existed that required updating. The scratchpad description tests (`TestScratchpadToolDescriptions_RequiredSections`) continue to pass — the template sections (`When to use`, `When NOT to use`, `Output shape`) are preserved in all three scratchpad tools.
