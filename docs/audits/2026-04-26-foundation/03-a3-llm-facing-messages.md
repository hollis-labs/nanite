# A3 Audit: LLM-facing messages inventory

**Ticket:** CW-20260419-0013
**Date:** 2026-04-26
**Auditor:** a3-audit sub-agent
**Total messages cataloged:** 22
**Currently leaking to UI (via SSE Summary field or ErrorEvent):** 18

No `Internal: true` field exists on `StreamEvent` today — it has no such flag in `internal/chat/engine.go`. All `tool_result`-typed StreamEvents carry the message in `Summary` and all leak to any SSE consumer.

---

## Inventory

| # | Message (truncated) | Site (file:line) | Trigger | Currently leaks | Proposed classification | Notes |
|---|---|---|---|---|---|---|
| 1 | `"Tool discovery cap reached (consecutive_empty=...). The tools currently loaded…"` | `chat_generate.go:1666` | `request_tools` hard cap (>maxCalls or consecutiveEmpty≥2) | yes — `tool_result` Summary | User-helpful | Recently rewritten (CW-20260419-0012); human-readable; good guidance. Keep as-is. |
| 2 | `"No new tools were loaded for this request. If you believe the right tools exist, try rephrasing…"` | `chat_generate.go:1698` | `request_tools` returns empty on first miss | yes — appended to `tool_result` Summary | Internal | Instructs the LLM how to retry; irrelevant noise to the user. |
| 3 | `"Loaded N tool(s): name1, name2\n\n[JSON array]"` | `toolclient/meta_tools.go:107` | Successful `request_tools` resolution | yes — `tool_result` Summary | Internal | Raw JSON tool schema dump. Useful only to the LLM; alarming/confusing to users. |
| 4 | `"No matching tools found."` | `toolclient/meta_tools.go:109` | `request_tools` with zero matches | yes — `tool_result` Summary | Internal | LLM retry signal; user sees "no matching tools found" with no context. |
| 5 | `"Tool %q isn't available for the rest of this turn — it returned the same result repeatedly…"` | `chat_tool_executor.go:97` | Exhausted/blocked tool pre-check | yes — `tool_result` Summary | Internal | Replaces the original BLOCKED message. Human-ish language but context budget rationale is LLM-internal plumbing. |
| 6 | `"PERMISSION DENIED: %s — %s"` (hard deny / DecisionDeny) | `chat_tool_executor.go:126` | Hard permission deny | yes — `tool_result` Summary | Needs rewrite | All-caps `PERMISSION DENIED` reads as system error to users. Rephrase: "Tool `%s` was not allowed: %s." |
| 7 | `"PERMISSION DENIED: %s — user denied"` | `chat_tool_executor.go:159` | User clicked Deny on approval prompt | yes — `tool_result` Summary | User-helpful | User-initiated; content is correct. Only prefix needs softening (same as #6). |
| 8 | `"PERMISSION DENIED: %s — approval timed out"` | `chat_tool_executor.go:157` | Approval request timed out (default 60s) | yes — `tool_result` Summary | User-helpful | Timeout is meaningful to user; language needs softening (same prefix issue). |
| 9 | `"PERMISSION DENIED: %s — unknown permission decision %q"` | `chat_tool_executor.go:193` | Permission engine returns unknown Decision type | yes — `tool_result` Summary | Needs rewrite | Exposes internal decision enum; rephrase as generic deny + log the enum internally. |
| 10 | `"Tool %q was refused by a policy plugin for this input. Retrying with the same arguments will be refused again…"` | `chat_tool_executor.go:221` | Plugin hook blocks tool (DecisionDeny from plugin) | yes — `tool_result` Summary | Internal | Policy block is an LLM signal to change strategy; user should see plugin message if anything, not this raw string. |
| 11 | `"EXECUTION_RULES_DENIED: %s — %s"` | `chat_tool_executor.go:240` | Agent execution-rules check fails | yes — `tool_result` Summary | Needs rewrite | Exposes internal sentinel prefix `EXECUTION_RULES_DENIED`; rephrase with clean copy. |
| 12 | `errMsg` from arg validator | `chat_tool_executor.go:267` | Tool input schema validation fails | yes — `tool_result` Summary | Internal | Raw JSON schema validation error text; useful to LLM for self-correction; potentially confusing to user. If arg errors are transient, classify Internal; if persistent, surface in developer mode. |
| 13 | `"Tool %q returned the same result %d times in a row, so the harness is holding further calls…"` | `chat_generate.go:1728` | `detectStuckLoop` threshold≥2 (blocks tool) | yes — `tool_result` Summary | Internal | Loop-detection halt. LLM guidance; pure plumbing to user. |
| 14 | `"\n\nNote: this tool has returned the same result %d times in a row…"` | `chat_generate.go:1734` | `detectStuckLoop` first repeat (warn, don't block) | yes — appended to `tool_result` Summary | Internal | Warning footnote appended to tool result; LLM-directed retry hint; invisible to users as-is but present in Summary field. |
| 15 | `"\n\n[TRUNCATED — full result cached as tool_result://%s (total_size=…). Use fetch_tool_result…]"` | `tool/cache.go:122` | Result exceeds soft truncation threshold (2 KiB default) | yes — part of `tool_result` Content | Internal | Pointer footer for LLM to retrieve more; meaningless to user; should be dev-mode visible only. |
| 16 | `"\n\n[TRUNCATED — result too large (%d bytes, exceeds hard cap %d). Only metadata was cached…]"` | `tool/cache.go:128` | Result exceeds hard cap (1 MiB default) | yes — part of `tool_result` Content | Needs rewrite | Hard-cap case: user should see "result too large to display" rather than internal byte counts and cap values. |
| 17 | `"[%d tool call/result blocks stripped during compaction]"` | `context/compaction.go:264` | `stageStripToolBlocks` during compaction pipeline | yes — injected as assistant text block in conversation | User-helpful | Visible in conversation history; explains why prior tool blocks are gone. Phrasing is acceptable. Confirm FE doesn't render it as-is in main chat. |
| 18 | `"Provider returned context-overflow; synchronously compacted %d stage(s)."` | `chat_generate.go:1291` | `slot_changed` Reasoning field on compaction-recoverable recovery | yes — in `slot_changed` Reasoning envelope field | User-helpful | Surfaced in `slot_changed` envelope `reasoning` field; consumed by FE context-panel card. Reasonable phrasing. |
| 19 | `"Provider request exceeded per-minute rate budget; synchronously compacted %d stage(s)."` | `chat_generate.go:1286+1291` | `slot_changed` Reasoning when rate-budget trigger | yes — in `slot_changed` Reasoning | User-helpful | Same as #18 but rate-budget trigger. Good copy. |
| 20 | `"Conversation slot exceeded budget; %d compaction stage(s) applied."` | `chat_generate.go:1499` | Pre-loop budget gate fires compaction | yes — in `slot_changed` Reasoning | User-helpful | Pre-loop path; wording slightly more technical ("slot") but acceptable. |
| 21 | `reason` field in `chat-loop-terminated` envelope | `chat_loop_terminated.go:20` | Loop exits abnormally (max_turns, runaway, compaction fail) | yes — in `plugin_envelope` stream event | Needs rewrite | Raw strings like `"context_overflow at stream start"`, `"rate_budget_exceeded at stream start"`, `"max_iter"` leak as-is. FE renders them; human copy needed. |
| 22 | `"IMPORTANT: You have no tools available in this session. Do NOT attempt to call any tools…"` | `chat_generate.go:34` | No tools in selection; prepended to system prompt | no — system prompt only, not streamed | Internal | Injected into system prompt (never the SSE stream); correctly Internal today. Listed for completeness; no action needed. |

---

## Classification summary

- **Internal:** 8 (entries #2, 3, 4, 5, 10, 13, 14, 15)
- **User-helpful:** 9 (entries #1, 7, 8, 17, 18, 19, 20, 22\*, 12\*\*)
- **Needs rewrite:** 5 (entries #6, 9, 11, 16, 21)

\* Entry #22 is already handled correctly (system prompt only); no SSE exposure.
\*\* Entry #12 (arg validator errors) leans Internal but benefits from developer-mode visibility given self-correction value.

---

## Currently missing: `Internal: true` flag

`chat.StreamEvent` has **no `Internal` field** today (`internal/chat/engine.go:72`). CW-20260419-0010 proposes adding one. Until that ships, all `tool_result`-type StreamEvents leak the `Summary` field to SSE consumers. The 8 Internal-classified messages above are the highest-priority adoption targets once the flag lands.

---

## Recommendations for I1 inspector (Phase 8)

- **Surface all `tool_result` StreamEvents in the dev panel**, not just errors — the bulk of LLM-internal traffic is classified Internal here (#2–5, 10, 13–15), and a developer debugging prompt-tuning needs to see every tool result the LLM received, including the JSON tool schemas from `request_tools` and stuck-loop mutations.
- **Add a `reason` decoder for `chat-loop-terminated` payload**: the `reason` field is machine-readable today (`"context_overflow at stream start"`, `"max_iter"`) and should map to human copy in the FE before Phase 8 ships — the inspector panel should show the raw value alongside the decoded copy so both audiences are served.
- **Show `slot_changed` Reasoning field in the inspector**: it is already human-ish (entries #18–20) but it lives inside an envelope that the FE may silently discard; the dev panel should always display it to make compaction behavior auditable without grepping logs.

---

## Follow-ups

- **Registry pattern** (`internal/chat/prompts/llm_messages.go` with named exports): scoped to Phase 8. All 22 messages cataloged here are candidates for centralization. A linter assertion (`go test -run TestLLMMessageRegistry`) could enforce that new LLM-facing strings are added to the registry rather than inlined at call sites.
- **`Internal: true` flag adoption** (CW-20260419-0010): once the flag exists, entries #2, 3, 4, 5, 10, 13, 14, 15 should be tagged in a single pass — straightforward mechanical change.
- **`PERMISSION DENIED` / `EXECUTION_RULES_DENIED` rewrite** (entries #6, 9, 11): three sites, all in `chat_tool_executor.go`. Low-lift; a single PR can clean all three sentinel-style prefixes.
- **`chat-loop-terminated` reason strings** (entry #21): define an enum of termination codes with human copy in the schema (`internal/envelope/schemas/chat-loop-terminated.schema.json`) so FE can decode without hardcoding strings.
