package install

import (
	"os"
	"path/filepath"
	"strings"
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

func TestPersistAdapterList_PreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := `nanite_version: 2.3.0

agents:
  frontend:
    name: Frontend Developer
    description: React/Tailwind frontend
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := persistAdapterList(path, []string{"claude", "codex"}); err != nil {
		t.Fatalf("persistAdapterList: %v", err)
	}

	got, _ := os.ReadFile(path)
	gotStr := string(got)

	if !strings.Contains(gotStr, "nanite_version: 2.3.0") {
		t.Errorf("nanite_version missing: %q", gotStr)
	}
	if !strings.Contains(gotStr, "frontend:") {
		t.Errorf("agent definition missing: %q", gotStr)
	}
	if !strings.Contains(gotStr, "Frontend Developer") {
		t.Errorf("agent name missing: %q", gotStr)
	}
	if !strings.Contains(gotStr, "adapters:") {
		t.Errorf("adapters key missing: %q", gotStr)
	}
	if !strings.Contains(gotStr, "claude") {
		t.Errorf("claude entry missing: %q", gotStr)
	}
	if !strings.Contains(gotStr, "codex") {
		t.Errorf("codex entry missing: %q", gotStr)
	}

	// Round-trip via loadProjectConfig to verify it parses cleanly.
	cfg, err := loadProjectConfig(path)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if cfg.Adapters == nil || len(*cfg.Adapters) != 2 {
		t.Errorf("re-parsed adapters: %v", cfg.Adapters)
	}
	if (*cfg.Adapters)[0] != "claude" || (*cfg.Adapters)[1] != "codex" {
		t.Errorf("re-parsed order wrong: %v", *cfg.Adapters)
	}
}

func TestPersistAdapterList_EmptyList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := "nanite_version: 2.3.0\nagents: {}\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := persistAdapterList(path, []string{}); err != nil {
		t.Fatalf("persistAdapterList: %v", err)
	}

	cfg, err := loadProjectConfig(path)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if cfg.Adapters == nil {
		t.Fatal("expected non-nil Adapters (empty != absent)")
	}
	if len(*cfg.Adapters) != 0 {
		t.Errorf("expected empty list, got %v", *cfg.Adapters)
	}
}

func TestPersistAdapterList_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := `nanite_version: 2.3.0
adapters:
  - claude
  - codex
agents: {}
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := persistAdapterList(path, []string{"gemini"}); err != nil {
		t.Fatalf("persistAdapterList: %v", err)
	}

	cfg, err := loadProjectConfig(path)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if cfg.Adapters == nil || len(*cfg.Adapters) != 1 || (*cfg.Adapters)[0] != "gemini" {
		t.Errorf("expected [gemini], got %v", cfg.Adapters)
	}
}
