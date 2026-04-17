# Whole-repo tooling and tests sweep — 2026-04-11

**Scope:** whole-repo-tooling-and-tests-sweep
**Reviewer:** nanite-reviewer-backend
**Branch:** audit-campaign-2026-04-11
**HEAD:** `git rev-parse HEAD` at dispatch time
**Shape:** mechanical tool sweep. Supersedes the queue items `plugin-tooling-and-tests`, `sandbox-tooling-and-tests`, and `installer-tooling-and-tests` (INDEX.md items 9–11).

## Scope

Run each of the eight canonical Go tooling commands against the entire Nanite tree once, capture output, classify signal. No code modifications. No test re-runs. No fixes. Output is an audit folder with one per-tool failure file plus an index summarizing the sweep.

Packages covered: whole module `github.com/hollis-labs/nanite` including `cmd/`, `internal/`, `pkg/provider/`, `plugins/`. Local `replace` sibling libs (`framework/libs/go-plugin`, `go-toolbroker`, `go-otel`, `go-providers`, `vanta-conduit`) are out of module scope — golangci-lint / go test exercise them transitively but findings reported here are filtered to files inside the nanite repo.

## Methodology

Eight-step tool list, run in order. Exact commands:

1. `go vet ./...`
2. `go build ./...`
3. `go test -race ./...`
4. `staticcheck ./...` — not installed
5. `golangci-lint run --timeout 10m --max-issues-per-linter=0 --max-same-issues=0` — project config `.golangci.yml` (v2 format, 11 linters enabled; covers staticcheck + errcheck as bundled linters)
6. `govulncheck ./...` — not installed
7. `errcheck ./...` — not installed (but bundled into golangci-lint and the errcheck findings are captured in `05-golangci-lint-findings.md`)
8. `go mod tidy` + `git diff go.mod go.sum` (diff revert via `git checkout -- go.mod go.sum`)

Notes on deferred/dropped tools:
- `staticcheck`, `govulncheck`, and `errcheck` standalone binaries are absent from this machine. Per the task brief, tools are NOT installed on demand. Their absence is recorded as a finding in `tool-availability.md`.
- golangci-lint v2 bundles `staticcheck` (SA/ST/S/QF analyzer families) and `errcheck` as linters, so the equivalent signal is captured — just not under the standalone binary names. Running the standalone tools in future sweeps would add (a) `govulncheck` CVE scanning, which golangci-lint does not include, and (b) the option to run staticcheck/errcheck with different rules than the golangci-lint profile.
- First golangci-lint pass was run with default caps (50 per linter, 3 per message) and reported 283 issues. A second uncapped pass (`--max-issues-per-linter=0 --max-same-issues=0`) reported **956 issues**. The uncapped totals are the source of truth; the capped numbers are noted because they're what `golangci-lint run` without flags produces and thus what CI or a developer running the command off-the-cuff would see.

## Summary table

| # | Tool | Status | Count | Exit |
|---|------|--------|-------|------|
| 1 | `go vet ./...` | ok | 0 findings | 0 |
| 2 | `go build ./...` | ok | compiles clean | 0 |
| 3 | `go test -race ./...` | ok | 44 pkgs pass, 18 pkgs no tests, 0 fail | 0 |
| 4 | `staticcheck ./...` | NOT INSTALLED | — | — |
| 5 | `golangci-lint run` (uncapped) | findings | 956 issues across 13 linters | 1 |
| 6 | `govulncheck ./...` | NOT INSTALLED | — | — |
| 7 | `errcheck ./...` | NOT INSTALLED standalone | 239 via golangci-lint | — |
| 8 | `go mod tidy` diff | drift | 1 line (indirect marker) | — |

## Top-line severity

**Overall:** Pass with known lint debt.

- Compile and test surface is clean: `go vet`, `go build`, and `go test -race ./...` all pass. No races. No hangs. No skipped packages.
- golangci-lint under the project's own `.golangci.yml` reports 956 issues — dominated by `gosec` (392), `errcheck` (239), `govet` shadow-checking (114), and `misspell` (65). Most are Low. A handful map to real subsystem concerns already flagged in completed audits (dev_bash subprocess warnings, sandbox proxy SSRF taint, MCP general_tools SSRF).
- `go mod tidy` produces a one-line drift: `github.com/mattn/go-isatty` is marked `// indirect` but is a direct dependency. Medium.
- `staticcheck`, `govulncheck`, and `errcheck` standalone binaries are absent from the dev environment. The project's `.agentrc/agents/backend.md` §Build & Run and reviewer context both reference these as part of the canonical tool set, but they cannot run reproducibly without being installed. Medium — documented in `tool-availability.md`.

