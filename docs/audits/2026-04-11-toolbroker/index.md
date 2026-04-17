# Audit — `toolbroker`

**Date:** 2026-04-11
**Reviewer agent:** `nanite-reviewer-backend`
**Skill:** `deep-review`

## Scope

Literal scope string: `toolbroker`.

Interpreted as: a security and correctness audit of the tool broker client (`internal/toolclient/`), focused on permission enforcement, progressive discovery, tool selection, argument handling, and error paths. The toolbroker is the upstream dispatcher for all tool execution in Nanite — it sits between the LLM's tool-use decisions and the MCP transport layer.

**Files read in full:**

- `internal/toolclient/broker.go` (308 lines)
- `internal/toolclient/permissions.go` (157 lines)
- `internal/toolclient/tool_knowledge.go` (495 lines)
- `internal/toolclient/intent.go` (119 lines)
- `internal/toolclient/meta_tools.go` (110 lines)
- `internal/toolclient/builtin.go` (63 lines)
- `internal/toolclient/config.go` (87 lines)
- `internal/toolclient/broker_test.go` (458 lines)
- `internal/toolclient/permissions_test.go` (233 lines)
- `internal/toolclient/tool_knowledge_test.go` (168 lines)
- `internal/toolclient/builtin_test.go` (130 lines)

**One-hop consumers read in full:**

- `internal/service/tool.go` (415 lines) — `ToolService` wrapping `ToolClient`, the actual execution path
- `internal/mcp/manager.go` (340 lines) — `ExecuteTool`, `ResolveToolServer`, `DiscoverTools`
- `internal/mcpserver/server.go` (80 lines) — Nanite-as-MCP-server, uses `SelfToolsTransport` directly

**Files sampled for cross-reference:**

- `internal/chat/orchestrator.go:L155-L205` — direct `MCPManager.ExecuteTool()` calls bypassing permissions
- `internal/chat/engine.go:L37` — `BuildToolCatalog` consuming `ToolSummary`
- `cmd/nanite/main.go` — tool registration wiring (confirmed via grep)

**External library read:**

- `../framework/libs/go-toolbroker/broker/local.go:L1-L80` — `LocalBroker` mutex patterns, `LoadRules`, `SelectTools`

**Prior audits read for context:**

- `docs/audits/2026-04-10-dev-tools-input-validation/index.md` — `dev_bash` RCE finding; toolbroker is the upstream dispatcher
- `docs/audits/2026-04-10-mcp-client-transport/index.md` — MCP tool results are untrusted; `internal/toolclient/broker.go` listed as one-hop consumer

## Methodology

Followed the 6-step deep-review procedure. Categories applied:

| Category | Applied | Notes |
|---|---|---|
| Security | Full | Permission enforcement, progressive discovery information leak, injection via arguments, tool selection determinism |
| Error handling | Full | Error message information leakage, silent fallbacks on invalid configuration |
| Memory & resources | Partial | Checked for unbounded growth in tool knowledge, builtin registry, intent scoring. No issues found beyond static catalog staleness. |
| Concurrency correctness | Checked | `BuiltinToolRegistry` uses `sync.RWMutex` correctly. `LocalBroker` (external lib) also mutex-protected. No goroutines spawned in toolclient. |
| Idioms | Sampled | Non-deterministic map iteration in `GetBuiltins()`. Minor. |
| Test quality | Sampled | Assessed coverage breadth. Good for happy paths; gaps in permission bypass paths. |
| Standards & tooling | Deferred | Narrow scope review. `go vet` was not run against this package in isolation. Recommended follow-up: `whole-repo-tooling-and-tests-sweep`. |

**Cross-audit checks performed:**

- Verified `dev-tools-input-validation` audit's `dev_bash` RCE finding: confirmed the toolbroker does not add centralized argument validation upstream of the transport layer (finding 07).
- Verified `mcp-client-transport` audit's note that `internal/toolclient/broker.go` was sampled but the service-layer fallback path was not audited: confirmed the fallback path bypasses permissions (finding 01).
- Checked for the "dead-code defense layer" pattern from the mcp-client-transport audit: no `validate|sanitize|guard` patterns exist in `internal/toolclient/`. The permission system is real and active but has bypass paths.

## Findings

### By severity

**Critical (0)**
- _none_

**High (3)**
- [01 — Permission bypass via MCPManager fallback in ToolService.Execute](01-high-permission-bypass-mcpmanager-fallback.md)
- [02 — Built-in tools bypass permission checks entirely](02-high-builtin-tools-bypass-permission-checks.md)
- [03 — request_tools meta-tool returns tool schemas without permission filtering](03-high-request-tools-meta-tool-bypasses-permissions.md)

