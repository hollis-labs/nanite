# Reviewer Context — Nanite Backend

> Loaded by `nanite-reviewer-backend`. Project-specific priorities for deep code review of the Nanite Go backend. Pair with `~/.nanite/skills/deep-review.md` for the review procedure and the severity rubric.
>
> **Release context:** Nanite is preparing its first beta for developer friends. Plugin reliability must be 100%, security-sensitive paths must be audited, and the reviewer should prioritize **ship blockers and high-impact issues** over nitpicks. This is not a "clean up all the lint" pass.
>
> **Companion docs** (read before reviewing):
> - `.nanite/agents/backend.md` — general backend context, stack, package map, resolved tech debt
> - `.nanite/agents/plugin-dev.md` — plugin system state, known limitations, audit fix plan
> - `docs/beta-known-issues.md` — canonical beta blocker list (treat as "do not re-flag")
> - `docs/architecture/plugin-audit-2026-04-10.md` — current plugin audit with prioritized fix list
> - `docs/architecture/plugin-envelope-emission-findings-2026-04-10.md` — raw envelope gap findings

## Stack snapshot

- **Language:** Go 1.26.1
- **Module:** `github.com/hollis-labs/nanite`
- **Router:** stdlib `http.ServeMux` with Go 1.22+ method routing
- **DB:** SQLite via `modernc.org/sqlite` (WAL, foreign keys, busy_timeout=5000)
- **Tracing:** OpenTelemetry via `hollis-labs/otel` wrapper
- **MCP:** `mark3labs/mcp-go` (indirect, via tool-broker)
- **Tooling:** `gofmt`, `goimports`, `golangci-lint` (uncapped via `make lint`), `staticcheck`, `errcheck`, `govulncheck`, `go vet`, `lefthook` pre-commit, `go test -race` (via `make test`). All four static-analysis binaries are installed via `go install` into `$GOBIN` — see `.nanite/agents/backend.md` §Build & Run for the canonical invocations.

Local `replace` directives point to sibling libs (`go-toolbroker`, `go-otel`, `go-providers`, `vanta-conduit`). Build fails without them — verify `go.mod` replace block is satisfied before running `go build`. `plugin-sdk` is a regular external module.

## High-priority review targets

These are the Nanite subsystems a deep review should touch first. They concentrate trust boundaries, concurrency, resource management, and security-sensitive code.

### 1. Plugin system — highest priority

**Path:** `internal/plugin/`, `internal/plugin/builtin/`, `plugins/`, `pkg/plugin/` (Nanite UI extensions), plus the external `github.com/hollis-labs/plugin-sdk` module for contract types

**Why it matters:**
- Plugins run in-process with the host (no isolation — explicit design choice).
- Every plugin currently imports `internal/*` packages, leaking host internals. The audit at `docs/architecture/plugin-audit-2026-04-10.md` has the full fix plan.
- Event dispatch, filter chains, and registration APIs are the extension surface for the whole system.

**Look hard at:**
- `internal/plugin/host.go` — registration, lifecycle, mutex holding patterns. There is a known bug: `Host.Shutdown()` holds the mutex across `p.Unload()` calls — any plugin Unload that re-enters the host will deadlock. Flag as **Critical** if this lands on a real re-entry path.
- `internal/plugin/triggers.go` — `TriggerDispatcher.Dispatch` is fire-and-forget. Test stub race was fixed 2026-04-10, but filter/disabled checks in `Dispatch` before the goroutine spawn are still synchronous — if that changes, the `TestTriggerDispatch_DisabledRule` / `TestTriggerDispatch_FilterExpr` tests become racy. Flag as High if touched.
- `internal/plugin/events.go` — 35+ typed Emit helpers; any panic in an event hook currently propagates. Check recover() placement.
- `internal/plugin/loader.go` — topological dependency sort. Cycle detection, missing deps.
- `internal/plugin/scaffold/` — templates have broken imports (audit P0-1). Pre-existing, don't re-flag unless the scope is "plugin scaffold".
- `internal/plugin/event_stream.go` — SSE bus with no frontend consumer. Dead endpoint (audit P0 / 3.3). Don't flag as a finding; note as Info if in scope.

**Known issues to NOT re-flag:**
- The 25+ items in `plugin-dev.md` §Known Limitations. They're tracked in the audit. Exception: if you find a NEW occurrence in code that isn't in the audit, flag it.
- Envelope emission is broken system-wide — tracked in `plugin-envelope-emission-findings-2026-04-10.md`.
- `registers.envelopes` in plugin.yaml is never read by the backend — documented dead path.

### 2. Chat engine & provider abstraction

**Path:** `internal/chat/`, `internal/provider/`, `internal/service/chat_generate.go`

