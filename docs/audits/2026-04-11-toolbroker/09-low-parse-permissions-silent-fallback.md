# [Low] ParsePermissions silently falls back to permissive defaults on invalid JSON

**Scope:** internal/toolclient
**Topic:** Error handling
**Date:** 2026-04-11

## Problem

`ParsePermissions()` returns a fully permissive default (empty allow/deny lists, 25 calls/turn) when the input JSON is malformed. No error is logged and no indication is given that the configured permissions were not applied.

## Evidence

`internal/toolclient/permissions.go:L104-L120`:

```go
func ParsePermissions(raw string) ToolPermissions {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return ToolPermissions{MaxCallsPerTurn: DefaultMaxCallsPerTurn}
	}

	var perms ToolPermissions
	if err := json.Unmarshal([]byte(raw), &perms); err != nil {
		return ToolPermissions{MaxCallsPerTurn: DefaultMaxCallsPerTurn}  // silent fallback
	}
	// ...
}
```

If an agent's `tool_permissions` column contains malformed JSON (e.g., a truncated string, a typo, or a migration error), the agent silently receives full permissions instead of its intended restrictions.

The caller `GetPermissions()` in `broker.go:L175-L187` also has a silent fallback:

```go
func (tb *ToolClient) GetPermissions(agentID string) ToolPermissions {
	if tb.Store == nil {
		return ToolPermissions{MaxCallsPerTurn: DefaultMaxCallsPerTurn}
	}
	agent, err := tb.Store.GetAgent(agentID)
	if err != nil {
		log.Printf("toolclient: could not load agent %s for permissions: %v", agentID, err)
		return ToolPermissions{MaxCallsPerTurn: DefaultMaxCallsPerTurn}
	}
	return ParsePermissions(agent.ToolPermissions)
}
```

`GetPermissions` logs on store errors but `ParsePermissions` does not log on JSON parse errors.

## Impact

A typo in an agent's permission configuration silently grants the agent full permissions. This is a fail-open design for a security-relevant configuration. The administrator has no way to know their restriction was not applied unless they manually test the agent's tool access.

## Recommendation

Log a warning on JSON parse failure so administrators can detect misconfigured permissions:

```go
if err := json.Unmarshal([]byte(raw), &perms); err != nil {
    log.Printf("toolclient: WARNING failed to parse tool_permissions (falling back to permissive defaults): %v", err)
    return ToolPermissions{MaxCallsPerTurn: DefaultMaxCallsPerTurn}
}
```

Consider whether fail-open is the correct default. For a security control, fail-closed (deny all tools on parse error) is more conservative. At minimum, log the raw input (truncated) so the administrator can see what failed.

## References

- `internal/toolclient/permissions.go:L104-L120` — `ParsePermissions()` silent fallback
- `internal/toolclient/broker.go:L175-L187` — `GetPermissions()` logs store errors but not parse errors
