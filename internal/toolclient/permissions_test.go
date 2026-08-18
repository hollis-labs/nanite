package toolclient

import (
	"encoding/json"
	"testing"
)

// All tool-name examples in this file use the uniform agent-facing
// naming established by ADR-002 (no `mcp__server__` prefix). The
// pattern matcher is naming-agnostic; the choice of examples merely
// reflects what production tool names look like post-internalization.

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
	raw := `{"allow_list":["engine_*"],"deny_list":["hadron_bp_*"],"max_calls_per_turn":5}`
	p := ParsePermissions(raw)
	if len(p.AllowList) != 1 || p.AllowList[0] != "engine_*" {
		t.Errorf("unexpected allow_list: %v", p.AllowList)
	}
	if len(p.DenyList) != 1 || p.DenyList[0] != "hadron_bp_*" {
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
	if !p.CheckPermission("task_create") {
		t.Error("expected allow with no restrictions")
	}
}

func TestCheckPermission_AllowList(t *testing.T) {
	p := ToolPermissions{
		AllowList:       []string{"engine_*"},
		MaxCallsPerTurn: 25,
	}

	if !p.CheckPermission("engine_task_create") {
		t.Error("expected allow for matching pattern")
	}
	if p.CheckPermission("hadron_health") {
		t.Error("expected deny for non-matching pattern")
	}
}

func TestCheckPermission_DenyList(t *testing.T) {
	p := ToolPermissions{
		DenyList:        []string{"hadron_bp_*"},
		MaxCallsPerTurn: 25,
	}

	if !p.CheckPermission("hadron_health") {
		t.Error("expected allow for non-matching deny pattern")
	}
	if p.CheckPermission("hadron_bp_clone_repos") {
		t.Error("expected deny for matching deny pattern")
	}
}

func TestCheckPermission_DenyTakesPrecedence(t *testing.T) {
	p := ToolPermissions{
		AllowList:       []string{"hadron_*"},
		DenyList:        []string{"hadron_bp_*"},
		MaxCallsPerTurn: 25,
	}

	if !p.CheckPermission("hadron_health") {
		t.Error("expected allow: matches allow, not deny")
	}
	if p.CheckPermission("hadron_bp_build_cortex_backend") {
		t.Error("expected deny: deny takes precedence over allow")
	}
}

func TestCheckPermission_ExactMatch(t *testing.T) {
	p := ToolPermissions{
		AllowList:       []string{"example_task_create"},
		MaxCallsPerTurn: 25,
	}

	if !p.CheckPermission("example_task_create") {
		t.Error("expected allow for exact match")
	}
	if p.CheckPermission("example_task_delete") {
		t.Error("expected deny for non-matching exact pattern")
	}
}

// --- Unmarshal alias tests (TASK-20260320-106) ---

func TestUnmarshalJSON_AllowShorthand(t *testing.T) {
	raw := `{"allow":["email_*"]}`
	var p ToolPermissions
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(p.AllowList) != 1 || p.AllowList[0] != "email_*" {
		t.Fatalf("expected AllowList=[email_*], got %v", p.AllowList)
	}
}

func TestUnmarshalJSON_AllowListCanonical(t *testing.T) {
	raw := `{"allow_list":["email_*"]}`
	var p ToolPermissions
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(p.AllowList) != 1 || p.AllowList[0] != "email_*" {
		t.Fatalf("expected AllowList=[email_*], got %v", p.AllowList)
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
	p := ParsePermissions(`{"allow":["email_*"]}`)
	if len(p.AllowList) != 1 || p.AllowList[0] != "email_*" {
		t.Fatalf("ParsePermissions did not handle shorthand allow: %v", p.AllowList)
	}
	if p.MaxCallsPerTurn != DefaultMaxCallsPerTurn {
		t.Fatalf("expected default max calls, got %d", p.MaxCallsPerTurn)
	}
}

func TestCheckPermission_WithMergedAllowList(t *testing.T) {
	raw := `{"allow":["email_*"],"allow_list":["teams_*"]}`
	p := ParsePermissions(raw)

	tests := []struct {
		tool string
		want bool
	}{
		{"email_send", true},
		{"teams_notify", true},
		{"dev_bash", false},
	}
	for _, tt := range tests {
		got := p.CheckPermission(tt.tool)
		if got != tt.want {
			t.Errorf("CheckPermission(%q) = %v, want %v", tt.tool, got, tt.want)
		}
	}
}

func TestCheckPermission_DenyShorthandTakesPrecedence(t *testing.T) {
	raw := `{"allow_list":["email_*"],"deny":["email_send"]}`
	p := ParsePermissions(raw)

	if p.CheckPermission("email_send") {
		t.Error("expected deny to take precedence for email_send")
	}
	if !p.CheckPermission("email_read") {
		t.Error("expected email_read to be allowed")
	}
}

func TestCheckPermission_EmptyPermissive(t *testing.T) {
	p := ParsePermissions(`{}`)
	if !p.CheckPermission("anything") {
		t.Error("empty permissions should be permissive")
	}
}
