package toolclient

import (
	"encoding/json"
	"testing"
)

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
	raw := `{"allow_list":["mcp__engine__*"],"deny_list":["mcp__hadron__hadron_bp_*"],"max_calls_per_turn":5}`
	p := ParsePermissions(raw)
	if len(p.AllowList) != 1 || p.AllowList[0] != "mcp__engine__*" {
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
	if !p.CheckPermission("mcp__engine__task_create") {
		t.Error("expected allow with no restrictions")
	}
}

func TestCheckPermission_AllowList(t *testing.T) {
	p := ToolPermissions{
		AllowList:       []string{"mcp__engine__*"},
		MaxCallsPerTurn: 25,
	}

	if !p.CheckPermission("mcp__engine__task_create") {
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
		AllowList:       []string{"mcp__engine__volon_task_create"},
		MaxCallsPerTurn: 25,
	}

	if !p.CheckPermission("mcp__engine__volon_task_create") {
		t.Error("expected allow for exact match")
	}
	if p.CheckPermission("mcp__engine__volon_task_delete") {
		t.Error("expected deny for non-matching exact pattern")
	}
}

// --- Unmarshal alias tests (TASK-20260320-106) ---

func TestUnmarshalJSON_AllowShorthand(t *testing.T) {
	raw := `{"allow":["mcp__email__*"]}`
	var p ToolPermissions
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(p.AllowList) != 1 || p.AllowList[0] != "mcp__email__*" {
		t.Fatalf("expected AllowList=[mcp__email__*], got %v", p.AllowList)
	}
}

func TestUnmarshalJSON_AllowListCanonical(t *testing.T) {
	raw := `{"allow_list":["mcp__email__*"]}`
	var p ToolPermissions
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(p.AllowList) != 1 || p.AllowList[0] != "mcp__email__*" {
		t.Fatalf("expected AllowList=[mcp__email__*], got %v", p.AllowList)
	}
}

func TestUnmarshalJSON_AllowBothMerged(t *testing.T) {
	raw := `{"allow":["a"],"allow_list":["b"]}`
	var p ToolPermissions
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(p.AllowList) != 2 {
		t.Fatalf("expected 2 entries, got %v", p.AllowList)
	}
	has := map[string]bool{}
	for _, v := range p.AllowList {
		has[v] = true
	}
	if !has["a"] || !has["b"] {
		t.Fatalf("expected both a and b, got %v", p.AllowList)
	}
}

func TestUnmarshalJSON_DenyShorthand(t *testing.T) {
	raw := `{"deny":["x"]}`
	var p ToolPermissions
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(p.DenyList) != 1 || p.DenyList[0] != "x" {
		t.Fatalf("expected DenyList=[x], got %v", p.DenyList)
	}
}

func TestUnmarshalJSON_EmptyObject(t *testing.T) {
	raw := `{}`
	var p ToolPermissions
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(p.AllowList) != 0 || len(p.DenyList) != 0 {
		t.Fatalf("expected empty lists, got allow=%v deny=%v", p.AllowList, p.DenyList)
	}
}

func TestUnmarshalJSON_MaxCallsPerTurn(t *testing.T) {
	raw := `{"allow_list":["a"],"max_calls_per_turn":10}`
	var p ToolPermissions
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.MaxCallsPerTurn != 10 {
		t.Fatalf("expected MaxCallsPerTurn=10, got %d", p.MaxCallsPerTurn)
	}
}

func TestParsePermissions_ShorthandAllow(t *testing.T) {
	p := ParsePermissions(`{"allow":["mcp__email__*"]}`)
	if len(p.AllowList) != 1 || p.AllowList[0] != "mcp__email__*" {
		t.Fatalf("ParsePermissions did not handle shorthand allow: %v", p.AllowList)
	}
	if p.MaxCallsPerTurn != DefaultMaxCallsPerTurn {
		t.Fatalf("expected default max calls, got %d", p.MaxCallsPerTurn)
	}
}

func TestCheckPermission_WithMergedAllowList(t *testing.T) {
	raw := `{"allow":["mcp__email__*"],"allow_list":["mcp__teams__*"]}`
	p := ParsePermissions(raw)

	tests := []struct {
		tool string
		want bool
	}{
		{"mcp__email__send", true},
		{"mcp__teams__notify", true},
		{"mcp__dev__bash", false},
	}
	for _, tt := range tests {
		got := p.CheckPermission(tt.tool)
		if got != tt.want {
			t.Errorf("CheckPermission(%q) = %v, want %v", tt.tool, got, tt.want)
		}
	}
}

func TestCheckPermission_DenyShorthandTakesPrecedence(t *testing.T) {
	raw := `{"allow_list":["mcp__email__*"],"deny":["mcp__email__send"]}`
	p := ParsePermissions(raw)

	if p.CheckPermission("mcp__email__send") {
		t.Error("expected deny to take precedence for mcp__email__send")
	}
	if !p.CheckPermission("mcp__email__read") {
		t.Error("expected mcp__email__read to be allowed")
	}
}

func TestCheckPermission_EmptyPermissive(t *testing.T) {
	p := ParsePermissions(`{}`)
	if !p.CheckPermission("anything") {
		t.Error("empty permissions should be permissive")
	}
}
