package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultAppConfigUsesLoopbackBindAddress(t *testing.T) {
	cfg := DefaultAppConfig()
	if got, want := cfg.HTTP.BindAddress, "127.0.0.1"; got != want {
		t.Fatalf("HTTP.BindAddress = %q, want %q", got, want)
	}
}

func TestLoadAppConfigBindAddressOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nanite.yaml")
	if err := os.WriteFile(path, []byte("http:\n  bind_address: 0.0.0.0\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadAppConfig(path)
	if err != nil {
		t.Fatalf("LoadAppConfig: %v", err)
	}
	if got, want := cfg.HTTP.BindAddress, "0.0.0.0"; got != want {
		t.Fatalf("HTTP.BindAddress = %q, want %q", got, want)
	}
}
