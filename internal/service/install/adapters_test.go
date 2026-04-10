package install

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestExtractAgentsFromConfig_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("nanite_version: 2.3.0\nagents: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := extractAgentsFromConfig(path)
	if err != nil {
		t.Fatalf("extractAgentsFromConfig: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestExtractAgentsFromConfig_Missing(t *testing.T) {
	got, err := extractAgentsFromConfig(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Errorf("missing file should return empty + nil err, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestExtractAgentsFromConfig_TwoAgents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `nanite_version: 2.3.0
agents:
  backend-dev:
    name: Backend Engineer
    description: Go service development
    roles: [backend, go]
  frontend-dev:
    name: Frontend Engineer
    description: React/Vue work
    roles: [frontend, react]
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := extractAgentsFromConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	// Order is map-iteration dependent — sort by slug for assertions.
	sort.Slice(got, func(i, j int) bool { return got[i].Slug < got[j].Slug })
	if got[0].Slug != "backend-dev" || got[0].Name != "Backend Engineer" || got[0].ID != "file-backend-dev" {
		t.Errorf("backend-dev mismatch: %+v", got[0])
	}
	if got[0].Description != "Go service development" {
		t.Errorf("backend-dev description = %q", got[0].Description)
	}
	if got[1].Slug != "frontend-dev" || got[1].Name != "Frontend Engineer" {
		t.Errorf("frontend-dev mismatch: %+v", got[1])
	}
}

func TestExtractAgentsFromConfig_NameDefaultsToSlug(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `nanite_version: 2.3.0
agents:
  bare:
    description: Has only a description
`
	os.WriteFile(path, []byte(body), 0o644)
	got, _ := extractAgentsFromConfig(path)
	if len(got) != 1 || got[0].Name != "bare" {
		t.Errorf("name should default to slug when missing: %+v", got)
	}
}

func TestSnapshotAdapterTargets(t *testing.T) {
	project := t.TempDir()
	archive := t.TempDir()

	mkFile := func(p, c string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Two files exist, two don't.
	mkFile(filepath.Join(project, "CLAUDE.md"), "claude content\n")
	mkFile(filepath.Join(project, "AGENTS.md"), "agents content\n")
	// GEMINI.md and OPENCODE.md don't exist.

	if err := snapshotAdapterTargets(project, archive); err != nil {
		t.Fatalf("snapshotAdapterTargets: %v", err)
	}

	// Existing files should be snapshotted.
	if data, err := os.ReadFile(filepath.Join(archive, "CLAUDE.md.pre-edit")); err != nil {
		t.Errorf("CLAUDE.md snapshot missing: %v", err)
	} else if string(data) != "claude content\n" {
		t.Errorf("CLAUDE.md snapshot content mismatch: %q", data)
	}
	if data, err := os.ReadFile(filepath.Join(archive, "AGENTS.md.pre-edit")); err != nil {
		t.Errorf("AGENTS.md snapshot missing: %v", err)
	} else if string(data) != "agents content\n" {
		t.Errorf("AGENTS.md snapshot content mismatch: %q", data)
	}
	// Missing files should NOT have snapshots.
	if _, err := os.Stat(filepath.Join(archive, "GEMINI.md.pre-edit")); !os.IsNotExist(err) {
		t.Errorf("GEMINI.md.pre-edit should not exist (source was missing)")
	}
	if _, err := os.Stat(filepath.Join(archive, "OPENCODE.md.pre-edit")); !os.IsNotExist(err) {
		t.Errorf("OPENCODE.md.pre-edit should not exist (source was missing)")
	}
}

func TestSyncAdaptersForProject_WritesAllFourCLIFiles(t *testing.T) {
	project := t.TempDir()

	// Set up .nanite/config.yaml with two agents so the adapters have
	// content to write.
	if err := os.MkdirAll(filepath.Join(project, ".nanite"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `nanite_version: 2.3.0
agents:
  backend-dev:
    name: Backend Engineer
    description: Go service development
  frontend-dev:
    name: Frontend Engineer
    description: React work
`
	if err := os.WriteFile(filepath.Join(project, ".nanite", "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := syncAdaptersForProject(project, []string{"claude", "codex", "gemini", "opencode"}); err != nil {
		t.Fatalf("syncAdaptersForProject: %v", err)
	}

	// All four CLI files should now exist with managed sections containing
	// both agent names.
	for _, name := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md", "OPENCODE.md"} {
		path := filepath.Join(project, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s missing after adapter sync: %v", name, err)
			continue
		}
		content := string(data)
		if !strings.Contains(content, "<!-- nanite:start -->") {
			t.Errorf("%s missing nanite:start marker:\n%s", name, content)
		}
		if !strings.Contains(content, "<!-- nanite:end -->") {
			t.Errorf("%s missing nanite:end marker:\n%s", name, content)
		}
		if !strings.Contains(content, "Backend Engineer") {
			t.Errorf("%s missing Backend Engineer:\n%s", name, content)
		}
		if !strings.Contains(content, "Frontend Engineer") {
			t.Errorf("%s missing Frontend Engineer:\n%s", name, content)
		}
	}
}

func TestSyncAdaptersForProject_EmptyAgentsListWritesPlaceholder(t *testing.T) {
	project := t.TempDir()

	// .nanite/config.yaml with no agents.
	if err := os.MkdirAll(filepath.Join(project, ".nanite"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(project, ".nanite", "config.yaml"),
		[]byte("nanite_version: 2.3.0\nagents: {}\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	if err := syncAdaptersForProject(project, []string{"claude", "codex", "gemini", "opencode"}); err != nil {
		t.Fatalf("syncAdaptersForProject: %v", err)
	}

	// All CLI files should exist with placeholder content (adapters now write
	// a managed section even when the agents list is empty).
	for _, name := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md", "OPENCODE.md"} {
		data, err := os.ReadFile(filepath.Join(project, name))
		if err != nil {
			t.Errorf("%s should exist after empty-agents sync: %v", name, err)
			continue
		}
		got := string(data)
		if !strings.Contains(got, "<!-- nanite:start -->") {
			t.Errorf("%s missing nanite:start marker", name)
		}
		if !strings.Contains(got, "No agents configured") {
			t.Errorf("%s missing placeholder text", name)
		}
	}
}

func TestSyncAdaptersForProject_FilterAllowed(t *testing.T) {
	dir := t.TempDir()
	naniteDir := filepath.Join(dir, ".nanite")
	if err := os.MkdirAll(naniteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a config with one agent so adapters produce non-placeholder content.
	cfg := `nanite_version: 2.3.0
agents:
  frontend:
    name: Frontend Developer
    description: React work
`
	if err := os.WriteFile(filepath.Join(naniteDir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run with only claude allowed.
	if err := syncAdaptersForProject(dir, []string{"claude"}); err != nil {
		t.Fatalf("syncAdaptersForProject: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Errorf("CLAUDE.md should exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Errorf("AGENTS.md should NOT exist (codex not allowed), stat: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "GEMINI.md")); !os.IsNotExist(err) {
		t.Errorf("GEMINI.md should NOT exist (gemini not allowed), stat: %v", err)
	}
}

func TestSyncAdaptersForProject_EmptyAllowedListWritesNoFiles(t *testing.T) {
	dir := t.TempDir()
	naniteDir := filepath.Join(dir, ".nanite")
	if err := os.MkdirAll(naniteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(naniteDir, "config.yaml"), []byte("agents: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := syncAdaptersForProject(dir, []string{}); err != nil {
		t.Fatalf("syncAdaptersForProject: %v", err)
	}
	for _, name := range adapterTargetFiles {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s should NOT exist with empty allowed list", name)
		}
	}
}

func TestNewBuiltinAdapterRegistry_RegistersFiveAdapters(t *testing.T) {
	reg := newBuiltinAdapterRegistry()
	adapters := reg.Adapters()
	if len(adapters) != 5 {
		t.Errorf("len = %d, want 5", len(adapters))
	}

	// Verify expected names are present.
	names := make(map[string]bool)
	for _, a := range adapters {
		names[a.Name()] = true
	}
	for _, want := range []string{"claude", "codex", "gemini", "opencode", "nanite-native"} {
		if !names[want] {
			t.Errorf("missing adapter: %q (got %v)", want, names)
		}
	}
}
