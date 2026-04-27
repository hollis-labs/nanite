# ADR-002: MCP Internalization — Uniform Tool Registry via Runtime Adapter

**Date:** 2026-04-26
**Status:** Accepted
**Deciders:** Chrispian (locked Option A in CW-20260426-0013), Claude (nanite-backend; CW-20260427-0017)
**Related:** Phase 5 — `feat/arch-seq-phase-5-broker-strategy`

---

## Context

Pre-internalization, every tool published by an MCP server reached the LLM under a name shaped `mcp__<server>__<tool>` (e.g. `mcp__mux__memory_write`, `mcp__engine__task_create`). The prefix served two purposes: it disambiguated tools across servers, and it gave dispatch / permission code a name-prefix signal to identify MCP-origin tools. Three problems compounded:

1. **Token bloat in tool descriptions.** Every prefix occupies ~14 characters that the agent never uses.
2. **LLM confusion.** Long, segmented names increase mis-call rate; the agent often elides the prefix and the runtime then has to recover via `MCPManager.ResolveToolServer`. That recovery path was a permission-bypass surface (an agent could call `dev_bash` to evade a `mcp__dev__*` deny rule, which the codebase had to patch by re-checking the resolved name).
3. **Surface enforcement coupling.** `dispatch.ChatToolSurface` rejected `mcp__*` by name pattern, mixing the "name shape" signal with the "what's allowed on Chat" decision.

Opencode-pattern adoption (full internalization) was raised, gated, and locked Option A on 2026-04-26 (Vanta `decisions_d_mcp_full_internalization`). The user's note was: *"reducing the prefixing was already on my list because it's so annoying and long. We can always add a layer to solve any issues if they arise."* Pre-launch latitude was explicitly granted; backwards-compat shims are forbidden.

### Alternatives considered

**A. Codegen — manifest-driven wrap layer.**
A YAML/JSON manifest enumerates every MCP server + tool the codebase knows about; a build-time generator emits Go code mapping uniform name → (server, tool). Stable, debuggable, but requires a manifest pipeline we don't have. MCP servers are dynamic (third-party HTTP, plugin subprocesses, runtime-discovered tools); the manifest would be perpetually drifted from reality. Rejected as a poor fit for the runtime nature of MCP.

**B. Runtime adapter — uniform-name index built at discovery time (chosen).**
`mcp.Manager` already enumerates every tool in `DiscoverTools(ctx)`. Extend that pass to assign each tool a uniform agent-facing name and store the mapping in a `uniformIndex map[string]*toolEntry`. All agent-facing surfaces (`GetAllTools`, `GetToolsForIntent`, broker registration) emit uniform names; `ExecuteTool` looks up the (server, tool) pair via the index. Server attribution is preserved on the entry struct so audit / telemetry retains source-server context. The wrap is co-located with the existing `internal/mcp` and `internal/toolclient` seams — no new package, no codegen pipeline. Pre-launch latitude lets us drop the legacy prefix entirely; a future mitigation layer can be added if specific issues surface (e.g., a third-party MCP whose tool names collide with first-party).

**C. Keep the legacy prefix; ship a "display name" overlay only.**
Rejected. The agent still sees the prefix in its tool list; the bypass / token-bloat / dispatch-coupling problems remain.

---

## Decision

**Adopt Option B.** Every MCP tool is registered under a uniform agent-facing name with no `mcp__server__` prefix. Server attribution is preserved as audit metadata via `Manager.ToolAttribution(uniformName) → (server, originalTool)` and on telemetry spans (`nanite.mcp.server`, `nanite.mcp.tool`, `nanite.mcp.uniform_name`).

### Mechanics

**Naming convention** (in `internal/mcp/naming.go`):

- **Default rule.** `UniformToolName(server, tool) = tool`. Strip any stray legacy `mcp__<server>__` prefix defensively (e.g. an MCP server that mis-reports its own name).
- **Collision policy.** When two servers publish the same `tool` name, both get rewritten to `<server>_<tool>` (single underscore). The bare slot is freed; both entries occupy disambiguated slots. Detection happens at registration time (`Manager.assignUniformNameLocked`) in deterministic server-name order so resolution is reproducible across runs. Each disambiguation logs a `WARN` and emits a `DiscoveryWarning{Reason: "uniform_name_collision_disambiguated"}`.
- **Reserved namespace defense.** `nanite_*` is reserved for first-party self-tools. If an MCP server publishes a tool whose bare name lives in `nanite_*`, it is force-prefixed to `<server>_<tool>` regardless of any collision. Defensive-only — the audit set today (mux + plugins + dev/general/code/self builtins) has no clashes.
- **Hard collision** (both bare and disambiguated slots taken — only possible when a server's tool name embeds another server's name) drops the tool with a `DiscoveryWarning{Reason: "uniform_name_collision"}` and a `WARN` log.

**Execution paths:**

- `Manager.ExecuteTool(ctx, uniformName, input)` — agent-facing entry. Looks up `(server, tool)` via `uniformIndex` and dispatches. Unknown name → `unknown MCP tool: <name>` error.
- `Manager.ExecuteToolOnServer(ctx, server, tool, input)` — explicit-server entry for internal callers (`chat.Orchestrator`, `contextbroker.ConduitSource` / `EngineSource`, `HadronBlueprintGate`) that already know which server they want. Bypasses the uniform index.

