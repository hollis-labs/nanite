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
