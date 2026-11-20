package authored

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func consistencyPackage(t *testing.T, extra string) (string, *os.Root) {
	t.Helper()
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "tools", "fixture")
	if err := os.MkdirAll(pkgDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "tool.yaml"), []byte("description: original\nkind: command\nentry: run.sh\n"+extra), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "run.sh"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil { //nolint:gosec // G306: package entry must be executable.
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return dir, root
}

func TestLoadToolRejectsCredentialURLBeforeExecution(t *testing.T) {
	dir, _ := consistencyPackage(t, "env: [QUEUE_URL]\n")
	t.Setenv("QUEUE_URL", "https://"+"owner:private-fixture"+"@queue.example/jobs")
	if _, err := LoadTool(dir, "fixture"); err == nil || !strings.Contains(err.Error(), "QUEUE_URL contains URL credentials") || strings.Contains(err.Error(), "private-fixture") {
		t.Fatalf("load-time credential rejection: %v", err)
	}
	if _, err := LoadTool(dir, "fixture", "QUEUE_URL"); err != nil {
		t.Fatalf("explicit host allowance: %v", err)
	}
	t.Setenv("QUEUE_URL", "https://queue.example/jobs")
	if _, err := LoadTool(dir, "fixture"); err != nil {
		t.Fatalf("safe URL: %v", err)
	}
}

func TestLoadToolRejectsManifestChangedAfterDigest(t *testing.T) {
	_, root := consistencyPackage(t, "")
	called := false
	_, err := loadToolWithDigest(root, "fixture", nil, func(root *os.Root, dir string) (string, error) {
		digest, err := packageDigest(root, dir)
		if err != nil {
			return "", err
		}
		// Replace the parsed manifest after a successful digest. This must be
		// caught by the loader's own check, not the digest's snapshot check.
		called = true
		if err := root.WriteFile(dir+"/tool.yaml", []byte("description: changed after digest\nkind: command\nentry: run.sh\n"), 0600); err != nil {
			t.Fatal(err)
		}
		return digest, nil
	})
	if !called || err == nil || !strings.Contains(err.Error(), "tool.yaml changed while loading") {
		t.Fatalf("manifest consistency: called=%v err=%v", called, err)
	}
}

func TestPackageDigestRejectsChangeBetweenSnapshots(t *testing.T) {
	_, root := consistencyPackage(t, "")
	changed := false
	_, err := packageDigestWithReader(root, "tools/fixture", func(root *os.Root, path string, info fs.FileInfo, limit int64) ([]byte, error) {
		data, err := readPackageFile(root, path, info, limit)
		if err != nil {
			return nil, err
		}
		if !changed {
			// Every opened file remains stable. Only the second directory snapshot
			// can catch an entry added after the first snapshot enumerated the files.
			changed = true
			if err := root.WriteFile("tools/fixture/added", []byte("new package content"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return data, nil
	})
	if !changed || err == nil || !strings.Contains(err.Error(), "package changed while reading") {
		t.Fatalf("package consistency: changed=%v err=%v", changed, err)
	}
}
