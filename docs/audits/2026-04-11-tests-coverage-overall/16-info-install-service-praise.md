# [Info] Install service: exemplary test suite at 2601 lines across 14 files

**Scope:** internal/service/install/
**Topic:** Test Quality — praise
**Date:** 2026-04-11

## Problem

No problem. The install service test suite is the best example of comprehensive testing in the codebase.

## Evidence

14 test files totaling 2601 lines:

| File | Lines | Coverage area |
|------|-------|--------------|
| `integration_test.go` | 334 | End-to-end install flow |
| `migrate_test.go` | 384 | Migration from old config formats |
| `adapters_test.go` | 284 | Adapter registration and lifecycle |
| `plugins_install_test.go` | 215 | Plugin installation (in api/) |
| `adapter_persist_test.go` | 202 | Adapter config persistence |
| `claudemd_test.go` | 169 | CLAUDE.md file generation |
| `adapter_prompt_test.go` | 161 | Adapter prompt construction |
| `adapter_select_test.go` | 161 | Adapter selection logic |
| `scaffold_test.go` | 139 | Project scaffolding |
| `resume_test.go` | 138 | Install resume after interruption |
| `rollback_test.go` | 122 | Rollback on failure |
| `adapter_cleanup_test.go` | 111 | Adapter cleanup on uninstall |
| `install_test.go` | 93 | Core install logic |
| `state_test.go` | 80 | State machine transitions |
| `adapter_detect_test.go` | 74 | CLI adapter detection |

This suite covers:
- Happy path and error paths
- State machine transitions
- Resume after interruption
- Rollback on failure
- Migration from legacy formats
- Integration tests with real store

## Impact

This test suite pattern should be replicated for the service layer (`chat_generate.go`, `chat_tool_executor.go`, `delegation.go`) and the API handler layer.

## Recommendation

When writing tests for `internal/service/chat_generate.go` and `internal/service/chat_tool_executor.go`, follow the install service's patterns:
- Separate files per concern (one for happy path, one for error paths, one for state transitions)
- Integration tests that wire up real store + mock provider
- State machine verification (stream states, tool loop states)

## References

- `internal/service/install/` — all 14 test files