**Why it matters:**
- Orchestrates LLM streaming, tool execution loops, delegation, and context assembly.
- Providers include both HTTP API adapters (Anthropic, OpenAI, Ollama, Gemini, Mistral, Azure, OpenRouter, OpenZen) and PTY bridges (Claude, Codex, Gemini, Copilot, Aider, Junie, Kiro, Qwen).
- Circuit breaker, rate limiter, retry, cache, and event pipeline all live in `internal/provider/`.

**Look hard at:**
- `internal/provider/pty.go` and all `pty_*.go` — subprocess spawning, stdin/stdout pipes, signal handling, reaping. **Goroutine leaks in PTY paths are the most likely Critical/High finding here.** Every PTY session needs a provable exit path on context cancellation.
- `internal/provider/circuit.go`, `ratelimit.go`, `retry.go` — concurrency primitives. Check mutex usage, atomics, channel close patterns.
- `internal/provider/scope_guard.go` — guardrails against prompt injection escaping the intended session scope. Any weakness here is a security finding.
- `internal/provider/event_pipeline.go` — streaming transforms. Error propagation, goroutine lifetime.
- `internal/chat/engine.go` — was decomposed from 1760→197 lines. Now a thin orchestrator. Verify no regression — the decomposition was recent.
- `internal/chat/delegate.go` / `orchestrator.go` — child session spawning. Ctx propagation, cleanup.
- `internal/service/chat_generate.go` — the one-and-only working envelope emission path (`captureEnvelopeData`). Don't break this.

### 3. MCP client & transports

**Path:** `internal/mcp/`, `internal/mcpserver/`, `internal/toolclient/`

**Why it matters:**
- MCP tools are external code execution surfaces. Tool arguments are attacker-controlled if a plugin or user can trigger a tool call.
- Three transports: stdio subprocess, HTTP/SSE, in-process self-tools. Each has its own lifecycle.

**Look hard at:**
- `internal/mcp/manager.go` — server lifecycle, reconnect logic, transport selection.
- `internal/mcp/stdio_transport.go` — subprocess lifecycle, reaping, zombie avoidance. Goroutine leaks.
- `internal/mcp/http_transport.go` — HTTP request/response handling, timeouts, TLS config.
- `internal/mcp/self_tools_transport.go` (762 lines) — in-process tools. Read carefully: this is where every built-in dev/general/self tool lives.
- `internal/mcp/dev_tools.go` — `grep`, `read`, `write`, `glob`, `edit` built-ins. **Path traversal** and **command injection** are the primary risks here. Every path argument should be validated and confined to the intended directory.
- `internal/mcp/general_tools.go` — `web_fetch`, `web_search`. **SSRF** is the primary risk. Check URL validation, redirect handling, outbound proxy awareness.
- `internal/toolclient/permissions.go` — permission checking on tool execution. Gaps here = privilege escalation.
- `internal/toolclient/broker.go` and `tool_knowledge.go` — progressive discovery logic, tool selection.

### 4. Sandbox & subprocess management

**Path:** `internal/sandbox/`, `internal/agent/adapter.go`, `internal/plugin/builtin/adapter-*/`

**Why it matters:**
- Sandbox is the primary security boundary for untrusted / semi-trusted code (user commands, agent-invoked subprocesses, CLI adapters).
- macOS uses `seatbelt`, Linux uses `bwrap`, with a domain-allowlisted localhost TCP network proxy injected via `HTTP_PROXY`.
- "Sandbox-first" design: OS sandbox is primary boundary, denylist is second line. YOLO mode = no sandbox, denylist remains.
- **User explicitly flagged this for extra attention.**

**Look hard at:**
- Sandbox config construction — any string that lands in a seatbelt profile or bwrap arg must be validated. Unescaped user paths = sandbox escape.
- The network proxy — domain allowlist correctness, bypass paths, DNS rebinding.
- `AgentExec` vs `UserExec` split — make sure they're actually used consistently. Any direct `exec.Command` in non-exec packages is a red flag.
- `internal/sandbox/` — delegates to adapter plugins via `AdapterRegistry.PopulateAllSandboxes()`. Check error handling: a populate failure should NOT silently leave the sandbox partially populated.
- Adapter plugins (`adapter-claude`, `adapter-codex`, `adapter-gemini`, `adapter-opencode`, `adapter-nanite-native`) — each writes files into a user directory. Managed section protocol (`<!-- nanite:start -->` / `<!-- nanite:end -->`) must be robust against user edits that insert nested markers or malformed HTML comments.
- `adapter-opencode` — header admits "format is unverified against Opencode CLI." Beta-blocker per `plugin-dev.md`. Flag as High if this landed in scope.

### 5. Store (SQLite) & migrations

