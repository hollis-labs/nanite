# MCP Trust Model

This document describes how nanite classifies MCP servers by trust tier,
which per-tier limits apply, and how the validator layer enforces the
trust boundary at discovery and tool-execution time. Introduced in Phase 3
S4b (2026-04-16) to close audit findings 02, 07, and 10.

## Why a trust model

An MCP server speaks arbitrary JSON-RPC into nanite. Without bounds, a
misbehaving or malicious server can:

- advertise a tool schema large enough to exhaust memory;
- return a 10 MiB+ response body;
- inject prompt-steering text through tool output;
- spoof other tool output via ANSI escape sequences;
- inherit nanite's host environment (AWS_*, GITHUB_TOKEN, …) into its
  subprocess.

Rather than apply a single one-size-fits-all ceiling, nanite classifies
each server into one of four **trust tiers** and picks per-tier limits.
Built-in tools shipped with nanite get more headroom; user-added
third-party HTTP MCP servers get the strictest caps.

## Trust tiers

| Tier | Source | Notes |
|---|---|---|
| `builtin` | Code-shipped transports in `internal/mcp/` (dev tools, general tools, code exec, self tools, memory tools). | Full host confidence. Largest limits. |
| `plugin_stdio` | Plugin-registered MCP servers backed by a subprocess plugin's JSON-RPC transport (`internal/plugin/subprocess`). | Plugins are code but may still misbehave. Middle limits. |
| `plugin_http` | Plugin-registered MCP servers speaking HTTP. | Same stance as `plugin_stdio`, slightly tighter because HTTP adds a network failure surface. |
| `third_party_http` | User-added via `POST /api/mcp-servers` or catalog-installed. Default for **any row** in the `mcp_servers` table whose tier is unset (D4 fail-closed). | Strictest limits. |

### Per-tier defaults

From `internal/mcp/validate.go:LimitsFor`:

| Limit | `builtin` | `plugin_stdio` | `plugin_http` | `third_party_http` |
|---|---:|---:|---:|---:|
| Max tool-name length | 256 | 128 | 128 | 128 |
| Max description length | 8 KiB | 4 KiB | 2 KiB | 2 KiB |
| Max InputSchema size | 256 KiB | 64 KiB | 32 KiB | 16 KiB |
| Max result size | 2 MiB | 512 KiB | 256 KiB | 128 KiB |
| Max tools per server | 1000 | 200 | 100 | 50 |

These are the defaults shipped in code. Per-server overrides will be
added in a follow-up; for now they're fixed.

## Where the tier lives

- Persisted on `store.MCPServerConfig.TrustTier` (migration
  `012_mcp_server_trust.sql`). Every row defaults to `third_party_http`
  because the DB only ever holds third-party-added servers — built-ins
  and plugins carry their tier at runtime via the registration API.
- Runtime: `Manager.AddServer(name, transport, tier)` records the tier
  in a `server_name → TrustTier` map. `Manager.AddHTTPServer` and
  `Manager.AddStdioServer` pass it through. `Manager.AddPluginServer`
  hardcodes `TierPluginStdio` (plugin servers are subprocess-backed by
  definition, and exposing the `mcp.TrustTier` type to `internal/plugin`
  would close an import cycle).

## Validator layer

`internal/mcp/validate.go` is the single home for every trust-boundary
rule. Pure functions, no state. The entry points:

### Discovery time

```go
ValidateToolSet(tier, tools)   // count cap + duplicate names
ValidateToolMeta(tier, tool)   // name charset/length, description, schema size
```

`Manager.DiscoverTools` runs `ValidateToolSet` first (truncating the
tool slice to the tier cap when exceeded), then `ValidateToolMeta` per
surviving tool. Tools with any error are skipped and the error surfaces
as a `DiscoveryWarning` whose `Reason` field is one of:

