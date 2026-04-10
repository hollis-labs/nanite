package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProjectConfig_TriStateAdapters(t *testing.T) {
	cases := []struct {
		name        string
		yaml        string
		wantNil     bool
		wantLen     int
		wantContent []string
	}{
		{
			name:    "key absent",
			yaml:    "nanite_version: 2.3.0\nagents: {}\n",
			wantNil: true,
		},
		{
			name:    "key present empty",
			yaml:    "nanite_version: 2.3.0\nadapters: []\nagents: {}\n",
			wantNil: false,
			wantLen: 0,
		},
		{
			name:        "key present populated",
			yaml:        "nanite_version: 2.3.0\nadapters:\n  - claude\n  - codex\nagents: {}\n",
			wantNil:     false,
			wantLen:     2,
			wantContent: []string{"claude", "codex"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadProjectConfig(path)
			if err != nil {
				t.Fatalf("loadProjectConfig: %v", err)
			}
			if tc.wantNil {
				if cfg.Adapters != nil {
					t.Fatalf("expected nil Adapters, got %v", *cfg.Adapters)
				}
				return
			}
			if cfg.Adapters == nil {
				t.Fatal("expected non-nil Adapters")
			}
			if len(*cfg.Adapters) != tc.wantLen {
				t.Fatalf("len: got %d, want %d", len(*cfg.Adapters), tc.wantLen)
			}
			for i, want := range tc.wantContent {
				if (*cfg.Adapters)[i] != want {
					t.Errorf("[%d]: got %q, want %q", i, (*cfg.Adapters)[i], want)
				}
			}
		})
	}
}

func TestLoadProjectConfig_MissingFileReturnsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	cfg, err := loadProjectConfig(path)
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil *projectConfig, got nil")
	}
	if cfg.Adapters != nil {
		t.Errorf("expected nil Adapters, got %v", *cfg.Adapters)
	}
	if cfg.Agents != nil {
		t.Errorf("expected nil Agents, got %v", cfg.Agents)
	}
}

func TestLoadProjectConfig_ParseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	// Malformed YAML: unterminated flow sequence.
	if err := os.WriteFile(path, []byte("adapters: [claude\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadProjectConfig(path)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if cfg != nil {
		t.Errorf("expected nil *projectConfig on parse error, got %+v", cfg)
	}
}
