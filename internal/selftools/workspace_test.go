package selftools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceConfinement(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "workspace")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	w := workspace{roots: []string{root}}
	outside := filepath.Join(base, "outside.txt")
	for path, text := range map[string]string{
		outside:                            "outside",
		filepath.Join(root, "visible.txt"): "original",
		filepath.Join(root, ".netrc"):      "protected",
	} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	private := filepath.Join(root, ".nanite")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(private, "secret.txt"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(base, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]string{
		"../outside.txt":     "outside the workspace",
		outside:              "outside the workspace",
		"link/outside.txt":   "symbolic link",
		".netrc":             "protected workspace path",
		".nanite/secret.txt": "protected workspace path",
	} {
		t.Run(path, func(t *testing.T) {
			ctx := t.Context()
			_, readErr := w.read(ctx, pathArgs{Path: path})
			_, writeErr := w.write(ctx, writeArgs{Path: path, Content: "replacement"})
			_, editErr := w.edit(ctx, editArgs{Path: path, Old: "outside", New: "replacement"})
			for _, err := range []error{readErr, writeErr, editErr} {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v, want %q", err, want)
				}
			}
		})
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "outside" { // #nosec G304 -- This path is under the test temporary directory.
		t.Fatalf("outside file = %q, %v", got, err)
	}
	if got, err := w.list(t.Context(), pathArgs{Path: "."}); err != nil || got != "visible.txt" {
		t.Fatalf("list = %q, %v", got, err)
	}
	if _, err := w.edit(t.Context(), editArgs{Path: "visible.txt", Old: "original", New: "edited"}); err != nil {
		t.Fatal(err)
	}
	if got, err := w.read(t.Context(), pathArgs{Path: filepath.Join(root, "visible.txt")}); err != nil || got != "edited" {
		t.Fatalf("read = %q, %v", got, err)
	}
}

// Every protected name, in any letter case and at any depth, is refused by
// read, write and edit and is invisible to list, search and find.
func TestEveryProtectedNameIsRefused(t *testing.T) {
	names := []string{".nanite", ".ssh", ".gnupg", ".aws", ".kube", ".docker", ".netrc", ".codex", ".config", ".local",
		".bash_profile", ".bashrc", ".zshrc", ".zprofile", ".profile", ".git", ".claude", ".claude.json", ".claude-backup",
		// Case variants alias the protected directory on case-insensitive filesystems.
		".GIT", ".Ssh", ".AWS", ".Nanite", ".CLAUDE", ".NetRC"}
	root := t.TempDir()
	w := workspace{roots: []string{root}}
	if err := os.WriteFile(filepath.Join(root, "visible.txt"), []byte("ordinary\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		for _, path := range []string{filepath.Join(name, "secret.txt"), filepath.Join("sub", name, "secret.txt")} {
			full := filepath.Join(root, path)
			if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte("CANARY\n"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Run(path, func(t *testing.T) {
				ctx := t.Context()
				_, readErr := w.read(ctx, pathArgs{Path: path})
				_, writeErr := w.write(ctx, writeArgs{Path: path, Content: "replaced"})
				_, editErr := w.edit(ctx, editArgs{Path: path, Old: "CANARY", New: "replaced"})
				_, newErr := w.write(ctx, writeArgs{Path: filepath.Join(filepath.Dir(path), "new.txt"), Content: "planted"})
				for op, err := range map[string]error{"read": readErr, "write": writeErr, "edit": editErr, "create": newErr} {
					if err == nil || !strings.Contains(err.Error(), "protected workspace path") {
						t.Errorf("%s: %v", op, err)
					}
				}
				if got, err := os.ReadFile(full); err != nil || string(got) != "CANARY\n" { // #nosec G304 -- under t.TempDir.
					t.Errorf("protected file changed: %q %v", got, err)
				}
				if _, err := os.Stat(filepath.Join(root, filepath.Dir(path), "new.txt")); !os.IsNotExist(err) {
					t.Errorf("file planted in protected directory: %v", err)
				}
			})
		}
	}
	ctx := t.Context()
	if got, err := w.list(ctx, pathArgs{Path: "."}); err != nil || got != "sub/\nvisible.txt" {
		t.Fatalf("list = %q, %v", got, err)
	}
	if got, err := w.search(ctx, searchArgs{Pattern: "CANARY"}); err != nil || strings.Contains(got, "CANARY") {
		t.Fatalf("search reached protected files: %q, %v", got, err)
	}
	if got, err := w.find(ctx, findArgs{Pattern: "**"}); err != nil || strings.Contains(got, "secret.txt") {
		t.Fatalf("find listed protected files: %q, %v", got, err)
	}
}

// Tool errors name workspace-relative paths, never the host path of the root.
func TestWorkspaceErrorsDoNotLeakHostPath(t *testing.T) {
	root := t.TempDir()
	w := workspace{roots: []string{root}}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	_, listFile := w.list(ctx, pathArgs{Path: "a.txt"})
	_, listMissing := w.list(ctx, pathArgs{Path: "missing"})
	_, readMissing := w.read(ctx, pathArgs{Path: "missing"})
	_, writeUnderFile := w.write(ctx, writeArgs{Path: "a.txt/x", Content: "y"})
	for name, err := range map[string]error{"list file": listFile, "list missing": listMissing, "read missing": readMissing, "write under file": writeUnderFile} {
		if err == nil || strings.Contains(err.Error(), root) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if !strings.Contains(listFile.Error(), "a.txt") {
		t.Errorf("list error lost the relative path: %v", listFile)
	}
}