- `invalid_tool_name` — empty, over the length cap, or bad charset
- `description_too_long`
- `schema_size_exceeded`
- `tool_count_capped`
- `duplicate_tool_name`

Warnings are returned from `Manager.GetDiscoveryWarnings()` and surfaced
per-server in `Manager.ListServers()`, which the Plugin Manager UI
renders.

### Execution time

```go
ValidateBlockType(block)                         // allowlist: text|image|resource
StripANSI(text)                                  // unconditional, D5
ScanInjection(text) → []InjectionHit             // observability-only, D3
ValidateResultSize(tier, totalBytes)             // tier-cap defense in depth
```

In `Manager.ExecuteTool`:

1. Each `ToolContent` block is run through `ValidateBlockType`. Unknown
   types are dropped (WARN logged) and don't contribute to the
   assembled result.
2. `StripANSI` runs on every text block. ANSI CSI and OSC sequences are
   removed unconditionally (cheap, reduces terminal-spoofing risk).
3. `ScanInjection` runs on each text block. Matches are **logged at WARN
   + emitted as a structured metric record** (`mcp_injection_hits_total`
   keyed by server/tool/rule). **Matches do not block the call** — this
   is the observation phase per design decision D3. When false-positive
   rates are known, a blocking follow-up (S4b.1) can layer on top.
4. After assembly, `ValidateResultSize` enforces the tier's
   `MaxResultBytes` ceiling as defense-in-depth on top of the
   transport's own `LimitReader` cap (see below).

### Transport-level tightening

Both `HTTPTransport` and `StdioTransport` expose a
`SetMaxResponseBytes(int)` setter. `Manager.AddServer` invokes it with
`LimitsFor(tier).MaxResultBytes` so the on-wire reader never buffers
past the tier ceiling. The 10 MiB package-default constants
(`maxHTTPResponseBytes`, `maxStdioResponseBytes`) remain as a safety
net if a caller doesn't go through `Manager.AddServer`.

## Environment inheritance (stdio transports)

Finding 10 closed. `NewStdioTransport(command, args, env, envAllowlist)`
takes an explicit list of host env-var names the subprocess is permitted
to inherit. The defaults are deliberately minimal:

- DB rows for new third-party servers: `env_allowlist = '[]'` (nothing
  inherited). Users must opt in explicitly per-server.
- Built-in stdio transports don't spawn subprocesses at all — they're
  in-process.

`PATH` must be in the allowlist, or `StdioTransport.start()` fails
loudly at first call with a clear error. Silent fallback to an empty
PATH was explicitly rejected because the resulting `exec: file not
found` is an opaque failure mode that hides the config error.

## Upgrading a server's trust tier

Most servers are discovered with the conservative default. If you
trust a specific server more, the canonical path is:

```sql
UPDATE mcp_servers
SET trust_tier = 'plugin_http'
WHERE name = 'my-company-internal-mcp';
```

A SET-tier API endpoint is a follow-up. For now, a DB update + nanite
restart is the supported path — this is intentionally conservative
because raising trust loosens every limit at once.

## Tuning

Per-tier defaults may be too tight for particular built-ins or plugins.
The expected tuning loop:

1. Instrument first. Watch the `mcp_result_size` attribute on tool-call
   spans and WARN logs for tier-cap breaches.
2. If a legitimate tool routinely breaches its tier cap, capture a BLG
   with sample sizes and either (a) raise the tier default globally or
   (b) move the tool to a special higher-tier transport.

## Referenced audit findings

- **Finding 02** (unbounded response body, Critical) — hotfixed by
  PR #42 (`9384714`) with a 10 MiB transport-level cap; S4b tightens
  that ceiling per-tier via `SetMaxResponseBytes`.
- **Finding 07** (no trust-boundary validation, High) — closed by the
  validator layer in this document.
- **Finding 10** (env-var inheritance, Medium) — closed by the
  `EnvAllowlist` mechanism on `StdioTransport`.
