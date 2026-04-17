# Deep Review: security-threat-model

## Scope

**Scope string:** `security-threat-model`

**Interpretation:** Trust-boundary audit against the 11-item enumeration in `reviewer-backend.md` §"Trust boundaries". For each boundary, determine whether a completed audit already covered it; if yes, cross-reference. If no, audit it in this pass.

**Boundaries audited fresh in this pass:**
- #5 Plugin manifests (plugin.yaml parsing, trust on install)
- #7 PTY bridge input/output (untrusted data flows, event injection, env inheritance)
- #9 Environment variables (secret exposure, subprocess inheritance, pattern gaps)
- #2 User messages in chat (prompt injection — scope_guard confirmation, filter gap)
- #11 Provider API paths (content-level validation gap, extending `provider-abstractions`)

**Boundaries cross-referenced (already covered by prior audits):**
- #1 HTTP API: `api-privilege-boundary` (3C/4H)
- #3 Tool arguments: `toolbroker` (3H)
- #4 MCP tool results: `mcp-client-transport` (2C/5H)
- #6 Plugin code: `plugin-system-plan-eval` (1C/6H), design-acknowledged
- #8 Subprocess execution: `sandbox-hardening` (3C/3H), `dev-tools-input-validation` (3C/5H)
- #10 File system: `managed-section-parser` (2H), `installer` (4H)

**Packages read in full:** `internal/plugin/config.go`, `internal/plugin/loader.go`, `internal/plugin/manage.go`, `pkg/provider/pty.go`, `pkg/provider/subprocess.go`, `pkg/provider/pty_claude.go`, `pkg/provider/pty_codex.go`, `pkg/provider/cli_adapter.go`, `pkg/provider/scope_guard.go`, `pkg/provider/event_pipeline.go`, `internal/sandbox/exec.go`, `internal/server/auth.go`, `internal/secrets/keyring.go`, `internal/api/plugins.go`, `internal/api/catalog.go`, `internal/mcp/stdio_transport.go`, `internal/plugin/subprocess/manager.go`, `internal/service/chat_generate.go`.

**Packages sampled:** `cmd/nanite/main.go` (provider init, env var reads), `internal/chat/engine.go` (stream event types).

## Methodology

- **Categories applied:** Security (primary — all findings). Concurrency, error handling, and other categories deferred as out of scope for a trust-boundary-focused audit.
- **Tools run:** None — this is a targeted trust-boundary audit, not a broad tooling sweep. Tooling was covered by `whole-repo-tooling-and-tests-sweep`.
- **Cross-audit grounding:** Read INDEX.md and skimmed all 22 completed audit index files. Cross-referenced findings from `chat-engine`, `mcp-client-transport`, `provider-abstractions`, `toolbroker`, `sandbox-hardening`, `dev-tools-input-validation`, `managed-section-parser`, `installer`, `backpressure-followup`, `eval-subprocess-pty-sdk`, `plugin-system-plan-eval`, and `memory-ranking`.
- **What was NOT checked:** Runtime behavior verification (no tests run, no live probing). Dependency CVEs (deferred to `dependency-supply-chain` scope). Frontend trust boundaries. Detailed provider-by-provider HTTP client audit (covered at HTTP level by `provider-abstractions`; content-level gap noted as finding 08).

## Findings

### By severity

**Critical (0)**
_none_ — the Critical findings in this threat space were already captured by prior audits (scope_guard dead code in `chat-engine`, CORS reflection in `api-privilege-boundary`, RCE via dev_bash in `dev-tools-input-validation`). This audit confirms and extends those findings but does not duplicate them.

**High (5)**
- [01 — Plugin manifest YAML deserialization trusts all keys](01-high-plugin-manifest-yaml-deserialization.md)
- [02 — PTY/subprocess bridges inherit full host environment including API keys](02-high-pty-bridge-full-env-inheritance.md)
- [03 — PTY bridge output parsed without validation — CLI can inject arbitrary events](03-high-pty-output-no-validation.md)
- [04 — No prompt injection defense — scope_guard dead, no user message sanitization](04-high-prompt-injection-no-defense.md)
- [05 — Environment variable secret exposure across multiple subsystems](05-high-env-var-secret-exposure-surface.md)

**Medium (4)**
- [06 — Plugin subprocess entrypoint allows command injection](06-medium-plugin-manifest-entrypoint-command-injection.md)
- [07 — Catalog install allows unsigned plugins with only a log warning](07-medium-catalog-unsigned-plugin-warning-not-blocking.md)
- [08 — Provider API response content validation gap](08-medium-provider-api-response-no-content-validation.md)
- [09 — PTY adapter logs raw CLI output on parse errors](09-medium-pty-log-leaks-parse-errors.md)

**Low (0)**
_none_

**Info (1)**
- [10 — Trust boundary audit coverage map](10-info-trust-boundary-coverage-map.md)

### By topic

