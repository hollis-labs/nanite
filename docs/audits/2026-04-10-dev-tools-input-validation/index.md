# Audit — `dev-tools-input-validation`

**Date:** 2026-04-10
**Reviewer agent:** `nanite-reviewer-backend`
**Skill:** `deep-review` (5th real run, post-refinement)

## Scope

Literal scope string: `dev-tools-input-validation`.

Interpreted as: a security + input-validation + resource-management audit of the built-in MCP developer tools registered by nanite at startup, specifically the two files the sandbox audit and the reviewer-backend context flagged as deferred from prior passes.

**Files read in full:**

- `internal/mcp/dev_tools.go` (578 lines) — `dev_read`, `dev_grep`, `dev_write`, `dev_glob`, `dev_edit`, `dev_bash`
- `internal/mcp/general_tools.go` (590 lines) — `web_fetch`, `json_parse`, `datetime`, `base64_encode`, `base64_decode`, `url_encode`, `url_decode`, `hash`, `math_eval`, `think`
- `internal/mcp/dev_tools_test.go` (326 lines) — existing test coverage
- `internal/mcp/general_tools_test.go` (365 lines) — existing test coverage
- `internal/mcp/code_exec_tools.go` — as the contrasting sandbox-wrapped pattern

**Files sampled (one hop) for cross-reference only:**

- `cmd/nanite/main.go:L394-L418` — registration of the transports
- `internal/sandbox/proxy.go`, `internal/sandbox/exec.go:L116` — to confirm `web_fetch` does not go through the proxy
- `internal/service/chat_generate.go:L862-L878` — the `captureEnvelopeData` consumer that makes finding 07 a cross-cut

**Prior audits read for context:**

- `docs/audits/2026-04-10-sandbox-hardening/01-critical-session-id-path-traversal-and-seatbelt-injection.md` — pattern match baseline
- `docs/audits/2026-04-10-sandbox-hardening/02-critical-proxy-ssrf-rfc1918-and-port.md` — SSRF baseline
- `docs/audits/2026-04-10-chat-engine/05-high-user-forged-envelope-injection.md` — the cross-audit downstream consumer
- `docs/audits/INDEX.md` cross-cutting themes section

**Out of scope (queued separately):**

- `mcp-client-transport` — JSON-RPC framing, dispatch, tool-result routing (parallel audit)
- `code_exec_tools.go` — covered by sandbox audit finding 01
- The full MCP server and plugin-hosted MCP infrastructure
- `internal/toolclient/` — permission checking and broker layer
- Chat engine internals, sandbox internals, plugin system — each has its own audit
- Frontend rendering of tool results

## Methodology

Followed the 6-step deep-review procedure. Categories applied:

- **Security** — input validation at the tool-argument trust boundary, path traversal, SSRF, symlink escape, command injection, envelope injection, sandbox bypass, TLS, content-type handling. Primary focus, consistent with the scope.
- **Memory & resources** — unbounded reads, unbounded walks, recursive parsers without depth caps, unclosed resources, goroutine lifetime.
- **Concurrency correctness** — `context.Context` propagation, cancellation on abort.
- **Error handling** — error-message discipline, info leakage, silent fallbacks.
- **Idioms** — small observations grouped at Low severity.

**Categories skipped with reason:**

- **Concurrency correctness (full)** — no goroutines spawned in these two files. `go vet -race` is still recommended in the follow-up tooling pass but is not load-bearing here.
- **Tooling** — `go vet`, `staticcheck`, `errcheck`, `golangci-lint`, `govulncheck` deferred per the scoped-review rule. Recommended follow-up: `dev-tools-tooling-and-tests`.
- **Test quality (comprehensive)** — existing tests audited at the "what do they cover vs miss" level in finding 13; a full test-quality pass is also deferred to the follow-up scope.

**Cross-audit checks performed explicitly:**

- Looked for the same class of input-validation gap the sandbox audit found in `code_exec_tools.go` (finding 01). Result: the path-argument class is *different* in shape (no s-expression interpolation here) but equally severe in primitive (symlink escape in `isAllowed`, finding 02).
- Looked for the SSRF class the sandbox audit found in the proxy (finding 02). Result: `web_fetch` is worse (finding 03) — it doesn't even use the proxy.
- Looked for the envelope-injection class the chat engine audit found (finding 05). Result: `web_fetch` is the cleanest delivery vector for it (finding 07).
- Grepped `internal/mcp/` for `exec.Command` / `os.Setenv("HTTP_PROXY"...)` to confirm the sandbox-bypass posture of `dev_bash` and `web_fetch`.

## Findings

### By severity

**Critical (3)**

- [01 — `dev_bash` executes LLM-controlled shell commands outside the sandbox](01-critical-dev-bash-bypasses-sandbox.md)
- [02 — `isAllowed` symlink check bypassable for non-existent paths — write/edit escape](02-critical-symlink-escape-in-allowlist-check.md)
- [03 — `web_fetch` SSRF: no scheme, no IP filter, no proxy, blind redirects](03-critical-web-fetch-ssrf.md)

**High (5)**

