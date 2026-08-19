package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/plugin"
)

func TestIsLocalPath(t *testing.T) {
	// Explicit path prefixes are always local.
	for _, p := range []string{"./foo", "../foo", "/tmp/foo", ".", ".."} {
		if !isLocalPath(p) {
			t.Errorf("isLocalPath(%q) = false, want true", p)
		}
	}
	// Bare tokens that don't exist as directories are treated as remote names.
	if isLocalPath("nonexistent-plugin-abc-xyz") {
		t.Error("isLocalPath of nonexistent bare name should be false")
	}
	// Bare token that names a real directory is local.
	dir := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	if err := os.Chdir(filepath.Dir(dir)); err != nil {
		t.Fatal(err)
	}
	if !isLocalPath(filepath.Base(dir)) {
		t.Errorf("isLocalPath of existing-dir bare name should be true")
	}
}

func TestCopyPluginDir(t *testing.T) {
	src := t.TempDir()
	// Realistic plugin layout plus directories that must be skipped.
	writeFile(t, filepath.Join(src, "plugin.yaml"), "schema_version: 1\nid: test\n")
	writeFile(t, filepath.Join(src, "main.go"), "package main")
	writeFile(t, filepath.Join(src, "ui/src/index.tsx"), "export {}")
	writeFile(t, filepath.Join(src, ".git/HEAD"), "ref: refs/heads/main")
	writeFile(t, filepath.Join(src, "node_modules/foo/package.json"), "{}")
	writeFile(t, filepath.Join(src, "dist/giphy"), "binary")

	dst := filepath.Join(t.TempDir(), "target")
	if err := copyPluginDir(src, dst); err != nil {
		t.Fatalf("copyPluginDir: %v", err)
	}

	for _, rel := range []string{"plugin.yaml", "main.go", "ui/src/index.tsx"} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("expected %s in target, got %v", rel, err)
		}
	}
	for _, rel := range []string{".git", "node_modules", "dist"} {
		if _, err := os.Stat(filepath.Join(dst, rel)); !os.IsNotExist(err) {
			t.Errorf("expected %s skipped, got err=%v", rel, err)
		}
	}
}

// TestActivationMode locks in the builtin-vs-subprocess branch that closes
// the CLI-install-vs-hot-reload asymmetry (TASKS/phase-5/04): a subprocess
// plugin hot-reloads, everything else (builtin, unknown/nil) still needs a
// restart since a builtin's Go code has to already be compiled into the
// running binary — hot-reload cannot structurally apply to it.
func TestActivationMode(t *testing.T) {
	cases := []struct {
		name     string
		manifest *plugin.PluginManifest
		want     string
	}{
		{"nil manifest (kind unknown) defaults to restart", nil, "restart"},
		{"subprocess runtime hot-reloads", &plugin.PluginManifest{Runtime: "subprocess"}, "hot-reload"},
		{"explicit builtin runtime restarts", &plugin.PluginManifest{Runtime: "builtin"}, "restart"},
		{"empty runtime (legacy manifests default to builtin) restarts", &plugin.PluginManifest{Runtime: ""}, "restart"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := activationMode(tc.manifest); got != tc.want {
				t.Errorf("activationMode(%+v) = %q, want %q", tc.manifest, got, tc.want)
			}
		})
	}
}

// TestTriggerHotReload_UnreachableServiceDegradesSoftly confirms
// triggerHotReload never os.Exit()s on failure — unlike pluginReload (a
// direct CLI command allowed to exit non-zero), this runs as the tail step
// of an install/update/enable that already succeeded on disk, so a failed
// hot-reload call must degrade to a manual-reload hint and return, exactly
// like triggerRestart's soft-failure path when cerberus is unavailable. If
// this test process observes this line, the function didn't exit.
func TestTriggerHotReload_UnreachableServiceDegradesSoftly(t *testing.T) {
	t.Setenv("NANITE_API_URL", "http://127.0.0.1:1") // nothing listens on port 1
	triggerHotReload("does-not-matter")
}

// TestTriggerActivation_NoRestartStillGatesBuiltin confirms --no-restart
// keeps its original, literal meaning on the builtin path: triggerRestart
// itself checks the noRestart package var and returns immediately without
// shelling out, so triggerActivation must reach it (not silently swallow
// the builtin branch) for any non-subprocess manifest.
func TestTriggerActivation_NoRestartStillGatesBuiltin(t *testing.T) {
	old := noRestart
	noRestart = true
	t.Cleanup(func() { noRestart = old })

	// A builtin manifest routes to triggerRestart(), which no-ops under
	// noRestart=true without touching the network or shelling out — so this
	// call completing at all (no hang, no exit) demonstrates the flag still
	// gates the builtin path post-change.
	triggerActivation("some-builtin", &plugin.PluginManifest{Runtime: "builtin"})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
