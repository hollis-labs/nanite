package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsLocalPath(t *testing.T) {
	// Explicit path prefixes are always local.
	for _, p := range []string{"./foo", "../foo", "/tmp/foo", ".", ".."} {
		if !isLocalPath(p) {
			t.Errorf("isLocalPath(%q) = false, want true", p)
		}
	}
	// Bare tokens that don't exist as directories are treated as remote names.
	if isLocalPath("nonexistent-plugin-abc-xyz") {
		t.Error("isLocalPath of nonexistent bare name should be false")
	}
	// Bare token that names a real directory is local.
	dir := t.TempDir()
	oldwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	if err := os.Chdir(filepath.Dir(dir)); err != nil {
		t.Fatal(err)
	}
	if !isLocalPath(filepath.Base(dir)) {
		t.Errorf("isLocalPath of existing-dir bare name should be true")
	}
}

func TestCopyPluginDir(t *testing.T) {
	src := t.TempDir()
	// Realistic plugin layout plus directories that must be skipped.
	writeFile(t, filepath.Join(src, "plugin.yaml"), "schema_version: 1\nid: test\n")
	writeFile(t, filepath.Join(src, "main.go"), "package main")
	writeFile(t, filepath.Join(src, "ui/src/index.tsx"), "export {}")
	writeFile(t, filepath.Join(src, ".git/HEAD"), "ref: refs/heads/main")
	writeFile(t, filepath.Join(src, "node_modules/foo/package.json"), "{}")
	writeFile(t, filepath.Join(src, "dist/giphy"), "binary")

	dst := filepath.Join(t.TempDir(), "target")
	if err := copyPluginDir(src, dst); err != nil {
		t.Fatalf("copyPluginDir: %v", err)
	}

	for _, rel := range []string{"plugin.yaml", "main.go", "ui/src/index.tsx"} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("expected %s in target, got %v", rel, err)
		}
	}
	for _, rel := range []string{".git", "node_modules", "dist"} {
		if _, err := os.Stat(filepath.Join(dst, rel)); !os.IsNotExist(err) {
			t.Errorf("expected %s skipped, got err=%v", rel, err)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
