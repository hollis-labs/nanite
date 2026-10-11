package api

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The local-authority writer must have one operator-authenticated consumer.
// This is an architectural pin, not a security boundary (Go cannot hide a
// method from the module); it makes a second consumer a deliberate, reviewed
// change instead of an accident.
func TestLocalAuthorityWriterHasOneConsumer(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	constructs := regexp.MustCompile(`\.LocalAuthority\(\)`)
	consumes := regexp.MustCompile(`\.NativeAuthority\b`)
	allowConstruct := map[string]bool{"internal/service/container.go": true}
	allowConsume := map[string]bool{"internal/api/native_authority.go": true, "internal/service/container.go": true}
	checked := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", "ui":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		checked++
		if constructs.Match(data) && !allowConstruct[rel] && rel != "internal/store/local_authority.go" {
			t.Errorf("%s calls Store.LocalAuthority(); only the container wiring may construct the writer", rel)
		}
		if consumes.Match(data) && !allowConsume[rel] {
			t.Errorf("%s uses the native authority writer; only internal/api/native_authority.go may", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 100 {
		t.Fatalf("scanned only %d Go files from %s; the walk is not finding the tree", checked, root)
	}
}