## Per-tool results

### 1. `go vet ./...` — pass
No findings. No file created. Note: standalone `go vet` runs only the default analyzer set, which does NOT include `shadow`. golangci-lint's `govet` linter opts into `shadow` and reports 114 shadowed-variable findings — see `05-golangci-lint-findings.md` §govet. This is not a contradiction; it's two different tool invocations.

### 2. `go build ./...` — pass
No findings. The full module compiles clean on `go version go1.26.1 darwin/arm64` with local `replace` directives resolved.

### 3. `go test -race ./...` — pass
44 packages passed under the race detector. 18 packages have no test files. 0 failures. 0 skipped. Full per-package run log captured at `/tmp/nanite-test-race.log` during the sweep (not preserved in the audit folder — re-running `go test -race ./...` reproduces it).

Longest-running packages (wall time, race-instrumented):
- `internal/mcp` — 65.863s
- `pkg/provider` — 32.831s
- `internal/worktree` — 2.765s
- `internal/chat` — 2.198s
- `internal/workflow` — 1.802s
- `internal/tool` — 1.496s

No per-package hang. No flakes observed in the single run. (Per task rules, flakes are not re-run.)

### 4. `staticcheck ./...` — not installed
See `tool-availability.md`. Equivalent signal is captured inside golangci-lint under the `staticcheck` linter (43 findings, see `05-golangci-lint-findings.md` §staticcheck), but the standalone tool would run the full SA/ST/S/QF families and may differ from what golangci-lint's `staticcheck` linter emits in v2.

### 5. `golangci-lint run` — 956 findings
See `05-golangci-lint-findings.md`.

Breakdown by linter (uncapped):

| Linter | Count | Notes |
|---|---|---|
| gosec | 392 | Dominated by file-permission and path-traversal-taint warnings |
| errcheck | 239 | Unchecked error returns throughout |
| govet | 114 | All shadow-checker — mostly shadowed `err` in nested blocks |
| misspell | 65 | Typos in comments and strings |
| staticcheck | 43 | Mostly `QF1012` (Fprintf style) |
| unused | 37 | Includes two PTY JSON parsers (`parseAiderJSON`, `parseKiroJSON`) never called — cross-audit signal |
| errorlint | 20 | `==` on errors, non-wrapping `%v` |
| revive | 18 | blank imports, `max`/`cap`/`real`/`new` redefinitions |
| nilerr | 12 | Returning `nil` when an in-scope error is non-nil |
| unparam | 11 | Always-same return values, unused parameters |
| ineffassign | 3 | Dead assignments |
| unconvert | 1 | `internal/plugin/subprocess/transport_test.go:64` |
| exhaustive | 1 | Missing `permission.DecisionAllow` in `internal/service/chat_tool_executor.go:118` |

### 6. `govulncheck ./...` — not installed
See `tool-availability.md`. No CVE signal available from this sweep.

### 7. `errcheck ./...` — not installed (standalone)
See `tool-availability.md`. Signal is captured inside golangci-lint (239 findings, see `05-golangci-lint-findings.md` §errcheck). Running standalone `errcheck` would likely produce a different (usually larger) set because golangci-lint's `errcheck` linter ignores some call sites the standalone tool would flag.

### 8. `go mod tidy` — drift
See `08-go-mod-tidy-drift.md`.

## Cross-audit notes

Multiple lint findings sit squarely inside subsystems already audited. They are NOT re-flagged as new findings; cross-references below show the overlap:

- **Dev tools (`2026-04-10-dev-tools-input-validation`)** — `internal/mcp/dev_tools.go` has 3 `nilerr` findings, 1 `revive redefines-builtin-id` (`real`), and various other low-level hits. The subsystem's critical findings (RCE via `dev_bash`, symlink escape in `isAllowed`) are documented in the existing audit. The lint findings here are adjacent noise, not new threats.
- **MCP client transport (`2026-04-10-mcp-client-transport`)** — `internal/mcp/general_tools.go` shows G501 (crypto/md5 blocklisted import) and G401 (weak crypto primitive). These are at `general_tools.go:5` and `:399` — the existing audit's critical findings (leaked subprocess/goroutine/FD per timeout; no response-size cap; no validation anywhere) are unrelated and the audit stays authoritative for the subsystem.
- **Sandbox hardening (`2026-04-10-sandbox-hardening`)** — `internal/sandbox/proxy.go` shows G112 (Slowloris), G706 (log injection taint), G704 (SSRF taint). The existing audit's findings (proxy SSRF via DNS-resolved IPs, missing port restrictions, Linux silent fallback) overlap with the G704 warnings; the existing audit is authoritative. G112 (Slowloris / `ReadHeaderTimeout` missing on the proxy's `http.Server`) is a NEW finding not covered in `sandbox-hardening` — see `05-golangci-lint-findings.md` §gosec-subsystem-notes.
- **Chat engine (`2026-04-10-chat-engine`)** — `internal/service/chat_generate.go:760,762` G118 (`context.Background/TODO while request-scoped context is available`) overlaps with the existing audit's no-panic-recovery and shared-provider-callback-race findings. The G118 hits are at the `captureEnvelopeData` goroutine spawn site — the context-drop is deliberate (`context.WithoutCancel`) for envelope capture after stream end. Noted as a cross-ref, not re-flagged. The concurrency-cancellation-sweep (2026-04-11) already documented this behavior as the source of orphaned `generateResponse` goroutines at `internal/service/chat.go:165/209/240`.
- **Memory extraction (`2026-04-11-concurrency-cancellation-sweep` context)** — `internal/memory/extraction.go:105,136` G118 hits are the same category: `context.Background` used in a goroutine spawned from a request path. Consistent with the concurrency-cancellation-sweep's "no uniform goroutine lifecycle discipline" theme. Not re-flagged.
- **Plugin subprocess (`2026-04-10-plugin-system-plan-eval`)** — `internal/plugin/subprocess/manager.go:125` G204 (subprocess with tainted input) and 3 `errcheck` on `sp.mgr.Stop()` at `plugin.go:129/143/155`. The unchecked Stop() errors are on the teardown path — consistent with the concurrency-cancellation-sweep's finding that `plugin/subprocess/Manager.Stop` is the *only* sound subsystem lifecycle in the tree, but that soundness doesn't extend to the caller ignoring its return value.
- **PTY providers (`pkg/provider/pty_*.go`)** — `parseAiderJSON` (`pty_aider.go:112`) and `parseKiroJSON` (`pty_kiro.go:109`) and `codexTurnCompleted` type (`pty_codex.go:50`) are flagged as `unused` by golangci-lint. This is significant: the reviewer-backend context specifically notes `adapter-opencode` as "format unverified against Opencode CLI" and flags it as a High candidate. Here three additional PTY adapter parsers (Aider, Kiro, Codex) appear to have dead parse code — either because they were built speculatively and the actual stream never exercises them, or because they were superseded by a shared parser without cleanup. **Not re-flagged as separate findings** (this is a tooling sweep, not an adapter audit), but noted in "Noticed but out of scope" as a candidate follow-up.
- **PTY retry / chat errors** — `pkg/provider/retry.go:118` and `internal/chat/errors.go:91` both hit G404 (weak random number generator). Both use `math/rand` for jitter / error IDs. Neither is security-sensitive (jitter and error IDs don't need crypto/rand), but gosec doesn't know that. Note for the error-handling audit if one happens.

## Noticed but out of scope

Observations made while traversing the tool output that are not tool failures but worth capturing for future audit scope selection:

- **PTY adapter parser dead code.** `parseAiderJSON` and `parseKiroJSON` are defined but never called. Same file as the adapters that claim to parse JSON streams from those CLIs. Adapter-opencode is already known to be unverified. Candidate scope: `pty-adapter-stream-correctness` — run each CLI through a black-box stream harness, verify the parser that's supposedly handling its output is actually being called.
- **`internal/service/chat_test.go` test-stub dead block.** 29 lines of `stubSessionService` / `stubAgentService` / `stubToolService` / `stubContextService` methods are flagged `unused` — a full set of test doubles that aren't wired into any test. Either they're scaffolding for an unwritten test file, or a test was deleted and the stubs weren't. Candidate cleanup task, not a finding.
- **Linters bundled vs. standalone mismatch.** `.agentrc/agents/backend.md` §Build & Run references `go vet`, `goimports`, `golangci-lint`, `lefthook` as the canonical tool set; reviewer-backend context §How to run this review adds `staticcheck`, `errcheck`, `govulncheck`. The uninstalled trio is a gap: either the backend.md canonical list is the real baseline (and the reviewer context is aspirational), or the reviewer context is the real baseline (and the dev environment is missing tools every backend reviewer should have). Candidate follow-up: reconcile the two canonical lists, add the three tools to lefthook / devcontainer setup.
- **gosec G306 (132 hits) and G301 (78 hits) — file and directory permission warnings.** Most are `0644`/`0755` where gosec wants `0600`/`0750`. Almost all are in non-sensitive contexts (scaffold output, cache files, test fixtures). A category-wide review of actually-sensitive file writes (session databases, plugin configs, credential caches) would produce a smaller, high-value list. Candidate scope: `file-permission-audit-sensitive-writes-only`.
- **gosec G204 on subprocess launch (21 hits).** Includes `internal/worktree/manager.go` (git commands with branch/path args), `internal/tool/yaml_loader.go` (tool execution), `pkg/provider/pty.go:105` (PTY subprocess spawn), `internal/plugin/subprocess/manager.go:125` (plugin subprocess start), `internal/mcp/stdio_transport` (implied via transitive). Each needs a focused look: is the "tainted" input actually user-controlled, is it run inside a sandbox, is there validation. Overlaps with `dev-tools-input-validation` for `dev_bash`, `mcp-client-transport` for stdio subprocess, and `plugin-system-plan-eval` for plugin subprocess, but `internal/worktree/manager.go` and `internal/tool/yaml_loader.go` are not covered by any completed audit. Candidate scope: `worktree-and-tool-yaml-subprocess-audit`.
- **gosec G304 (117 hits) — potential file inclusion via variable.** Path-traversal-taint warnings on any `os.Open(path)` where `path` came from outside the function. Overlaps with the existing `pathsafe.ResolveUnder` refactor proposal from `dev-tools-input-validation`. This is the volume evidence for why that refactor would pay off — 117 independent call sites that would become one primitive.
- **`ui/node_modules/flatted/golang/pkg/flatted` package is in-module.** The test run caught a Go package living inside `ui/node_modules`. That's unusual — frontend deps shouldn't contain Go sources that get compiled as part of the backend test suite. Candidate: verify this is intentional (flatted ships a Go reference impl?), and if not, add `ui/node_modules/` to the module's ignore list or move the frontend deps out of the Go build scope.
- **Reviewer environment has `golangci-lint` 2.11.3 but `.golangci.yml` declares `run.go: "1.25"`** while the module builds with Go 1.26.1. Not a failure — the `run.go` in v2 is just the Go version for analyzer semantics. Worth noting: when nanite ships a Go 1.26 feature that surfaces in lint rules, the `.golangci.yml` version may need bumping.
- **`exhaustive` finding at `internal/service/chat_tool_executor.go:118`** — missing `permission.DecisionAllow` case in a permission-decision switch. This is a permission-system boundary. Probably benign (the default case likely handles it), but any permission-related missing switch case in a code-review context deserves a direct read. Candidate: pair with the queued `api-privilege-boundary` audit.
- **`nilerr` hits (12)** — `internal/api/autocomplete.go`, `internal/builders/tools.go`, `internal/mcp/dev_tools.go`, `internal/plugin/builtin/adapter-nanite-native/plugin.go`, `internal/plugin/subprocess/plugin.go`, `internal/store/session_overrides.go`. Each is "error is non-nil but function returns `nil`" — the classic "swallowed error" antipattern. A subsystem pass would classify each as: intentional (logged and continue) vs. bug (error lost). Several are in already-audited subsystems so not a fresh scope, but the `autocomplete.go` and `session_overrides.go` hits are in un-audited packages and worth a look.
