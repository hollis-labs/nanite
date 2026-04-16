# Tool Broker — Execution-Path Validation

Phase 3 S4a adds execution-time hardening to the chat generation tool-use loop.

## Validation stages (in order)

1. **Blocked check** — tool was hard-blocked from a prior stuck loop.
2. **Permission check** — `permission.Engine` allow/deny/ask.
3. **Execution rules** — agent toolset + permission re-check at execute time.
4. **Arg validation** — LLM-generated `tool_use.Input` validated against the tool's `InputSchema` via `santhosh-tekuri/jsonschema/v6`.
5. **Plugin pre-hook** — `tool.executing` hook can cancel.
6. **Per-tool cap** — default 10 calls per tool per turn (configurable via `UserSettings.tool_per_turn_cap`). Meta-tools (`fetch_tool_result`, `search_tool_result`, `request_tools`) are exempt.
7. **Execute** — tool runs via ToolService → ToolClient → MCPManager.
8. **Error sanitization** — `SanitizeToolError` strips home-dir paths, secret env vars, and truncates Go stack traces.
9. **Result cache** — results exceeding 64 KiB (configurable) are truncated with a pointer footer; the full body is cached for retrieval via meta-tools.

## Per-tool call cap

- Default: 10 per turn per tool name.
- Configurable via `UserSettings.tool_per_turn_cap` (0 = no cap).
- Per-tool overrides via agent constraints (`perToolMax` in `iterationLimits`).
- Cap-exceeded tools emit a structured "BLOCKED" error.

## Result cache-and-pointer pattern

When a tool result exceeds the soft truncation threshold (default 64 KiB), the full body is stored in `tool_result_cache` and a truncated view is returned to the LLM with a pointer:

```
<first 64 KiB of content>

[TRUNCATED — full result cached as tool_result://<id> ...]
```

Two meta-tools are registered as core builtins:

- **`fetch_tool_result`** — `{id, offset?, length?}` → slice of cached body.
- **`search_tool_result`** — `{id, pattern, max_matches?}` → regex matches with context (like `grep -C 2`).

### Thresholds (configurable via UserSettings)

| Setting | Default | Description |
|---------|---------|-------------|
| `tool_result_soft_truncate_bytes` | 65,536 (64 KiB) | Truncation threshold |
| `tool_result_hard_cap_bytes` | 1,048,576 (1 MiB) | Max body stored (above = metadata only) |
| `tool_result_cache_ttl_seconds` | 3,600 (1 hour) | Cache entry lifetime |

### S4b interaction

S4b's per-tier `LimitReader` enforces a hard ceiling at the transport level. S4a's cache reads the post-LimitReader output. Both work together: S4b caps the raw bytes, S4a caches what makes it through.

## Error sanitization

`internal/chat/sanitize.go:SanitizeToolError` applies four rules (best-effort, not a security boundary):

1. Replace `$HOME` prefix with `~`.
2. Replace `/Users/<name>/` or `/home/<name>/` paths with `~/`.
3. Strip lines matching `*_KEY=*`, `*_TOKEN=*`, `*_SECRET=*`, `*_PASS=*`, `*_PASSWORD=*`.
4. Truncate Go stack traces to the first frame.

## Tool-name validation

On MCP discovery, tool names are validated against `^[a-zA-Z0-9_-]+$` with a 128-character max length. Invalid names are warn-skipped and recorded as `DiscoveryWarning` on the server, visible via the Plugin Manager API.
