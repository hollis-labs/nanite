package definition

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/agent/agentdir"
	rules "github.com/hollis-labs/libs/security/policy/permission"

	"github.com/hollis-labs/nanite/internal/authored"
)

func TestOverridesApply(t *testing.T) {
	skills := t.TempDir()
	for _, name := range []string{"shared", "extra"} {
		if err := os.MkdirAll(filepath.Join(skills, name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skills, name, "SKILL.md"), []byte("---\ndescription: plugin "+name+"\n---\nbody\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	base := func() Definition {
		return Definition{
			Snapshot: agentdir.Snapshot{
				MCP:    agentdir.MCPConfig{Servers: map[string]agentdir.MCPServer{"a": {Command: "authored-a"}, "b": {Command: "authored-b"}}},
				Skills: []agentdir.SkillRef{{Name: "shared", Description: "authored"}},
			},
			Runtime: agentdir.Runtime{Model: "authored-model"},
			Use:     map[string]authored.Selection{authored.UseChat: {Provider: "p", Model: "chat-model"}},
			Skills:  map[string]agentdir.SkillRef{"shared": {Name: "shared", Description: "authored"}},
			Context: []Context{{Name: "authored", Text: "a"}},
			Rules:   []byte("permissions:\n  mode: plan\n  rules:\n    - tool: bash\n      behavior: ask\n"),
		}
	}
	def := base()
	if err := (Overrides{}).Apply(&def); err != nil || def.Runtime.Model != "authored-model" || len(def.Snapshot.MCP.Servers) != 2 {
		t.Fatalf("zero overrides changed it: %+v %v", def, err)
	}
	o := Overrides{
		Model: "flag-model", PermissionMode: "yolo",
		MCP:       agentdir.MCPConfig{Servers: map[string]agentdir.MCPServer{"b": {Command: "flag-b"}, "c": {Command: "flag-c"}}},
		Context:   []Context{{Name: "host", Text: "h"}},
		Rules:     []rules.Rule{{Tool: "mcp__exo__*", Behavior: rules.DecisionAllow}},
		SkillDirs: []string{skills},
	}
	if err := o.Apply(&def); err != nil {
		t.Fatal(err)
	}
	if def.Runtime.Model != "flag-model" || def.Use[authored.UseChat].Model != "flag-model" || def.Snapshot.Profile.Permissions.Mode != "yolo" {
		t.Fatalf("model/mode: %+v", def)
	}
	s := def.Snapshot.MCP.Servers
	if len(s) != 3 || s["a"].Command != "authored-a" || s["b"].Command != "flag-b" || s["c"].Command != "flag-c" {
		t.Fatalf("mcp %+v", s)
	}
	if len(def.Context) != 2 || def.Context[1].Name != "host" {
		t.Fatalf("context %+v", def.Context)
	}
	if def.Skills["shared"].Description != "authored" || def.Skills["extra"].Description != "plugin extra" || def.GlobalSkills["extra"].Name != "extra" {
		t.Fatalf("skills %+v", def.Skills)
	}
	if r := string(def.Rules); !strings.Contains(r, "mcp__exo__*") || !strings.Contains(r, "bash") || !strings.Contains(r, "mode: plan") {
		t.Fatalf("rules %s", r)
	}
	// Strict keeps only the host's servers; a bad rule is refused.
	def = base()
	if err := (Overrides{StrictMCP: true, MCP: o.MCP}).Apply(&def); err != nil || len(def.Snapshot.MCP.Servers) != 2 || def.Snapshot.MCP.Servers["a"].Command != "" {
		t.Fatalf("strict %+v %v", def.Snapshot.MCP, err)
	}
	def = base()
	if err := (Overrides{Rules: []rules.Rule{{Tool: "x", Behavior: "sometimes"}}}).Apply(&def); err == nil {
		t.Fatal("bad rule accepted")
	}
}