**Path:** `internal/store/`, `internal/store/migrations/`

**Why it matters:**
- Every write path goes through the store. Data integrity and schema correctness are fundamental.
- Schema was recently squashed to `001_schema.sql` — verify the squash is clean and no orphaned columns/tables remain.
- 43 test files cover this layer; verify they still run clean under `-race`.

**Look hard at:**
- SQL injection — raw `database/sql` queries throughout. Every query must use parameter placeholders. String concatenation in SQL is an automatic Critical.
- Transaction handling — commit/rollback on error paths, nested transactions, SQLite `BEGIN IMMEDIATE` vs default.
- `store.go` — WAL config, busy_timeout, foreign keys. Connection pool size tuning.
- `seed.go` — idempotency. Reseed must be safe.
- Nil check pattern — established convention: reads return empty/safe defaults, writes return errors. Deviations are a Medium finding.
- `ext_settings` JSON column — parse/serialize correctness, key collision with `widget_visibility`, `widget_order`, etc.

### 6. HTTP API & middleware

**Path:** `internal/api/`, `internal/server/`

**Why it matters:**
- Trust boundary between external clients and the core system.
- 100+ routes. 22 handler files. Centralized registration in `api.go:RegisterRoutes()`.

**Look hard at:**
- Input validation on every POST/PUT handler — request body decoding, field validation, length limits.
- `internal/server/auth.go` — basic auth skip list. Missing entries = unauth access to protected routes.
- Middleware order: `recover → logging → basicAuth → cors → mux`. Any reorder is a finding.
- CORS config in `internal/server/server.go:93-109` — "allows any localhost origin in dev." Verify prod behavior.
- SSE endpoints — connection lifetime, cleanup on disconnect, session takeover races. `session_takeover` event is already a known pattern; verify it covers all SSE endpoints, not just message streaming.
- SSE deduplication — one active stream per session. Takeover races.
- `internal/api/plugins.go` — plugin install paths. Plugin install is a privilege boundary; if a user can install a plugin via the API, the API surface needs hard validation.
- `internal/api/event_stream.go` — dead endpoint (no consumer). Don't flag; Info if in scope.

### 7. Memory / context broker

**Path:** `internal/memory/`, `internal/contextbroker/`

**Why it matters:**
- Memory extraction runs on every turn and post-compact. Embeddings are generated via OpenAI (`text-embedding-3-large`) or Ollama (`nomic-embed-text`) fallback.
- Context broker queries 5 sources (Conduit 25%, memory 15%, pcc 30%, engine 15%, session 15%) with token budget allocation.

**Look hard at:**
- Token budget enforcement — over-budget responses must be truncated, not silently oversized.
- Embedding provider fallback — what happens when OpenAI fails and Ollama isn't available? Silent disable? Crash?
- Similarity ranking in `source_memory.go` — enabled 2026-04-08. Verify no regression in nearest-neighbor correctness.
- PII leakage in memory extraction — if a user pastes a secret into a chat, does it persist in the memory store?

### 8. Worker lifecycle

**Path:** `internal/worker/`

**Why it matters:**
- Concurrent full-worker spawns are explicitly supported. Three prior data races were fixed 2026-04-10. Do not regress.

**Look hard at:**
- `Worker.GetStatus()` / `SetStatus()` — all field access through the RWMutex.
- `Manager.List()` returns `[]Snapshot`, not `[]*Worker`. Any new method that returns `*Worker` directly is a race waiting to happen.
- `ReapStale()` — same concern.
- `TestListConcurrentFieldAccess` — this test exists specifically to catch regressions. Verify it still exercises `Status`, `SessionID`, `WorktreePath` concurrently.

## Trust boundaries — enumerate these for any security pass

Untrusted input enters Nanite at these points. Every security-focused finding should map to at least one of these.

