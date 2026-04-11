# MCP client transport audit — 2026-04-10

## Scope

**Scope string:** `mcp-client-transport` — audit Nanite's MCP client transport infrastructure end-to-end (subprocess management for stdio MCP servers, HTTP/SSE transport for remote MCP servers, JSON-RPC framing / correlation / dispatch, tool discovery + registration, connection lifecycle, plugin-hosted MCP server integration points).

**Scope shape:** Subsystem — `internal/mcp/` client side + one-hop consumers.

**Read in full:**
- `internal/mcp/manager.go` (478 lines) — transport-agnostic dispatcher
- `internal/mcp/stdio_transport.go` (199 lines) — subprocess stdio transport
- `internal/mcp/http_transport.go` (154 lines) — HTTP POST transport + shared JSON-RPC types
- `internal/mcp/self_tools_transport.go` (1120 lines) — in-process self-tools transport (dispatch + handlers; handlers skimmed for context-propagation pattern only)
- `internal/mcp/memory_tools.go` (first 100 lines) — in-process memory transport (the one that does it right)
- `internal/toolclient/broker.go` + `internal/toolclient/permissions.go` — one-hop consumer that routes tool calls through the Manager
- `cmd/nanite/main.go:L520-L575` — MCP server loading wire-up
- `internal/api/mcp_servers.go` — API-side registration + the `POST /api/mcp-servers` trust boundary

**Sampled:**
- `internal/mcpserver/server.go` (Nanite-as-server side, first 100 lines) — noted as out of scope but uses `SelfToolsTransport` so any fix there is shared
- `internal/mcp/dev_tools.go`, `general_tools.go`, `code_exec_tools.go` — only `CallTool` function signatures (handler bodies are parallel-audit scope)
- `internal/chat/orchestrator.go` — grep for `ExecuteTool` usage
- `internal/service/tool.go` — grep for `CallTool`/`ExecuteTool` routing

**Skipped (out of scope):**
- Handler bodies of `dev_tools.go` / `general_tools.go` — parallel `dev-tools-input-validation` audit
- `code_exec_tools.go` internals — covered by sandbox audit
- Plugin internals beyond the MCP integration point — covered by plugin audit
- Chat engine usage of MCP results — covered by chat engine audit
- `internal/mcpserver/` (Nanite-as-MCP-server, not client-side) — noted as a follow-up scope

## Methodology

Categories applied from the deep-review Go rubric, with **Concurrency correctness** as highest emphasis per the task mandate. All six core categories considered:

| Category | Applied | Notes |
|---|---|---|
| Concurrency correctness | ✓ | Single-mutex serialization, goroutine leak on timeout, context propagation gap, response ID correlation |
| Memory & resource leaks | ✓ | Subprocess leak on timeout, FD leak, goroutine leak, unbounded response body |
| Security | ✓ | Trust boundary at tool result, env var inheritance, unbounded reads, dead-code defense check (none exists) |
| Error handling | ✓ | No panic recovery in dispatch, crash detection missing, silent discovery failure |
| Protocol correctness | ✓ | JSON-RPC ID never validated, malformed-input handling |
| Idiomatic Go & design | ✓ | Interface shape, context drop, inline interface assertions |
| Test quality | Deferred | Scoped review — transport-layer tests are absent; recommended follow-up `mcp-client-tooling-and-tests` |
| Standards & tooling | Deferred | `go vet ./internal/mcp/...` ran clean; full `go test -race ./...` / `govulncheck` / `staticcheck` / `golangci-lint` deferred per the scoped-review rule |

**Tooling deferred.** Per the skill's scoped-review rule, this narrow subsystem audit defers tooling commands to a follow-up pass. Only `go vet ./internal/mcp/...` was run (clean). No test files exist for `stdio_transport.go`, `http_transport.go`, or `manager.go`, so a `-race` run against the transport itself is vacuous — the follow-up scope needs to write the fakes before the race runner has anything to check. Recommended follow-up: **`mcp-client-tooling-and-tests`**.

**Cross-audit grounding.** This audit explicitly verified whether the MCP client transport repeats the patterns the plugin audit found in `internal/plugin/subprocess/`:

- **Plugin finding 02** (single-mutex serialization + response correlation gap) → MCP client has the **same pattern, independently implemented** in `stdio_transport.go`. Filed as finding 03.
- **Plugin finding 03** (timeout closes pipe permanently) → MCP client has a **related-but-distinct pattern**: timeout doesn't close the pipe but *does* leak the subprocess and orphan the reader goroutine while marking the transport `started=false` and re-starting on next call. Filed as findings 01 and 04.
- **Chat engine finding 04** (context-blind channel send / goroutine leak) → Closest analog is finding 05 (context dropped at transport boundary so cancellation doesn't propagate) — same class of bug, different surface.
- **Chat engine finding 02** (no panic recovery in `generateResponse`) → The MCP client has the **same gap**; filed as finding 06.
- **`scope_guard` dead-code pattern** from chat engine audit → Explicitly checked. **No dead-code defense layer exists in `internal/mcp/`.** A ripgrep for `validate|sanitize|Validate|Sanitize|scope_guard` across the package returns zero matches. There is no component to be dead; there is no component at all. Filed as finding 07 (no trust-boundary validation).

## Findings

### By severity

**Critical (2)**
- [01 — Stdio transport leaks subprocess/goroutine/pipes on every timeout or cancellation](01-critical-stdio-subprocess-leak-on-timeout.md)
- [02 — Unbounded MCP response body allows memory DoS from any MCP server](02-critical-unbounded-response-body-memory-dos.md)

**High (5)**
- [03 — Single-mutex serialization + response ID correlation gap (cross-audit: plugin finding 02)](03-high-transport-serialization-and-id-correlation-gap.md)
- [04 — Transport marked dead but subprocess alive; no crash detection (cross-audit: plugin finding 03)](04-high-transport-marked-dead-subprocess-alive.md)
- [05 — In-process transports systematically ignore ctx parameter](05-high-context-ignored-in-inprocess-transports.md)
- [06 — No panic recovery anywhere in MCP dispatch (cross-audit: chat engine finding 02)](06-high-no-panic-recovery-in-dispatch.md)
- [07 — No trust-boundary validation on tool schemas, args, results, or server input](07-high-no-trust-boundary-validation.md)

**Medium (3)**
- [08 — Stdio subprocess stderr sent to /dev/null — diagnostics vanish](08-medium-stderr-discarded-observability-hole.md)
- [09 — Manager.ListServers always reports Connected=true; no concurrency bound](09-medium-no-response-id-bounds-on-listservers.md)
- [10 — Stdio subprocess inherits the full host environment by default (secret leakage surface)](10-medium-env-var-inheritance-and-command-trust.md)

**Low (1 grouped, 8 items)**
- [11 — Collateral observations across the MCP client transport](11-low-collateral-observations.md)

**Info (1 grouped)**
- [12 — Observations and praise](12-info-observations-and-praise.md)

### By topic

**Concurrency & lifecycle**
- [01 — Stdio subprocess leaks on timeout](01-critical-stdio-subprocess-leak-on-timeout.md)
- [03 — Serialization + ID correlation gap](03-high-transport-serialization-and-id-correlation-gap.md)
- [04 — No crash detection](04-high-transport-marked-dead-subprocess-alive.md)
- [05 — Context dropped at transport boundary](05-high-context-ignored-in-inprocess-transports.md)
- [09 — ListServers + concurrency bounds](09-medium-no-response-id-bounds-on-listservers.md)

**Memory & resources**
- [01 — Subprocess/goroutine/FD leak](01-critical-stdio-subprocess-leak-on-timeout.md)
- [02 — Unbounded response body](02-critical-unbounded-response-body-memory-dos.md)
- [11.5 — `RemoveServer` in-place slice reuse](11-low-collateral-observations.md)

**Security (trust boundary)**
- [02 — Memory DoS from unbounded response](02-critical-unbounded-response-body-memory-dos.md)
- [07 — No validation on tool schemas, args, or results](07-high-no-trust-boundary-validation.md)
- [10 — Env var inheritance leaks secrets to subprocess](10-medium-env-var-inheritance-and-command-trust.md)
- [12.8 — `mcp_servers.go` API handler is the upstream trust-boundary gate (out of scope; flagged for api-privilege-boundary audit)](12-info-observations-and-praise.md)

**Error handling & observability**
- [04 — No crash detection](04-high-transport-marked-dead-subprocess-alive.md)
- [06 — No panic recovery](06-high-no-panic-recovery-in-dispatch.md)
- [08 — Stderr discarded](08-medium-stderr-discarded-observability-hole.md)
- [11.2 — `DiscoverTools` silently skips failures](11-low-collateral-observations.md)

**Protocol correctness**
- [03 — JSON-RPC ID correlation never checked](03-high-transport-serialization-and-id-correlation-gap.md)
- [07 — Tool schema not validated against spec](07-high-no-trust-boundary-validation.md)
- [11.4 — `parsePrefixedToolName` first-match fragility](11-low-collateral-observations.md)
- [11.6 — JSON mutation via `strings.Replace`](11-low-collateral-observations.md)

**Idiomatic Go & design**
- [05 — Interface contract violated by `_ context.Context`](05-high-context-ignored-in-inprocess-transports.md)
- [11.3 — Inline `Close() error` type assertion](11-low-collateral-observations.md)
- [11.7 — `http.DefaultClient` vs scoped clients](11-low-collateral-observations.md)
- [12 — Praise for `MCPTransport` interface shape and `memory_tools.go`](12-info-observations-and-praise.md)

## Recommended next steps

Prioritized fix order (strictly technical; no timing guidance):

1. **Finding 02 — unbounded response body.** This is the single highest technical risk: any MCP server, malicious or merely buggy, can OOM the host. The fix is small (wrap in `http.MaxBytesReader` / `io.LimitReader`) and immediately closes a memory-DoS class. Land first.

2. **Finding 01 — subprocess leak on timeout.** Cheap mechanical fix (`killAndReapLocked` helper). Closes a compounding resource leak. Land second.

3. **Finding 03 + 04 together — concurrent-transport rework.** Replace the serialized `call` with a reader-goroutine-per-transport + pending-request map + supervisor goroutine. Subsumes the ID correlation gap, the crash-detection gap, and the serialization bottleneck. Worth sharing with `internal/plugin/subprocess/transport.go` if the plugin audit's B.0 lands; otherwise the MCP client rework is self-contained. ~300 lines, localized to `stdio_transport.go` plus tests.

4. **Finding 06 — panic recovery at `Manager.ExecuteTool`.** Single `defer recover()` at the dispatch boundary. 15 lines. Land independently of the transport rework.

5. **Finding 05 — context propagation.** Rename `_ context.Context` to `ctx context.Context` in the four transports and thread through. Mechanical, ~50 sites. Can be done in parallel with the rework.

6. **Finding 07 — trust-boundary validator layer.** New `internal/mcp/validate.go` with size caps, schema validation, tool-name allowlist, and result-type filtering. Larger scope; pairs with the `api-privilege-boundary` audit's fix at the API handler.

7. **Finding 08 — stderr draining.** Small fix, big diagnostics win. Land with the transport rework.

8. **Finding 10 — clean env default.** Requires a migration to `MCPServerConfig` and a docs update, but the code change is a 10-line diff. Coordinate with the `api-privilege-boundary` audit.

9. **Finding 09 — observability + concurrency bounds.** Add state-probe interface, thread through `ListServers`, add per-server semaphore. Can be deferred until findings 01-04 are in.

**Follow-up review passes this audit recommends:**

- **`mcp-client-tooling-and-tests`** — this scope deferred tooling. Run `go vet`, `-race`, `staticcheck`, `govulncheck`, `golangci-lint` against `internal/mcp/` AND write the test fakes (malicious server, concurrent caller, correlation-gap, panic-in-handler) that this audit recommends. Bundle with the plugin tooling follow-up if possible.
- **`api-privilege-boundary`** — already queued in the index. Should land before or alongside finding 10 (env inheritance) and finding 07 (server input validation) since both depend on tighter API-handler hygiene.
- **`mcp-server-hosting`** (new) — `internal/mcpserver/` is Nanite-as-MCP-server via `mark3labs/mcp-go`. It reuses `SelfToolsTransport` and thus inherits findings 05 and 06 automatically, but the server's own framing, dispatch, and lifecycle are separate code and have not been reviewed.
- **`plugin-mcp-integration`** (new, conditional) — if the plugin plan's Track B.5 lands a `PluginMCPTransport`, a focused review of that integration point is warranted. It should explicitly consider whether to share transport internals between `internal/plugin/subprocess/transport.go` and `internal/mcp/stdio_transport.go`.

## Known issues skipped

Not re-flagged per the reviewer-backend context's "DO NOT re-flag" list and the four completed audits:

1. `internal/mcp/code_exec_tools.go` — covered by the sandbox audit (`2026-04-10-sandbox-hardening`). Any input-validation or sandbox-escape concerns in that file are tracked there.
2. `internal/mcp/dev_tools.go` and `internal/mcp/general_tools.go` handler bodies — covered by the queued parallel audit `dev-tools-input-validation`. Only the transport-level context drop (finding 05) is filed here.
3. `scope_guard.go` dead-code — already flagged as chat engine finding 01; not re-flagged. Cross-referenced in finding 07 as the reason no downstream filter exists to mitigate unbounded/malicious tool results.
4. `internal/plugin/subprocess/transport.go` serialization and timeout bugs — already flagged as plugin audit findings 02 and 03. The MCP client's parallel bugs are filed as findings 03 and 04 of this audit, with explicit cross-references.
5. `internal/plugin/host.go` `Shutdown` mutex-across-Unload — plugin-dev known limitation, out of scope here.
6. `shadcn-ui` typo, `.claude/commands` broken symlinks, `CLAUDE.md` — per task mandate.
7. `scope_guard` + `event_pipeline` dead-code class — already documented; cross-referenced not re-flagged.

## Noticed but out of scope

Observations made while traversing the code that belong to other scopes:

- **`internal/api/mcp_servers.go` handler validation.** The `POST /api/mcp-servers` and `POST /api/mcp-servers/import` endpoints accept `command`, `args`, and `env` fields with no validation beyond "name is non-empty, transport_type is stdio|sse." Whatever the caller supplies lands in `exec.Command` (no shell, argv-based — so no shell metacharacter injection) but the command path itself can be anything. This is a privilege-escalation surface for any path that reaches the API behind basic auth. Belongs to the queued **`api-privilege-boundary`** scope.

- **`internal/mcpserver/` Nanite-as-MCP-server.** Depends on `mark3labs/mcp-go` library (a third-party dependency not audited here). Reuses `SelfToolsTransport` directly. Worth a dedicated **`mcp-server-hosting`** scope when that subcommand gets real users.

- **`internal/toolclient/broker.go` token budget estimator.** `EstimateToolTokens` does a full `json.Marshal` per tool per call to estimate tokens. If a malicious MCP server ships a 100 KiB schema and the estimator runs on every chat turn, that's repeated marshal overhead. Minor; not in this audit's scope. Worth a note during the **`provider-abstractions`** or **`chat-engine`** follow-up.

- **`internal/toolclient/permissions.go` `MatchPattern` using `path.Match`.** `path.Match` treats `*` as a path segment, not a free-form glob. A tool named `mcp__server__foo/bar` and a pattern `mcp__server__foo*` will behave subtly differently from what a user likely expects. Combined with finding 07's no-constraint on tool names, there's a defense-in-depth concern around permission-bypass via oddly-named tools. Belongs to the queued **`api-privilege-boundary`** or a new `tool-permission-semantics` pass.

- **`AutoDiscover` runs synchronously inside HTTP handler.** `internal/api/mcp_servers.go:L63` calls `a.Services.MCP.AutoDiscover(context.Background(), ...)` inside `handleCreateMCPServer`. A slow or hung MCP server stalls the HTTP request. Move to a background goroutine (with panic recovery per finding 06) for the `api-privilege-boundary` or an `mcp-discovery-async` pass.

- **No Pdeathsig / Setpgid.** MCP subprocesses don't set `SysProcAttr.Pdeathsig` (Linux) or `Setpgid` / posix_spawn (macOS). If nanite crashes, MCP subprocesses become orphans reparented to init. Minor, but worth a single config-file fix during the lifecycle rework.

- **`memory_tools.go` uses `UserID = "default"` as a hardcoded default.** `internal/mcp/memory_tools.go:L24`. Not a transport concern; a multi-user memory concern. Belongs to the queued **`memory-context-broker`** or a new `multi-user-memory` scope.

- **`NewSelfToolsTransport` doesn't inject `TodoStore` or `A2A` at construction.** Both are "nil-safe; set after construction" per field comments, but the handler code (see finding 06) doesn't check for nil. This is the injection-order fragility that finding 06 names; noting it separately because the right long-term fix is a required constructor argument, not defensive nil checks.

- **No MCP capability negotiation.** The MCP protocol supports capability handshakes on `initialize` / `initialized`. Nanite's client goes straight to `tools/list` without performing the initialize handshake. This is protocol-incomplete and may break when connecting to a spec-compliant server that requires initialization. Belongs to a future `mcp-protocol-conformance` scope.
