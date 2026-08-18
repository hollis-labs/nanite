# Tool Calling & MCP Integration

## 1. Purpose

This subsystem answers two questions for every chat turn: *what tools does the agent know about right now*, and *what actually happens between the LLM emitting a `tool_use` block and a `tool_result` coming back*. Tool provenance is heterogeneous — four in-process "builtin" Go transports, zero or more persisted third-party MCP servers (stdio/HTTP), zero or more plugin-registered MCP servers (subprocess JSON-RPC), and an optional Vanta HTTP server — and all of it is flattened into one uniform, uncontextualized namespace the LLM sees as a flat list of `tool_name`/`description`/`input_schema` triples. Everything downstream of that flattening (selection, permissioning, trust-tiered validation, execution, truncation/caching) operates on the uniform name only; the originating server is retained solely as audit metadata.

## 2. Key entry points/files

- `internal/mcp/manager.go` — `Manager`: server registry, `uniformIndex` (uniform name → tool entry), `DiscoverTools`, `assignUniformNameLocked` (collision policy), `ExecuteTool` / `ExecuteToolOnServer`.
- `internal/mcp/naming.go` — `UniformToolName`, `DisambiguatedToolName`, `IsReservedSelfToolName`, `IsFirstPartyBuiltinServerName` (the four-server closed set).
- `internal/mcp/validate.go` — `TrustTier` enum, `LimitsFor` (per-tier ceilings), `ValidateToolMeta`/`ValidateToolSet` (discovery-time), `ValidateBlockType`/`StripANSI`/`ScanInjection`/`ValidateResultSize` (execution-time).
- `internal/mcp/self_tools*.go`, `dev_tools.go`, `general_tools.go`, `code_exec_tools.go` — the four in-process builtin `MCPTransport` implementations (servers `self`, `dev`, `general`, `code`).
- `internal/mcp/stdio_transport.go`, `http_transport.go`, `plugin_transport.go` — the three external transport kinds (subprocess, HTTP, plugin JSON-RPC).
- `internal/toolclient/broker.go` — `ToolClient`: `SelectToolsAsProvider`, `FinalizeToolSelection`, `CallTool`/`CallToolWithPolicyCheck`, `CheckPermission`, the `developer_mode` dev-tool gate.
- `internal/toolclient/config.go` — `NaniteDefaultRules`/`DefaultConfig` (the tool-broker rule set actually in effect).
- `internal/toolclient/meta_tools.go` — definitions for `request_tools`, `fetch_tool_result`, `search_tool_result`.
- `internal/toolclient/permissions.go`, `enricher.go` — `ToolPermissions.CheckPermission`; `storeEnricher` (reads `tool_enrichments`).
- `internal/service/tool.go` — `ToolService.SelectForAgent` (assembles the per-turn tool set), progressive-discovery threshold, C2 auto-repair pipeline (`attemptRepair`).
- `internal/service/chat_tool_executor.go` — `preCheckTools` / `executeToolBatch` / `postProcessToolResults` / `handleResultCacheMetaTool` (the execution pipeline the main chat loop drives).
- `internal/tool/cache.go` — `ResultCache` (DB-backed `tool_result_cache`, the cache-and-pointer mechanism).
- `internal/truncate/truncate.go` — file-based fallback truncation for results the cache doesn't handle.
- `internal/dispatch/chat_surface.go` — `ChatToolSurface` (chat-role-only exclusion list).
- `internal/plugin/registrations.go`, `config.go` — declarative `plugin.yaml registers.mcp_servers[].tools` → `Manager.AddPluginServer`.
- `internal/store/mcp_servers.go`, `tool_enrichments.go`, `agent_known_tools.go` — persistence.
- `cmd/nanite/main.go` `initMCP()` — boot-time wiring of all server sources.
- `docs/tool-naming-convention.md`, `docs/tool-naming-audit.md`, `docs/mcp-trust-model.md` — the naming rule, the per-tool verdict table, and the trust-tier table.

## 3. Flow

### 3.1 Boot-time registration (building the raw server set)

`initMCP()` in `cmd/nanite/main.go` builds one `mcp.Manager` and registers servers from four sources, in this order:

