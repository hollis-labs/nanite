package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseMD_ValidSkill(t *testing.T) {
	data := []byte(`---
name: Go Lint
slug: go-lint
description: Run Go linting on the project
argument-hint: "[package-path]"
allowed-tools: [shell, dev_read]
model: haiku
effort: low
context: fork
tags: [code, lint, go]
---

Run golangci-lint on the specified package.

Current files:
` + "!`echo test-output`")

	def, err := ParseMD(data)
	if err != nil {
		t.Fatalf("ParseMD: %v", err)
	}

	if def.Name != "Go Lint" {
		t.Errorf("Name = %q, want %q", def.Name, "Go Lint")
	}
	if def.Slug != "go-lint" {
		t.Errorf("Slug = %q, want %q", def.Slug, "go-lint")
	}
	if def.Description != "Run Go linting on the project" {
		t.Errorf("Description = %q", def.Description)
	}
	if def.ArgumentHint != "[package-path]" {
		t.Errorf("ArgumentHint = %q", def.ArgumentHint)
	}
	if len(def.AllowedTools) != 2 || def.AllowedTools[0] != "shell" {
		t.Errorf("AllowedTools = %v", def.AllowedTools)
	}
	if def.Model != "haiku" {
		t.Errorf("Model = %q", def.Model)
	}
	if def.Effort != "low" {
		t.Errorf("Effort = %q", def.Effort)
	}
	if def.Context != "fork" {
		t.Errorf("Context = %q, want %q", def.Context, "fork")
	}
	if len(def.Tags) != 3 || def.Tags[2] != "go" {
		t.Errorf("Tags = %v", def.Tags)
	}
	if def.Prompt == "" {
		t.Error("Prompt should not be empty")
	}
}

func TestParseMD_DefaultContextInline(t *testing.T) {
	data := []byte(`---
name: Test
slug: test
description: Test skill
---
Do the thing.
`)
	def, err := ParseMD(data)
	if err != nil {
		t.Fatalf("ParseMD: %v", err)
	}
	if def.Context != "inline" {
		t.Errorf("Context = %q, want %q", def.Context, "inline")
	}
}

func TestParseMD_MissingSlug(t *testing.T) {
	data := []byte(`---
name: No Slug
description: Missing slug
---
Body.
`)
	// ParseMD no longer rejects empty slug — that's deferred to ParseMDFile
	// so the filename fallback can work.
	def, err := ParseMD(data)
	if err != nil {
		t.Fatalf("ParseMD should allow empty slug: %v", err)
	}
	if def.Slug != "" {
		t.Errorf("Slug = %q, want empty", def.Slug)
	}
}

func TestParseMD_NoFrontmatter(t *testing.T) {
	data := []byte("Just a plain markdown file.")
	_, err := ParseMD(data)
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestParseMDFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "my-skill.md")
	content := `---
name: My Skill
slug: my-skill
description: A test skill
---
Prompt body here.
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	def, err := ParseMDFile(path)
	if err != nil {
		t.Fatalf("ParseMDFile: %v", err)
	}
	if def.Slug != "my-skill" {
		t.Errorf("Slug = %q", def.Slug)
	}
	if def.SourceRef != path {
		t.Errorf("SourceRef = %q, want %q", def.SourceRef, path)
	}
}

func TestSlugFromFilename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"go-lint.md", "go-lint"},
		{"/path/to/dev-read.md", "dev-read"},
		{"skill.yaml.md", "skill.yaml"},
	}
	for _, tt := range tests {
		got := SlugFromFilename(tt.input)
		if got != tt.want {
			t.Errorf("SlugFromFilename(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
