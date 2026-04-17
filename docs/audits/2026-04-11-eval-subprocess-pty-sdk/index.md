# Subprocess / PTY / SDK usage patterns audit — 2026-04-11

## Scope

**Scope string:** `eval-subprocess-pty-sdk`

**Interpretation:** End-to-end review of how Nanite spawns CLI agents and other subprocesses. Covers the spawning mechanisms (PTY bridge, subprocess bridge, sandbox exec), all 8 CLI adapters, the 5 agent adapter plugins, process tracking, and non-sandbox `exec.Command` usage across the codebase.

**Packages read in full:**
- `pkg/provider/pty.go`, `pty_*.go` (8 adapters), `subprocess.go`, `cli_adapter.go`, `cli_detect.go` — PTY/subprocess bridge + all CLI adapters
- `pkg/provider/provider.go` — context key helpers for sandbox dir, process callback, activity callback, CLI session ID
- `internal/agent/adapter.go` — CLIAgentAdapter interface + AdapterRegistry
- `internal/sandbox/exec.go` — AgentExec/UserExec reference implementation
- `internal/chat/proctrack.go` — ProcessTracker lifecycle
- `internal/mcp/stdio_transport.go` — MCP subprocess transport
- `internal/plugin/subprocess/manager.go` — subprocess plugin manager
- `internal/shell/exec.go` — shell command execution
- `internal/skill/context.go` — skill context command execution
- `internal/tool/yaml_loader.go` — YAML tool exec
- `internal/plugin/builtin/adapter-claude/plugin.go` — Claude agent adapter plugin
- `internal/plugin/builtin/adapter-codex/plugin.go` — Codex agent adapter plugin
- `internal/plugin/builtin/adapter-gemini/plugin.go` — Gemini agent adapter plugin
- `internal/plugin/builtin/adapter-opencode/plugin.go` — Opencode agent adapter plugin
- `internal/plugin/builtin/adapter-nanite-native/plugin.go` — Nanite-native agent adapter plugin (partial)

**Packages sampled:**
- `cmd/nanite/main.go` — CLI adapter registration (`initProviders`)
- `internal/mcp/dev_tools.go` — `dev_bash` tool exec path
- `internal/api/plugins.go` — plugin install git clone

**Packages skipped:**
- `internal/sandbox/seatbelt*.go`, `bwrap*.go` — OS-level sandbox profiles (covered by `2026-04-10-sandbox-hardening`)
- `internal/sandbox/denylist.go`, `proxy.go` — denylist and proxy (covered by `2026-04-10-sandbox-hardening`)

## Methodology

**Categories applied:**
- Security — env var inheritance, sandbox bypass, command injection, template injection
- Concurrency correctness — process lifecycle, double-wait risks, goroutine leaks
- Memory & Resources — zombie processes, FD leaks, subprocess reaping
- Error Handling — process exit handling, scanner error handling
- Idioms — adapter interface consistency, best-pattern determination

**Categories deferred:**
- Standards & Tooling — `go vet`, `go test -race`, etc. deferred to `whole-repo-tooling-and-tests-sweep` (already completed 2026-04-11).
- Test Quality — adapter test files exist and were spot-checked but not systematically reviewed. Recommend a follow-up `pty-adapter-test-quality` scope.

**Tools run:** Grep-based code search only. Build/test deferred per narrow scope.

**Cross-audit preflight:** Read index files for:
- `2026-04-11-panic-recovery-sweep` — 14 provider SSE reader goroutines, no recover. Cross-referenced in finding 05.
- `2026-04-11-concurrency-cancellation-sweep` — provider lifecycle mapped. Built upon.
- `2026-04-11-provider-abstractions` — PTY double-wait risk (05), subprocess no-graceful-kill (06). Confirmed, cross-referenced.
- `2026-04-11-whole-repo-tooling-and-tests-sweep` — dead PTY parsers flagged. Confirmed in finding 09.
- `2026-04-10-sandbox-hardening` — sandbox posture baseline.

## Findings

### By severity

**Critical (0)**
- _none_

**High (3)**
- [01 — PTY and Subprocess bridges inherit full parent environment including secrets](01-high-pty-subprocess-env-inheritance.md)
- [02 — Multiple exec.Command call sites bypass sandbox.AgentExec](02-high-multiple-sandbox-bypass-paths.md)
- [03 — ProcessTracker uses SIGKILL without graceful shutdown](03-high-process-tracker-sigkill-only.md)

**Medium (5)**
- [04 — SubprocessBridge context cancellation uses SIGKILL and double-kills](04-medium-subprocess-bridge-no-graceful-kill.md)
- [05 — PTYBridge has latent double-cmd.Wait() risk](05-medium-pty-double-wait-latent.md)
- [06 — MCP StdioTransport leaks subprocess on read timeout or context cancel](06-medium-mcp-stdio-no-reap-on-error.md)
- [07 — CopilotAdapter.Detect() executes subprocess during provider registration](07-medium-copilot-detect-runs-subprocess.md)
- [08 — YAML tool exec renders Go templates from user input before shell execution](08-medium-yaml-tool-template-injection.md)