**Medium (5)**
- [04 — Tool name collision: first-match-wins in ResolveToolServer is non-deterministic](04-medium-tool-name-collision-first-match-wins.md)
- [05 — Error messages from tool execution leak internal state to LLM](05-medium-error-messages-leak-internal-state.md)
- [06 — MaxCallsPerTurn permission field is parsed but never enforced](06-medium-maxcallsperturn-not-enforced.md)
- [07 — Tool arguments are passed through to execution without any validation](07-medium-tool-arguments-passed-through-unvalidated.md)
- [08 — Tool knowledge catalog is hardcoded and will drift from actual tool availability](08-medium-tool-knowledge-static-catalog-staleness.md)

**Low (2)**
- [09 — ParsePermissions silently falls back to permissive defaults on invalid JSON](09-low-parse-permissions-silent-fallback.md)
- [10 — MatchPattern swallows path.Match errors](10-low-glob-pattern-match-error-swallowed.md)

**Info (1)**
- [11 — Minor observations (iteration order, test praise, allocation)](11-low-observations.md)

### By topic

**Permission enforcement**
- [01 — Permission bypass via MCPManager fallback](01-high-permission-bypass-mcpmanager-fallback.md)
- [02 — Built-in tools bypass permission checks](02-high-builtin-tools-bypass-permission-checks.md)
- [06 — MaxCallsPerTurn parsed but never enforced](06-medium-maxcallsperturn-not-enforced.md)
- [09 — ParsePermissions silent fallback on bad JSON](09-low-parse-permissions-silent-fallback.md)
- [10 — MatchPattern swallows errors](10-low-glob-pattern-match-error-swallowed.md)

**Progressive discovery**
- [03 — request_tools meta-tool bypasses permissions](03-high-request-tools-meta-tool-bypasses-permissions.md)
- [08 — Tool knowledge catalog staleness](08-medium-tool-knowledge-static-catalog-staleness.md)

**Tool selection logic**
- [04 — Tool name collision non-determinism](04-medium-tool-name-collision-first-match-wins.md)

**Injection / argument handling**
- [07 — Tool arguments passed through unvalidated](07-medium-tool-arguments-passed-through-unvalidated.md)

**Error handling**
- [05 — Error messages leak internal state](05-medium-error-messages-leak-internal-state.md)

**Idioms & test quality**
- [11 — Minor observations](11-low-observations.md)

## Recommended next steps

1. **Fix permission bypass paths (findings 01, 02, 03).** These are the highest-leverage fixes. The permission model exists and is well-implemented, but three bypass paths make it ineffective for restricted agents. Fix order: 02 (built-in bypass, one line change) -> 01 (fallback bypass) -> 03 (progressive discovery leak).
2. **Enforce or remove MaxCallsPerTurn (finding 06).** Dead configuration creates a false sense of security. Either wire it up or delete it.
3. **Sanitize error messages (finding 05).** Straightforward string sanitization at the `ToolService.Execute` boundary.
4. **Log collision warnings (finding 04).** One-line addition to `DiscoverTools()`.
5. **Argument validation (finding 07).** Larger effort — consider as a follow-up scope `toolbroker-argument-validation`.
6. **Follow-up audit scope:** `toolbroker-argument-validation` — design and implement centralized schema validation at the `CallTool` chokepoint.

## Known issues skipped

- `dev_bash` RCE via LLM-controlled commands — tracked in `docs/audits/2026-04-10-dev-tools-input-validation/`. The toolbroker is the upstream dispatcher but the fix is in the tool handler, not the broker.
- MCP tool result trust — tracked in `docs/audits/2026-04-10-mcp-client-transport/`. The toolbroker receives these results but the fix is in the transport layer.
- `Host.Shutdown()` mutex-across-Unload deadlock — tracked in `plugin-dev.md`. Not in scope.

## Noticed but out of scope

- **`internal/service/tool.go:L236-L268` — `GetToolMeta()` uses name-based heuristics for safety classification.** The heuristic (`strings.Contains(toolName, "bash")` -> destructive) is fragile. A tool named `rebash_output` would be classified as destructive. A tool named `execute_arbitrary_code` would not be classified as destructive. Suggested follow-up scope: `tool-meta-safety-classification`.
- **`internal/service/tool.go:L99-L174` — `SelectForAgent()` has duplicated intent extraction logic.** The `extractIntent()` function in `service/tool.go` is documented as "Mirrors chat.ExtractIntent logic" with its own copy of stop words. Drift between the two copies is a maintainability risk. Suggested follow-up: code dedup pass.
- **`internal/mcp/manager.go:L126` — `DiscoverTools()` map iteration for server discovery order.** Beyond the name-collision finding (04), the non-deterministic iteration order means the tool slice order (and therefore broker priority) varies across restarts. This affects which tools are pruned by `PruneToolsToTokenBudget` when over budget. Suggested follow-up: deterministic server ordering in the MCP manager.
- **`internal/mcpserver/server.go:L69-L79` — Nanite-as-MCP-server calls `SelfToolsTransport.CallTool()` directly, bypassing `ToolClient`.** External MCP clients connecting to Nanite's server get direct access to self-service tools with no permission enforcement. Suggested follow-up scope: `mcpserver-permission-boundary`.
