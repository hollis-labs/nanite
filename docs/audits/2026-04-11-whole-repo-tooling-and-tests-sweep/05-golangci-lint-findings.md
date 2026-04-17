# golangci-lint findings — 2026-04-11 sweep

**Command:** `golangci-lint run --timeout 10m --max-issues-per-linter=0 --max-same-issues=0`
**Config:** project-committed `.golangci.yml` (v2 format, 11 linters enabled)
**Exit code:** 1
**Total findings:** 956 issues

> **Cap note:** running the same command without `--max-issues-per-linter=0 --max-same-issues=0` reports only 283 issues — each linter is capped at 50 findings and each message at 3 repetitions by default. Local devs and a default-flag CI invocation will see the 283 number. The 956 number is the ground truth. **The discrepancy is itself a finding**: any CI or lefthook invocation that relies on the default caps is silently swallowing 673 lint signals.

## Severity framing

Per the task brief, lint findings get mechanical severities:
- **Low** — style, idiom, naming, typos, missing godoc.
- **Medium** — correctness-adjacent lint findings that would become a bug under the right circumstances (error-handling drift, nil-err patterns, unchecked errors on writes, missing switch cases in permission / security code).
- **High / Critical** — reserved for lint findings that name a concrete bug a reader can trace to impact. Most of those are already captured in the per-subsystem audits; this file cross-refs them rather than re-flagging.

## Findings by linter

### gosec (392) — Low to Medium

Distribution by rule code:

| Rule | Count | Short description |
|---|---|---|
| G306 | 132 | WriteFile permissions should be 0600 or less |
| G304 | 117 | Potential file inclusion via variable (taint) |
| G301 | 78 | Directory permissions should be 0750 or less |
| G204 | 21 | Subprocess launched with tainted input or variable |
| G703 | 8 | Path traversal via taint analysis |
| G706 | 6 | Log injection via taint analysis |
| G704 | 5 | SSRF via taint analysis |
| G120 | 4 | (archive/zip slip or similar) |
| G118 | 4 | Goroutine uses `context.Background/TODO` while request-scoped context is available |
| G404 | 3 | Weak RNG (math/rand instead of crypto/rand) |
| G501 | 2 | Blocklisted import crypto/md5 |
| G401 | 2 | Use of weak cryptographic primitive |
| G305 | 2 | (File traversal when extracting) |
| G115 | 2 | Integer overflow conversion |
| G110 | 2 | (Potential DoS via decompression bomb) |
| G402 | 1 | TLS InsecureSkipVerify set to true |
| G201 | 1 | SQL string formatting |
| G114 | 1 | Use of `net/http` serve without timeouts |
| G112 | 1 | Potential Slowloris — `http.Server` with no `ReadHeaderTimeout` |

#### Subsystem notes — cross-audit