- [04 — `dev_bash` reads unbounded output and drops caller context](04-high-dev-bash-output-unbounded-and-context-ignored.md)
- [05 — `math_eval` recursive-descent stack overflow on deep nesting](05-high-math-eval-stack-overflow.md)
- [06 — `dev_grep` loads every scanned file into memory before matching](06-high-dev-grep-loads-full-files-into-memory.md)
- [07 — `web_fetch` response body forwarded to LLM — envelope injection cross-cut](07-high-web-fetch-envelope-injection-from-response.md)
- [08 — Both transports' `CallTool` discard caller context](08-high-tool-context-not-propagated.md)

**Medium (3)**

- [09 — `dev_write` / `dev_edit` are non-atomic and silently reset file permissions](09-medium-write-edit-non-atomic-and-mode-reset.md)
- [10 — `dev_grep` / `dev_glob` walks are unbounded in file count and depth](10-medium-walk-and-glob-unbounded.md)
- [11 — Grouped input-handling and error-handling observations (5 sub-items)](11-medium-input-and-error-observations.md)

**Low (1 grouped, 10 items)**

- [12 — Grouped minor observations and small refactor opportunities](12-low-observations.md)

**Info (1 grouped)**

- [13 — Praise and design notes](13-info-praise-and-notes.md)

### By topic

**Sandbox bypass / command injection**

- [01 — `dev_bash` executes outside the sandbox](01-critical-dev-bash-bypasses-sandbox.md)

**Path traversal / symlink escape**

- [02 — `isAllowed` symlink check bypassable](02-critical-symlink-escape-in-allowlist-check.md)
- [09 — non-atomic writes + permission reset](09-medium-write-edit-non-atomic-and-mode-reset.md) (related — same write paths)

**SSRF / outbound network**

- [03 — `web_fetch` SSRF](03-critical-web-fetch-ssrf.md)
- [07 — `web_fetch` envelope injection from response body](07-high-web-fetch-envelope-injection-from-response.md)

**Memory & resource bounds**

- [04 — `dev_bash` unbounded output](04-high-dev-bash-output-unbounded-and-context-ignored.md)
- [05 — `math_eval` unbounded recursion](05-high-math-eval-stack-overflow.md)
- [06 — `dev_grep` loads files into memory](06-high-dev-grep-loads-full-files-into-memory.md)
- [10 — walk and glob unbounded](10-medium-walk-and-glob-unbounded.md)

**Concurrency correctness / cancellation**

- [04 — `dev_bash` context ignored](04-high-dev-bash-output-unbounded-and-context-ignored.md)
- [08 — both transports drop caller context](08-high-tool-context-not-propagated.md)

**Input validation / error handling**

- [11 — grouped observations (constructor error, echo paths, `intArg` fallback, edit line hint, `parseDotPath`)](11-medium-input-and-error-observations.md)

**Idioms, style, minor**

- [12 — grouped low observations](12-low-observations.md)

**Praise / design notes**

- [13 — praise](13-info-praise-and-notes.md)

## Recommended next steps

Order below reflects technical severity and fix-path coupling, not release timing.

1. **Close finding 01 first.** Whichever option is chosen — remove `dev_bash` or route through `sandbox.AgentExec` — this unblocks both finding 04 (output bounds and context) and one of the two attack legs in finding 02 (which assumes a symlink-planting primitive; `dev_bash` provides that primitive trivially before the fix).

2. **Close finding 02 in parallel.** The `isAllowed` fix is mechanical and lives in one helper used by every file tool. The shared-helper recommendation (`internal/pathsafe.ResolveUnder`) is a good cross-audit unifier — the installer audit's symlink findings and the sandbox audit's seatbelt-interpolation finding can share the same primitive.

3. **Close finding 03.** SSRF is the cleanest remote-exploit vector in nanite. The custom `DialContext` with IP filter is a known-good pattern that can be copied from other Go projects.

4. **Close finding 05 (math_eval depth limit).** Smallest diff of any High finding. Should not wait for the others.

5. **Close findings 04, 06, 08 together.** They share the "context plumbing + resource bounds" theme and all touch `CallTool` signatures. One PR can address all three.

6. **Close finding 07 in coordination with chat engine finding 05.** Best-effort scrub in `web_fetch` is a defense-in-depth layer; the real fix is in `captureEnvelopeData`. Both should land together.

7. **Mediums (09–11)** after the Criticals/Highs are out of the way. The atomic-write helper in finding 09 is a clean small helper worth extracting even before all the Highs are closed.

### Recommended follow-up scopes

- **`dev-tools-tooling-and-tests`** — deferred per the scoped-review rule. Should run `go vet`, `go test -race`, `staticcheck`, `golangci-lint run`, `errcheck`, `govulncheck` scoped to `internal/mcp/` and fill the test-coverage gaps listed in finding 13.
- **`internal/pathsafe` consolidation** — not a review scope, a refactor scope. Once findings 02 and the sandbox audit's findings 01 land, an `internal/pathsafe` package with `ResolveUnder(root, userPath)` and `SafeOpen(root, relPath, flags, mode)` primitives would unify symlink-safe path handling across sandbox, installer, and MCP dev tools. The installer audit's cross-cutting theme about shared `safe-symlink-resolver` helper still applies.

## Known issues skipped

Per the reviewer-backend context's "DO NOT re-flag" list:

- Broken `.claude/commands` / `.claude/skills` symlinks, `shadcn-ui` typo, `.agentrc/` vs `.nanite/` path drift — untouched.
- `scope_guard.go` dead-code finding — already filed in chat engine audit finding 01.
- `captureEnvelopeData` envelope injection — already filed in chat engine audit finding 05. This audit's finding 07 is filed as the **delivery-vector half** of that cross-cut, explicitly linked, not as a duplicate.
- Sandbox finding 01's `session_id` / seatbelt path — already filed in sandbox audit. This audit's finding 02 is a **different file** with a **different root cause** (`isAllowed` symlink fallback, not `filepath.Join` traversal) — filed as a distinct finding in its own trust-boundary but explicitly cross-referenced.
- The `_ context.Context` pattern that the chat engine audit noted as a general Go idiom concern is filed here as finding 08 because it has concrete downstream impact in `dev_bash` and `web_fetch`.

## Noticed but out of scope

Observations made while traversing the code that belong to adjacent scopes. Suggested follow-up in brackets.

- **`NewCodeExecTransport("")`** — `cmd/nanite/main.go:L403` passes an empty `DefaultSessionID`, so the fallback `"code-exec"` is always used in practice. If more than one agent ever shares the host, they all collide on one sandbox session. [cross-cut with sandbox hardening follow-up]
- **`loadPersistedMCPServers`** — `cmd/nanite/main.go:L407` — not opened. Persisted MCP server configs come from the store; worth an audit of how server URLs, auth, and transport selections are validated at load time. [queued: `mcp-client-transport`]
- **`mcpManager.AutoDiscover`** — `cmd/nanite/main.go:L409` — runs at boot against every registered transport. If any discovery call blocks, nanite boots slowly; if any transport panics during discovery, the boot sequence may crash. [queued: `mcp-client-transport`]
- **`memory_tools.go`** — present in `internal/mcp/` alongside the audited files, not in scope. Likely has its own trust-boundary concerns for memory store writes. [candidate: new scope `mcp-memory-tools`]
- **`self_tools_transport.go`** (762 lines per reviewer-backend context) — not read. The reviewer-backend context flags it as "where every built-in dev/general/self tool lives." Worth a dedicated pass. [candidate: new scope `mcp-self-tools`]
- **Chat engine finding 05 as a systemic issue** — `captureEnvelopeData` is the downstream consumer of every tool result in the system. The fix for that finding should define whether tools are responsible for sanitizing their own output or whether the chat engine is the single chokepoint. This audit's finding 07 leans toward "defense in depth at the tool level," but the authoritative call belongs to the chat engine maintainer. [cross-cut already tracked]
- **The `intArg` helper** in `dev_tools.go` is shared with `code_exec_tools.go` (same package) but is defined in `dev_tools.go`. Moving it to a `helpers.go` file or similar is a package-hygiene improvement. [future `internal/mcp` cleanup]
- **Tool registration is spread across main.go** — `cmd/nanite/main.go:L398-L406` has six lines adding transports by hand. A registration table or a slice-of-constructors would make it harder to forget one. [future `internal/mcp` cleanup]
- **`dev_tools` and `general_tools` share `textResult` / `errorResult` helpers** declared in `dev_tools.go:L567-L578` — the `general_tools.go` file uses them without declaring them. This is fine within one package but the helpers belong in a shared file. [future `internal/mcp` cleanup]
- **`dev_edit`'s "first replacement line" hint** — filed at 11.4, but worth noting here because the same pattern ("reconstruct line number from byte offset") shows up in other places across nanite's codebase per the sandbox audit. A shared `internal/strutil` helper would be useful. [future `internal/strutil`]
- **`web_fetch` doesn't set a `User-Agent` header** — uses Go's default `Go-http-client/1.1`. Many public APIs rate-limit or block this UA. Not a security finding, but a reliability concern. The fix is `req.Header.Set("User-Agent", "nanite/X.Y")`. [note for the web_fetch rewrite]
- **`math_eval` exposes `^` as exponentiation**, which differs from many languages where `^` is XOR. LLMs consistently get this wrong in both directions. Documentation note. [note for tool description cleanup]

## Skill refinement signals

_Fifth real run of `deep-review`, post-refinement + post-clarification + post-chat-engine-validation. See report-back notes._

## References

- `.nanite/agents/reviewer-backend.md` — project context loaded
- `~/.nanite/skills/deep-review.md` — governing skill
- `~/.nanite/roles/domain/code-review.md`, `~/.nanite/roles/stack/go.md` — identity roles
- Prior audits: `2026-04-10-sandbox-hardening`, `2026-04-10-chat-engine`, `2026-04-10-installer`, `2026-04-10-plugin-system-plan-eval`
- `docs/audits/INDEX.md` — campaign index
