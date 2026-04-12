# [Info] Positive observations

**Scope:** Subprocess / PTY / SDK patterns
**Topic:** Architecture — praise
**Date:** 2026-04-11

## Process tracker is well-designed

`internal/chat/proctrack.go` provides session-scoped process tracking with:
- Health check with idle detection (`HealthCheck`, `KillStale`)
- Capacity limiting (`AtCapacity`)
- Clean session-level and global teardown (`KillSession`, `KillAll`)
- Activity tracking via PID-based `Touch` callback

The integration with PTY/subprocess bridges via context-carried `ProcessCallback` and `ActivityCallback` (`pkg/provider/provider.go:L153-185`) is clean — no import cycle between `chat` and `provider` packages.

## CLIAdapter interface is well-scoped

The 4-method `CLIAdapter` interface (`Name`, `BuildArgs`, `ParseLine`, `Detect`) is appropriately minimal. All 8 implementations are consistent in structure. The `ParseLine([]byte) ([]StreamEvent, error)` signature enables clean testing via table-driven byte inputs.

## Sandbox AgentExec is a strong reference

`internal/sandbox/exec.go` demonstrates the target security posture:
- Minimal env (`HOME`, `USER`, `LANG`, `TERM` only)
- Restricted `PATH` (3 dirs)
- Secret filtering with pattern matching
- Denylist enforcement
- CWD scoping to sandbox directory
- OS-level sandbox integration (`applyOSSandbox`)
- Output size limits (1MB per stream)
- Timeout clamping with sane defaults

The gap is that this posture is not applied to PTY/subprocess bridges or several other `exec.Command` call sites.

## Dual registration (PTY + subprocess) is pragmatic

Registering each adapter as both `pty-<name>` and `sub-<name>` in `main.go:L375-381` gives the system flexibility to choose the right spawning strategy per context (PTY for interactive CLIs that need a TTY, subprocess for non-interactive or Windows).

## AdapterRegistry has good concurrent access patterns

`internal/agent/adapter.go` — copy-before-iterate pattern (`Adapters()` returns a copy), RWMutex with proper scoping, deterministic sort on registration. This is a well-implemented concurrent registry.

## References

- Files cited above
