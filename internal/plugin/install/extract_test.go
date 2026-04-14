package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tarEntry describes one member of a test archive.
type tarEntry struct {
	Name     string
	Mode     int64
	Size     int64
	Typeflag byte
	Linkname string
	Body     []byte
}

// buildTarGz writes a tar.gz to a temp file and returns its path. Size in
// header defaults to len(Body) when zero.
func buildTarGz(t *testing.T, entries []tarEntry) string {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, e := range entries {
		size := e.Size
		if size == 0 {
			size = int64(len(e.Body))
		}
		hdr := &tar.Header{
			Name:     e.Name,
			Mode:     e.Mode,
			Size:     size,
			Typeflag: e.Typeflag,
			Linkname: e.Linkname,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if len(e.Body) > 0 {
			if _, err := tw.Write(e.Body); err != nil {
				t.Fatalf("write body: %v", err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tw close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gw close: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "archive.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	return path
}

func freshTarget(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func TestExtract_HappyPath(t *testing.T) {
	archive := buildTarGz(t, []tarEntry{
		{Name: "plugin.yaml", Mode: 0o644, Body: []byte("name: test\n")},
		{Name: "bin/", Mode: 0o755, Typeflag: tar.TypeDir},
		{Name: "bin/run", Mode: 0o755, Body: []byte("#!/bin/sh\necho hi\n")},
	})
	dest := freshTarget(t)
	e := &TarGzExtractor{}
	if err := e.Extract(context.Background(), Handle{Kind: "archive", Path: archive}, dest, nil); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "plugin.yaml")); err != nil {
		t.Errorf("plugin.yaml missing: %v", err)
	}
	info, err := os.Stat(filepath.Join(dest, "bin/run"))
	if err != nil {
		t.Fatalf("bin/run missing: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("bin/run mode = %v, want 0755", info.Mode().Perm())
	}
}

func TestExtract_AdversarialCases(t *testing.T) {
	cases := []struct {
		name    string
		entries []tarEntry
		wantMsg string
	}{
		{
			name: "absolute path",
			entries: []tarEntry{
				{Name: "/etc/passwd", Mode: 0o644, Body: []byte("x")},
			},
			wantMsg: "absolute path",
		},
		{
			name: "parent-dir traversal",
			entries: []tarEntry{
				{Name: "../outside.txt", Mode: 0o644, Body: []byte("x")},
			},
			wantMsg: "parent-dir segment",
		},
		{
			name: "nested parent-dir",
			entries: []tarEntry{
				{Name: "sub/../../outside.txt", Mode: 0o644, Body: []byte("x")},
			},
			wantMsg: "parent-dir segment",
		},
		{
			name: "symlink entry",
			entries: []tarEntry{
				{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
			},
			wantMsg: "link entries not allowed",
		},
		{
			name: "hardlink entry",
			entries: []tarEntry{
				{Name: "file", Mode: 0o644, Body: []byte("ok")},
				{Name: "hard", Typeflag: tar.TypeLink, Linkname: "file"},
			},
			wantMsg: "link entries not allowed",
		},
		{
			name: "char device",
			entries: []tarEntry{
				{Name: "dev", Typeflag: tar.TypeChar},
			},
			wantMsg: "device/fifo",
		},
		{
			name: "block device",
			entries: []tarEntry{
				{Name: "dev", Typeflag: tar.TypeBlock},
			},
			wantMsg: "device/fifo",
		},
		{
			name: "fifo",
			entries: []tarEntry{
				{Name: "pipe", Typeflag: tar.TypeFifo},
			},
			wantMsg: "device/fifo",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			archive := buildTarGz(t, tc.entries)
			dest := freshTarget(t)
			e := &TarGzExtractor{}
			err := e.Extract(context.Background(), Handle{Kind: "archive", Path: archive}, dest, nil)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantMsg)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("err = %v; want contains %q", err, tc.wantMsg)
			}
		})
	}
}

func TestExtract_OversizedFile(t *testing.T) {
	archive := buildTarGz(t, []tarEntry{
		{Name: "big.bin", Mode: 0o644, Body: bytes.Repeat([]byte("A"), 200)},
	})
	dest := freshTarget(t)
	e := &TarGzExtractor{MaxFileBytes: 100}
	err := e.Extract(context.Background(), Handle{Kind: "archive", Path: archive}, dest, nil)
	if err == nil || !strings.Contains(err.Error(), "per-file cap") {
		t.Fatalf("err = %v; want per-file cap error", err)
	}
}

func TestExtract_TooManyEntries(t *testing.T) {
	var entries []tarEntry
	for i := 0; i < 10; i++ {
		entries = append(entries, tarEntry{Name: filepath.Join("d", "f"+string(rune('a'+i))+".txt"), Mode: 0o644, Body: []byte("x")})
	}
	archive := buildTarGz(t, entries)
	dest := freshTarget(t)
	e := &TarGzExtractor{MaxEntries: 3}
	err := e.Extract(context.Background(), Handle{Kind: "archive", Path: archive}, dest, nil)
	if err == nil || !strings.Contains(err.Error(), "entries") {
		t.Fatalf("err = %v; want too-many-entries error", err)
	}
}

func TestExtract_TotalSizeCap(t *testing.T) {
	archive := buildTarGz(t, []tarEntry{
		{Name: "a.bin", Mode: 0o644, Body: bytes.Repeat([]byte("A"), 100)},
		{Name: "b.bin", Mode: 0o644, Body: bytes.Repeat([]byte("B"), 100)},
	})
	dest := freshTarget(t)
	e := &TarGzExtractor{MaxTotalUncompressedBytes: 150, MaxCompressionRatio: 1000}
	err := e.Extract(context.Background(), Handle{Kind: "archive", Path: archive}, dest, nil)
	if err == nil || !strings.Contains(err.Error(), "total size") {
		t.Fatalf("err = %v; want total-size error", err)
	}
}

func TestExtract_CompressionRatioCap(t *testing.T) {
	// Highly-compressible payload: gzip of zero-bytes is ~50-100 bytes for
	// 50KB of zeros.
	archive := buildTarGz(t, []tarEntry{
		{Name: "bomb.bin", Mode: 0o644, Body: bytes.Repeat([]byte{0}, 50_000)},
	})
	dest := freshTarget(t)
	e := &TarGzExtractor{MaxCompressionRatio: 5, MaxTotalUncompressedBytes: 1_000_000, MaxFileBytes: 1_000_000}
	err := e.Extract(context.Background(), Handle{Kind: "archive", Path: archive}, dest, nil)
	if err == nil || !strings.Contains(err.Error(), "compression ratio") {
		t.Fatalf("err = %v; want compression-ratio error", err)
	}
}

func TestExtract_UnsupportedType(t *testing.T) {
	archive := buildTarGz(t, []tarEntry{
		{Name: "x", Typeflag: 'X'},
	})
	dest := freshTarget(t)
	e := &TarGzExtractor{}
	err := e.Extract(context.Background(), Handle{Kind: "archive", Path: archive}, dest, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported tar entry type") {
		t.Fatalf("err = %v; want unsupported-type error", err)
	}
}

func TestExtract_UnknownHandleKind(t *testing.T) {
	e := &TarGzExtractor{}
	err := e.Extract(context.Background(), Handle{Kind: "bogus"}, freshTarget(t), nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported handle kind") {
		t.Fatalf("err = %v", err)
	}
}

func TestExtract_Directory_HappyPath(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "plugin.yaml"), []byte("name: local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(src, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "bin", "run"), []byte("ok"), 0o755); err != nil {
		t.Fatal(err)
	}
	dest := freshTarget(t)
	e := &TarGzExtractor{}
	if err := e.Extract(context.Background(), Handle{Kind: "directory", Path: src}, dest, nil); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "plugin.yaml")); err != nil {
		t.Errorf("plugin.yaml missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "bin", "run")); err != nil {
		t.Errorf("bin/run missing: %v", err)
	}
}

func TestExtract_Directory_RejectsSymlinks(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "real.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.txt", filepath.Join(src, "link")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	dest := freshTarget(t)
	e := &TarGzExtractor{}
	err := e.Extract(context.Background(), Handle{Kind: "directory", Path: src}, dest, nil)
	if err == nil || !strings.Contains(err.Error(), "symlinks in source dir") {
		t.Fatalf("err = %v; want symlink rejection", err)
	}
}
