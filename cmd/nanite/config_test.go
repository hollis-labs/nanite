package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstalledConfigDiscovery(t *testing.T) {
	t.Setenv("SUBSTRATE_CONFIG", "")
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	restore, err := useConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	restore()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("discovery created files: %v %v", entries, err)
	}
	path := filepath.Join(dir, substrateApp, "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("logging: {level: error}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	restore, err = useConfigFile()
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
	t.Setenv("SUBSTRATE_CONFIG", "/explicit/config.yaml")
	restore, err = useConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	if got := os.Getenv("SUBSTRATE_CONFIG"); got != "/explicit/config.yaml" {
		t.Fatalf("explicit config replaced: %q", got)
	}
}
