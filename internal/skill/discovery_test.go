package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkillFile(t *testing.T, dir, filename, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscover_ProjectSkills(t *testing.T) {
	root := t.TempDir()
	skillsDir := filepath.Join(root, ".nanite", "skills")
	writeSkillFile(t, skillsDir, "lint.md", `---
name: Lint
slug: lint
description: Run linter
---
Lint the code.
`)
	writeSkillFile(t, skillsDir, "test.md", `---
name: Test
slug: test
description: Run tests
---
Run the tests.
`)

	defs, err := Discover(DiscoverOptions{WorkingDir: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("got %d defs, want 2", len(defs))
	}
	for _, d := range defs {
		if d.Source != "project" {
			t.Errorf("skill %s source = %q, want %q", d.Slug, d.Source, "project")
		}
	}
}

func TestDiscover_PriorityOrder(t *testing.T) {
	root := t.TempDir()

	// Same slug in both project and agentrc — project should win.
	projectDir := filepath.Join(root, ".nanite", "skills")
	agentrcDir := filepath.Join(root, ".agentrc", "skills")

	writeSkillFile(t, projectDir, "lint.md", `---
name: Project Lint
slug: lint
description: Project linter
---
Project version.
`)
	writeSkillFile(t, agentrcDir, "lint.md", `---
name: Agentrc Lint
slug: lint
description: Agentrc linter
---
Agentrc version.
`)

	defs, err := Discover(DiscoverOptions{WorkingDir: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("got %d defs, want 1 (dedup by slug)", len(defs))
	}
	if defs[0].Name != "Project Lint" {
		t.Errorf("expected project version, got %q", defs[0].Name)
	}
	if defs[0].Source != "project" {
		t.Errorf("source = %q, want %q", defs[0].Source, "project")
	}
}

func TestDiscover_PluginSkills(t *testing.T) {
	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins")
	pluginSkillsDir := filepath.Join(pluginsDir, "my-plugin", "skills")

	writeSkillFile(t, pluginSkillsDir, "deploy.md", `---
name: Deploy
slug: deploy
description: Deploy plugin skill
---
Deploy it.
`)

	defs, err := Discover(DiscoverOptions{
		WorkingDir: root,
		PluginsDir: pluginsDir,
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("got %d defs, want 1", len(defs))
	}
	if defs[0].Source != "plugin" {
		t.Errorf("source = %q, want %q", defs[0].Source, "plugin")
	}
}

func TestDiscover_MissingDirsOK(t *testing.T) {
	root := t.TempDir()
	// No skill directories exist — should return empty, no error.
	defs, err := Discover(DiscoverOptions{WorkingDir: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(defs) != 0 {
		t.Errorf("got %d defs, want 0", len(defs))
	}
}
