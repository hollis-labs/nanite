# [Low/Info] Minor observations

**Scope:** internal/toolclient
**Topic:** Idioms, test quality
**Date:** 2026-04-11

## 11a — [Low] BuiltinToolRegistry.GetBuiltins iteration order is non-deterministic

**Problem:** `GetBuiltins()` iterates over `r.tools` (a `map[string][]provider.ToolDefinition`) and appends tools. Map iteration order in Go is non-deterministic, so the order of built-in tools in the returned slice varies across calls.

**Evidence:** `internal/toolclient/builtin.go:L33-L42`:
```go
func (r *BuiltinToolRegistry) GetBuiltins() []provider.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var all []provider.ToolDefinition
	for _, tools := range r.tools {
		all = append(all, tools...)
	}
	return all
}
```

**Impact:** Tool ordering in the LLM prompt varies across calls. LLMs are sensitive to tool ordering. Not a correctness bug but could cause non-reproducible behavior in testing.

**Recommendation:** Sort the map keys before iterating, or use a slice of category-tool pairs instead of a map.

---

## 11b — [Info] Test coverage is solid for permissions and intent scoring

**Problem:** None — this is praise.

**Evidence:** 
- `permissions_test.go` covers: empty permissions, allow list, deny list, deny-takes-precedence, exact match, shorthand aliases, merged allow lists, shorthand deny precedence, empty permissive behavior. 18 test functions.
- `broker_test.go` covers: tool selection, max cap, token estimation, token pruning (under/over budget, keeps-at-least-one, empty), config rule merging, intent scoring, meta-tool handling. 18 test functions.
- `tool_knowledge_test.go` covers: category completeness, field validation, intent matching, summary compactness. 3 test functions.
- `builtin_test.go` covers: registration, always-available, category retrieval, empty registry, overwrite. 5 test functions.

**Impact:** Good coverage for the happy paths and core edge cases. The permission model is well-tested.

**Recommendation:** Consider adding tests for the gaps identified in findings 01-03 (permission bypass paths). Specifically:
- Test that `SelectToolsAsProvider` with a deny list for a built-in tool still returns that tool (documenting the current behavior as a regression baseline).
- Test that `HandleRequestTools` returns tools denied by the agent's permissions (same purpose).

---

## 11c — [Info] Config.RulesFor creates a new slice on every call

**Problem:** `RulesFor()` allocates a new slice and copies the base rules on every invocation. In `SelectTools()`, this is called on every tool selection when `workspaceID` or `agentID` is non-empty.

**Evidence:** `internal/toolclient/config.go:L69-L86`:
```go
func (c *Config) RulesFor(workspaceID, agentID string) []broker.Rule {
	rules := make([]broker.Rule, len(c.Rules))
	copy(rules, c.Rules)
	// ...
}
```

Then in `broker.go:L72-L75`:
```go
if workspaceID != "" || agentID != "" {
    rules := tb.Config.RulesFor(workspaceID, agentID)
    tb.LocalBroker.LoadRules(rules)
}
```

**Impact:** Negligible. The rule set is small (typically <20 rules) and tool selection happens at most once per LLM turn. Not a performance concern at current scale.

**Recommendation:** No action needed. Noted for awareness if the call frequency increases.

## References

- `internal/toolclient/builtin.go:L33-L42` — non-deterministic iteration
- `internal/toolclient/permissions_test.go` — comprehensive permission tests
- `internal/toolclient/broker_test.go` — comprehensive broker tests
- `internal/toolclient/config.go:L69-L86` — per-call allocation
