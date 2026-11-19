package definition

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsAndSkillPrecedence(t *testing.T) {
	root := t.TempDir()
	const agent = `---
schema_version: "2"
definition_id: def:definition-test
revision: "1"
name: definition-test
description: Test fixture.
behavior:
  purpose: Test definition loading.
requirements: {}
harness_profile:
  permissions:
    profile: default
continuity:
  mode: ephemeral
---
Instructions.
`
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "agent.md"), agent)
	write(filepath.Join(root, "host.yaml"), "kind: embedded\nlifetime: application\napplication_id: test\n")
	write(filepath.Join(root, "skills", "shared", "SKILL.md"), "---\nname: shared\ndescription: local\n---\nlocal body")
	global := t.TempDir()
	write(filepath.Join(global, "shared", "SKILL.md"), "---\nname: shared\ndescription: global\n---\nglobal body")
	write(filepath.Join(global, "extra", "SKILL.md"), "---\nname: extra\ndescription: extra\n---\nextra body")
	for _, tc := range []struct {
		name, path, model, wantModel string
		total                        int
	}{
		{"defaults and merge", global, "", "claude-sonnet-5-5", 2},
		{"global disabled", "-", "custom-model", "custom-model", 1},
		{"global missing", filepath.Join(global, "missing"), "", "claude-sonnet-5-5", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NANITE_DEFAULT_MODEL", tc.model)
			def, err := Load(root, tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if def.Runtime.Provider != "anthropic" || def.Runtime.Model != tc.wantModel || def.Runtime.MaxSteps != 50 || def.Runtime.ToolConcurrency != 4 {
				t.Fatalf("runtime defaults: %+v", def.Runtime)
			}
			if len(def.Skills) != tc.total || def.Skills["shared"].Description != "local" {
				t.Fatalf("skills: %+v", def.Skills)
			}
		})
	}
}
