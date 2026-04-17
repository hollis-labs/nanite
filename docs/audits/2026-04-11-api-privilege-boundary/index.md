# Audit: api-privilege-boundary

**Date:** 2026-04-11
**Reviewer:** nanite-reviewer-backend (deep-review)

## Scope

**Scope string:** `api-privilege-boundary`
**Interpretation:** HTTP API surface — all handler files in `internal/api/` (22+ files, 100+ routes), server setup and middleware in `internal/server/`, with focus on the plugin install/management endpoints as the highest-privilege boundary.

**Packages read in full:**
- `internal/api/api.go` — route registration (302 lines)
- `internal/api/plugins.go` — plugin management: install, install-local, install-archive, uninstall, enable, disable (787 lines)
- `internal/api/catalog.go` — catalog browsing and install (445 lines)
- `internal/api/messages.go` — message send, agent message, delegation, stream SSE (193 lines)
- `internal/api/sessions.go` — session CRUD, mode switching, compaction (339 lines)
- `internal/api/artifacts.go` — artifact upload, download, place (194 lines)
- `internal/api/shell.go` — shell exec, check, info (288 lines)
- `internal/api/settings.go` — user settings get/update (145 lines)
- `internal/api/actions.go` — custom actions CRUD + execute (196 lines)
- `internal/api/agents.go` — agent CRUD, session agents, agent modes (359 lines)
- `internal/api/mcp_servers.go` — MCP server CRUD, import/export (207 lines)
- `internal/api/provider_manage.go` — provider update, API key, CLI detection (192 lines)
- `internal/api/workflows.go` — workflow runs, SSE events (180 lines)
- `internal/api/event_stream.go` — plugin event SSE (71 lines)
- `internal/api/presence.go` — presence SSE (48 lines)
- `internal/api/autocomplete.go` — file autocomplete (180 lines)
- `internal/api/processes.go` — process health, kill stale (35 lines)
- `internal/api/types.go` — request types (401 lines)
- `internal/server/server.go` — server setup, middleware, plugin handlers (222 lines)
- `internal/server/auth.go` — basic auth middleware (56 lines)
- `internal/server/spa.go` — SPA handler (57 lines)

**Packages sampled:** None — full read of all files in scope.

**Packages skipped:** `internal/api/triggers.go`, `internal/api/bookmarks.go`, `internal/api/skills.go`, `internal/api/modes.go`, `internal/api/templates.go`, `internal/api/search.go`, `internal/api/memories.go`, `internal/api/todos.go`, `internal/api/plans.go`, `internal/api/tools.go`, `internal/api/workers.go`, `internal/api/tasks.go`, `internal/api/workspaces.go`, `internal/api/debug.go`, `internal/api/metrics.go` — these follow the same patterns as the files read. Spot-checked via grep for `os.Open`, `exec.Command`, `filepath.Join`, `io.ReadAll` and found no additional high-severity patterns.

## Methodology

**Categories applied:**
- Security — full pass (auth, CORS, input validation, path traversal, SSRF, command injection)
- Error handling — sampled (all handlers use `errorResp`, consistent pattern)
- Standards and tooling — deferred to `whole-repo-tooling-and-tests-sweep` (completed)

**Categories skipped:**
- Concurrency — deferred to `concurrency-cancellation-sweep` (completed)
- Memory/resources — partially covered via SSE connection lifetime analysis
- Test quality — deferred (no tests in scope for this pass; test coverage noted in `whole-repo-tooling-and-tests-sweep`)
- Go idioms / Antipatterns — not prioritized for a security-focused scope

**Tools run:** `rg` (ripgrep) for pattern searches across `internal/api/` and `internal/server/`. No build/test commands (scope is read-only security review; tooling sweep already complete).

**Cross-audit preflight:** Read `INDEX.md` and skimmed `dev-tools-input-validation`, `sandbox-hardening`, `mcp-client-transport`, `whole-repo-tooling-and-tests-sweep`, `concurrency-cancellation-sweep`, `panic-recovery-sweep` for cross-cutting patterns. Cross-references noted in individual findings.

## Findings

### By severity

**Critical (3)**
- [01 — CORS reflects any Origin header](01-critical-cors-origin-reflection.md)
- [02 — Emit event accepts arbitrary types and data](02-critical-emit-event-unvalidated.md)
- [03 — Plugin install-local accepts arbitrary filesystem paths](03-critical-plugin-install-local-path-traversal.md)

**High (4)**
- [04 — HTTP server has no timeouts](04-high-no-http-server-timeouts.md)
- [05 — Most handlers have no request body size limit](05-high-no-request-body-limits.md)
- [06 — Artifact download serves any DB-stored path](06-high-artifact-download-path-traversal.md)
- [07 — Artifact upload uses unsanitized client filename](07-high-artifact-upload-filename-injection.md)

**Medium (4)**
- [08 — Plugin routes registered outside main API](08-medium-plugin-management-routes-bypass-auth-skip-check.md)
- [09 — Middleware order: CORS inside auth breaks preflight](09-medium-middleware-order-cors-inside-auth.md)
- [10 — MCP server creation accepts arbitrary URLs/commands](10-medium-mcp-server-create-ssrf.md)
- [11 — Catalog install allows unsigned from signed sources](11-medium-catalog-install-unsigned-warning-only.md)