- `internal/sandbox/proxy.go` — G112 at `:45`, G706 at `:90`/`:147`, G704 at `:96`/`:153`/`:171`. The G704 SSRF-taint hits overlap with findings 02 (proxy SSRF via DNS-resolved IPs) and 03 (missing port restrictions) from `docs/audits/2026-04-10-sandbox-hardening/`. **G112 is NEW and not covered by that audit**: the sandbox proxy's `http.Server` at `proxy.go:45` is constructed with only `Handler` set — no `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, or `IdleTimeout`. A slow-header client can tie up a server goroutine indefinitely. The proxy is listening on localhost only (mitigates external Slowloris), but any sandboxed subprocess that can reach localhost can trigger the condition. Severity: **Medium** (contained blast radius because localhost-only, but easy to exploit from inside a sandboxed process, which is exactly the threat model).
- `internal/server/server.go:63` — G114 `http.Serve` without timeouts. This is the main HTTP server that hosts the nanite API. Uses `http.Server` with read/write timeouts set elsewhere; gosec is likely tripping on a specific call pattern. Cross-ref: the main server config path goes through `internal/server/server.go` and the reviewer-backend context calls out this package for middleware order review. **Not in an existing audit** — deserves a direct look under `api-privilege-boundary` queued scope.
- `internal/mcp/general_tools.go:5` — G501 crypto/md5 blocklisted import; `:399` — G401 weak crypto primitive. Used for URL hashing / cache keys in `web_fetch`/`web_search`. Non-security use. Cross-ref: `2026-04-10-mcp-client-transport` audit — not re-flagged; the existing audit's no-validation-anywhere finding is the real concern.
- `internal/mcp/self_tools_transport.go:466,471` — G704 SSRF taint analysis. Overlaps with the `self-tools-transport` queue item (spawned from dev-tools audit). Not re-flagged here.
- `internal/service/chat_generate.go:760,762` — G118 `context.Background` in goroutine. Cross-ref: `2026-04-11-concurrency-cancellation-sweep` authoritative finding on `generateResponse` goroutine orphaning. Context drop is deliberate here (`captureEnvelopeData` must outlive the stream) but there is no replacement shutdown-context — same gap documented in the concurrency sweep.
- `internal/memory/extraction.go:105,136` — G118 same class. Consistent with the concurrency sweep's "no uniform goroutine lifecycle" theme. Not re-flagged.
- `internal/plugin/subprocess/manager.go:125` — G204 subprocess launched with tainted input. Overlaps with `2026-04-10-plugin-system-plan-eval`. Not re-flagged.
- `internal/worktree/manager.go:90,120,129,173,181` — G204 subprocess launched with variable. **This package is NOT covered by any completed audit.** `internal/worktree/` runs git commands with branch and path arguments from callers. Candidate scope: `worktree-subprocess-audit` under "Noticed but out of scope" in the index.
- `internal/tool/yaml_loader.go:180,233` — G204 subprocess launched with variable. Also not in any completed audit. Candidate scope: `tool-yaml-loader-audit`.
- `pkg/provider/pty.go:105` — G204 on PTY subprocess spawn. Expected given the PTY design. Cross-ref: reviewer-backend context §"Chat engine & provider abstraction" lists the PTY bridge as a priority target for future audit. Not re-flagged here.
- `plugins/support-ticket/tickets.go:171` — G404 weak RNG for ticket ID generation. Cosmetic — ticket IDs don't need crypto/rand. Low.

#### Subsystem notes — not yet audited

The following gosec findings are in subsystems not yet covered by a deep-review audit. Each is a candidate for a future focused pass:

- `internal/sandbox/proxy.go:45` G112 Slowloris — **captured as a new Medium finding** (see "NEW FINDINGS" section below).
- `internal/server/server.go:63` G114 — reviewer should verify the API server's timeout configuration end-to-end.
- `internal/assets/framework.go:87,103` G306 — `WriteFile(..., 0644)` for extracted framework assets. Non-sensitive content but inconsistent with the project's default mode. Low.
- `internal/chat/errors.go:91` G404 — `math/rand` for error ID generation. Low.
- `internal/coordination/badger.go:92` G115 — uint64→int64 overflow conversion. Depends on actual range of values; badger.Size() returns uint64 — if it could exceed MaxInt64, the cast wraps. Realistically a 9 exabyte store isn't a concern. Low.
- `internal/plugin/scaffold/scaffold.go:83,105,116,124` G301 — scaffold directory creation with 0755. Intentional (scaffold templates are meant to be readable). Low.
- `internal/plugin/scaffold/scaffold.go:163` G304 — `os.Open` on user-provided template path. Scaffold is a developer tool, not a runtime tool; low risk. Low.
- `internal/service/install/migrate.go:248` G703 — path traversal via taint. **This is the installer package.** Cross-ref: `2026-04-10-installer` audit found 0 Criticals and 4 Highs. Symlink-following and managed-section parser risks are already covered. G703 hit at `:248` is in the migration path — deserves a read to see if the existing audit covers it.

### errcheck (239) — Low to Medium

Distribution by top directories:

| Directory | Count |
|---|---|
| `internal/api/` | ~70 |
| `internal/store/` | ~40 |
| `internal/plugin/` | ~30 |
| `pkg/provider/` | ~20 |
| `cmd/nanite/` | ~15 |
| `internal/server/` | ~10 |
| rest | ~54 |

#### Highest-volume files

| File | Count | Pattern |
|---|---|---|
| `internal/api/plugins.go` | 23 | JSON encoder writes, Close() calls |
| `internal/api/catalog.go` | 13 | Same shape |
| `cmd/nanite/main.go` | 8 | `fs.Parse(args)`, Close() in subcommand setup |
| `cmd/nanite/a2a_cmd.go` | 8 | `fs.Parse`, `s.Close` on shutdown |
| `internal/store/agents.go` | 7 | Store write helpers |
| `plugins/fragments-engine/plugin.go` | 6 | Plugin helpers |
| `internal/worktree/manager.go` | 6 | git subprocess calls |
| `internal/store/sessions.go` | 6 | SQL writes |
| `internal/server/server.go` | 6 | HTTP write responses |
| `internal/api/mcp_servers.go` | 6 | Same shape |

#### Classification

Most of these fall into three categories:

1. **`defer resp.Body.Close()` and `defer f.Close()`** — conventionally ignored in Go. Low.
2. **`fs.Parse(args)` in CLI setup** — error return is "usage error" and cobra-style frameworks just print it. Low.
3. **`json.NewEncoder(w).Encode(v)` in HTTP handlers** — write error is unrecoverable by the handler (header is already sent). Low, but the project uses `a.jsonResp(w, status, data)` helpers in most places; the raw `json.NewEncoder(w).Encode` call sites are inconsistent with the established pattern.

#### Medium — `Stop()` / `Close()` return value ignored on teardown

Specific sites where the unchecked error is on a real teardown contract, not a cosmetic defer:

- `internal/plugin/subprocess/plugin.go:129,143,155` — `sp.mgr.Stop()` ignored in 3 error paths. `subprocess.Manager.Stop` is the reference-implementation sound lifecycle per the `concurrency-cancellation-sweep`. If Stop returns an error (e.g., shutdown timeout exceeded, force-kill failed), the caller loses that signal. Medium.
- `internal/plugin/catalog.go:258` — `defer f.Close()` on a file being **written to**. Unlike read-close, write-close can fail with data loss if buffered data hasn't flushed. The catalog cache files are written during plugin discovery. Medium.
- `internal/plugin/scaffold/scaffold.go:167` — same pattern, `defer f.Close()` on a write. Scaffold is developer-facing so data loss here is low stakes. Low.
- `internal/mcpconfig/mcpconfig.go:145,150` — `json.Unmarshal` errors ignored on parsing `sc.Args` and `sc.Env` from the DB-persisted MCP config. If the stored JSON is corrupted, the MCP server launches with empty args/env and silently misbehaves. Medium.
- `internal/plugin/auto_triggers.go:39` — `host.RegisterEventHook(eventTypes, handler)` error ignored. If registration fails, the plugin's event hooks silently don't fire. Medium.
- `internal/builders/registry.go:67,68,69` — `r.Register(NewAgentBuilder(s))` × 3. Builder registration failures are silent. Medium.
- `internal/plugin/catalog.go:221` — `os.MkdirAll(cf.cacheDir, 0755)` ignored. If the cache dir can't be created, subsequent file writes fail anyway — Low.
- `internal/crossapp/engine_client.go:52` — `defer resp.Body.Close()` on an HTTP response. Low.

### govet (114) — Low

All 114 findings are from the `shadow` analyzer, which is NOT enabled by default in `go vet ./...` but IS enabled via golangci-lint's `govet` config. That's why step 1 (`go vet ./...`) reports zero issues while step 5's govet linter reports 114.

