package api

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
)

// approvedPublishedPluginFixture copies an externally verified release into a
// private test installation and approves exactly its accepted byte snapshot.
func approvedPublishedPluginFixture(t *testing.T, env, owner, version string) string {
	t.Helper()
	published := os.Getenv(env)
	if published == "" {
		t.Skip("requires externally verified published plugin bundle: " + env)
	}
	root := t.TempDir()
	directory := filepath.Join(root, owner)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	input, err := os.OpenRoot(published) // #nosec G703 -- opt-in fixture selects an externally verified release directory.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = output.Close() }()
	if err = fs.WalkDir(input.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return output.MkdirAll(relative, 0700)
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if !info.Mode().IsRegular() {
			return fs.ErrInvalid
		}
		raw, readErr := input.ReadFile(relative)
		if readErr != nil {
			return readErr
		}
		return output.WriteFile(relative, raw, info.Mode().Perm())
	}); err != nil {
		t.Fatal(err)
	}
	review, err := naniteplugin.BuildInstallReview(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	if review.ID != owner || review.Version != version {
		t.Fatal("published fixture identity differs", review.ID, review.Version)
	}
	if err = naniteplugin.SaveInstallApproval(root, review, review.Digest()); err != nil {
		t.Fatal(err)
	}
	return root
}
