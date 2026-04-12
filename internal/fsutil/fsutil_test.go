package fsutil

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestAtomicWriteFile_Table(t *testing.T) {
	dir := t.TempDir()

	type tc struct {
		name   string
		setup  func(path string)
		data   []byte
		mode   os.FileMode
		assert func(t *testing.T, path string, err error)
	}
	cases := []tc{
		{
			name: "new file",
			data: []byte("hello"),
			mode: 0o600,
			assert: func(t *testing.T, path string, err error) {
				if err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != "hello" {
					t.Fatalf("got %q", got)
				}
			},
		},
		{
			name: "overwrite existing",
			setup: func(path string) {
				if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			data: []byte("new"),
			mode: 0o640,
			assert: func(t *testing.T, path string, err error) {
				if err != nil {
					t.Fatal(err)
				}
				got, _ := os.ReadFile(path)
				if string(got) != "new" {
					t.Fatalf("got %q", got)
				}
			},
		},
		{
			name: "mode preservation",
			data: []byte("x"),
			mode: 0o600,
			assert: func(t *testing.T, path string, err error) {
				if err != nil {
					t.Fatal(err)
				}
				if runtime.GOOS == "windows" {
					return
				}
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0o600 {
					t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
				}
			},
		},
		{
			name: "empty data writes zero-byte file",
			data: []byte{},
			mode: 0o644,
			assert: func(t *testing.T, path string, err error) {
				if err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if info.Size() != 0 {
					t.Fatalf("size %d want 0", info.Size())
				}
			},
		},
	}

	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(dir, fmt.Sprintf("f-%d.txt", i))
			if c.setup != nil {
				c.setup(path)
			}
			err := AtomicWriteFile(path, c.data, c.mode)
			c.assert(t, path, err)
		})
	}
}

func TestAtomicWriteFile_EmptyPath(t *testing.T) {
	if err := AtomicWriteFile("", nil, 0o600); err == nil {
		t.Fatal("expected error on empty path")
	}
}

func TestAtomicWriteFile_NoPartialOnError(t *testing.T) {
	// Write into a non-existent directory — CreateTemp fails, no file leaks.
	path := filepath.Join(t.TempDir(), "does-not-exist", "f.txt")
	if err := AtomicWriteFile(path, []byte("x"), 0o600); err == nil {
		t.Fatal("expected error")
	}
	// No stray temp files at the parent.
	parent := filepath.Dir(path)
	entries, err := os.ReadDir(parent)
	if err == nil {
		for _, e := range entries {
			t.Errorf("stray entry %q", e.Name())
		}
	}
}

func TestAtomicWriter_StreamingWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stream.txt")

	w, err := AtomicWriter(path, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range []string{"hello ", "world", "!"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello world!" {
		t.Fatalf("got %q", got)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v", info.Mode().Perm())
		}
	}
}

func TestAtomicWriter_AbortLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aborted.txt")

	w, err := AtomicWriter(path, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("nope")); err != nil {
		t.Fatal(err)
	}
	aw := w.(*atomicWriter)
	if err := aw.Abort(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("target should not exist after abort")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("temp left behind: %v", entries)
	}
}

func TestAtomicWriter_EmptyPath(t *testing.T) {
	if _, err := AtomicWriter("", 0o600); err == nil {
		t.Fatal("expected error on empty path")
	}
}

func TestAtomicWriter_CreateTempFailsInMissingDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-dir", "x.txt")
	if _, err := AtomicWriter(path, 0o600); err == nil {
		t.Fatal("expected error")
	}
}

func TestAtomicWriter_AbortAfterCloseIsNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	w, err := AtomicWriter(path, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// After Close, Abort returns nil.
	if err := w.(*atomicWriter).Abort(); err != nil {
		t.Fatalf("abort after close: %v", err)
	}
}

func TestAtomicWriteFile_RenameFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}
	// Target path is a directory — rename of a file over a non-empty dir
	// fails on POSIX, exercising the rename error path and temp cleanup.
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	// Put something inside so rename-over-dir fails on both macOS and Linux.
	if err := os.WriteFile(filepath.Join(target, "child"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWriteFile(target, []byte("data"), 0o600); err == nil {
		t.Fatal("expected error renaming over non-empty dir")
	}
	// No temp files left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "target" {
			t.Errorf("stray: %q", e.Name())
		}
	}
}

func TestAtomicWriter_RenameOverNonEmptyDirFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "child"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := AtomicWriter(target, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err == nil {
		t.Fatal("expected rename error")
	}
	// Temp should be cleaned up — no extra files in dir beyond "target".
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "target" {
			t.Errorf("stray: %q", e.Name())
		}
	}
}

func TestAtomicWriter_WriteAfterCloseFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wac.txt")
	w, err := AtomicWriter(path, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("x")); err == nil {
		t.Fatal("expected error writing after close")
	}
	// Second Close is a no-op.
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAtomicWriteFile_ConcurrentWriters(t *testing.T) {
	// Two goroutines race to write the same path. At the end:
	//  - the file exists,
	//  - the contents match one of the two inputs exactly (no partial),
	//  - no temp files remain.
	dir := t.TempDir()
	path := filepath.Join(dir, "race.txt")

	a := bytes.Repeat([]byte("A"), 64*1024)
	b := bytes.Repeat([]byte("B"), 64*1024)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			_ = AtomicWriteFile(path, a, 0o600)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			_ = AtomicWriteFile(path, b, 0o600)
		}
	}()
	wg.Wait()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, a) && !bytes.Equal(got, b) {
		t.Fatalf("file contents not one of the inputs: len=%d", len(got))
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() == filepath.Base(path) {
			continue
		}
		t.Errorf("stray temp file: %q", e.Name())
	}
}