Distribution:
- **Shadowed `err`** — 111 findings. The dominant pattern: `if err := ...; err != nil { ... }` inside a function that already has an outer `err` variable. Go idiomatically accepts this; the shadow analyzer is stricter than community consensus. Low.
- **Shadowed `ok`** — 4 findings (type-assertion ok-comma pattern).
- **Unused write to field** — 2 findings at `pkg/provider/...` on `apiKey`. Worth a direct look — a write to a struct field that's never read is usually a bug.

Top files by shadow count:

| File | Count |
|---|---|
| `internal/service/install/migrate.go` | 10 |
| `internal/service/install/integration_test.go` | 8 |
| `internal/builders/builder_test.go` | 7 |
| `cmd/nanite/main.go` | 7 |
| `pkg/provider/cache_test.go` | 4 |
| `internal/store/execution_metrics_test.go` | 4 |
| `internal/service/install/migrate_test.go` | 4 |
| `internal/service/context_test.go` | 4 |
| `internal/service/a2a/handoff_test.go` | 4 |
| `internal/api/memories.go` | 4 |

Severity: **Low** across the board. Shadow warnings are a style preference, not a correctness issue, unless a specific site misuses the shadowed value. Worth a grep for the 2 "unused write to field apiKey" hits — those are more actionable.

### misspell (65) — Low

| Typo | Count | Fix |
|---|---|---|
| `cancelled` → `canceled` | 54 | — |
| `behaviour` → `behavior` | 4 | — |
| `serialises`/`serialised` → `serialize*` | 2 | — |
| `summarises` → `summarizes` | 1 | — |
| `recognised` → `recognized` | 1 | — |
| `Initialise` → `Initialize` | 1 | — |
| `honour` → `honor` | 1 | — |
| `defence` → `defense` | 1 | — |

All British-vs-American spellings. `cancelled` vs `canceled` is literally a stdlib consistency issue (`context.Canceled` is the canonical spelling). Low severity. Mechanical fix. Sample sites:

- `internal/mcp/stdio_transport.go:132` — "cancelled"
- `internal/plugin/events.go:580,588` — "cancelled" in event catalog
- `internal/plugin/filter.go:21` — "defence" in a comment about defense-in-depth
- `internal/permission/engine.go:219` — "cancelled" in permission logic

### staticcheck (43) — Low

Breakdown of most common rules:
- **QF1012** (majority) — `WriteString(fmt.Sprintf(...))` should be `Fprintf(...)`. Pure style. Concentrated in `internal/chat/commands_builtin.go` (many sites), `internal/chat/context.go`, `internal/chat/commands.go`, `internal/mcp/memory_tools.go`, `internal/contextbroker/source_memory.go`.
- **ST1005** — error strings capitalized. `internal/plugin/builtin/giphy/giphy.go:117`.
- **ST1020** — exported function comment should start with function name. `internal/tool/broker/rules.go:129`, `internal/chat/envelope.go:94`.
- **ST1021** — exported type comment. `internal/plugin/types.go:7`.
- **ST1022** — exported const comment. `internal/task/backend.go:18`.
- **SA1019** — `strings.Title` deprecated. `internal/plugin/scaffold/scaffold.go:79`. Low but worth fixing — the replacement is `golang.org/x/text/cases`.
- **S1011** — `append` with slice of slices. `internal/plugin/host.go:1045`.
- **S1039** — unnecessary `fmt.Sprintf`. `internal/plugin/builtin/sessionstats/handlers.go:192`, `internal/chat/commands_builtin.go:89`.
- **SA9003** — empty branch. `internal/chat/proctrack_test.go:301`.

All Low. None map to the concerns the reviewer-backend context flags as priority.

### unused (37) — Low, with two non-trivial cross-audit signals

Complete list of unused symbols:

```
internal/api/catalog.go:434:6: func checksumFile is unused
internal/sandbox/sandbox.go:58:6: func writeFile is unused
internal/service/chat_test.go:16-71: type stubSessionService + 7 methods unused
internal/service/chat_test.go:34-48: type stubAgentService + 8 methods unused
internal/service/chat_test.go:50-62: type stubToolService + 4 methods unused
internal/service/chat_test.go:66-71: type stubContextService + 2 methods unused
internal/service/chat_tool_executor.go:47: field originalIndex unused
internal/service/chat_tool_executor.go:56: type toolExecContext unused
internal/service/tool_test.go:14-20: type stubMCPManager + 1 method unused
pkg/provider/event_pipeline_test.go:274: field events unused
pkg/provider/pty_aider.go:112: func parseAiderJSON is unused
pkg/provider/pty_codex.go:50: type codexTurnCompleted is unused
pkg/provider/pty_kiro.go:109: func parseKiroJSON is unused
```

**Two non-trivial signals worth flagging:**

1. **`pkg/provider/pty_aider.go:parseAiderJSON`** and **`pkg/provider/pty_kiro.go:parseKiroJSON`** — defined but never called. These are JSON-stream parsers for PTY adapters claiming to parse structured output from the Aider and Kiro CLIs. The reviewer-backend context already flags `adapter-opencode` as "format unverified against Opencode CLI" (per `plugin-dev.md`). Finding two **more** PTY adapters with dead parse code is strong evidence the PTY adapter suite has a systemic "parser written before the real CLI output was characterized, never wired up" problem. **Severity: Medium as a pattern**. Individual instances are Low (dead code), but the aggregation suggests `adapter-opencode`, `pty_aider`, `pty_kiro`, and `pty_codex` (the `codexTurnCompleted` dead type) should all be verified against the actual CLI streams they claim to handle. Candidate scope: `pty-adapter-stream-correctness`.

2. **`internal/service/chat_test.go` test-stub block** — 29 distinct unused symbols (4 stub types + 25 methods). This is a full set of test doubles for the chat service that aren't wired into any test. Either they were scaffolded for a test file that never got written, or a test was deleted without cleaning up the stubs. Severity: Low (dead test code is harmless), but it's Medium as a signal that the chat service test coverage has a gap — someone wanted to test `SessionService`/`AgentService`/`ToolService`/`ContextService` in isolation and didn't finish. Cross-ref: `2026-04-10-chat-engine` audit noted `scope_guard.go` + `pkg/provider/event_pipeline.go` wrapper are dead code supposedly implementing prompt-injection containment — this is a different kind of dead code (test stubs, not production defense), but consistent with a pattern of "component written, never wired in."

### errorlint (20) — Low to Medium