1. **Four in-process builtin transports**, each `mcpManager.AddServer(name, transport, mcp.TierBuiltin)`: `dev` (filesystem/shell), `general` (encoding/math/web-fetch utilities), `code` (sandboxed code exec), `self` (`mcp.SelfServerName` — scratchpad, messaging, skills, subagent spawn, UI signals, ~60 tools). These are Go structs instantiated directly in `main.go`; there is no config file that lists them.
2. **Vanta** (optional): `registerVantaServer` adds an HTTP server named `vanta` (configurable) at `TierPluginHTTP` by default if `cfg.Vanta.URL` is set, with a Bearer token from env or config.
3. **Persisted third-party servers**: `loadPersistedMCPServers` reads every enabled row from the `mcp_servers` table and calls `AddStdioServer` or `AddHTTPServer` with the row's stored `trust_tier` (defaults to `third_party_http` per the D4 fail-closed rule — every DB row is assumed third-party unless an operator explicitly raised it).
4. **Plugin-registered servers**: subprocess plugins declare `registers.mcp_servers` in `plugin.yaml`; `registerManifestMCPServers` calls `Manager.AddPluginServer`, which always registers at `TierPluginStdio` (hardcoded — plugin servers are subprocess-backed by definition). Builtin plugins are explicitly skipped here (a comment notes builtins register MCP servers directly, not through the manifest path) — no `plugin.yaml` currently in this repo actually populates `registers.mcp_servers`, so this path is exercised only by real subprocess plugins, not anything checked into this codebase today.

