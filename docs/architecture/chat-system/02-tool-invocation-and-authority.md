# 02 · Tool Invocation & Authority

> **Scope:** how tools get registered, presented to the model, executed, and authorized. Covers the in-process tool-broker, `dispatch.ChatToolSurface`, override blocks, and what a chat agent actually sees in 2026-05.
>
> **Related:** [06 Permission](06-permission-and-authority.md) (the gates), [03 SSE Envelope](03-sse-envelope-and-interaction-protocol.md) (`tool_call`/`tool_result` events), [05 External Agent Execution](05-external-agent-execution.md) (subagent spawn is a tool).

## Purpose

Decide which tools the model sees this turn, render their descriptions with the right authority cues, run permission/concurrency checks, dispatch to MCP, and feed truncated results back into the message stream.

## Key files

- `internal/dispatch/role.go`, `internal/dispatch/execute.go` — dispatch surface
- `internal/mcp/manager.go` — MCP server registry + `Manager.Call`
- `internal/mcp/dev_tools.go:112-261` — `dev_*` tools + `resolveAllowed` + path-grant fallback
- `internal/mcp/self_tools_*.go` — built-in `nanite_*` self-tools
- `internal/mcp/vanta_descriptions.go:28-84` — Vanta tool description overrides for the chat surface
- `internal/service/chat_tool_executor.go` — `executeToolBatch`, `executeSingleTool`, parallel/serial dispatch
- `internal/service/tool.go` — `SelectForAgent`, `ToolSelection`
- `internal/toolclient/broker.go` — **in-process** `go-toolbroker` integration
- `internal/permission/path_grants.go` — path-grant authority

## Entry points

- Per-turn: `s.tools.SelectForAgent(agent, mode, sessionID)` → `ToolSelection{Tools, Progressive, Catalog, OverrideBlock}`.
- Per `tool_use` block: `s.preCheckTools` → `executeToolBatch`.
- `tool_use` with `Name == "request_tools"` → `handleRequestTools` (progressive discovery — agent asks for more tools mid-conversation).

## Happy path

1. **Select** `SelectForAgent` returns the tool list, a `Progressive` flag (whether to seed only essentials), `Catalog` summaries, and an `OverrideBlock` (composed by `go-toolbroker.ComposeOverrideBlock`).
2. **Compose prefix** `composeExtraSystemPrefix` builds the per-turn dynamic prefix:
   - no-tools warning (if `len(Tools) == 0`)
   - progressive-discovery catalog (if `Progressive`)
   - `nativeToolGuide` constant (`chat_generate.go:93-115`)
   - the broker's override block (per-tool hints, conflict resolution, examples)
3. **Provider call** `provider.StreamChat` with `Tools` populated.
4. **Tool use** Provider returns one or more `tool_use` `ContentBlock`s.
5. **Permission gate** `preCheckTools` checks:
   - profile permissions / `tools_allow_list` glob
   - `resolveAllowed` for `dev_*` (path allow-list root match → path-grant fallback → symlink-aware escape) — full chain in [06](06-permission-and-authority.md)
   - concurrency limits (parallel-safe vs serial)
6. **Notify-pause** for `dev_*` (skip `dev_grep`/`dev_glob`): emit `notify_pause` SSE, sleep 1.5s, then run.
7. **Execute** `executeSingleTool` → `MCPManager.Call(toolName, input)` → MCP server (in-process or remote via http_transport) → JSON result.
8. **Truncate** `truncate.OutputForModel` clips overly large outputs.
9. **Append** result as a `tool_result` `ContentBlock` on a new synthetic user-role message. SSE `tool_result` event fires.
10. **Loop** back to step 3 until `stop_reason != "tool_use"` (typically `end_turn`).

## Sad path

- **Permission denial** → `recordPermissionDenial` (`chat_loop_state.go:462`) increments consecutive failures. After `runawayFailCap=10` consecutive failures, harness emits `chat-loop-terminated` envelope ([04](04-chat-harness-and-loop-orchestration.md)).
- **Tool not found** → `tool_result` with `IsError=true`, content describes the miss. The model usually corrects on next turn.
- **MCP server unreachable** → `IsError=true`, content describes the transport failure.
- **`request_tools` collision** → progressive discovery returns the requested tool spec; if not in catalog, error result.
- **Truncation** → result body has a marker indicating bytes dropped.

## Logic gates

