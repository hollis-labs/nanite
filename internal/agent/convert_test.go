package agent

import (
	"encoding/json"
	"testing"
)

func TestDefinition_ToProfile(t *testing.T) {
	def := &Definition{
		Name:           "Code Agent",
		Slug:           "code",
		Description:    "A coding agent",
		Icon:           "code",
		Avatar:         "https://example.com/code.png",
		Model:          "claude-sonnet-4-20250514",
		Tools:          []string{"read", "write", "bash"},
		Skills:         []string{"go-build"},
		MCPServers:     []string{"engine"},
		Tags:           []string{"backend"},
		Directories:    []string{"./src"},
		PermissionMode: "yolo",
		SystemPrompt:   "You are a code agent.",
		Source:         "project",
		SourceRef:      "/path/to/code.md",
	}

	p := def.ToProfile()

	if p.ID != "file-code" {
		t.Errorf("ID = %q, want %q", p.ID, "file-code")
	}
	if p.Name != "Code Agent" {
		t.Errorf("Name = %q", p.Name)
	}
	if p.Slug != "code" {
		t.Errorf("Slug = %q", p.Slug)
	}
	if p.SystemPrompt != "You are a code agent." {
		t.Errorf("SystemPrompt = %q", p.SystemPrompt)
	}
	if p.DefaultModel != "claude-sonnet-4-20250514" {
		t.Errorf("DefaultModel = %q", p.DefaultModel)
	}
	if !p.CanExecute {
		t.Error("CanExecute should be true for yolo mode")
	}
	if p.Source != "project" {
		t.Errorf("Source = %q", p.Source)
	}
	if p.Status != "active" {
		t.Errorf("Status = %q", p.Status)
	}

	// Check JSON array fields.
	var tools []string
	if err := json.Unmarshal([]byte(p.Tools), &tools); err != nil {
		t.Fatalf("Tools JSON: %v", err)
	}
	if len(tools) != 3 {
		t.Errorf("Tools = %v", tools)
	}

	var tags []string
	if err := json.Unmarshal([]byte(p.Tags), &tags); err != nil {
		t.Fatalf("Tags JSON: %v", err)
	}
	if len(tags) != 1 || tags[0] != "backend" {
		t.Errorf("Tags = %v", tags)
	}

	var servers []string
	if err := json.Unmarshal([]byte(p.MCPServers), &servers); err != nil {
		t.Fatalf("MCPServers JSON: %v", err)
	}
	if len(servers) != 1 || servers[0] != "engine" {
		t.Errorf("MCPServers = %v", servers)
	}

	// Phase 0 item 21 ("Cut Modes, in full") deleted Definition.Modes /
	// ModeDefinition — the legacy agent_profiles.modes column is always
	// "[]" now (see ToProfile's doc comment).
	if p.Modes != "[]" {
		t.Errorf("Modes = %q, want \"[]\" (Legacy Agent Mode was cut)", p.Modes)
	}

	// An unset (zero-value) Constraints still serializes to "{}" — see
	// TestDefinition_ToProfile_Constraints for the populated case.
	if p.Constraints != "{}" {
		t.Errorf("Constraints = %q, want %q", p.Constraints, "{}")
	}

	// Check tool permissions derived from tools.
	var tp map[string]any
	if err := json.Unmarshal([]byte(p.ToolPermissions), &tp); err != nil {
		t.Fatalf("ToolPermissions JSON: %v", err)
	}
	if tp["allow_list"] == nil {
		t.Error("ToolPermissions missing allow_list")
	}
}

// TestDefinition_ToProfile_Constraints verifies a populated `constraints:`
// frontmatter block actually survives into store.AgentProfile.Constraints.
// Found broken 2026-08-16: AgentConstraints was an empty struct (a
// leftover from CW-20260512-0123 removing the old numeric-only fields),
// so any real field added since -- including CW-20260520-0001's
// SubagentCompletionPolicy -- silently parsed to nothing here, no matter
// what a file's frontmatter declared.
func TestDefinition_ToProfile_Constraints(t *testing.T) {
	def := &Definition{
		Name:         "Orchestrator-shaped",
		Slug:         "orchestrator-shaped",
		SystemPrompt: "You dispatch work.",
		Source:       "project",
		Constraints: AgentConstraints{
			HardCeiling:              150,
			SubagentCompletionPolicy: "auto_summarize",
			MessageWakePolicy:        "render_and_wait",
		},
	}

	p := def.ToProfile()

	var c map[string]any
	if err := json.Unmarshal([]byte(p.Constraints), &c); err != nil {
		t.Fatalf("Constraints JSON: %v (raw: %q)", err, p.Constraints)
	}
	if got, want := c["subagent_completion_policy"], "auto_summarize"; got != want {
		t.Errorf("constraints.subagent_completion_policy = %v, want %q", got, want)
	}
	// Phase 0 item 12 (2026-08-18) removed MaxTurns from AgentConstraints
	// (soft/telemetry-only, never gated the loop). hard_ceiling now stands
	// in as the numeric-field regression check this test originally used
	// max_turns for.
	if got, want := c["hard_ceiling"], float64(150); got != want {
		t.Errorf("constraints.hard_ceiling = %v, want %v", got, want)
	}
	// MessageWakePolicy (CW-20260816-0065) was missing from this struct
	// until the code-review pass that added it — a `constraints:
	// messageWakePolicy: ...` frontmatter key was silently dropped by
	// yaml.Unmarshal before that fix. Verify it now survives into the
	// marshaled JSON that internal/chat.ParseAgentConstraints reads back.
	if got, want := c["message_wake_policy"], "render_and_wait"; got != want {
		t.Errorf("constraints.message_wake_policy = %v, want %q", got, want)
	}
}

func TestDefinition_ToProfile_Minimal(t *testing.T) {
	def := &Definition{
		Name:         "Minimal",
		Slug:         "minimal",
		SystemPrompt: "Hello.",
		Source:       "builtin",
	}

	p := def.ToProfile()

	if p.ID != "file-minimal" {
		t.Errorf("ID = %q", p.ID)
	}
	if p.Tools != "[]" {
		t.Errorf("Tools = %q, want %q", p.Tools, "[]")
	}
	if p.Tags != "[]" {
		t.Errorf("Tags = %q, want %q", p.Tags, "[]")
	}
	if p.Constraints != "{}" {
		t.Errorf("Constraints = %q, want %q", p.Constraints, "{}")
	}
	if p.ToolPermissions != "{}" {
		t.Errorf("ToolPermissions = %q, want %q", p.ToolPermissions, "{}")
	}
	if p.CanExecute {
		t.Error("CanExecute should be false for non-yolo")
	}
}

// Phase 0 item 21 ("Cut Modes, in full") deleted Definition.ToModes() along
// with store.AgentMode / ModeDefinition — there is no more inline
// agent-file mode concept to convert. TestDefinition_ToModes was removed
// with it.

func TestIsFileBasedID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"file-code", true},
		{"file-default", true},
		{"file-", false},
		{"mentat-001", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsFileBasedID(tt.id); got != tt.want {
			t.Errorf("IsFileBasedID(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestSlugFromFileID(t *testing.T) {
	if got := SlugFromFileID("file-code"); got != "code" {
		t.Errorf("SlugFromFileID(file-code) = %q", got)
	}
	if got := SlugFromFileID("mentat-001"); got != "" {
		t.Errorf("SlugFromFileID(mentat-001) = %q, want empty", got)
	}
}
