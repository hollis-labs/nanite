// Package fsutil provides crash-safe filesystem primitives.
//
// AtomicWriteFile and AtomicWriter write to a sibling temp file, fsync, then
// os.Rename over the destination. The rename is atomic on POSIX so readers
// never observe a partial or truncated file.
//
// Both primitives preserve the requested mode on the final file and remove
// the temp file on any error path, including partial writes.
package fsutil

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// AtomicWriteFile writes data to path atomically. The caller-supplied mode
// is applied to the destination file.
//
// Failure modes:
//   - Temp creation, write, fsync, and rename errors are returned wrapped.
//   - The temp file is removed on any error (best effort).
//   - If path already exists, it is replaced atomically.
func AtomicWriteFile(path string, data []byte, mode os.FileMode) (retErr error) {
	if path == "" {
		return errors.New("fsutil: empty path")
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return fmt.Errorf("fsutil: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if retErr != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsutil: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsutil: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("fsutil: close temp: %w", err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("fsutil: chmod: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("fsutil: rename: %w", err)
	}
	return nil
}

// AtomicWriter returns an io.WriteCloser whose Close renames the temp file
// over path. If Close is never called, or an error occurs before Close, the
// temp file is removed.
//
// The returned writer is not safe for concurrent use. Callers may use an
// Abort method (via the concrete *atomicWriter) to explicitly discard.
func AtomicWriter(path string, mode os.FileMode) (io.WriteCloser, error) {
	if path == "" {
		return nil, errors.New("fsutil: empty path")
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return nil, fmt.Errorf("fsutil: create temp: %w", err)
	}
	return &atomicWriter{
		f:      tmp,
		tmp:    tmp.Name(),
		target: path,
		mode:   mode,
	}, nil
}

type atomicWriter struct {
	f      *os.File
	tmp    string
	target string
	mode   os.FileMode
	closed bool
}

func (w *atomicWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, errors.New("fsutil: write after close")
	}
	return w.f.Write(p)
}

// Close fsyncs and renames the temp over the target. If any step fails, the
// temp is removed.
func (w *atomicWriter) Close() (retErr error) {
	if w.closed {
		return nil
	}
	w.closed = true
	defer func() {
		if retErr != nil {
			_ = os.Remove(w.tmp)
		}
	}()

	if err := w.f.Sync(); err != nil {
		_ = w.f.Close()
		return fmt.Errorf("fsutil: fsync: %w", err)
	}
	if err := w.f.Close(); err != nil {
		return fmt.Errorf("fsutil: close temp: %w", err)
	}
	if err := os.Chmod(w.tmp, w.mode); err != nil {
		return fmt.Errorf("fsutil: chmod: %w", err)
	}
	if err := os.Rename(w.tmp, w.target); err != nil {
		return fmt.Errorf("fsutil: rename: %w", err)
	}
	return nil
}

// Abort discards the temp without renaming. Safe to call after Close (no-op).
func (w *atomicWriter) Abort() error {
	if w.closed {
		return nil
	}
	w.closed = true
	_ = w.f.Close()
	return os.Remove(w.tmp)
}
