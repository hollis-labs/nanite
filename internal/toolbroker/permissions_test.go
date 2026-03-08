package toolbroker

import "testing"

func TestParsePermissions_Empty(t *testing.T) {
	p := ParsePermissions("")
	if p.MaxCallsPerTurn != DefaultMaxCallsPerTurn {
		t.Errorf("expected max_calls=%d, got %d", DefaultMaxCallsPerTurn, p.MaxCallsPerTurn)
	}
	if len(p.AllowList) != 0 || len(p.DenyList) != 0 {
		t.Error("expected empty allow/deny lists")
	}
}

func TestParsePermissions_EmptyObject(t *testing.T) {
	p := ParsePermissions("{}")
	if p.MaxCallsPerTurn != DefaultMaxCallsPerTurn {
		t.Errorf("expected max_calls=%d, got %d", DefaultMaxCallsPerTurn, p.MaxCallsPerTurn)
	}
}

func TestParsePermissions_WithLists(t *testing.T) {
	raw := `{"allow_list":["mcp__volon__*"],"deny_list":["mcp__hadron__hadron_bp_*"],"max_calls_per_turn":5}`
	p := ParsePermissions(raw)
	if len(p.AllowList) != 1 || p.AllowList[0] != "mcp__volon__*" {
		t.Errorf("unexpected allow_list: %v", p.AllowList)
	}
	if len(p.DenyList) != 1 || p.DenyList[0] != "mcp__hadron__hadron_bp_*" {
		t.Errorf("unexpected deny_list: %v", p.DenyList)
	}
	if p.MaxCallsPerTurn != 5 {
		t.Errorf("expected max_calls=5, got %d", p.MaxCallsPerTurn)
	}
}

func TestParsePermissions_InvalidJSON(t *testing.T) {
	p := ParsePermissions("not json")
	if p.MaxCallsPerTurn != DefaultMaxCallsPerTurn {
		t.Error("expected default on invalid JSON")
	}
}

func TestCheckPermission_NoRestrictions(t *testing.T) {
	p := ToolPermissions{MaxCallsPerTurn: 25}
	if !p.CheckPermission("mcp__volon__task_create") {
		t.Error("expected allow with no restrictions")
	}
}

func TestCheckPermission_AllowList(t *testing.T) {
	p := ToolPermissions{
		AllowList:       []string{"mcp__volon__*"},
		MaxCallsPerTurn: 25,
	}

	if !p.CheckPermission("mcp__volon__task_create") {
		t.Error("expected allow for matching pattern")
	}
	if p.CheckPermission("mcp__hadron__hadron_health") {
		t.Error("expected deny for non-matching pattern")
	}
}

func TestCheckPermission_DenyList(t *testing.T) {
	p := ToolPermissions{
		DenyList:        []string{"mcp__hadron__hadron_bp_*"},
		MaxCallsPerTurn: 25,
	}

	if !p.CheckPermission("mcp__hadron__hadron_health") {
		t.Error("expected allow for non-matching deny pattern")
	}
	if p.CheckPermission("mcp__hadron__hadron_bp_clone_repos") {
		t.Error("expected deny for matching deny pattern")
	}
}

func TestCheckPermission_DenyTakesPrecedence(t *testing.T) {
	p := ToolPermissions{
		AllowList:       []string{"mcp__hadron__*"},
		DenyList:        []string{"mcp__hadron__hadron_bp_*"},
		MaxCallsPerTurn: 25,
	}

	if !p.CheckPermission("mcp__hadron__hadron_health") {
		t.Error("expected allow: matches allow, not deny")
	}
	if p.CheckPermission("mcp__hadron__hadron_bp_build_cortex_backend") {
		t.Error("expected deny: deny takes precedence over allow")
	}
}

func TestCheckPermission_ExactMatch(t *testing.T) {
	p := ToolPermissions{
		AllowList:       []string{"mcp__volon__volon_task_create"},
		MaxCallsPerTurn: 25,
	}

	if !p.CheckPermission("mcp__volon__volon_task_create") {
		t.Error("expected allow for exact match")
	}
	if p.CheckPermission("mcp__volon__volon_task_delete") {
		t.Error("expected deny for non-matching exact pattern")
	}
}