Three patterns:
- **`==`/`!=` on errors** — 10 sites. Uses direct equality where `errors.Is` is required for wrapped-error robustness. `internal/coordination/badger.go` (6 sites inside the badger wrapper's Get/Put paths + tests), `internal/api/bookmarks.go:85`, `internal/store/sessions.go:223` (session fetch error path).
- **Type assertion on error** — 4 sites. Uses `err.(*SomeType)` where `errors.As` is required. `internal/plugin/subprocess/plugin.go:415`, `internal/plugin/subprocess/plugin_test.go:260`, `internal/plugin/subprocess/transport_test.go:122`, `internal/sandbox/exec.go:199`, `internal/shell/exec.go:76`.
- **Non-wrapping `%v`/`%s` format verbs in `fmt.Errorf`** — 6 sites. Uses `%v` where `%w` is required to preserve the chain. Concentrated in `internal/service/a2a/` (handoff.go, service.go, subscribe.go) — that package has a consistent "don't wrap errors" pattern.

Severity: **Medium**. `errorlint` findings are correctness-adjacent — they indicate places where an error that gets wrapped by middleware or retry logic will stop matching. `internal/store/sessions.go:223` and `internal/coordination/badger.go` hits are the ones most likely to cause a real misbehavior under error wrapping. The `internal/service/a2a/` cluster deserves a direct look — if any of those errors cross a boundary where wrapping is expected, they become silently-swallowed errors.

### nilerr (12) — Medium

These are the highest-signal findings in the lint output. Each instance is: "error is non-nil but the function returns `nil`." Classic swallowed-error pattern. Sites:

```
internal/api/autocomplete.go:65, 71
internal/builders/tools.go:157, 179
internal/mcp/dev_tools.go:411, 423, 429
internal/plugin/builtin/adapter-nanite-native/plugin.go:247, 359, 407
internal/plugin/subprocess/plugin.go:291
internal/store/session_overrides.go:15
```

Cross-audit coverage:
- `internal/mcp/dev_tools.go` — already under `2026-04-10-dev-tools-input-validation`. The 3 hits here are likely the same swallowed-error pattern the audit noted in the symlink-escape finding. Not re-flagged.
- `internal/plugin/subprocess/plugin.go` — already under `2026-04-10-plugin-system-plan-eval`. Not re-flagged.
- `internal/plugin/builtin/adapter-nanite-native/plugin.go` (3 hits) — adapter plugin. The adapter plugins are flagged for a future pass in the reviewer context but not yet audited. **Not covered by any existing audit — candidate for `adapter-plugins-audit`**.
- `internal/builders/tools.go` (2 hits) — builder package not covered. Low priority.
- `internal/api/autocomplete.go` (2 hits) — API handler not covered. Included in the queued `api-privilege-boundary` scope by virtue of being an `internal/api/` handler.
- `internal/store/session_overrides.go:15` — store file not covered. The reviewer-backend context queues `store-and-migrations` — this is a known gap.

Severity: **Medium** — these are real swallowed-error sites the deeper audits should investigate, not lint noise.

### revive (18) — Low

- **`redefines-builtin-id`** — 11 sites redefining `max`, `cap`, `real`, `new`. In Go 1.21+, `max`/`cap` are builtin functions; in 1.18+ `real` and `new` already were. Shadowing them is legal but confusing. Concentrated in `internal/service/chat_loop_state.go` (`max`), `internal/worker/manager.go` (`max`), `internal/workflow/store.go` (`cap`), `internal/mcp/dev_tools.go` (`real`, `new` variables). Low — no behavioral impact, but worth a renaming pass.
- **`blank-imports`** — 3 sites (`internal/plugin/allplugins/allplugins.go:11`, `internal/plugin/builtin/sessionstats/handlers.go:11`, `internal/store/store.go:12`). Blank imports should be in `main` or `_test` packages or have a comment justifying them. Each should get a one-line comment. Low.
- **`context-as-argument`** — `internal/plugin/subprocess/transport.go:142` has context.Context as a non-first parameter. Low idiom deviation.
- **`var-naming`** — 3 sites with underscores in Go identifiers. Low.

### unparam (11) — Low

Functions / methods where a parameter is always the same value, or a return value is always the same. Notable:
- `cmd/nanite/a2a_cmd.go:295` — `newA2AServiceForCLI` result `error` is always nil. Low idiom deviation.
- `internal/mcp/self_tools_transport.go:275,384` — `callListAgents` and `callRefreshEngine` both take an `args` parameter they never use.
- `internal/sandbox/exec.go:179` — `runCmd` takes a `timeout` parameter it ignores. **Worth a direct read** — a function named `runCmd` that accepts but ignores a `timeout` is a bug shaped like documentation.
- `pkg/provider/event_pipeline.go:177` — `(*EventReactionPipeline).shouldTerminate` takes a `ctx` it never uses. Cross-ref: `2026-04-10-chat-engine` already flagged `event_pipeline.go` as dead code. Consistent.
- `internal/service/chat_tool_executor.go:379` — `postProcessToolResults` takes `ctx` it never uses. Cross-ref: the chat engine audit flagged the three-phase split as a well-designed pattern; the unused ctx here is minor.

### ineffassign (3) — Low

- `internal/api/sessions.go:268` — `total` assigned but never read.
- `internal/service/tool.go:126` — `seen` assigned but never read.
- `pkg/provider/model_ops_test.go:34` — `prov` in a test.

### unconvert (1), exhaustive (1)

- `internal/plugin/subprocess/transport_test.go:64` — unnecessary type conversion. Low.
- `internal/service/chat_tool_executor.go:118` — exhaustive switch missing `permission.DecisionAllow`. **Medium** — permission-decision switches that are missing cases are the shape of a silent privilege bug. Worth a direct read.

## NEW FINDINGS surfaced by the sweep (not in any prior audit)

Filing these concisely here rather than as per-file numbered findings, per the task brief that this is a mechanical sweep:

### 1. Slowloris via sandbox proxy — Medium

**Site:** `internal/sandbox/proxy.go:45`
**Rule:** gosec G112
**Problem:** The sandbox proxy's `http.Server` is constructed with only `Handler` set. No `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, or `IdleTimeout`. A sandboxed subprocess that can reach the localhost proxy (which is the whole point — every sandboxed subprocess has `HTTP_PROXY` pointing here) can stall the proxy's accept loop by slow-feeding HTTP headers. Low impact on external attackers (localhost only) but high relevance for the threat model: the sandbox proxy is specifically the control boundary between partially-trusted sandboxed processes and the outbound network.

**Recommendation:** Set `ReadHeaderTimeout: 5 * time.Second` at minimum. Also set a reasonable `ReadTimeout` and `WriteTimeout`. Example:

```go
p.server = &http.Server{
    Handler:           http.HandlerFunc(p.handleRequest),
    ReadHeaderTimeout: 5 * time.Second,
    ReadTimeout:       30 * time.Second,
    WriteTimeout:      30 * time.Second,
    IdleTimeout:       60 * time.Second,
}
```

**Cross-ref:** `2026-04-10-sandbox-hardening` authoritative for sandbox proxy; this finding is a distinct Medium not in that audit.

### 2. Main HTTP server timeout gap — Medium (needs verification)

**Site:** `internal/server/server.go:63`
**Rule:** gosec G114
**Problem:** gosec reports "Use of net/http serve function that has no support for setting timeouts." Needs a direct read to confirm — may be a false positive if `s.Server` (not `http.Serve`) is used with timeouts set on the struct elsewhere. If real, this is the public HTTP API surface and needs full timeout configuration.

**Action:** Direct read required before classifying. Defer to `api-privilege-boundary` queue item.

### 3. `runCmd` accepts `timeout` but ignores it — Medium

**Site:** `internal/sandbox/exec.go:179`
**Rule:** unparam
**Problem:** `func runCmd(..., timeout duration)` ignores the `timeout` parameter. Any caller relying on the timeout to bound a subprocess run gets no enforcement. Sandbox subprocess execution with no timeout is exactly the class of bug that's invisible until a subprocess hangs.

**Action:** Direct read required to determine whether callers actually depend on the timeout. If yes, this is a Critical-class bug (unbounded subprocess hang in sandbox.exec). Flagged here as Medium pending verification.

### 4. Permission-decision switch missing case — Medium

**Site:** `internal/service/chat_tool_executor.go:118`
**Rule:** exhaustive
**Problem:** `switch` on `permission.Decision` type is missing the `permission.DecisionAllow` case. If the default case correctly handles "allow", this is Low. If it doesn't, this is a silent allow-by-default or deny-by-default bug in tool-call permission handling.

**Action:** Direct read required. Defer to the queued `api-privilege-boundary` scope which should cover `toolclient/permissions.go` and its callers.

### 5. Default golangci-lint caps hide 70% of the signal

**Problem:** Running `golangci-lint run` (as lefthook or a developer would) caps at 50 issues per linter and 3 repetitions per message. Uncapped reports 956 issues; capped reports 283. A CI check that relies on the default invocation sees only 30% of the lint debt.

**Recommendation:** Either:
- (a) Lower the actual lint debt until the cap doesn't matter (target state), or
- (b) Pin `.golangci.yml` to include `issues.max-issues-per-linter: 0` and `issues.max-same-issues: 0` so runs always show the full signal, or
- (c) Add a separate `golangci-lint run --max-issues-per-linter=0 --max-same-issues=0` invocation to a slower "deep lint" CI job.

**Severity:** Medium as a process finding. The debt itself is captured above; this is specifically about the visibility gap.
