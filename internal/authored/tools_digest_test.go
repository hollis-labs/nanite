package authored

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPackageReadRefusesAFileSwappedForFIFO(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "entry")
	if err := os.WriteFile(path, []byte("regular"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := readPackageFile(root, "entry", info, 1024); done <- err }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("FIFO read: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("reading swapped FIFO blocked")
	}
}

type countedReader struct {
	io.Reader
	n int
}

func (r *countedReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.n += n
	return n, err
}
func TestPackageReadCapsGrowingContents(t *testing.T) {
	r := &countedReader{Reader: bytes.NewReader(bytes.Repeat([]byte("x"), 4096))}
	if _, err := readPackageBytes(r, 100); err == nil {
		t.Fatal("oversized file accepted")
	}
	if r.n != 101 {
		t.Fatalf("read %d bytes, want budget plus one sentinel", r.n)
	}
}

func TestPackageDigestInvalidatesMetadataChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "entry")
	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	digest := func() string {
		t.Helper()
		d, err := packageDigest(root, ".")
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	first := digest()
	if got := digest(); got != first {
		t.Fatal("unchanged package digest differs")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	second := digest()
	if second == first {
		t.Fatal("same-size rewrite with restored mtime reused cached digest")
	}
	replacement := filepath.Join(dir, "replacement")
	if err := os.WriteFile(replacement, []byte("third"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	third := digest()
	if third == second {
		t.Fatal("replacement inode reused cached digest")
	}
	if err := os.Chmod(path, 0700); err != nil { //nolint:gosec // G302: exercise digest invalidation on executable mode changes.
		t.Fatal(err)
	}
	if got := digest(); got == third {
		t.Fatal("executable mode change reused cached digest")
	}
}

func TestCredentialEnvironmentHeuristics(t *testing.T) {
	for _, name := range []string{"DATABASE_URL", "GITHUB_PAT", "PAT", "SERVICE_DSN", "DSN", "DB_PASSWORD"} {
		if err := CheckToolEnv(name, nil); err == nil {
			t.Errorf("accepted %s", name)
		}
		if err := CheckToolEnv(name, []string{name}); err != nil {
			t.Errorf("host allowance for %s: %v", name, err)
		}
	}
	for _, value := range []string{"https://" + "user:password" + "@example.test/path", "postgres://user@example.test/db", "https://example.test/?access_token=value", "https://example.test/?password=value"} {
		if err := CheckToolEnvValue("SERVICE_URL", value, nil); err == nil {
			t.Errorf("accepted credential URL %q", value)
		}
		if err := CheckToolEnvValue("SERVICE_URL", value, []string{"SERVICE_URL"}); err != nil {
			t.Errorf("host allowance: %v", err)
		}
	}
	if err := CheckToolEnvValue("SERVICE_URL", "https://example.test/?page=2", nil); err != nil {
		t.Fatal(err)
	}
}