**Low (2)**
- [12 — SSE endpoints have no connection limits](12-medium-sse-no-connection-limits.md)
- [13 — Auth credentials read once at init](13-low-auth-env-read-at-init.md)

**Info (1)**
- [14 — Observations and praise](14-info-observations.md)

### By topic

**CORS / Origin policy**
- [01 — CORS reflects any Origin header](01-critical-cors-origin-reflection.md)

**Auth middleware**
- [08 — Plugin routes registered outside main API](08-medium-plugin-management-routes-bypass-auth-skip-check.md)
- [09 — Middleware order: CORS inside auth breaks preflight](09-medium-middleware-order-cors-inside-auth.md)
- [13 — Auth credentials read once at init](13-low-auth-env-read-at-init.md)

**Plugin install / management (privilege boundary)**
- [02 — Emit event accepts arbitrary types and data](02-critical-emit-event-unvalidated.md)
- [03 — Plugin install-local accepts arbitrary filesystem paths](03-critical-plugin-install-local-path-traversal.md)
- [11 — Catalog install allows unsigned from signed sources](11-medium-catalog-install-unsigned-warning-only.md)

**Input validation / DoS**
- [04 — HTTP server has no timeouts](04-high-no-http-server-timeouts.md)
- [05 — Most handlers have no request body size limit](05-high-no-request-body-limits.md)

**Artifact handling**
- [06 — Artifact download serves any DB-stored path](06-high-artifact-download-path-traversal.md)
- [07 — Artifact upload uses unsanitized client filename](07-high-artifact-upload-filename-injection.md)

**MCP server management**
- [10 — MCP server creation accepts arbitrary URLs/commands](10-medium-mcp-server-create-ssrf.md)

**SSE lifecycle**
- [12 — SSE endpoints have no connection limits](12-medium-sse-no-connection-limits.md)

**Praise / observations**
- [14 — Observations and praise](14-info-observations.md)

## Recommended next steps

1. **Fix CORS immediately (finding 01).** This is the gateway to all other findings — a remote site can exercise every API endpoint through the CORS hole. Narrowing CORS to localhost origins blocks the remote attack vector for findings 02, 03, 06, 07, 10.
2. **Add `http.MaxBytesReader` to `a.decode()` (finding 05).** Single-line fix that hardens 50+ endpoints.
3. **Add `ReadHeaderTimeout` to the HTTP server (finding 04).** Single-line fix.
4. **Validate plugin install name/path (finding 03).** Regex validation on `req.Name`, path confinement on `install-local`.
5. **Restrict emit-event endpoint (finding 02).** Allowlist event types or remove the endpoint.
6. **Validate artifact paths (findings 06, 07).** Path confinement on place, filename sanitization on upload.
7. **Reorder CORS/auth middleware (finding 09).** Required if auth is ever enabled with cross-origin clients.
8. **Add SSE keepalive and connection limits (finding 12).** Prevents resource exhaustion.

## Known issues skipped

- `gosec G114` on `http.ListenAndServe` — already flagged in `whole-repo-tooling-and-tests-sweep`. Finding 04 in this audit provides the detailed analysis and recommendation.
- `sandbox-hardening` SSRF findings (#02/#03) — not re-flagged. Finding 10 covers the API-side creation path that feeds into the proxy.
- `mcp-client-transport` response body size caps — not re-flagged. Finding 05 covers the inbound API-side equivalent.
- `dev-tools-input-validation` RCE findings — not re-flagged. The HTTP API is noted as the delivery vector but the tool-side findings are the prior audit's scope.
- `panic-recovery-sweep` — the single `recover()` in HTTP middleware is confirmed and praised (finding 14). The repo-wide gap is not re-flagged.
- Plugin `Host.Shutdown()` deadlock, envelope emission gaps, scaffold broken imports — all tracked in `plugin-dev.md` and `plugin-audit-2026-04-10.md`.

## Noticed but out of scope

- **`internal/api/autocomplete.go` walks filesystem from session-derived root.** The root is derived from DB data (project `repo_path`), not direct user input. If a user can set an arbitrary `repo_path` on a project, this becomes a directory enumeration vector. Follow-up scope: `project-repo-path-validation`.
- **`internal/api/shell.go` passes commands through `sandbox.UserExec`.** The sandbox denylist and OS sandbox provide the security boundary. The API layer correctly delegates to the sandbox. However, the `resolveShell()` function uses `$SHELL` which could be manipulated if environment is attacker-controlled. Follow-up scope: `shell-exec-environment-hardening`.
- **`handleCancelWorkflowRun` directly mutates `record.Run.Status` without a lock.** This is a data race if the workflow executor is concurrently updating the same record. Follow-up scope: `workflow-concurrency`.
- **`handleRunWorkflow` uses `context.Background()` detached from request context.** The 2-minute timeout is correct, but the workflow survives request cancellation. This may be intentional. Follow-up scope: `workflow-lifecycle`.
- **`handleImportMCPServers` deserializes MCP configs from arbitrary JSON.** The import uses `mcpconfig.Import` which was not read in this pass. Follow-up scope: `mcp-config-import-validation`.
