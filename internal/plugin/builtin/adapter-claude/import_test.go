package adapterclaude

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/fsutil"
)

const reviewerSubagent = `---
name: code-reviewer
description: Expert code review specialist
tools: Read, Grep, Glob, Bash
model: sonnet
---
You are a code reviewer. Review carefully.
`

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := fsutil.AtomicWriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestImport_SingleSubagentFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code-reviewer.md")
	writeFile(t, path, reviewerSubagent)

	defs, err := New().Adapter().Import(path)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("want 1 definition, got %d", len(defs))
	}
	d := defs[0]
	if d.Slug != "code-reviewer" {
		t.Errorf("Slug = %q, want %q", d.Slug, "code-reviewer")
	}
	if d.Name != "code-reviewer" {
		t.Errorf("Name = %q", d.Name)
	}
	if d.Description != "Expert code review specialist" {
		t.Errorf("Description = %q", d.Description)
	}
	if d.SystemPrompt != "You are a code reviewer. Review carefully." {
		t.Errorf("SystemPrompt = %q — the body IS the agent", d.SystemPrompt)
	}
	if d.Source != AdapterName {
		t.Errorf("Source = %q, want %q — the adapter names the ecosystem it read FROM", d.Source, AdapterName)
	}
	if d.SourceRef != path {
		t.Errorf("SourceRef = %q, want %q", d.SourceRef, path)
	}
}

// TestImport_DropsForeignModelAndTools is the decided rule, tested.
//
// A Claude `model:` alias is not a Nanite model ID, and Claude tool names are
// not in Nanite's catalog — carrying either across produces a profile that
// looks configured and silently is not.
func TestImport_DropsForeignModelAndTools(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code-reviewer.md")
	writeFile(t, path, reviewerSubagent)

	defs, err := New().Adapter().Import(path)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if defs[0].Model != "" {
		t.Errorf("Model = %q, want blank — ResolveProviderAndModel re-resolves per request", defs[0].Model)
	}
	if len(defs[0].Tools) != 0 {
		t.Errorf("Tools = %v, want none — Claude tool names would produce grants that never resolve", defs[0].Tools)
	}
}

// TestImport_ToolsAsList covers the other shape Claude's tools field takes.
func TestImport_ToolsAsList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "listy.md")
	writeFile(t, path, "---\nname: listy\ntools:\n  - Read\n  - Bash\n---\nbody\n")

	defs, err := New().Adapter().Import(path)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(defs) != 1 || len(defs[0].Tools) != 0 {
		t.Errorf("a YAML-list tools field must be dropped the same way a string one is: %+v", defs)
	}
}

// TestImport_ProjectRootFindsNestedSubagents — a project root, and equally a
// Cairn-planted boot directory, holds its subagents at .claude/agents. This is
// the answer to CW-20260910-0012's open boot-directory question: no separate
// code path is needed.
func TestImport_ProjectRootFindsNestedSubagents(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, SubagentsDir, "code-reviewer.md"), reviewerSubagent)
	writeFile(t, filepath.Join(root, SubagentsDir, "planner.md"), "---\nname: planner\n---\nPlan things.\n")
	// A boot directory's own prose must not be mistaken for an agent.
	writeFile(t, filepath.Join(root, "CLAUDE.md"), "# Project\n\nNot an agent.\n")

	defs, err := New().Adapter().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("want 2 definitions from %s, got %d: %+v", SubagentsDir, len(defs), defs)
	}
	got := map[string]bool{defs[0].Slug: true, defs[1].Slug: true}
	if !got["code-reviewer"] || !got["planner"] {
		t.Errorf("slugs = %v", got)
	}
}

// TestImport_DirectoryOfSubagents — pointing straight at .claude/agents works
// too, so an operator does not have to know which level to name.
func TestImport_DirectoryOfSubagents(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "code-reviewer.md"), reviewerSubagent)

	defs, err := New().Adapter().Import(dir)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("want 1, got %d", len(defs))
	}
}

// TestImport_DeclinesWhatIsNotThisFormat — (nil, nil) is "not mine," which is
// what lets a registry try the next adapter. A markdown file with no
// frontmatter, and one whose frontmatter has no `name`, are both ordinary
// files that happen to live nearby.
func TestImport_DeclinesWhatIsNotThisFormat(t *testing.T) {
	for _, tc := range []struct{ name, file, body string }{
		{"no frontmatter", "notes.md", "# Notes\n\nJust prose.\n"},
		{"frontmatter without name", "meta.md", "---\ndescription: something\n---\nbody\n"},
		{"not markdown", "config.yaml", "name: nope\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tc.file)
			writeFile(t, path, tc.body)

			defs, err := New().Adapter().Import(path)
			if err != nil {
				t.Fatalf("Import returned an error; declining is (nil, nil): %v", err)
			}
			if defs != nil {
				t.Errorf("want nil (not my format), got %+v", defs)
			}
		})
	}
}

// TestImport_UnusableNameIsAnError — a name that cannot become a valid slug
// is this adapter's file and it is broken, so it is an error rather than a
// silent decline.
func TestImport_UnusableNameIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.md")
	writeFile(t, path, "---\nname: \"!!!\"\n---\nbody\n")

	if _, err := New().Adapter().Import(path); err == nil {
		t.Fatal("expected an error for a name that yields no usable slug")
	}
}

func TestSlugifyName(t *testing.T) {
	for in, want := range map[string]string{
		"code-reviewer":  "code-reviewer",
		"Code Reviewer":  "code-reviewer",
		"  Backend Dev ": "backend-dev",
		"API_Helper":     "api-helper",
	} {
		if got := slugifyName(in); got != want {
			t.Errorf("slugifyName(%q) = %q, want %q", in, got, want)
		}
		if err := agent.ValidateSlug(slugifyName(in)); err != nil {
			t.Errorf("slugifyName(%q) produced an invalid slug: %v", in, err)
		}
	}
}

// TestImport_NeverWritesToSource — the one-way rule at the adapter boundary.
func TestImport_NeverWritesToSource(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, SubagentsDir, "code-reviewer.md")
	writeFile(t, path, reviewerSubagent)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if _, err := New().Adapter().Import(root); err != nil {
		t.Fatalf("Import: %v", err)
	}

	after, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatalf("stat after: %v", statErr)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Error("Import modified the source file; import is one-way")
	}
}
