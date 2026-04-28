package permission

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRule_matchToolName(t *testing.T) {
	r := Rule{Tool: "shell", Behavior: DecisionDeny}
	if !r.Matches("shell", nil) {
		t.Error("should match exact tool name")
	}
	if r.Matches("dev_edit", nil) {
		t.Error("should not match different tool")
	}
}

func TestRule_matchToolGlob(t *testing.T) {
	// Uniform names post ADR-002 — `dev_*` glob still matches the dev_*
	// suite without the legacy `mcp__dev__` prefix.
	r := Rule{Tool: "dev_*", Behavior: DecisionAllow}
	if !r.Matches("dev_edit", nil) {
		t.Error("should match glob")
	}
	if !r.Matches("dev_read", nil) {
		t.Error("should match glob")
	}
	if r.Matches("task_create", nil) {
		t.Error("should not match different prefix")
	}
}

func TestRule_matchInputPath(t *testing.T) {
	r := Rule{Tool: "dev_edit", Pattern: "/src/**", Behavior: DecisionAllow}
	if !r.Matches("dev_edit", map[string]any{"path": "/src/main.go"}) {
		t.Error("should match path in /src/")
	}
	if r.Matches("dev_edit", map[string]any{"path": "/config/app.yaml"}) {
		t.Error("should not match path outside /src/")
	}
}

func TestRule_matchInputCommand(t *testing.T) {
	r := Rule{Tool: "shell", Pattern: "rm -rf", Behavior: DecisionDeny}
	if !r.Matches("shell", map[string]any{"command": "rm -rf /tmp/stuff"}) {
		t.Error("should match command containing pattern")
	}
	if r.Matches("shell", map[string]any{"command": "ls -la"}) {
		t.Error("should not match unrelated command")
	}
}

func TestLoadRulesFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "permissions.yaml")

	content := `permissions:
  mode: default
  rules:
    - tool: "shell"
      pattern: "rm -rf"
      behavior: deny
    - tool: "dev_edit"
      pattern: "/src/**"
      behavior: allow
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	rs, err := LoadRulesFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Mode != ModeDefault {
		t.Errorf("expected default mode, got %s", rs.Mode)
	}
	if len(rs.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rs.Rules))
	}
	if rs.Rules[0].Tool != "shell" {
		t.Errorf("first rule tool: got %s", rs.Rules[0].Tool)
	}
	if rs.Rules[0].Source != path {
		t.Errorf("rule source not tagged")
	}
}

func TestSaveAndLoadRules(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".nanite", "permissions.yaml")

	rs := &RuleSet{
		Mode: ModeAcceptEdits,
		Rules: []Rule{
			{Tool: "shell", Behavior: DecisionAsk},
		},
	}

	if err := SaveRulesToFile(path, rs); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadRulesFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Mode != ModeAcceptEdits {
		t.Errorf("mode: got %s", loaded.Mode)
	}
	if len(loaded.Rules) != 1 {
		t.Fatalf("rules: got %d", len(loaded.Rules))
	}
}

func TestMergeRuleSets(t *testing.T) {
	user := &RuleSet{
		Mode: ModeDefault,
		Rules: []Rule{
			{Tool: "shell", Behavior: DecisionAsk},
		},
	}
	project := &RuleSet{
		Mode: ModeAcceptEdits,
		Rules: []Rule{
			{Tool: "dev_edit", Behavior: DecisionAllow},
		},
	}

	// User has higher priority (first arg).
	merged := MergeRuleSets(user, project)
	if merged.Mode != ModeDefault {
		t.Errorf("expected user mode to win, got %s", merged.Mode)
	}
	if len(merged.Rules) != 2 {
		t.Fatalf("expected 2 merged rules, got %d", len(merged.Rules))
	}
}

func TestEvaluate_noRules(t *testing.T) {
	rs := &RuleSet{}
	result := rs.Evaluate("anything", nil)
	if result != nil {
		t.Error("empty ruleset should return nil")
	}
}