Separately, `ToolClient.Builtins` (a *different* registry from `Manager`'s server map) is populated by explicit `RegisterBuiltins("dev", ...)`, `RegisterBuiltins("self-service", ...)`, and `RegisterBuiltins("result-cache", ...)` calls in `initMCP()`. This second registry exists so `ToolClient.SelectToolsAsProvider` can always prepend builtins even if the broker/manager path returns nothing, and so `IsBuiltinTool` can classify a uniform name as builtin-vs-MCP-origin without a name-prefix signal (post-ADR-002 there is no prefix left to sniff).

### 3.2 Discovery — from per-server tool lists to one uniform namespace

`Manager.DiscoverTools` runs once at boot (via `AutoDiscover`) and iterates every registered server **in sorted server-name order** — this ordering is deliberate and load-bearing: collision resolution is a function of registration order, so sorting makes it reproducible across runs. For each server:

1. Call `transport.ListTools(ctx)`.
2. Run `ValidateToolSet(tier, tools)` — cross-tool checks (tool-count-vs-tier-ceiling is advisory-only; duplicate names within the same server are dropped).
3. Per tool, run `ValidateToolMeta(tier, tool)` — name charset/length, description length, schema byte size, all keyed to the server's trust tier. Any failure drops the tool with a `DiscoveryWarning`.
4. Call `assignUniformNameLocked(serverName, toolName)` to compute the agent-facing name and resolve collisions (see §3.3).
5. Insert a `*toolEntry{serverName, uniformName, tool}` into both `m.tools` (a pointer slice) and `m.uniformIndex` (uniform name → same pointer).
6. Register every accepted tool with the `go-toolbroker` `LocalBroker` under its uniform name, carrying `Server` as audit-only metadata.

### 3.3 Naming and collision resolution

The agent-facing name is normally just the tool's bare name (`UniformToolName` strips a stray legacy `mcp__server__` prefix if present, otherwise passes through unchanged). Collisions are resolved by `assignUniformNameLocked` in this order:

1. **Reserved-namespace defense** — if the newcomer is one of the four first-party builtin servers (`self`/`dev`/`code`/`general`, tested by `IsFirstPartyBuiltinServerName`), it *always* wins the bare slot. If a non-builtin server already sits on that slot, the incumbent is rewritten in place to its disambiguated form (`<server>_<tool>`) and a `uniform_name_collision_disambiguated` warning is logged.
2. **Reverse case** — if the newcomer is non-builtin and the bare slot is already held by a builtin, the newcomer is force-prefixed (`<server>_<tool>`) instead.
3. **Default** — if the slot is free, the newcomer gets the bare name.
4. **Ordinary collision** (two non-builtin servers) — both incumbent and newcomer are rewritten to their disambiguated forms.
5. **Hard collision** — if a disambiguated slot is *also* taken, the newcomer tool is dropped entirely with a `uniform_name_collision` warning.

The written convention (`docs/tool-naming-convention.md`) layers a naming discipline on top of this mechanism: use `<concept>_<verb>`, add a provider prefix only on a *real* collision (not speculatively), and reserve `nanite_*`... except that reservation was itself superseded — a 2026-05-01 rename arc (documented in `docs/tool-naming-audit.md` Appendix A) stripped the `nanite_` prefix from ~64 self-tools and rebased the reserved-namespace defense from a name-prefix check to a server-identity check (`server == "self"`), because a prefix-based defense breaks the moment the prefix is removed.

### 3.4 Per-turn tool-set assembly (`ToolService.SelectForAgent`)

Every chat turn (`internal/service/tool.go`) re-derives the tool set the LLM will see, in this order:

1. `extractIntent(userMessage)` — a crude keyword extractor (lowercase, strip punctuation, drop stopwords, join up to 3 keywords) produces an "intent" string and hint list. This is **not** semantic; it is literally the user's own words.
2. `ToolClient.SelectToolsAsProvider(ctx, intent, hints, workspaceID, agentID)`:
   - Loads the broker rule set for this workspace/agent (`Config.RulesFor`). In production this is `NaniteDefaultRules()` — a single hardcoded catch-all rule (`Intent:"*"`, `Match:"*"`, `Action:include`) — because `toolclient.LoadConfig` (which would load rules from a YAML file) is defined but never called anywhere in the codebase. Every real selection therefore returns the entire connected catalog from the broker layer; narrowing happens entirely in the layers listed below.
   - Prepends builtins from `ToolClient.Builtins`, applying the `developer_mode` dev-tool gate (any `dev_*` tool is stripped unless `user_settings.developer_mode` is true) and `CheckPermission`.
   - Appends broker-selected MCP tools, same two gates applied.
3. If the broker returned zero MCP-origin tools (detected via `IsBuiltinTool` classification, since there's no name-prefix signal left), fall back to `discoverAgentMCPTools` — a *direct* `DiscoverServerTools` call against the agent's `agent.MCPServers` JSON list, bypassing the broker/collision machinery entirely for that path.
4. `filterToolsByPermissions` — re-applies `CheckPermission` to every tool in the assembled list (closes the gap left by the direct-discovery fallback, which never went through step 2's gate).
5. `filterToolsByAllowlist` — the agent profile's `tools` JSON column (pattern-matched via `MatchPattern`).
6. `applyChatSurfaceFilter` — **only** when `agent.Slug == "default"` (the chat-role profile): drops `tool_describe`, `tool_validate`, `lesson_capture`, `card_show` (`dispatch.DefaultChatToolSurface()`). Every other profile (Worker, Planner, executor, hint-selector, mux-orchestrator) bypasses this filter.
7. `ToolClient.FinalizeToolSelection` — applies `MaxSelectedTools` (15) and a token-budget prune (`ToolTokenBudgetPct`, default 20% of the context window). This step is deliberately **last**, after permission/allowlist filtering — an earlier design applied the cap before allowlist filtering and could truncate out a tool the agent's own allowlist explicitly permitted.
8. `ToolClient.RenderDescriptions` — an opt-in per-call description-rewrite hook (`describer.Registry`) for a small set of tools (`task_execute`, `tool_list`, `skill_list`) whose description depends on the calling agent's identity/dispatch allowlist.

If the resulting MCP-origin tool count exceeds `ProgressiveDiscoveryThreshold` (10), the turn switches to **progressive discovery**: only builtins + the `request_tools` meta-tool are sent, and a compact `name: truncated-description` catalog string (`chat.BuildToolCatalog`) is injected into the system prompt instead of full schemas. The agent calls `request_tools({tool_names: [...]})` or `request_tools({intent: "..."})` mid-turn to hydrate full schemas for specific tools before calling them.

### 3.5 Execution path — from `tool_use` to `tool_result`

```mermaid
sequenceDiagram
    participant LLM
    participant Loop as chat_generate.go main loop
    participant Pre as preCheckTools
    participant Batch as executeToolBatch
    participant TSvc as ToolService.Execute
    participant TC as ToolClient.CallTool
    participant Mgr as mcp.Manager.ExecuteTool
    participant Xport as MCPTransport (builtin / stdio / http / plugin)
    participant Cache as ResultCache (tool_result_cache)
    participant Post as postProcessToolResults

    LLM->>Loop: assistant turn with tool_use block(s)
    Loop->>Pre: toolUseBlocks[]
    Pre->>Pre: blocked/exhausted check (loop + count-cap detectors)
    Pre->>Pre: permission.Check -> allow / deny / ask(approval UI)
    Pre->>Pre: plugin pre-hook "tool.executing" (may cancel)
    Pre->>Pre: enforceExecutionRulesForTool + arg-schema validate
    Pre-->>Batch: toolPlan{ready|blocked|denied|meta}
    Batch->>Batch: split concurrent-safe vs serial (by tool-name heuristic)
    Batch->>TSvc: Execute(agentID, toolName, input)
    TSvc->>TC: CallTool(agentID, toolName, input)
    TC->>TC: dev-tool gate (developer_mode) + CheckPermission (2nd time, execution backstop)
    TC->>Mgr: ExecuteTool(uniformName, input)
    Mgr->>Mgr: uniformIndex[uniformName] -> {server, originalToolName, tier}
    Mgr->>Xport: CallTool(originalToolName, input)
    Xport-->>Mgr: ToolResult{Content[], IsError}
    Mgr->>Mgr: ValidateBlockType (drop unknown types)
    Mgr->>Mgr: StripANSI + ScanInjection (log-only) per text block
    Mgr->>Mgr: ValidateResultSize(tier) defense-in-depth cap
    Mgr-->>TC: assembled text (or tool-error)
    TC-->>TSvc: output
    TSvc->>TSvc: on recoverable error: C2 auto-repair (1 LLM-assisted retry, hard cap)
    TSvc-->>Batch: ToolResult{Output, IsError}
    Batch-->>Post: raw output per tool
    Post->>Post: detectStuckLoop (same-result repetition guard)
    Post->>Cache: StoreResult(sessionID, toolCallID, body) if body > 2 KiB soft threshold
    Cache-->>Post: truncated preview + "tool_result://<ULID>" footer (if cached)
    Post->>Post: truncate.OutputForModel fallback (non-cached / error / exempt tools)
    Post-->>LLM: tool_result content block appended to next turn
    Note over LLM,Cache: LLM may later call fetch_tool_result / search_tool_result with the ULID to page/search the full cached body
```

Notable details visible only at this layer:

- **The permission gate fires twice** by design: once at selection time (a tool the agent isn't permitted to use is never shown a schema for) and again at execution time inside `ToolClient.CallTool` — the code comment calls this the "load-bearing backstop" in case a name slips past the description filter.
- **`request_tools`, `fetch_tool_result`, `search_tool_result` are exempt from the per-tool call cap** (`toolclient.MetaToolNames()`), and the latter two are handled entirely inside `chat_tool_executor.go` (`handleResultCacheMetaTool`) — they never go through `ToolService.Execute` or `mcp.Manager` at all; they read directly from `ResultCache`.
- **Two independent truncation mechanisms exist.** `internal/tool.ResultCache` (DB-backed `tool_result_cache`, soft-truncate 2 KiB / hard-cap 1 MiB / TTL) is tried first for any non-error, non-scratchpad, non-cache-exempt result; if the cache doesn't apply (error results, scratchpad tools, cache-exempt tools like `tool_describe`), the older file-based `internal/truncate` package applies instead (writes the full body to `~/.nanite/tool-output/*.txt` for operator debugging, shows the LLM a 4 KB static or per-model-dynamic-up-to-32 KB preview, but — per an explicit 2026-04-19 fix — does **not** surface the on-disk file path to the LLM, because the LLM was previously mistaking it for a `fetch_tool_result` id).
- **Error results are exempt from both truncation and caching** — the code comment traces this to a real incident (a truncated error message caused the agent to hallucinate "I don't have access to a memory recall tool").
- **Concurrency safety is inferred from tool-name suffix/substring heuristics** (`ToolMetaInfo.IsConcurrencySafe` in `service/tool.go`: `_read`/`_list`/`_get`/`_search`/`_glob`/`_grep` suffixes and `web_fetch`/`web_search` substrings are read-only/parallel-safe; `delete`/`remove`/`shell`/`bash`/`drop`/`reset` substrings are destructive), not from any declared tool metadata.
- **The C2 auto-repair pipeline** (`attemptRepair` in `service/tool.go`) sits between a recoverable tool error and the agent seeing it: on a classified-recoverable error (schema mismatch, missing required field, etc.), a small LLM call reshapes the arguments and retries **once** (hard cap, no nested repair), gated by an env var, a `RepairConfig` wiring, and a user preference (`auto_repair_pref`). This is directly relevant to "wrong tool call" symptoms — a malformed call from the primary agent may be silently corrected before the agent ever sees the original error.

## 4. The naming/collision problem, concretely

Commit `5144590` ("fix: protect all first-party builtin MCP tools from proxy name collisions") is a concrete instance of this bug class. Prior to the fix, the reserved-namespace defense in `assignUniformNameLocked` only protected the `self` server's bare-name slot (`IsReservedSelfToolName`). Once the Agent Mux proxy started returning `loom`/`fragments-engine` tools that ship the *same generic scaffold vocabulary* as nanite's own builtins (`dev_bash`, `dev_read`, etc.), the deterministic sorted-server-name iteration in `DiscoverTools` put `"Agent Mux"` alphabetically before `"dev"`. Because `dev` had no reserved-namespace protection, the proxy's tool won the bare `dev_bash` slot at registration time, and nanite's own `dev` server was never renamed to a disambiguated form (there was no logic to force-prefix an unprotected builtin when it lost a bare-slot race) — it simply became unreachable: any agent calling `dev_bash` was silently routed to the wrong tool, and any agent that later tried to reach the *original* `dev` tool by its old bare name got `"unknown MCP tool: dev_bash"`.

The fix (`IsFirstPartyBuiltinServerName`, generalizing the self-only check to the closed four-name set `self`/`dev`/`code`/`general`) is deliberately **name-keyed, not tier-keyed** — the doc comment explains that test fixtures legitimately register hostile/arbitrary servers at `TierBuiltin` to exercise collision behavior, so trust tier alone cannot distinguish "one of nanite's real first-party builtins" from "some other server that happens to carry that tier." This is a subtle point: trust tier and namespace-ownership are two different axes that happen to overlap for the real builtins but must be checked independently.

A second, unrelated bug was found *while verifying* the first fix and fixed in the same commit: `Manager.tools` was a value slice (`[]toolEntry`). A collision-rename mutates an entry in place through a pointer taken at insertion time (`&m.tools[i]`); if a later `append` reallocated the backing array, that pointer would be silently orphaned, so the rename would "stick" in `uniformIndex` (used for execution routing — unaffected) but silently revert in the `m.tools` slice (used for the tool listing / broker-registration view) — meaning a renamed tool's *listing* could show stale information even though execution routed correctly. Fixed by converting `Manager.tools` to `[]*toolEntry`.

Two regression tests were added: one reproduces the exact production ordering ("Agent Mux" before "dev") and asserts execution still routes to the real builtin; the other forces slice growth past a collision point to prove the listing view doesn't go stale.

## 5. Data model touched

- **`mcp_servers`** (`internal/store/mcp_servers.go`, base schema in `001_schema.sql`, trust columns added in `012_mcp_server_trust.sql`) — persisted third-party/catalog-installed server configs only. Columns: `name`, `transport_type` (`stdio`/`sse`), `command`/`args`/`env` (stdio) or `url` (HTTP), `enabled`, `trust_tier` (defaults `third_party_http` — every row is fail-closed third-party by construction; builtin and plugin servers never get a row here, they're registered purely at runtime), `env_allowlist` (JSON array of host env-var names a stdio subprocess may inherit — defaults to `[]`, nothing inherited).
- **`tool_enrichments`** (`022_tool_enrichments.sql`) — `tool_name` (PK) → `hints_json` (a serialized `broker.Hints` struct) → `updated_at`. Read at selection time via `toolclient.storeEnricher`/`GetToolEnrichment`, feeding the broker's per-tool "Tool Overrides" markdown block appended to the system prompt. **No runtime code path currently writes to this table** — `Store.UpsertToolEnrichment` has no callers outside its own file and tests; the migration's own comment says it's meant to be populated "by admin tooling or the probe agent," neither of which appears to exist in this codebase yet. The table is schema-ready but effectively dormant.
- **`tool_result_cache`** (`011_tool_broker_execution.sql`, `sort_order`/TTL tuning in `077_...sql`) — `id` (ULID PK), `session_id`, `tool_name`, `tool_call_id` (the LLM's `tool_use_id`), `created_at`/`expires_at`, `byte_size`, `was_truncated`, `body` (nullable — NULL when the result exceeded the 1 MiB hard cap, metadata-only). Scoped by `session_id` on every read (`Fetch`/`Search`) to prevent cross-session access via a leaked/guessed ULID. TTL default was raised from 3600s to 31536000s (one year) in migration 077 with the rationale that durable agents need session-scale retention, not a one-hour window.
- **`agent_known_tools`** (`068_per_agent_state.sql`, `sort_order` added in `077_...sql`) — PK `(agent_id, tool_name)`: `pinned`, `activation_count`, `last_used_at`, `added_at`, `ttl_seconds` (nullable = immortal), `reason`, `sort_order` (operator-authored, backfilled from `agent_profiles.role_tools` array order). Notably, `BumpActivation` — originally meant to increment `activation_count`/`last_used_at` on every tool call — was made a **deliberate no-op** (FU-14, 2026-05-20): the per-call churn was re-sorting the known-tool block every turn, changing its byte order, and invalidating Anthropic's prompt cache (observed as `cache_read=0` every other turn plus degenerate echo output in a live shadow run). The sort is now static, seeded from a hardcoded `roleSeedPriority` table; real usage telemetry still lands in `event_log` (`event_type='tool_call'`) for offline tuning instead. A sibling table `agent_known_tools_reaper.go` sweeps non-pinned, TTL-expired rows periodically.
- **`agent_profiles.tool_permissions` / `.tools` / `.role_tools` / `.parent_dispatch_allowlist`** — four separate JSON columns on the agent profile, each independently narrowing or seeding the tool surface (deny/allow list, exact-match allowlist, boot-time known-tool seed, and dispatch-permission list for the describer hook, respectively). Not new tables, but part of the same data model.

## 6. Configuration & manual-setup points

- **Adding a new first-party builtin tool** means writing Go code in `internal/mcp/*_tools.go` and wiring the transport into `initMCP()` in `cmd/nanite/main.go` — there is no config-driven registration path for builtins.
- **The first-party reserved-namespace list is a closed, hardcoded four-name switch** (`IsFirstPartyBuiltinServerName` in `naming.go`: `self`/`dev`/`code`/`general`). A hypothetical fifth in-process builtin transport would need this switch updated by hand or it gets no collision protection — exactly the bug class commit `5144590` fixed for the first four.
- **`ToolClient.Builtins` is a second, parallel builtin registry**, populated by explicit `RegisterBuiltins(...)` calls in `initMCP()` (currently three groups: `"dev"`, `"self-service"`, `"result-cache"`), independent of `mcp.Manager`'s server map. Nothing in the code enforces that this list and the Manager's builtin server set stay in sync.
- **Third-party MCP servers** are added via `POST /api/mcp-servers` (persisted to `mcp_servers`) or a catalog install, at the fail-closed `third_party_http` tier by default. Per `docs/mcp-trust-model.md`, there is no API endpoint to raise a server's trust tier yet — the documented supported path is a direct `UPDATE mcp_servers SET trust_tier = ...` SQL statement followed by a full nanite restart.
- **Plugin tools are declared statically** in `plugin.yaml`'s `registers.mcp_servers[].tools` list; the `mcp/list_tools` JSON-RPC method is auto-served from that manifest (a plugin can intercept it manually for a dynamic catalog, documented as "advanced, rare"). No `plugin.yaml` in this repository currently populates `registers.mcp_servers`.
- **The broker rule set is effectively unconfigurable in production today.** `toolclient.NaniteDefaultRules()` is a single hardcoded catch-all (`intent:"*"` → include everything already registered); `toolclient.LoadConfig`, which would load a custom rule YAML, exists but is never called from `cmd/nanite/main.go`. All real narrowing happens downstream (permissions, allowlist, chat-surface filter, dev-mode gate, cap/prune, progressive discovery) rather than at the broker-rule layer the code was originally designed around.
- **The chat-role-only exclusion list** (`dispatch.DefaultChatToolSurface()`: `tool_describe`, `tool_validate`, `lesson_capture`, `card_show`) is a hardcoded Go map keyed to the exact profile slug `"default"`. Adding a fifth "lens primitive" tool that should be chat-hidden requires editing this Go literal.
- **The `developer_mode` dev-tool gate is duplicated across two independent constants**: `toolclient.DevServerName` (`"dev"`, in `broker.go`) and `mcp.DevServerName` (`"dev"`, in `naming.go`) are separate Go constants in separate packages that happen to hold the same string today; nothing ties them together beyond that coincidence.
- **`tool_enrichments` rows must be inserted by hand** (direct SQL, since `UpsertToolEnrichment` has no runtime caller) — the "admin tooling or probe agent" the migration comment anticipates does not appear to exist yet.
- **Per-agent tool surface configuration is spread across four separate JSON columns** on `agent_profiles` (`tools`, `tool_permissions`, `role_tools`, `parent_dispatch_allowlist`), each parsed and applied independently in `SelectForAgent`, with no single place that shows "the effective tool set for this agent."

## 7. Cross-references

- **provider-llm-roundtrip** — how `llmtypes.ToolDefinition` gets serialized into the actual Anthropic/OpenAI/etc. request payload, and how `tool_use`/`tool_result` blocks map onto each provider's wire format.
- **chat-engine-orchestration** — the main loop in `internal/service/chat_generate.go` that calls `SelectForAgent` before slot assembly and drives `preCheckTools`/`executeToolBatch`/`postProcessToolResults` each turn; also owns session-mode `tool_overrides`, the stuck-loop/count-cap detectors, and the per-turn cache-marker/prefix-stability concerns referenced above.
- **plugin-system** — the full `plugin.yaml` manifest schema (`registers.*`), the subprocess JSON-RPC transport, `LoadType`/`ToolOverrides` opt-in tool loading, and plugin lifecycle (load/unload, `RemoveServersByPlugin`).
- **reflexes-internal-tooling** — `self_tools.go`'s reflex set (`ReflexSet`/`ReflexLogger`), which sits alongside but is architecturally distinct from the MCP tool-calling path proper.

## 8. Open questions

- Two parallel "builtin" registries exist (`mcp.Manager`'s server map vs. `ToolClient.Builtins`) with independent registration call sites in `main.go` and no enforced invariant that they describe the same tool set — unclear whether this is intentional layering or historical accumulation.
- The `developer_mode` dev-tool gate depends on a name-prefix check (`isDevTool`: `strings.HasPrefix(toolName, "dev_")`) sourced from two independently-defined `DevServerName` constants in two packages. It is not obvious what happens if a third-party or plugin server ever legitimately registers a tool whose uniform name happens to start with `dev_` (the collision-disambiguation logic would normally prevent a bare-name collision with the real `dev` server, but the gate itself matches on string prefix, not on server attribution).
- `tool_enrichments` has full CRUD in the store layer, a working read path (`storeEnricher`), and a migration comment describing its intended write path — but no runtime writer exists in the codebase. It's unclear whether this is a genuinely unfinished feature (admin UI / probe agent not yet built) or a deprecated design that the read path should also be removed from.
- The broker's rule-based selection layer (`go-toolbroker`, `NaniteDefaultRules`) is, by the code's own doc comments, currently a no-op relative to its original design intent (a single catch-all rule always fires; `LoadConfig` for custom rules is unreachable from boot). All real selection narrowing happens in application-layer filters bolted on after the broker call. It's unclear whether the broker abstraction is still earning its complexity given this, or whether custom-rule loading is simply an unfinished wiring gap.
- `SelectForAgent` reconstructs the entire tool set from scratch every turn (intent extraction → broker call → multiple filter passes → cap/prune → per-call description render). There's no visible caching of this per-agent computation across turns within a session, beyond whatever prompt-caching benefit comes from the resulting tool array being byte-stable (which `agent_known_tools`'s `BumpActivation` no-op was specifically introduced to preserve).
- The concurrency-safety classification used to decide which tools execute in parallel vs. serially (`GetToolMeta` in `service/tool.go`) is entirely name-heuristic (suffix/substring matching on `_read`, `delete`, `bash`, etc.) rather than derived from any declared tool metadata (MCP tool definitions have no first-class "safe to parallelize" field). A tool whose name doesn't match any listed pattern defaults to `IsConcurrencySafe = false` (serial), but a tool named misleadingly (e.g., a destructive action ending in `_get`) would be misclassified.
- Progressive discovery's `request_tools` catalog description is hard-truncated to 80 characters per tool (`chat.BuildToolCatalog`) with no indication in the catalog of which tools are read-only vs. destructive, or which server they came from — the agent choosing among a name+80-char-description list is one plausible contributor to "wrong tool" selection when many similarly-named tools from different servers/concepts are all summarized this tersely.
- Trust tier and first-party-builtin-namespace status are explicitly documented (in the `IsFirstPartyBuiltinServerName` doc comment) as two independent axes that happen to align for the four real builtins today. Any future code path that conflates "is `TierBuiltin`" with "is a first-party self-tool" would reintroduce a variant of the collision bug commit `5144590` fixed.
