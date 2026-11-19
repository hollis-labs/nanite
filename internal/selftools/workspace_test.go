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
