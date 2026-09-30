package service

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestValidateProjectRepoPathPolicy moved here from internal/api with the
// rule it tests; the HTTP tests for the rule stay in internal/api.
func TestValidateProjectRepoPathPolicy(t *testing.T) {
	// This package's tests point HOME at a path that may not exist; give the
	// rule a real home to judge against (under the OS temp dir, which on
	// macOS sits below /var — the AD-27 home-under-/var case).
	t.Setenv("HOME", t.TempDir())
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	homeChild, err := os.MkdirTemp(home, ".nanite-repo-policy-")
	if err != nil {
		t.Fatalf("MkdirTemp under home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(homeChild) })

	if _, err := ValidateProjectRepoPath(""); err != nil {
		t.Fatalf("empty repo_path should remain allowed: %v", err)
	}
	if _, err := ValidateProjectRepoPath(homeChild); err != nil {
		t.Fatalf("home subdirectory should be allowed: %v", err)
	}
	for name, path := range map[string]string{
		"filesystem-root": string(filepath.Separator),
		"home-itself":     home,
		"missing":         filepath.Join(t.TempDir(), "missing"),
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := ValidateProjectRepoPath(path); err == nil {
				t.Fatalf("ValidateProjectRepoPath(%q) = %q, want rejection", path, got)
			}
		})
	}

	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := ValidateProjectRepoPath(file); err == nil {
		t.Fatal("regular file accepted as repo_path")
	}

	if runtime.GOOS != "windows" {
		for _, path := range []string{"/etc", "/usr", "/var", "/System"} {
			if got, err := ValidateProjectRepoPath(path); err == nil {
				t.Fatalf("ValidateProjectRepoPath(%q) = %q, want system-tree rejection", path, got)
			}
		}
	}
}
