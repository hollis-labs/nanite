package toolregistry

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/authored"
)

type fetchHook struct {
	BlobStore
	before func()
}

func (f fetchHook) Fetch(ctx context.Context, ref string) ([]byte, error) {
	f.before()
	return f.BlobStore.Fetch(ctx, ref)
}

func TestInstallAppearingPackageDoesNotPin(t *testing.T) {
	_, client, r := publishFixture(t)
	target := t.TempDir()
	client.Blobs = fetchHook{client.Blobs, func() {
		if err := os.Mkdir(filepath.Join(target, "tools/echo"), 0700); err != nil {
			t.Fatal(err)
		}
	}}
	if _, err := Install(t.Context(), client, target, r.Ref()); err == nil || !strings.Contains(err.Error(), "package appeared") {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, lockFile)); !os.IsNotExist(err) {
		t.Fatalf("failed install wrote lock: %v", err)
	}
}

func TestInstallCleansStaleStages(t *testing.T) {
	_, client, r := publishFixture(t)
	target := t.TempDir()
	stale := filepath.Join(target, ".nanite-install-abandoned")
	if err := os.MkdirAll(filepath.Join(stale, "tools/partial"), 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	marker := filepath.Join(outside, "keep")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(target, ".nanite-install-link")); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(target, ".nanite-install-file")
	if err := os.WriteFile(keep, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(t.Context(), client, target, r.Ref()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale stage remains: %v", err)
	}
	for _, p := range []string{marker, keep} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInstallChecksCASBeforePackage(t *testing.T) {
	_, client, r := publishFixture(t)
	data, err := client.Blobs.Fetch(t.Context(), r.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	// Tar metadata changes the CAS address but not the package tree or its digest.
	changed := mutateTar(t, data, func(h *tar.Header, b []byte) []byte { h.Uid = 123; return b })
	client.Blobs = changedRegistry{data: changed}
	if _, err := Install(t.Context(), client, t.TempDir(), r.Ref()); err == nil || !strings.Contains(err.Error(), "artifact SHA-256 mismatch") {
		t.Fatalf("CAS verification: %v", err)
	}
}

func TestInstallNamePinnedToOtherDigest(t *testing.T) {
	_, client, r := publishFixture(t)
	target := t.TempDir()
	root, err := os.OpenRoot(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	prior := r
	prior.Version = "0.9.0"
	prior.ID = "tool-package:agt_fixture/echo@0.9.0"
	prior.Digest = "sha256:" + strings.Repeat("0", 64)
	if err := writeLock(root, packageLock{Version: 1, Packages: map[string]Record{prior.Ref(): prior}}); err != nil {
		t.Fatal(err)
	}
	// No live package exists: only the name pin prevents taking it over.
	if _, err := Install(t.Context(), client, target, r.Ref()); err == nil || !strings.Contains(err.Error(), "name already pinned to another digest") {
		t.Fatalf("name pin: %v", err)
	}
}

func TestUnpackLimits(t *testing.T) {
	t.Run("setuid", func(t *testing.T) {
		assertUnpackError(t, oneTar(t, &tar.Header{Name: "file", Mode: 04755, Typeflag: tar.TypeReg}, nil), "path or mode")
	})
	t.Run("artifact bytes", func(t *testing.T) {
		// A valid small tar padded past the envelope cap would otherwise extract.
		data := oneTar(t, &tar.Header{Name: "file", Mode: 0600, Typeflag: tar.TypeReg}, nil)
		data = append(data, make([]byte, maxBundleBytes+1-len(data))...)
		assertUnpackError(t, data, "artifact too large")
	})
	t.Run("package bytes", func(t *testing.T) {
		var buf bytes.Buffer
		w := tar.NewWriter(&buf)
		// The declared size must be rejected before trying to copy missing bytes.
		if err := w.WriteHeader(&tar.Header{Name: "file", Mode: 0600, Typeflag: tar.TypeReg, Size: int64(authored.MaxToolPackageBytes) + 1}); err != nil {
			t.Fatal(err)
		}
		assertUnpackError(t, buf.Bytes(), "package exceeds byte limit")
	})
	t.Run("entry count", func(t *testing.T) {
		var buf bytes.Buffer
		w := tar.NewWriter(&buf)
		for i := 0; i <= maxEntries; i++ {
			if err := w.WriteHeader(&tar.Header{Name: fmt.Sprintf("d%d/", i), Mode: 0755, Typeflag: tar.TypeDir}); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		assertUnpackError(t, buf.Bytes(), "path or mode")
	})
}

func assertUnpackError(t *testing.T, data []byte, want string) {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := unpack(root, "echo", data); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("want %s, got %v", want, err)
	}
}
