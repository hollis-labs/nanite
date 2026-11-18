package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInstalledConfigDiscovery(t *testing.T) {
	t.Setenv("SUBSTRATE_CONFIG", "")
	t.Setenv("NANITE_CONFIG", "")
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	restore, err := useConfigFile(false)
	if err != nil {
		t.Fatal(err)
	}
	restore()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("discovery created files: %v %v", entries, err)
	}
	path := filepath.Join(dir, substrateApp, "nanite.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("logging: {level: error}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	restore, err = useConfigFile(false)
	if err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("SUBSTRATE_CONFIG"); got != path {
		t.Fatalf("config = %q, want %q", got, path)
	}
	restore()
	if got := os.Getenv("SUBSTRATE_CONFIG"); got != "" {
		t.Fatalf("restore left %q", got)
	}
	t.Setenv("NANITE_CONFIG", "/nanite-override.yaml")
	restore, err = useConfigFile(false)
	if err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("SUBSTRATE_CONFIG"); got != "/nanite-override.yaml" {
		t.Fatalf("Nanite override = %q", got)
	}
	restore()
	t.Setenv("SUBSTRATE_CONFIG", "/explicit/config.yaml")
	restore, err = useConfigFile(false)
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	if got := os.Getenv("SUBSTRATE_CONFIG"); got != "/explicit/config.yaml" {
		t.Fatalf("explicit config replaced: %q", got)
	}
}

func TestCommandsIgnoreConfigAndKeepWorkingDirectoryClean(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("SUBSTRATE_CONFIG", "")
	t.Setenv("NANITE_CONFIG", "")
	configDir := filepath.Join(xdg, "nanite")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(configDir, "config.yaml")
	bad := []byte("dev_tools_allowed_paths: [legacy]\n")
	if err := os.WriteFile(legacy, bad, 0600); err != nil {
		t.Fatal(err)
	}
	restore, err := useConfigFile(false)
	if err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("SUBSTRATE_CONFIG"); got != "" {
		t.Fatalf("legacy config was selected: %q", got)
	}
	restore()
	if err := runArgs(context.Background(), []string{"version"}); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(configDir, "nanite.yaml")
	if err := os.WriteFile(broken, []byte("broken: ["), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUBSTRATE_CONFIG", broken)
	t.Setenv("NANITE_CONFIG", broken)
	target := filepath.Join(t.TempDir(), "agent")
	for _, args := range [][]string{{"version"}, {"init", target}, {"-C", target, "validate"}, {"--help"}} {
		if err := runArgs(context.Background(), args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	entries, err := os.ReadDir(cwd)
	if err != nil || len(entries) != 0 {
		t.Fatalf("working directory contains %v, error %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(target, ".nanite")); !os.IsNotExist(err) {
		t.Fatalf("unexpected agent state: %v", err)
	}
	content, err := os.ReadFile(legacy) // #nosec G304 -- Reads a fixture under the test temporary directory.
	if err != nil || string(content) != string(bad) {
		t.Fatalf("legacy config changed: %v", err)
	}
	if got := os.Getenv("SUBSTRATE_CONFIG"); got != broken {
		t.Fatalf("override restored as %q", got)
	}
}
