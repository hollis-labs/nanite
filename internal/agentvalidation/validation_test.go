package agentvalidation

import (
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestValidAgent(t *testing.T) {
	agent := &store.AgentProfile{
		Name:         "Test Agent",
		Slug:         "test-agent",
		SystemPrompt: "You are a test agent.",
		MCPServers:   `["conduit"]`,
	}
	result := ValidateAgentConfig(agent)
	if !result.OK() {
		t.Fatalf("expected no errors, got: %v", result.Errors)
	}
	if len(result.Warnings) > 0 {
		t.Fatalf("expected no warnings, got: %v", result.Warnings)
	}
}

// TestToolPermissionsNotValidated is the regression test for
// TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md: the
// tool_permissions column is inert everywhere now (agent_tools is the sole
// tool-selection gate), so its content -- malformed JSON, an empty
// allow_list, or bad glob syntax, all of which used to be blocking errors
// or warnings here -- must no longer affect validation at all. This
// replaces TestMalformedToolPermissionsJSON, TestEmptyAllowList,
// TestNoMCPServersPermissivePermissions, TestInvalidGlobPattern,
// TestEmptyPermissionsWithMCPServers, and TestDenyListWithInvalidGlob,
// which asserted the opposite, pre-this-task behavior.
func TestToolPermissionsNotValidated(t *testing.T) {
	agent := &store.AgentProfile{
		Name:            "Anything Goes",
		Slug:            "anything-goes",
		SystemPrompt:    "You have garbage tool_permissions.",
		MCPServers:      `[]`,
		ToolPermissions: `{"allow_list": [}`, // malformed JSON
	}
	result := ValidateAgentConfig(agent)
	if !result.OK() {
		t.Fatalf("expected no errors (tool_permissions is no longer validated), got: %v", result.Errors)
	}
	if len(result.Warnings) > 0 {
		t.Fatalf("expected no warnings (tool_permissions is no longer validated), got: %v", result.Warnings)
	}
}

func TestValidAgentWithAllowAndMCPServers(t *testing.T) {
	agent := &store.AgentProfile{
		Name:         "Full Agent",
		Slug:         "full-agent",
		SystemPrompt: "You are a full agent.",
		MCPServers:   `["conduit","engine"]`,
	}
	result := ValidateAgentConfig(agent)
	if !result.OK() {
		t.Fatalf("expected no errors, got: %v", result.Errors)
	}
	if len(result.Warnings) > 0 {
		t.Fatalf("expected no warnings, got: %v", result.Warnings)
	}
}

// TestParentDispatchAllowlist exercises the validation added in CW-20260512-0107.
// The field is a JSON string array of role slugs; empty/"[]" mean "no dispatch
// permission" and must validate. Malformed JSON, non-array JSON, and non-string
// elements must be rejected so the API fails fast.
func TestParentDispatchAllowlist(t *testing.T) {
	cases := []struct {
		name      string
		allowlist string
		wantErr   bool
	}{
		{"empty string accepted", "", false},
		{"empty array accepted", "[]", false},
		{"single role accepted", `["worker"]`, false},
		{"multiple roles accepted", `["worker","researcher"]`, false},
		{"not json rejected", "not json", true},
		{"object rejected", `{"role":"worker"}`, true},
		{"non-string element rejected", `["worker",42]`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent := &store.AgentProfile{
				Name:                    "Allowlist Agent",
				Slug:                    "allowlist-agent",
				SystemPrompt:            "You dispatch.",
				MCPServers:              `["conduit"]`,
				ParentDispatchAllowlist: tc.allowlist,
			}
			result := ValidateAgentConfig(agent)
			gotErr := !result.OK()
			if gotErr != tc.wantErr {
				t.Fatalf("ValidateAgentConfig(parent_dispatch_allowlist=%q): wantErr=%v gotErr=%v errors=%v",
					tc.allowlist, tc.wantErr, gotErr, result.Errors)
			}
		})
	}
}