**Result-handling pipeline** (ANSI strip → injection scan → block validation → tier size cap) is shared between both execution paths via `assembleToolResultText`.

**Surface enforcement** (`internal/dispatch/role.go`):
The Chat surface guarantee is preserved but its mechanism shifts. Pre-internalization, the surface was "exact prefix match against `nanite_*` / meta-tools, AND reject `mcp__*` by pattern". Post-internalization, it is just "exact prefix match against `nanite_*` / meta-tools". MCP-origin tools (uniform names like `task_create`, `memory_write`, `clockwork_task_get`) don't share any allow-list prefix and are filtered automatically. `role_test.go` fixtures updated to assert the new names.

**Permissions** (`internal/toolclient/permissions.go`):
`MatchPattern` is naming-agnostic. The historical bare-vs-prefixed double-check in `ToolClient.CallTool` is removed — there is exactly one name per tool, hence exactly one permission check. This eliminates the `dev_bash` ↔ `mcp__dev__*` bypass the pre-fix code patched.

**Tool-knowledge index** (auto-discovered skills, `Manager.AutoDiscover`):
`store.skills.tool_bindings` holds the uniform agent-facing name; `slug` is derived from it. Migration is rebuild-on-next-run via the existing `INSERT OR IGNORE` semantics. The `tool_enrichments` table is keyed by tool name and re-keys naturally on next write — no migration needed.

### Attribution preservation

The wrap layer's contract is "agent-facing names are uniform; audit metadata is rich". Three preservation points:

1. **`Manager.uniformIndex` entries** carry `serverName` alongside `tool.Name` — accessible via `ToolAttribution(uniformName)`.
2. **OTel spans** on `ExecuteTool` / `ExecuteToolOnServer` set `nanite.mcp.server`, `nanite.mcp.tool`, and (on the index path) `nanite.mcp.uniform_name`.
3. **Discovery warnings** (`mcp.DiscoveryWarning`) carry `ServerName` so collision-related telemetry is server-attributed.

Audit pipelines downstream that previously parsed `mcp__<server>__<tool>` strings should switch to consuming the structured `ServerName` field on toolcall records / spans.

---

## Consequences

**Positive:**

- Tool descriptions shrink ~14 chars each (one MCP-tool list saves ~1.4 KB of context across 100 tools).
- LLM mis-call rate drops; recovery via `ResolveToolServer` is no longer load-bearing.
- Permission policies become readable: `deny_list: ["dev_*"]` instead of `["dev_*", "mcp__dev__*"]`.
- Single name = single permission check. The bare-vs-prefixed bypass is gone by construction.
- Surface enforcement decouples from name-shape signals.

**Negative / known limitations:**

- **Breaking change.** Every existing call site that constructed `"mcp__<server>__<tool>"` strings (or matched on the `mcp__` prefix) had to be touched. Pre-launch latitude makes this acceptable; no external consumers are preserved.
- **Loss of "this came from MCP" name signal.** Code that needed it (progressive-discovery filter, `WrapExistingTools` classifier) now consults `BuiltinToolRegistry.Has(name)` (positive signal) instead of `strings.HasPrefix(name, "mcp__")` (negative signal). Where toolclient is unavailable (`countMCPOriginTools(nil, …)`), the count falls back pessimistically to "all tools are MCP-origin" — keeps the historical behaviour of triggering progressive discovery on a large surface.
- **Collision audit obligation.** Today's MCP audit set has no collisions, but a future third-party server could collide. The runtime collision policy is defensive (rewrite both); operators should monitor for `uniform_name_collision*` discovery warnings.
- **Reserved namespace is an honor system.** A malicious MCP could publish many tools with names that look like first-party (`memory_write`, `nanite_secret`). The `nanite_*` defense catches the latter; the former relies on operator review of registered MCP servers (existing trust-tier responsibility).

---

## Migration notes

- No SQL migration required. The `tool_enrichments` table is keyed by name and re-keys on next write. `skills` rows for auto-discovered tools rebuild on the next `AutoDiscover` pass.
- Agent profile permission policies that referenced `mcp__server__*` patterns have to be rewritten to uniform-name patterns. Current internal agent profiles (under `config/agents/`) have already been updated.
- Audit / telemetry consumers that parsed `mcp__server__tool` from logs should switch to the structured `nanite.mcp.server` + `nanite.mcp.tool` span attributes.

---

## Verification

- `go test -race -count=1 ./internal/...` clean.
- `rg "mcp__" --type go` returns only:
  (a) doc-string references explaining the legacy form (the wrap layer's source-of-record), and
  (b) attribution-preserving telemetry comments.
  Zero `mcp__` strings remain in dispatch surfaces, broker decisions, tool-knowledge index entries, or test fixtures asserting agent-visible names.
- New tests cover the naming canonicalizer (`TestUniformToolName`, `TestDisambiguatedToolName`, `TestIsReservedSelfToolName`), collision policy (`TestManager_UniformIndex_CollisionDisambiguates`), reserved-namespace defense (`TestManager_UniformIndex_ReservedNamespaceForcesPrefix`), and the explicit-server entry point (`TestManager_ExecuteToolOnServer`).

---

## Out of scope

- Reasoning-augmented broker discovery (D3, CW-20260419-0011) — operates on the uniform surface this ADR establishes.
- D-naming convention sweep beyond what falls out naturally from internalization (CW-20260426-0012).
- Programmatic Tool Calling (D6, CW-20260420-0019).
