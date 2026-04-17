# [Info] Well-tested packages: sandbox/exec, permission engine, toolclient/permissions, managed section, install service

**Scope:** Multiple packages
**Topic:** Test Quality — praise
**Date:** 2026-04-11

## Problem

No problem. These packages have excellent test coverage relative to their source size and criticality.

## Evidence

### internal/sandbox/exec_test.go (256 lines)
Covers: basic command execution, denylist blocking, timeout handling, environment variable filtering (API keys stripped), CWD restriction to sandbox directory, extra env filtering. Tests both `AgentExec` and `UserExec` paths. The `TestIsSecretKey` test covers 13 key patterns. The `TestCheckDenylist` test covers 7 blocked and 6 allowed commands.

### internal/permission/engine_test.go (209 lines)
Covers: all 4 permission modes (Yolo, Plan, Default, AcceptEdits), rule overrides with deny-takes-priority semantics, session grants with grant/clear lifecycle, approval flow with timeout/cancel/respond, and context cancellation. This is a model test suite — every decision path is exercised.

### internal/toolclient/permissions_test.go (232 lines)
Covers: empty/default permissions, allow/deny list matching, wildcard patterns, deny-takes-precedence, exact match, JSON unmarshal with shorthand aliases, merged allow lists. Thorough edge case coverage.

### internal/agent/managed_section_test.go (287 lines)
Covers: new file creation, user content preservation, section replacement, idempotent writes, read with/without markers, remove with missing file/no markers/preserve outside/empty after. Good boundary coverage (though adversarial inputs are a gap — see finding 05).

### internal/worker/manager_test.go (323 lines)
Covers: SpawnFull happy/failure paths, concurrency limit enforcement, worker cancellation, SpawnLight, List/ActiveCount, Shutdown, and the critical `TestListConcurrentFieldAccess` regression test that specifically exercises concurrent field reads/writes under -race. This test was written in response to real data races found on 2026-04-10.

### internal/sandbox/proxy_test.go (245 lines)
Covers: allowed/denied HTTP, allowed/denied CONNECT, wildcard domain matching (10-case table), Stop cleanup, missing host handling. Good functional coverage (security-specific gaps noted in finding 03).

### internal/service/install/ (2601 lines across 14 test files)
The install service is the best-tested subsystem in the codebase. Covers: adapter detection, cleanup, persistence, prompt, selection, archive, CLAUDE.md generation, installation flow, integration, migration, resume, rollback, scaffold, and state management. This is the gold standard for test coverage in this project.

## Impact

These packages demonstrate that the project has the infrastructure and patterns for quality testing. The `newTestStore()` helper, the `tempDevTools()` helper, and the `stubDelegator` mock pattern are reusable across packages. The gap is not tooling or skill — it's coverage extension to the remaining untested subsystems.

## Recommendation

Use these as reference implementations when writing tests for the untested packages:
- `permission/engine_test.go` — model for decision-path coverage
- `worker/manager_test.go` — model for concurrency tests under -race
- `service/install/*_test.go` — model for integration test suites
- `sandbox/exec_test.go` — model for security-boundary tests

## References

- All files cited above in `internal/`
