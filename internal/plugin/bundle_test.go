package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveBundleFileConfinesExecutableAndAssets(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0700); err != nil { // #nosec G306 -- executable fixture used to test bundle escape rejection.
		t.Fatal(err)
	}
	executable := filepath.Join(root, "plugin")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0700); err != nil { // #nosec G306 -- executable fixture requires an execute bit.
		t.Fatal(err)
	}
	if _, err := ResolveBundleFile(root, "plugin", true); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"escape", "../outside", outside, ".", "missing"} {
		for _, executable := range []bool{false, true} {
			if _, err := ResolveBundleFile(root, path, executable); err == nil {
				t.Fatalf("accepted %q executable=%v", path, executable)
			}
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(executable, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ResolveBundleFile(root, "plugin", true); err == nil {
			t.Fatal("accepted non executable")
		}
		if _, err := ResolveBundleFile(root, "plugin", false); err != nil {
			t.Fatal(err)
		}
	}
}
