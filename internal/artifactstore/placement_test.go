package artifactstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectPlacedConfinesFilesAndRecordsMetadata(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "report.txt")
	if err := os.WriteFile(path, []byte("REPORT"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"report.txt", path} {
		got, err := InspectPlaced(root, input, "report.txt", "")
		if err != nil {
			t.Fatal(err)
		}
		if got.Path != path || got.Size != 6 || !strings.HasPrefix(got.MIME, "text/plain") {
			t.Fatalf("wrong placed metadata: %+v", got)
		}
	}
	explicit, err := InspectPlaced(root, path, "unknown.extension", "application/custom")
	if err != nil || explicit.MIME != "application/custom" {
		t.Fatalf("MIME override: %+v, %v", explicit, err)
	}
}

func TestInspectPlacedRefusesMissingNonregularAndEscapingPaths(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "artifacts")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "secret.txt")
	if err := os.WriteFile(outside, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"missing.txt", ".", outside, "../secret.txt"} {
		if got, err := InspectPlaced(root, input, "report.txt", ""); err == nil {
			t.Fatalf("accepted %q: %+v", input, got)
		}
	}
	link := filepath.Join(root, "escape.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{link, "escape.txt"} {
		if _, err := InspectPlaced(root, input, "report.txt", ""); err == nil {
			t.Fatalf("accepted escaping symlink %q", input)
		}
	}
}
