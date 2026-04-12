# [Medium] API handlers: 396 lines of tests for 5200+ lines of handlers

**Scope:** internal/api/
**Topic:** Test Quality — trust boundary coverage gap
**Date:** 2026-04-11

## Problem

The `internal/api/` package contains 5200+ lines of HTTP handler code across 22 files serving 100+ routes. Test coverage is 396 lines across 2 test files: `api_test.go` (181 lines) and `plugins_install_test.go` (215 lines). This is a 7.6% test-to-source ratio on a trust boundary layer.

## Evidence

Largest untested handler files:

| File | Lines | Risk |
|------|-------|------|
| `plugins.go` | 787 | Plugin install is a privilege boundary |
| `catalog.go` | 492 | Catalog queries, search |
| `memories.go` | 492 | Memory CRUD, potential PII exposure |
| `agents.go` | 359 | Agent CRUD with validation |
| `sessions.go` | 339 | Session lifecycle |
| `shell.go` | 288 | Subprocess execution via API |
| `tools.go` | 258 | Tool broker API |
| `a2a.go` | 238 | Agent-to-agent messaging |
| `mcp_servers.go` | 207 | MCP server management |
| `tasks.go` | 197 | Task CRUD |
| `actions.go` | 196 | Action dispatch |
| `artifacts.go` | 194 | Artifact CRUD |
| `messages.go` | 193 | Message send + SSE streaming |

The existing `api_test.go` tests:
- `TestHealthEndpoint` — health check returns 200
- Basic route registration via `newTestAPI()`

The existing `plugins_install_test.go` tests plugin installation flow.

The reviewer-backend context flags: "Input validation on every POST/PUT handler — request body decoding, field validation, length limits" and "plugin install paths. Plugin install is a privilege boundary."

## Impact

The HTTP API is trust boundary #1 in the reviewer-backend context. Every POST/PUT handler decodes a request body and passes it to the service/store layer. Without tests, there is no verification of input validation, error handling, or authorization checks on any route except health and plugin install. `shell.go` (288 lines) exposes subprocess execution via the API with no test coverage.

## Recommendation

This is a large surface area. Priority targets:
1. `shell.go` — subprocess execution, must verify command validation
2. `plugins.go` — remaining handlers beyond install (enable/disable, config, uninstall)
3. `agents.go` — CRUD with field validation
4. `sessions.go` — session creation with agent resolution logic

Use the existing `newTestAPI()` helper which sets up a real store and service container.

## References

- Reviewer-backend context: "Input validation on every POST/PUT handler"
- Reviewer-backend context: "Plugin install is a privilege boundary"
- `internal/api/api_test.go` — existing test infrastructure
