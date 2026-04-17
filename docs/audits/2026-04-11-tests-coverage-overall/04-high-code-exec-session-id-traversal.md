# [High] Code exec tools missing session ID path traversal test

**Scope:** internal/mcp/, internal/sandbox/
**Topic:** Test Quality — security-critical path traversal gap
**Date:** 2026-04-11

## Problem

`nanite_code_execute` uses the session ID to construct a sandbox directory path via `sandbox.Dir(sessionID)`. Neither `internal/mcp/code_exec_tools_test.go` nor `internal/sandbox/exec_test.go` tests what happens when the session ID contains path traversal characters (`../`, absolute paths, null bytes).

## Evidence

`internal/mcp/code_exec_tools_test.go` creates transports with clean session IDs:
- `"test-shell"` (line 14)
- `"test-shell-default"` (line 35)
- `"test-timeout"` (line 53)
- `"test-python"` (line 78)
- `"test-exitcode"` (line 99)
- `"test-invalid"` (line 151)
- `"test-large"` (line 118)

`internal/sandbox/exec_test.go` also uses clean session IDs:
- `"test-basic"` (line 14)
- `"test-deny"` (line 39)
- `"test-timeout"` (line 55)
- `"test-env"` (line 78)
- `"test-cwd"` (line 98)
- `"test-extra-env"` (line 119)

`internal/sandbox/exec.go:L84-95` passes the session ID directly to `Dir()`:
```go
func AgentExec(opts AgentExecOpts) (*ExecResult, error) {
    // ...
    sandboxDir, err := Dir(opts.SessionID)
```

The `sandbox-hardening` audit (`2026-04-10`) identified path traversal in `nanite_code_execute` via session ID as a Critical finding.

## Impact

If `Dir()` does not sanitize the session ID, an attacker-controlled session ID like `../../etc` could cause code execution in an arbitrary directory. Even if `Dir()` does sanitize, the absence of a test means a regression could silently remove the sanitization.

## Recommendation

Add tests:

1. In `code_exec_tools_test.go`:
```go
func TestCodeExecute_SessionIDTraversal(t *testing.T) {
    transport := NewCodeExecTransport("../../etc")
    result, err := transport.CallTool(ctx, "nanite_code_execute", map[string]any{
        "code": "pwd", "language": "shell",
    })
    // Verify either: error returned, or CWD is within the sandbox base dir
}
```

2. In `sandbox_test.go`:
```go
func TestDir_TraversalBlocked(t *testing.T) {
    _, err := Dir("../../../etc")
    // Verify error or verify the resulting path is still under the sandbox base
}
```

## References

- `2026-04-10-sandbox-hardening` — path traversal in `nanite_code_execute` via session ID (Critical)
- `internal/sandbox/exec.go:L84-95` — session ID passed to Dir()
- `internal/mcp/code_exec_tools_test.go` — all session IDs are clean strings
