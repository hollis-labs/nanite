package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultAppConfigUsesLoopbackBindAddress(t *testing.T) {
	cfg := DefaultAppConfig()
	if got, want := cfg.HTTP.BindAddress, "127.0.0.1"; got != want {
		t.Fatalf("HTTP.BindAddress = %q, want %q", got, want)
	}
}

func TestResolveHTTPBindAddress(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		want        string
		wantErrText string
	}{
		{name: "empty uses loopback default", value: "", want: DefaultHTTPBindAddress},
		{name: "wide IPv4", value: "0.0.0.0", want: "0.0.0.0"},
		{name: "specific IPv4", value: "192.0.2.10", want: "192.0.2.10"},
		{name: "localhost hostname", value: "localhost", want: "localhost"},
		{name: "DNS hostname", value: "nanite.internal", want: "nanite.internal"},
		{name: "raw IPv6 loopback", value: "::1", want: "::1"},
		{name: "raw IPv6 wide", value: "::", want: "::"},
		{name: "raw IPv6 address", value: "2001:db8::10", want: "2001:db8::10"},
		{name: "whitespace only", value: " ", wantErrText: "surrounding whitespace"},
		{name: "surrounding whitespace", value: " localhost ", wantErrText: "surrounding whitespace"},
		{name: "bracketed IPv6", value: "[::1]", wantErrText: "unbracketed"},
		{name: "bracketed IPv6 with port", value: "[::1]:8090", wantErrText: "unbracketed"},
		{name: "IPv4 with port", value: "127.0.0.1:8090", wantErrText: "host only without a port"},
		{name: "hostname with port", value: "localhost:8090", wantErrText: "host only without a port"},
		{name: "malformed IPv4", value: "999.0.0.1", wantErrText: "valid ASCII hostname or raw IPv4/IPv6"},
		{name: "malformed IPv6", value: "2001:db8::zz", wantErrText: "valid raw unbracketed IPv6"},
		{name: "malformed hostname character", value: "bad_host", wantErrText: "valid ASCII hostname"},
		{name: "malformed empty hostname label", value: "bad..host", wantErrText: "valid ASCII hostname"},
		{name: "malformed hostname edge hyphen", value: "-bad.example", wantErrText: "valid ASCII hostname"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveHTTPBindAddress(tt.value)
			if tt.wantErrText != "" {
				if err == nil {
					t.Fatalf("ResolveHTTPBindAddress(%q) = %q, want error containing %q", tt.value, got, tt.wantErrText)
				}
				if !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("error = %q, want substring %q", err, tt.wantErrText)
				}
				if !strings.Contains(err.Error(), "http.bind_address") {
					t.Fatalf("error = %q, want option name", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveHTTPBindAddress(%q): %v", tt.value, err)
			}
			if got != tt.want {
				t.Fatalf("ResolveHTTPBindAddress(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
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

func TestLoadAppConfigRejectsInvalidBindAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nanite.yaml")
	if err := os.WriteFile(path, []byte("http:\n  bind_address: localhost:8090\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := LoadAppConfig(path)
	if err == nil {
		t.Fatal("LoadAppConfig accepted bind_address carrying a port")
	}
	if !strings.Contains(err.Error(), "http.bind_address") || !strings.Contains(err.Error(), "host only") {
		t.Fatalf("LoadAppConfig error = %q, want host-only bind diagnostic", err)
	}
}
