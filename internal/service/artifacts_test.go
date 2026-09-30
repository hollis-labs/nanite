package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type closeErrorArtifactFile struct {
	ArtifactTempFile
	err error
}

func (f *closeErrorArtifactFile) Close() error {
	if err := f.ArtifactTempFile.Close(); err != nil {
		return err
	}
	return f.err
}

type dataThenErrorReader struct {
	data []byte
	err  error
	done bool
}

func (r *dataThenErrorReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, r.err
	}
	r.done = true
	return copy(p, r.data), r.err
}

// Moved from internal/api with writeAtomically.
func TestWriteArtifactAtomicallyPreservesCopyErrorOverCloseError(t *testing.T) {
	dir := t.TempDir()
	finalPath := filepath.Join(dir, "artifact.txt")
	if err := os.WriteFile(finalPath, []byte("ORIGINAL"), 0o600); err != nil {
		t.Fatalf("seed final artifact: %v", err)
	}
	copyErr := errors.New("injected copy failure")
	closeErr := errors.New("injected cleanup close failure")
	var tempPath string
	svc := NewArtifactService(nil, nil)
	svc.SetTempFileFactory(func(dir, pattern string) (ArtifactTempFile, error) {
		file, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return nil, err
		}
		tempPath = file.Name()
		return &closeErrorArtifactFile{ArtifactTempFile: file, err: closeErr}, nil
	})

	_, err := svc.writeAtomically(dir, finalPath, &dataThenErrorReader{data: []byte("partial"), err: copyErr})
	if !errors.Is(err, copyErr) {
		t.Fatalf("write error = %v, want primary copy error", err)
	}
	if errors.Is(err, closeErr) {
		t.Fatalf("write error = %v, cleanup close error replaced/joined primary error", err)
	}
	// #nosec G304 -- finalPath is constructed beneath t.TempDir above.
	got, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("read preserved final artifact: %v", err)
	}
	if string(got) != "ORIGINAL" {
		t.Fatalf("final artifact = %q, want original content", got)
	}
	if _, statErr := os.Stat(tempPath); !os.IsNotExist(statErr) {
		t.Fatalf("staging file still exists after copy failure: err=%v", statErr)
	}
}