- **Phase 3 chat-surface deny-list is empty.** `dispatch.ChatToolSurface` was a deny-list filter that ran *after* tool selection to clamp what reached the LLM. It was removed; `grep "EnforceChatSurface\|ChatToolSurface\|IsChatSurfaceTool"` returns 0 hits in `internal/`. The chat agent now reaches: every `dev_*`, `web_fetch`, every `mcp__*`, all Vanta `memory_*`/`knowledge_*`/`context_*`, and the full `nanite_*` self-tool surface.
- **Strict mode is OFF by default** (commit 42a77a7 — "flip strict mode default off + drop chat-role tool-surface clamp"). Tool input schema strict-validation is not enforced; the model gets coerced inputs through.
- **Permission gates are now the only filter.** What the model sees = `tools_allow_list` glob (file/profile permissions) + path grants (gate at execution time, not visibility time). See [06](06-permission-and-authority.md).
- **Vanta tool descriptions are overridden for chat.** Default Vanta descriptions are SDK-shaped; `vanta_descriptions.go:34` swaps in chat-shaped phrasing (verb-led, generic paths, no `permission.HomeDir()` embedding).
- **Tool descriptions carry authority with the model.** Per the lessons doc: keep descriptions generic for paths (no embedding `permission.HomeDir()`); accept `~/` verbatim per `tildeAcceptanceNote()`. (See [06](06-permission-and-authority.md).)
- **Tool-broker is in-process.** `github.com/hollis-labs/go-toolbroker` is imported as a Go package. Not a sidecar process. Composes descriptions, runs hint enrichers, produces a per-turn override block.

## Tool surface today (chat agent)

| Class | Examples | Gate |
|---|---|---|
| `dev_*` | `dev_bash`, `dev_read_text_file`, `dev_write_text_file`, `dev_edit_*` | path allow-list + path-grant fallback + notify-pause |
| `web_*` | `web_fetch` | provider safety policy |
| `mcp__*` | every registered MCP server tool | per-server transport auth |
| Vanta | `memory_recall`, `memory_write`, `knowledge_*`, `context_*`, `conduit_lookup` | transport auth |
| `nanite_*` self-tools | `nanite_show_card`, `nanite_validate`, `nanite_tool_describe`, `nanite_message_send`, … | profile permissions |
| Subagent dispatch | `dispatch.ExecuteTask` (spawns child session) | H1 trust gate + dispatch role rules ([05](05-external-agent-execution.md)) |

## Current gaps

- **G-CACHE-RACE** — telemetry `cacheable_prefix_tokens` and rate-budget pre-flight estimate can mis-report under concurrent sessions because `prov` is a singleton and `SetCacheHints`/`EstimateCacheablePrefix` mutate shared state. Affects gating, not catastrophic. See [10](10-context-window-management.md) + [gaps.md](gaps.md#g-cache-race).
- ~~**G-PROGRESSIVE-ALLOW-LIST**~~ — **Closed 2026-08-15 (CW-20260815-0011).** The traced mismatch: `SelectToolsAsProvider` applied `MaxSelectedTools` (a hard 15-tool cap) internally, before `SelectForAgent`'s own `tools_allow_list` (schema-v2 `filterToolsByAllowlist`) filter ever ran downstream — so a tool could be present in `selection.Catalog`'s underlying candidate set yet get capped away before the allow-list check saw it, independent of whether the allow-list would have kept it. Fixed by moving the cap + token-budget prune into `ToolClient.FinalizeToolSelection`, called by `SelectForAgent` only after permission AND allow-list filtering are both done. See ADR-001's 2026-08-15 update for the full root cause.
- **G-STALE-TOOL-SURFACE-DOC** — this doc's "Tool surface today" table and the `mcp__*` naming in `## Logic gates` predate ADR-002's tool-name internalization: Nanite tools are agent-facing under uniform names (no `mcp__server__` prefix) and now include `torque_*`, `mux_*`, `tether_*`, and the broader connected-MCP catalog beyond Vanta. Flagged during the CW-20260815-0011 investigation; a full pass to bring this doc current is tracked separately, not done as part of that ticket.

## Test surface

- Mock `provider.Provider` returning fixed `tool_use` blocks.
- Mock `ToolService` and `MCPManager.Call`.
- Real `permission.PathGrants` (path-grant interactions are too load-bearing to mock).
- Fixtures: progressive-discovery flow (`request_tools`), Vanta description override, `dev_bash` notify-pause timing, parallel batch with one denied + one allowed.
