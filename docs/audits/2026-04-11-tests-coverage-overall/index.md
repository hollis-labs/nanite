# Test Coverage Overall — 2026-04-11

**Scope:** tests-coverage-overall
**Reviewer:** nanite-reviewer-backend
**Branch:** audit-campaign-2026-04-11
**Shape:** test-design gap analysis — coverage-gap map

## Scope

Test-design gap analysis across the entire Nanite Go backend. For each package with tests (44 packages per tooling-sweep), assess whether the test suite covers the critical paths identified by prior audits. For each package without tests (18 packages per tooling-sweep), assess whether the absence is justified or a gap.

Focus areas: security-critical paths (sandbox exec, plugin loading, auth middleware, managed-section parser, provider key handling), concurrency-critical paths (worker lifecycle, plugin shutdown, event dispatch), and data-integrity paths (store transactions, migration, seed idempotency).

Packages read in full (test files + corresponding source): `internal/sandbox/` (3 test files, 3 source files), `internal/mcp/` (4 test files, key source files), `internal/plugin/` (9 test files, key source files), `internal/worker/` (1 test file, source), `internal/server/` (1 test file), `internal/permission/` (2 test files), `internal/toolclient/` (1 permissions test file), `internal/agent/` (managed_section_test.go), `internal/store/` (17 test files, seed.go, store.go), `internal/chat/` (8 test files), `internal/service/` (service-level test files), `internal/api/` (2 test files), `pkg/provider/` (all test files sampled).

Packages sampled: `internal/service/install/` (14 test files, line counts reviewed), `internal/contextbroker/` (3 test files noted), remaining tested packages confirmed via file listing.

Packages skipped: `ui/` (frontend, out of scope for backend review).

## Methodology

No tools were run. No tests were executed. This is a read-only assessment comparing test file contents against the code paths they exercise, using prior audit findings as the checklist of critical paths.

Categories applied: Test Quality (primary), Security (secondary — assessing whether security-critical paths have test coverage), Concurrency (secondary — assessing whether concurrency-critical paths are tested under -race).

Categories deferred: Standards and Tooling (covered by `whole-repo-tooling-and-tests-sweep`), Error Handling (partially covered but not the primary lens), Idioms/Antipatterns (out of scope for a coverage audit).

Cross-audit sources consulted:
- `2026-04-11-whole-repo-tooling-and-tests-sweep` — baseline: 44 pkgs pass, 18 pkgs no tests, 0 fail, all -race clean
- `2026-04-10-sandbox-hardening` — proxy SSRF via DNS rebinding, Linux silent fallback, missing port restrictions
- `2026-04-10-dev-tools-input-validation` — RCE via dev_bash, symlink escape in isAllowed
- `2026-04-10-mcp-client-transport` — leaked subprocess/goroutine/FD per timeout
- `2026-04-11-managed-section-parser` — marker injection robustness
- `2026-04-11-security-threat-model` — trust boundary enumeration
- `2026-04-11-concurrency-cancellation-sweep` — orphaned goroutines, no uniform lifecycle discipline
- `2026-04-11-panic-recovery-sweep` — recover() placement gaps

## Findings

### By severity

**Critical (2)**
- [01 — Service layer chat_generate.go / chat_tool_executor.go have zero test coverage](01-critical-service-layer-untested.md)
- [02 — scope_guard.go (security control) has no dedicated test file](02-critical-scope-guard-no-tests.md)

**High (5)**
- [03 — Proxy tests missing DNS rebinding, Slowloris, and port restriction coverage](03-high-proxy-test-gaps.md)
- [04 — Code exec tools missing session ID path traversal test](04-high-code-exec-session-id-traversal.md)
- [05 — Managed section parser missing marker injection tests](05-high-managed-section-injection-gap.md)
- [06 — Plugin host_test.go missing Shutdown deadlock and panic recovery tests](06-high-plugin-host-test-gaps.md)
- [07 — Provider package: 12 source files with zero test coverage including 5 provider adapters](07-high-provider-untested-files.md)

**Medium (5)**
- [08 — Store layer: 11 source files with no matching test file](08-medium-store-untested-files.md)
- [09 — Chat package: 6 source files untested including orchestrator and delegate](09-medium-chat-untested-files.md)
- [10 — API handlers: 396 lines of tests for 5200+ lines of handlers](10-medium-api-handler-coverage.md)
- [11 — 8 builtin plugins with zero test coverage](11-medium-builtin-plugins-untested.md)
- [12 — Dev tools tests missing symlink escape and path traversal via ../ cases](12-medium-dev-tools-traversal-gap.md)

**Low (2)**
- [13 — Untested utility packages: secrets, brand, version, crossapp, allplugins, skill/builtin](13-low-untested-utility-packages.md)
- [14 — Worker manager tests use time.Sleep for synchronization](14-low-worker-sleep-sync.md)

**Info (2)**
- [15 — Well-tested packages: sandbox/exec, permission engine, toolclient/permissions, managed section](15-info-well-tested-packages.md)
- [16 — Install service: exemplary test suite at 2601 lines across 14 files](16-info-install-service-praise.md)

### By topic

**Sandbox / subprocess execution**
- [01 — Service layer chat_generate.go / chat_tool_executor.go have zero test coverage](01-critical-service-layer-untested.md)
- [03 — Proxy tests missing DNS rebinding, Slowloris, and port restriction coverage](03-high-proxy-test-gaps.md)
- [04 — Code exec tools missing session ID path traversal test](04-high-code-exec-session-id-traversal.md)
- [15 — Well-tested packages](15-info-well-tested-packages.md)

