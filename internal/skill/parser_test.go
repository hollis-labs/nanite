package skill

import (
	"os"
	"path/filepath"
	"strings"
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

func TestParsePackageDir_FullPackage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "SKILL.md", `---
name: Package Skill
slug: package-skill
description: A full package with scripts, references, assets, parameters, and dependencies.
context: inline
scripts:
  - scripts/run.sh
references:
  - references/notes.md
assets:
  - assets/logo.txt
parameters:
  - name: target
    description: what to target
    required: true
  - name: verbose
    resolver_slot: verbosity
dependencies:
  - other-skill
---
Body content.
`)
	writeFile(t, dir, "scripts/run.sh", "#!/bin/sh\necho hi\n")
	writeFile(t, dir, "references/notes.md", "# notes\n")
	writeFile(t, dir, "assets/logo.txt", "logo\n")

	def, files, err := ParsePackageDir(dir)
	if err != nil {
		t.Fatalf("ParsePackageDir: %v", err)
	}

	if def.Slug != "package-skill" {
		t.Errorf("Slug = %q, want %q", def.Slug, "package-skill")
	}
	if def.SourceRef != dir {
		t.Errorf("SourceRef = %q, want %q", def.SourceRef, dir)
	}
	if len(def.Scripts) != 1 || def.Scripts[0] != "scripts/run.sh" {
		t.Errorf("Scripts = %v", def.Scripts)
	}
	if len(def.References) != 1 || def.References[0] != "references/notes.md" {
		t.Errorf("References = %v", def.References)
	}
	if len(def.Assets) != 1 || def.Assets[0] != "assets/logo.txt" {
		t.Errorf("Assets = %v", def.Assets)
	}
	if len(def.Parameters) != 2 {
		t.Fatalf("Parameters = %v, want 2 entries", def.Parameters)
	}
	if def.Parameters[0].Name != "target" || !def.Parameters[0].Required {
		t.Errorf("Parameters[0] = %+v", def.Parameters[0])
	}
	if def.Parameters[1].ResolverSlot != "verbosity" {
		t.Errorf("Parameters[1].ResolverSlot = %q, want %q", def.Parameters[1].ResolverSlot, "verbosity")
	}
	if len(def.Dependencies) != 1 || def.Dependencies[0] != "other-skill" {
		t.Errorf("Dependencies = %v", def.Dependencies)
	}

	for _, want := range []string{"SKILL.md", "scripts/run.sh", "references/notes.md", "assets/logo.txt"} {
		if _, ok := files[want]; !ok {
			t.Errorf("PackageFiles missing %q, got keys %v", want, keysOf(files))
		}
	}
}

func TestParsePackageDir_SlugFallsBackToDirName(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "my-package")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, pkgDir, "SKILL.md", `---
name: No Slug Package
description: Frontmatter omits slug entirely.
---
Body.
`)

	def, _, err := ParsePackageDir(pkgDir)
	if err != nil {
		t.Fatalf("ParsePackageDir: %v", err)
	}
	if def.Slug != "my-package" {
		t.Errorf("Slug = %q, want %q (directory-name fallback)", def.Slug, "my-package")
	}
}

func TestParsePackageDir_MissingSkillFile(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := ParsePackageDir(dir); err == nil {
		t.Fatal("expected an error for a package directory with no SKILL.md")
	}
}

func TestParsePackageDir_RejectsSymlinkedSkillFileOutsidePackage(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside-skill.md")
	writeFile(t, filepath.Dir(outside), filepath.Base(outside), `---
name: Outside Skill
slug: outside-skill
description: must not be imported through a package symlink
---
outside secret
`)
	if err := os.Symlink(outside, filepath.Join(dir, skillFileName)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if def, files, err := ParsePackageDir(dir); err == nil {
		t.Fatalf("ParsePackageDir followed an escaping SKILL.md symlink: def=%+v files=%v", def, keysOf(files))
	}
}

func TestParsePackageDir_RejectsSymlinkedPackageFileOutsidePackage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, skillFileName, `---
name: Safe Package
slug: safe-package
description: package with a hostile source symlink
---
safe body
`)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("outside asset secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "asset-link.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if def, files, err := ParsePackageDir(dir); err == nil {
		t.Fatalf("ParsePackageDir followed an escaping package-file symlink: def=%+v files=%v", def, keysOf(files))
	}
}

func TestReadPackageFile_RejectsEscapingPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outsideDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("outside secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "nested", "secret.txt"), []byte("nested outside secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outsideDir, "secret.txt"), filepath.Join(root, "escape.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(root, "escape-dir")); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}

	outsideRel, err := filepath.Rel(root, filepath.Join(outsideDir, "secret.txt"))
	if err != nil {
		t.Fatal(err)
	}
	dotdotMid := "sub" + string(filepath.Separator) + ".." + string(filepath.Separator) + ".." + string(filepath.Separator) + outsideRel
	for _, rel := range []string{
		dotdotMid,
		"escape.txt",
		filepath.Join("escape-dir", "nested", "secret.txt"),
	} {
		t.Run(strings.ReplaceAll(rel, string(filepath.Separator), "_"), func(t *testing.T) {
			if data, err := readPackageFile(root, rel); err == nil {
				t.Fatalf("readPackageFile(%q) read %q; want confinement error", rel, data)
			}
		})
	}
}

func writeFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", relPath, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

func keysOf(m PackageFiles) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
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