1. **HTTP API requests** — body, query, path params. External caller (CLI, web UI, other service).
2. **User messages in chat** — content arbitrary. May include prompt injection.
3. **Tool arguments** — LLM-generated, attacker-influenceable via prompt injection.
4. **MCP tool results** — returned by an external MCP server. Could be malicious if the server is compromised.
5. **Plugin manifests (plugin.yaml)** — installed plugins. Any parser that trusts the file layout is a potential vector.
6. **Plugin code** — runs in-process with no isolation. A malicious plugin has full host access. Primary mitigation is install-time review, not runtime sandboxing.
7. **PTY bridge input/output** — stdin/stdout from spawned CLI agents. These CLIs accept arbitrary user text and can be tricked by the user into executing arbitrary commands via their own tool APIs.
8. **Subprocess execution (UserExec / AgentExec)** — user-triggered shell commands. Sandbox is the primary control.
9. **Environment variables** — `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `NANITE_AUTH_USER/PASSWORD`, `HTTP_PROXY`, `SUPPORT_DATABASE_URL`, etc. Shouldn't leak in logs or error messages.
10. **File system** — read/write tools, sandbox population, plugin discovery. Path traversal attacks.
11. **Outbound HTTP** — providers, MCP HTTP transports, `web_fetch`, `web_search`. SSRF targets.

## Pre-existing known issues — DO NOT re-flag

These are already tracked. Re-flagging them in findings is noise. If you find a NEW occurrence that isn't covered by the tracker, flag that as a distinct finding with a reference to the tracker.

1. **Beta known issues** (`docs/beta-known-issues.md`) — all P0 items are closed as of 2026-04-10. P1 is empty.
2. **Plugin framework limitations** (`.nanite/agents/plugin-dev.md` §Known Limitations, items 1–26). Particularly:
   - Scaffold templates have broken imports (P0-1)
   - `adapter-opencode` format unverified (beta blocker)
   - `oembed` envelope path is dead
   - `fragments-engine` chat-header 503s without Engine MCP
   - `Host.Shutdown()` mutex-across-Unload deadlock risk
   - No `Host.RegisterEnvelopeType` on SDK — all contract gaps in audit §3.2
   - Plugin isolation gap (design choice)
   - HTTP route leak on plugin unload (stdlib limitation)
3. **Engine backlog post-beta items** — query `engine_backlog_list --project-id nanite`. These are deferred, not forgotten.
   - BLG-20260410-001 PTY tool-level presence (P3)
   - BLG-20260410-002 Install service `--dry-run` (P3)
   - BLG-20260410-003 Provider `prompt_too_long` recovery (P2)
   - BLG-20260410-004 Plugin host per-plugin event hook cleanup (P3)
4. **Deliberate architecture** — not bugs, don't flag:
   - `http.ServeMux` doesn't support route removal on unload — stdlib constraint
   - Migrations are DDL-only, seed data lives in `seed.go` — enforced rule
   - "Two binaries, only one is live" footgun — documented extensively in `backend.md`
   - `.agentrc/` vs `.nanite/` path drift in some places — `nanite install` Phase 4 Task 16 will resolve
   - Broken `.claude/commands` and `.claude/skills` symlinks in this repo — same fix
   - `shadcn-ui` typo in `config.yaml` for `nanite-frontend` — will be addressed separately

## How to run this review

When `nanite-reviewer-backend` is booted with a scope:

1. Read this file. Note the high-priority targets and the no-flag list.
2. Read `.nanite/agents/backend.md` for the general stack, package inventory, and resolved tech debt.
3. Read `.nanite/agents/plugin-dev.md` ONLY if the scope touches plugins.
4. Invoke the `deep-review` skill with the scope. The skill's methodology section drives the rest of the review.
5. Collect tooling evidence. The preferred path is `make lint` (full uncapped pipeline) and `make test` (race-enabled). Individual invocations for targeted passes:
   - `go vet ./...` — always
   - `make test` (== `go test -race ./...`) — for concurrency-heavy scopes
   - `make lint` — full pipeline: `go vet` + `golangci-lint run --max-issues-per-linter=0 --max-same-issues=0` + `staticcheck ./...` + `errcheck ./...` + `govulncheck ./...`. Default caps in golangci-lint suppressed ~70% of findings in a prior audit (283 vs 956 issues); uncapped is the canonical reviewer setting.
   - `golangci-lint run --new --timeout 30s` — legacy pre-commit invocation (changed lines only); use only when reproducing the pre-commit hook, not for audits.
   - `staticcheck ./...` and `errcheck ./...` — targeted; both are run by `make lint`.
   - `govulncheck ./...` — targeted; also available as `make vuln`. Required for the security category.
   - `go mod tidy` diff check for the tooling category.
6. Write findings to `docs/audits/<date>-<slug>/` per the skill's output contract.
7. Return the one-line confirmation. Do NOT dump findings into the chat.

## Reviewer guardrails

- **Do not fix code.** Review only. Findings are the deliverable.
- **Do not re-flag pre-existing known issues.** See list above. If in doubt, skip and note as Info in the index.
- **Err on the side of severity for security and data-loss findings.** A "maybe exploitable" path is High at minimum.
- **Praise is a valid Info finding.** If a package is well-tested and well-structured, say so — it tells the maintainer what to protect.
- **Small findings can be grouped.** Don't create ten 3-line finding files when one `NN-info-style-observations.md` roundup is more useful.
- **Release context is "first beta for developer friends."** Blockers = Critical/High. Polish = Low/Info. Medium is the grey zone; err on the side of High if the finding might hit a developer friend in their first hour of use.