// TestPrefixGlobIsValid and TestToolsInvalidGlobPattern exercise
// validateGlobPattern via the still-live v2 `tools` field (item 5 in
// ValidateAgentConfig) -- the only remaining validated glob-pattern column
// as of TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md
// (tool_permissions' own glob validation was removed with the rest of that
// column's enforcement machinery).
func TestPrefixGlobIsValid(t *testing.T) {
	// Patterns ending in * are prefix globs and should always be valid
	agent := &store.AgentProfile{
		Name:         "Prefix Glob",
		Slug:         "prefix-glob",
		SystemPrompt: "You use prefix globs.",
		MCPServers:   `["conduit"]`,
		Tools:        `["memory_*","engine_task_*"]`,
	}
	result := ValidateAgentConfig(agent)
	if !result.OK() {
		t.Fatalf("expected no errors for prefix globs, got: %v", result.Errors)
	}
}

func TestToolsInvalidGlobPattern(t *testing.T) {
	agent := &store.AgentProfile{
		Name:         "Bad Glob",
		Slug:         "bad-glob",
		SystemPrompt: "You have bad patterns.",
		MCPServers:   `["conduit"]`,
		Tools:        `["memory_[invalid"]`,
	}
	result := ValidateAgentConfig(agent)
	if result.OK() {
		t.Fatal("expected errors for invalid glob pattern in tools, got none")
	}
}

// TestConstraintsSubagentCompletionPolicy exercises the real, current
// constraints schema — found broken 2026-08-16 when a direct PUT to set
// auto_summarize on the Orchestrator profile was rejected: the validator
// still treated the entire constraints object as deprecated/numbers-only,
// years after CW-20260520-0001 added a real string field
// (subagent_completion_policy) to internal/chat.AgentConstraints.
func TestConstraintsSubagentCompletionPolicy(t *testing.T) {
	tests := []struct {
		name        string
		constraints string
		wantOK      bool
		wantErrLike string
	}{
		{
			name:        "auto_summarize is valid",
			constraints: `{"subagent_completion_policy":"auto_summarize"}`,
			wantOK:      true,
		},
		{
			name:        "render_and_wait is valid",
			constraints: `{"subagent_completion_policy":"render_and_wait"}`,
			wantOK:      true,
		},
		{
			name:        "batch is valid",
			constraints: `{"subagent_completion_policy":"batch"}`,
			wantOK:      true,
		},
		{
			name:        "unrecognized value is rejected",
			constraints: `{"subagent_completion_policy":"sometimes"}`,
			wantOK:      false,
			wantErrLike: "unrecognized value",
		},
		{
			name:        "wrong type is rejected",
			constraints: `{"subagent_completion_policy":42}`,
			wantOK:      false,
			wantErrLike: "must be a string",
		},
		{
			name:        "numeric Phase-4 fields are valid",
			constraints: `{"hard_ceiling":100,"consecutive_fail_cap":3,"runaway_fail_cap":10,"idle_timeout_seconds":900}`,
			wantOK:      true,
		},
		{
			// Phase 0 item 12 cut max_turns from the numeric-constraint schema
			// (it was a soft, telemetry-only budget that never gated the loop).
			// A profile still carrying a stale max_turns key from before the
			// cut must warn, not hard-fail validation.
			name:        "stale max_turns key is only a warning",
			constraints: `{"max_turns":50}`,
			wantOK:      true,
		},
		{
			name:        "hard_ceiling rejects negative",
			constraints: `{"hard_ceiling":-5}`,
			wantOK:      false,
			wantErrLike: "must be a non-negative number",
		},
		{
			name:        "wrong-typed numeric field is rejected",
			constraints: `{"hard_ceiling":"fifty"}`,
			wantOK:      false,
			wantErrLike: "must be a number",
		},
		{
			name:        "genuinely unknown key is only a warning",
			constraints: `{"some_future_key":"whatever"}`,
			wantOK:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &store.AgentProfile{
				Name:         "Test Agent",
				Slug:         "test-agent",
				SystemPrompt: "You are a test agent.",
				Constraints:  tt.constraints,
			}
			result := ValidateAgentConfig(agent)
			if result.OK() != tt.wantOK {
				t.Fatalf("ValidateAgentConfig(%s).OK() = %v, want %v (errors: %v)", tt.constraints, result.OK(), tt.wantOK, result.Errors)
			}
			if !tt.wantOK {
				found := false
				for _, e := range result.Errors {
					if strings.Contains(e, tt.wantErrLike) {
						found = true
					}
				}
				if !found {
					t.Errorf("expected an error containing %q, got: %v", tt.wantErrLike, result.Errors)
				}
			}
		})
	}
}
