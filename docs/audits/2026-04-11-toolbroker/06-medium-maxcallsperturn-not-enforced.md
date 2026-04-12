# [Medium] MaxCallsPerTurn permission field is parsed but never enforced

**Scope:** internal/toolclient
**Topic:** Security — permission checking
**Date:** 2026-04-11

## Problem

`ToolPermissions.MaxCallsPerTurn` is parsed from agent configuration and has a default value of 25, but no code path in the toolclient package (or its callers) counts or limits the number of tool calls per turn.

## Evidence

`internal/toolclient/permissions.go:L17`:

```go
type ToolPermissions struct {
	AllowList          []string `json:"allow_list,omitempty"`
	DenyList           []string `json:"deny_list,omitempty"`
	MaxCallsPerTurn    int      `json:"max_calls_per_turn,omitempty"`
	AllowDelegation    bool     `json:"allow_delegation,omitempty"`
	AllowCodeExecution bool     `json:"allow_code_execution,omitempty"`
}
```

`internal/toolclient/permissions.go:L115-L117`:

```go
if perms.MaxCallsPerTurn <= 0 {
    perms.MaxCallsPerTurn = DefaultMaxCallsPerTurn
}
```

Grep for `MaxCallsPerTurn` usage across the codebase shows it is only referenced in:
- `permissions.go` — struct definition, parsing, and default assignment
- `permissions_test.go` — test assertions that the value is parsed correctly

No call site reads `perms.MaxCallsPerTurn` to enforce a limit. The `CallTool()` method does not maintain a counter. The `ToolService.Execute()` method does not track call counts per turn.

Similarly, `AllowDelegation` and `AllowCodeExecution` are parsed but never checked anywhere in the execution path.

## Impact

Administrators who configure `max_calls_per_turn: 5` to limit an agent's tool call budget believe they are constraining the agent, but the limit has no effect. An LLM-driven agent can make unlimited tool calls per turn. This is a defense-in-depth gap: if a prompt injection causes the agent to enter a tool-call loop, there is no circuit breaker at the permission layer.

The `allow_delegation` and `allow_code_execution` fields are similarly dead configuration — they create a false sense of control over agent capabilities.

## Recommendation

Either enforce the limits or remove the fields to avoid misleading configuration:

**Option A (enforce):** Add a per-turn call counter to `ToolService.Execute()` or to the chat engine's tool-call loop. Reset the counter at the start of each turn. Deny calls when the counter exceeds `MaxCallsPerTurn`.

**Option B (remove):** Delete `MaxCallsPerTurn`, `AllowDelegation`, and `AllowCodeExecution` from `ToolPermissions` until enforcement is implemented. Add `// TODO: implement enforcement` comments in the struct definition if the intent is to add them later.

Option A is recommended given the defense-in-depth value.

## References

- `internal/toolclient/permissions.go:L12-L20` — struct definition with unenforced fields
- `internal/toolclient/permissions.go:L104-L120` — `ParsePermissions()` sets default of 25
- `internal/toolclient/broker.go:L150-L172` — `CallTool()` has no call counter
- `internal/service/tool.go:L192-L213` — `Execute()` has no call counter
