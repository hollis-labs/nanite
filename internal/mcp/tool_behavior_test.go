package mcp

import "testing"

// A client that guesses "is this a write?" from the tool's name will
// eventually guess wrong, and the direction it fails is letting a write
// through unasked. These pin the declared-behavior path that replaces the
// guess.

func addToolForTest(t *testing.T, m *Manager, server, name string, ann map[string]any) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := &toolEntry{
		serverName:  server,
		uniformName: name,
		tool:        Tool{Name: name, Annotations: ann},
	}
	m.tools = append(m.tools, entry)
	m.uniformIndex[name] = entry
}

func TestToolBehaviorReportsDeclaredWrite(t *testing.T) {
	m := NewManager()
	addToolForTest(t, m, "hr-server", "request_time_off", map[string]any{
		"readOnlyHint": false, "destructiveHint": true,
	})

	readOnly, destructive, ok := m.ToolBehavior("request_time_off")
	if !ok {
		t.Fatal("declared annotations should be reported")
	}
	if readOnly {
		t.Error("a declared write must not read as read-only")
	}
	if !destructive {
		t.Error("destructiveHint must survive to the caller — the approval gate keys off it")
	}
}

func TestToolBehaviorReportsDeclaredRead(t *testing.T) {
	m := NewManager()
	addToolForTest(t, m, "hr-server", "get_time_off_balances", map[string]any{
		"readOnlyHint": true, "destructiveHint": false,
	})

	readOnly, destructive, ok := m.ToolBehavior("get_time_off_balances")
	if !ok || !readOnly || destructive {
		t.Fatalf("want read-only, got readOnly=%v destructive=%v ok=%v", readOnly, destructive, ok)
	}
}

func TestToolBehaviorUnknownWhenNothingDeclared(t *testing.T) {
	// A server that annotates nothing must read as "unknown", never as "safe":
	// the caller has to keep its own fallback rather than be told read-only.
	m := NewManager()
	addToolForTest(t, m, "hr-server", "mystery_tool", nil)

	if _, _, ok := m.ToolBehavior("mystery_tool"); ok {
		t.Error("absent annotations must report ok=false, not a confident false/false")
	}
}

func TestToolBehaviorUnknownForUnregisteredName(t *testing.T) {
	m := NewManager()
	if _, _, ok := m.ToolBehavior("never_registered"); ok {
		t.Error("an unregistered name must not report as known")
	}
}

func TestToolBehaviorIgnoresNonBooleanHints(t *testing.T) {
	// A server sending "true" as a string must not be read as true.
	m := NewManager()
	addToolForTest(t, m, "hr-server", "odd_tool", map[string]any{"readOnlyHint": "true"})

	readOnly, _, ok := m.ToolBehavior("odd_tool")
	if !ok {
		t.Fatal("annotations were present, so the lookup should succeed")
	}
	if readOnly {
		t.Error("a non-boolean hint must not be coerced to true")
	}
}