**Low (2)**
- [09 — Dead parser functions: parseAiderJSON, parseKiroJSON, codexTurnCompleted](09-low-dead-parsers-confirmed.md)
- [10 — Copilot, Aider, and Kiro adapters lack reliable completion detection](10-low-copilot-aider-kiro-no-completion-detection.md)

**Info (2)**
- [11 — Best-pattern analysis: Claude adapter is the reference implementation](11-info-best-pattern-analysis.md)
- [12 — Positive observations](12-info-positive-observations.md)

### By topic

**Security — environment and secrets**
- [01 — PTY/Subprocess env inheritance](01-high-pty-subprocess-env-inheritance.md)
- [02 — Sandbox bypass paths](02-high-multiple-sandbox-bypass-paths.md)
- [08 — YAML tool template injection](08-medium-yaml-tool-template-injection.md)

**Concurrency — process lifecycle**
- [03 — ProcessTracker SIGKILL-only](03-high-process-tracker-sigkill-only.md)
- [04 — SubprocessBridge no graceful kill](04-medium-subprocess-bridge-no-graceful-kill.md)
- [05 — PTY double-wait risk](05-medium-pty-double-wait-latent.md)

**Memory & Resources — subprocess reaping**
- [06 — MCP stdio subprocess leak](06-medium-mcp-stdio-no-reap-on-error.md)
- [07 — Copilot detect subprocess at init](07-medium-copilot-detect-runs-subprocess.md)

**Idioms — adapter patterns**
- [09 — Dead parsers](09-low-dead-parsers-confirmed.md)
- [10 — Missing completion detection](10-low-copilot-aider-kiro-no-completion-detection.md)
- [11 — Best pattern analysis](11-info-best-pattern-analysis.md)
- [12 — Positive observations](12-info-positive-observations.md)

## Recommended next steps

1. **Fix env inheritance (01)** — Apply `filterSecrets()` to PTY/subprocess bridge env. This is the highest-impact single fix.
2. **Route dev_bash through sandbox (02)** — The `dev_bash` MCP tool runs arbitrary shell commands unsandboxed from LLM-generated arguments. Cross-check with `2026-04-10-dev-tools-input-validation` for existing coverage.
3. **Add graceful kill to ProcessTracker (03) and SubprocessBridge (04)** — Extract `killProcess` to shared helper, use SIGTERM-first everywhere.
4. **Fix MCP stdio subprocess leak (06)** — Kill subprocess on timeout/cancel in `StdioTransport.call()`.
5. **Add sync.Once to PTY cmd.Wait (05)** — Prevent latent double-wait panic.
6. **Follow-up scope: `pty-adapter-test-quality`** — Systematic review of all 8 adapter test files for coverage of error paths, cancellation, and edge cases.
7. **Follow-up scope: `exec-command-inventory`** — Track all `exec.Command` / `exec.CommandContext` call sites and classify each as sandboxed, partially sandboxed, or unsandboxed. Build a policy for which paths must use `AgentExec` vs. `UserExec` vs. raw exec.

## Known issues skipped

- PTY double-wait risk — already filed as `2026-04-11-provider-abstractions/05-medium-pty-double-wait-risk.md`. Confirmed and cross-referenced in finding 05 here.
- SubprocessBridge no graceful kill — already filed as `2026-04-11-provider-abstractions/06-medium-subprocess-no-graceful-kill.md`. Confirmed and cross-referenced in finding 04 here.
- Dead parsers (`parseAiderJSON`, `parseKiroJSON`, `codexTurnCompleted`) — already flagged in `2026-04-11-whole-repo-tooling-and-tests-sweep`. Confirmed in finding 09.
- `adapter-opencode` format unverified — tracked in `plugin-dev.md`. Not re-flagged.
- Provider SSE goroutines without recover — tracked in `2026-04-11-panic-recovery-sweep`. Cross-referenced only.

## Noticed but out of scope

- `internal/plugin/subprocess/manager.go` — The subprocess plugin manager has a well-structured lifecycle (Start/Stop/health/restart) but `attemptRestart` at `:281` calls `time.Sleep(backoff)` on a goroutine that holds no context. If the host is shutting down, the restart goroutine sleeps through the shutdown. Suggest scope: `plugin-subprocess-manager-lifecycle`.
- `internal/mcp/dev_tools.go:L527` — The `dev_bash` tool's `workDir` comes from the tool arguments. No validation that it's within an allowed directory. May overlap with `2026-04-10-dev-tools-input-validation`. Suggest scope: `dev-tools-cwd-validation`.
- `cmd/nanite/plugin_cmd.go:L100` — `exec.Command("cerberus", "restart", ...)` has no timeout. If cerberus is down, the plugin install command hangs. Suggest scope: `cli-command-robustness`.
- `internal/worktree/manager.go` — Multiple `exec.Command("git", ...)` calls with no timeout, no env filtering. Low risk (admin operations) but inconsistent with sandbox posture. Suggest scope: `worktree-hardening`.