**Plugin manifests (#5)**
- [01 — Plugin manifest YAML deserialization trusts all keys](01-high-plugin-manifest-yaml-deserialization.md)
- [06 — Plugin subprocess entrypoint allows command injection](06-medium-plugin-manifest-entrypoint-command-injection.md)
- [07 — Catalog install allows unsigned plugins with only a log warning](07-medium-catalog-unsigned-plugin-warning-not-blocking.md)

**PTY bridge input/output (#7)**
- [02 — PTY/subprocess bridges inherit full host environment including API keys](02-high-pty-bridge-full-env-inheritance.md)
- [03 — PTY bridge output parsed without validation — CLI can inject arbitrary events](03-high-pty-output-no-validation.md)
- [09 — PTY adapter logs raw CLI output on parse errors](09-medium-pty-log-leaks-parse-errors.md)

**Environment variables (#9)**
- [02 — PTY/subprocess bridges inherit full host environment including API keys](02-high-pty-bridge-full-env-inheritance.md)
- [05 — Environment variable secret exposure across multiple subsystems](05-high-env-var-secret-exposure-surface.md)

**User messages / prompt injection (#2)**
- [04 — No prompt injection defense — scope_guard dead, no user message sanitization](04-high-prompt-injection-no-defense.md)

**Provider API paths (#11)**
- [08 — Provider API response content validation gap](08-medium-provider-api-response-no-content-validation.md)

**Cross-cutting**
- [10 — Trust boundary audit coverage map](10-info-trust-boundary-coverage-map.md)

## Recommended next steps

1. **Centralize subprocess environment filtering.** Extract `filterSecrets` from `sandbox/exec.go` into a shared `internal/secrets/env.go` and apply it to every subprocess spawn: PTY bridge, subprocess bridge, MCP stdio transport, plugin subprocess manager, git clone in plugin install. This closes findings 02 and 05 in one primitive. Highest ROI fix.

2. **Add plugin manifest validation.** Implement `ValidateManifest` with name-slug, entrypoint confinement, and env_var namespacing. Closes findings 01 and 06.

3. **Wire scope_guard or implement tool-name validation in the chat loop.** The minimum viable defense: in the tool-use loop, validate that tool_use events reference tools from the request's `tools` list. This single check closes the content-validation gap across all provider types (findings 03, 04, 08).

4. **Block unsigned plugins from signed sources.** Change the log warning to a rejection. Closes finding 07.

5. **Redact PTY log output.** Truncate or strip sensitive content from parse-error log messages. Closes finding 09.

6. **Queue `plugin-capability-model` audit** (INDEX.md item 40) — the plugin trust model (in-process, full host access) is design-acknowledged but the capability surface has not been mapped. That audit would complement this threat model by documenting exactly what a malicious plugin can do.

## Known issues skipped

- `scope_guard.go` dead code — already Critical in `chat-engine` audit. This audit confirms the impact but does not re-flag.
- CORS origin reflection — already Critical in `api-privilege-boundary`. Same class (trust boundary without enforcement) but different boundary.
- `dev_bash` RCE — already Critical in `dev-tools-input-validation`. The prompt injection vector (finding 04) feeds into this exploit but the exploit itself is already tracked.
- Plugin `Host.Shutdown()` deadlock, event hook panic propagation — already tracked in `plugin-system-plan-eval` and `panic-recovery-sweep`.
- MCP trust model "no code" — already flagged in `mcp-client-transport`. This audit's finding 08 extends the analysis to provider API responses but does not re-flag the MCP side.

## Noticed but out of scope

- **`internal/worktree/manager.go` git subprocess safety** — 5 gosec G204 hits, subsystem not yet audited. Queued as INDEX.md item 51 (`worktree-subprocess-audit`).
- **`internal/tool/yaml_loader.go` subprocess invocation** — 2 gosec G204 hits. Queued as INDEX.md item 52 (`tool-yaml-loader-subprocess-audit`).
- **`internal/mcpserver/server.go` bypasses ToolClient permission layer** — external MCP clients get direct tool access. Queued as INDEX.md item 60 (`mcpserver-permission-boundary`).
- **`internal/api/autocomplete.go` directory enumeration** — session-derived `repo_path` walks filesystem. Queued as INDEX.md item 55 (`project-repo-path-validation`).
- **Plugin host `RegisterService` exposes store/MCP/toolclient to all plugins** — `cmd/nanite/main.go:L175-L177` registers the store, MCP manager, and tool client as plugin services. Any plugin can call `host.GetService("store")` and get the full `*store.Store` with all CRUD methods. This is the plugin-capability-model audit's territory (INDEX.md item 40).
- **`CLIConfig.Env` field in `pkg/provider/cli_adapter.go:L28`** — allows additional env vars to be specified in CLI adapter configs. Not currently used by any built-in adapter but if user-configurable adapters are added, this is an env injection vector. Future scope: `cli-adapter-config-validation`.
