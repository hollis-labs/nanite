package selftools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func searchFixture(t *testing.T) workspace {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"README.md":          "Nanite reads files.\nSecond line.\n",
		"docs/guide.md":      "The gate decides.\nAsk the gate twice.\n",
		"docs/deep/notes.md": "nothing here\n",
		"src/main.go":        "package main // gate\n",
		".git/config":        "gate in a protected directory\n",
		"bin/blob":           "gate\x00binary",
	}
	for name, text := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(dir, "README.md"), filepath.Join(dir, "link.md")); err != nil {
		t.Fatal(err)
	}
	return workspace{roots: []string{dir}}
}

func TestFindFiles(t *testing.T) {
	w := searchFixture(t)
	for pattern, want := range map[string]string{
		"*.md":         "README.md\ndocs/deep/notes.md\ndocs/guide.md",
		"docs/*.md":    "docs/guide.md",
		"docs/**/*.md": "docs/deep/notes.md\ndocs/guide.md",
		"*.txt":        "No files match.",
	} {
		got, err := w.find(t.Context(), findArgs{Pattern: pattern})
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", pattern, got, err, want)
		}
	}
	got, err := w.find(t.Context(), findArgs{Pattern: "*.md", Path: "docs"})
	if err != nil || got != "deep/notes.md\nguide.md" {
		t.Errorf("scoped: got %q, %v", got, err)
	}
}

func TestSearchFiles(t *testing.T) {
	w := searchFixture(t)
	got, err := w.search(t.Context(), searchArgs{Pattern: "gate"})
	want := "docs/guide.md:1: The gate decides.\ndocs/guide.md:2: Ask the gate twice.\nsrc/main.go:1: package main // gate"
	if err != nil || got != want {
		t.Fatalf("got %q, %v", got, err)
	}
	got, err = w.search(t.Context(), searchArgs{Pattern: "(?i)GATE", Glob: "*.go"})
	if err != nil || got != "src/main.go:1: package main // gate" {
		t.Fatalf("glob: got %q, %v", got, err)
	}
	got, err = w.search(t.Context(), searchArgs{Pattern: "twice", Path: "docs/guide.md"})
	if err != nil || got != "guide.md:2: Ask the gate twice." {
		t.Fatalf("single file: got %q, %v", got, err)
	}
	if got, _ := w.search(t.Context(), searchArgs{Pattern: "absent"}); got != "No matches." {
		t.Fatalf("got %q", got)
	}
	if _, err := w.search(t.Context(), searchArgs{Pattern: "("}); err == nil {
		t.Fatal("invalid pattern accepted")
	}
	if _, err := w.search(t.Context(), searchArgs{Pattern: "x", Path: "../.."}); err == nil {
		t.Fatal("path outside the workspace accepted")
	}
}

func TestSearchStopsAtLimit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "many.txt"), []byte(strings.Repeat("hit\n", maxSearchResults+50)), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := (workspace{roots: []string{dir}}).search(t.Context(), searchArgs{Pattern: "hit"})
	if err != nil || strings.Count(got, "many.txt:") != maxSearchResults || !strings.Contains(got, "stopped at 200 results") {
		t.Fatalf("lines=%d err=%v tail=%q", strings.Count(got, "many.txt:"), err, got[max(0, len(got)-80):])
	}
}

// Globs match non-ASCII names: each UTF-8 sequence is quoted whole.
func TestFindFilesNonASCII(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"é.md", "日本/語.txt", "naïve-notes.md", "plain.md"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("accent\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w := workspace{roots: []string{dir}}
	for pattern, want := range map[string]string{
		"é.md":     "é.md",
		"日本/*":     "日本/語.txt",
		"語.txt":    "日本/語.txt",
		"na?ve-*":  "naïve-notes.md",
		"*é*":      "é.md",
		"**/*.txt": "日本/語.txt",
	} {
		got, err := w.find(t.Context(), findArgs{Pattern: pattern})
		if err != nil || got != want {
			t.Errorf("find %q: got %q, %v; want %q", pattern, got, err, want)
		}
	}
	if got, err := w.search(t.Context(), searchArgs{Pattern: "accent", Glob: "é.md"}); err != nil || got != "é.md:1: accent" {
		t.Errorf("search with non-ASCII glob: got %q, %v", got, err)
	}
}