**Plugin system**
- [06 — Plugin host_test.go missing Shutdown deadlock and panic recovery tests](06-high-plugin-host-test-gaps.md)
- [11 — 8 builtin plugins with zero test coverage](11-medium-builtin-plugins-untested.md)

**Security controls**
- [02 — scope_guard.go (security control) has no dedicated test file](02-critical-scope-guard-no-tests.md)
- [05 — Managed section parser missing marker injection tests](05-high-managed-section-injection-gap.md)
- [12 — Dev tools tests missing symlink escape and path traversal via ../ cases](12-medium-dev-tools-traversal-gap.md)

**Provider abstractions**
- [07 — Provider package: 12 source files with zero test coverage](07-high-provider-untested-files.md)

**Data integrity (store)**
- [08 — Store layer: 11 source files with no matching test file](08-medium-store-untested-files.md)
- [15 — Well-tested packages](15-info-well-tested-packages.md)

**Chat engine / orchestration**
- [01 — Service layer chat_generate.go / chat_tool_executor.go have zero test coverage](01-critical-service-layer-untested.md)
- [09 — Chat package: 6 source files untested](09-medium-chat-untested-files.md)

**HTTP API**
- [10 — API handlers: 396 lines of tests for 5200+ lines](10-medium-api-handler-coverage.md)

**Test quality / design**
- [14 — Worker manager tests use time.Sleep for synchronization](14-low-worker-sleep-sync.md)
- [16 — Install service: exemplary test suite](16-info-install-service-praise.md)

**Utility packages**
- [13 — Untested utility packages](13-low-untested-utility-packages.md)

## Recommended next steps

1. **Highest priority:** Write tests for `internal/service/chat_generate.go` (1161 lines, zero tests) and `internal/service/chat_tool_executor.go` (534 lines, zero tests). These are the primary orchestration paths through which every chat message flows. Test the `captureEnvelopeData` goroutine path specifically.
2. **Security-critical:** Add a dedicated test file for `pkg/provider/scope_guard.go`. The existing 50-line test in `event_pipeline_test.go` only covers basic tool-use pattern matching; it does not test file-access path checking, text-content scanning, or violation mode enforcement.
3. **Security-critical:** Add proxy tests for DNS rebinding (resolve domain to localhost after allowlist check), Slowloris (verify ReadHeaderTimeout is set), and port restriction bypass.
4. **Security-critical:** Add `nanite_code_execute` test with session ID containing `../` to verify path traversal is blocked.
5. **Security-critical:** Add managed section tests for nested `<!-- nanite:start -->` markers and malformed HTML comments.
6. **High value:** Add plugin host tests for `Shutdown()` with a plugin whose `Unload()` calls back into the host (deadlock scenario), and for panic propagation from event hooks.
7. **Broad coverage pass:** Write at least one test per untested store source file (`skills.go`, `seed.go` beyond idempotency, `prompt_templates.go`, `artifacts.go`, `plugin_settings.go`, `providers.go`, `templates.go`, `bookmarks.go`, `events.go`, `mcp_servers.go`, `broker.go`).
8. **Follow-up scope:** `api-handler-integration-tests` — the API layer has 5200+ lines of handlers with only 396 lines of test coverage. Priority targets: `plugins.go` (787 lines, install path is a privilege boundary), `shell.go` (288 lines, subprocess execution), `agents.go` (359 lines, CRUD with validation).

## Known issues skipped

- Plugin scaffold template broken imports (P0-1 in plugin-dev.md) — pre-existing, tracked.
- Envelope emission system-wide breakage — tracked in `plugin-envelope-emission-findings-2026-04-10.md`.
- Host.Shutdown() mutex-across-Unload deadlock risk — tracked in reviewer-backend context as known. Flagged here as a TEST GAP (no test for the known bug), not as a re-discovery of the bug.
- PTY adapter dead parse code (`parseAiderJSON`, `parseKiroJSON`) — tracked in tooling-sweep.

## Noticed but out of scope

- **`internal/service/delegation.go` (340 lines) has no tests.** This is the delegation orchestration layer that spawns child sessions. Given the concurrency-cancellation-sweep found orphaned goroutines in the delegation path, this is a high-value test target. Candidate scope: `delegation-service-test-coverage`.
- **`internal/service/stream.go` (193 lines) has no tests.** SSE streaming is a trust boundary (session takeover races). Candidate scope: `sse-stream-test-coverage`.
- **`internal/crossapp/engine_client.go` (97 lines) has no tests.** Cross-app communication is a trust boundary for inter-service calls. Candidate scope: `crossapp-test-coverage`.
- **`internal/mcp/manager.go` (449 lines) has no dedicated test file.** MCP server lifecycle (reconnect, transport selection) is complex. Candidate scope: `mcp-manager-test-coverage`.
- **Frontend test coverage was not assessed.** The `ui/` directory was out of scope. Candidate scope: `frontend-test-coverage-overall`.
- **Fuzz testing is absent across the entire codebase.** No `*_fuzz_test.go` files exist. The managed-section parser, JSON path parser, and provider stream parsers are prime candidates for fuzzing. Candidate scope: `fuzz-test-introduction`.
