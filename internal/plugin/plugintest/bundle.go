// Package plugintest builds test declarations from actual staged fixture bytes.
// It issues no install approval, capability or production identity.
package plugintest

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
)

func Inventory(t testing.TB, declaration *manifest.Manifest, root string) {
	t.Helper()
	declaration.Artifact.Files = nil
	confined, openErr := os.OpenRoot(root)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer func() {
		if closeErr := confined.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == manifest.Filename {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fs.ErrInvalid
		}
		data, err := confined.ReadFile(filepath.FromSlash(rel))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		declaration.Artifact.Files = append(declaration.Artifact.Files, manifest.ArtifactFile{Path: rel, SHA256: hex.EncodeToString(sum[:]), Executable: info.Mode().Perm()&0111 != 0})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	declaration.Artifact.TreeSHA256, err = manifest.TreeDigest(declaration.Artifact.Files)
	if err != nil {
		t.Fatal(err)
	}
}
