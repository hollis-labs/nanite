// Package migratedtemplate shares one fully migrated SQLite database per test
// process. Opening a fresh Nanite store runs every migration, which costs about
// 30s under -race; copying an already-migrated file and opening that costs
// milliseconds. Each caller still gets its own private, fully migrated copy.
//
// It deliberately imports nothing from internal/store, so the store package's
// own (internal) tests can use it without an import cycle.
package migratedtemplate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

var (
	once sync.Once
	path string
	err  error
)

// Copy writes a private copy of the process-wide migrated template to dst.
//
// build must create a database at the path it is given, run every migration,
// and close it (so the file is self-contained). It runs at most once per test
// process: the first caller's build defines the template, so one test binary
// should use one build function.
func Copy(t testing.TB, dst string, build func(path string) error) {
	t.Helper()
	once.Do(func() {
		var dir string
		if dir, err = os.MkdirTemp("", "nanite-migrated-template-*"); err != nil {
			return
		}
		path = filepath.Join(dir, "template.db")
		err = build(path)
	})
	if err != nil {
		t.Fatalf("create migrated database template: %v", err)
	}
	if copyErr := copyFile(dst, path); copyErr != nil {
		t.Fatalf("copy migrated database template: %v", copyErr)
	}
}

func copyFile(dst, src string) error {
	in, err := os.Open(src) // #nosec G304 -- src is the template this package created in a private temp dir
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst) // #nosec G304 -- dst is a test-owned temp path chosen by the caller
	if err != nil {
		return err
	}
	if _, copyErr := io.Copy(out, in); copyErr != nil {
		_ = out.Close()
		return copyErr
	}
	if closeErr := out.Close(); closeErr != nil {
		return closeErr
	}
	info, statErr := os.Stat(src)
	if statErr != nil {
		return statErr
	}
	if chmodErr := os.Chmod(dst, info.Mode()); chmodErr != nil {
		return fmt.Errorf("chmod copied file: %w", chmodErr)
	}
	return nil
}
