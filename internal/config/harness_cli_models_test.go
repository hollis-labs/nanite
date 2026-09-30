package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadYAML(t *testing.T, body string) (*TunablesConfig, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "nanite.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadAppConfig(p)
}

func TestHarnessCLIModelsValid(t *testing.T) {
	cfg, err := loadYAML(t, "harness:\n  cli_models:\n    claude: claude-opus-5\n    codex: claude-sonnet-5\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Harness.CLIModels["claude"] != "claude-opus-5" || cfg.Harness.CLIModels["codex"] != "claude-sonnet-5" {
		t.Errorf("cli_models = %v", cfg.Harness.CLIModels)
	}
	// Absent is fine, and is the default.
	cfg, err = loadYAML(t, "harness:\n  profile: dev\n")
	if err != nil || len(cfg.Harness.CLIModels) != 0 {
		t.Errorf("absent cli_models: %v %v", cfg.Harness.CLIModels, err)
	}
	if DefaultAppConfig().Harness.CLIModels != nil {
		t.Error("default config must declare no CLI models")
	}
}

// A name that does not exist fails at load, and the error is the typed one main
// aborts on rather than falling back to defaults.
func TestHarnessCLIModelsFailLoudly(t *testing.T) {
	for name, body := range map[string]string{
		"unknown model":        "harness:\n  cli_models:\n    claude: claude-opus-99\n",
		"pseudo CLI model":     "harness:\n  cli_models:\n    claude: claude-cli\n",
		"empty model":          "harness:\n  cli_models:\n    claude: \"\"\n",
		"unknown CLI kind":     "harness:\n  cli_models:\n    claud: claude-opus-5\n",
		"provider, not a kind": "harness:\n  cli_models:\n    pty-claude: claude-opus-5\n",
	} {
		_, err := loadYAML(t, body)
		if !errors.Is(err, ErrInvalidHarnessConfig) {
			t.Errorf("%s: err = %v, want ErrInvalidHarnessConfig", name, err)
		}
	}
	_, err := loadYAML(t, "harness:\n  cli_models:\n    claud: claude-opus-5\n    claude: nope\n")
	if err == nil || !strings.Contains(err.Error(), `unknown CLI kind "claud"`) || !strings.Contains(err.Error(), `"nope"`) {
		t.Errorf("every problem should be reported: %v", err)
	}
	// Other parse failures are not this error (main still falls back for them).
	if _, err := loadYAML(t, "harness: [not, a, map]\n"); err == nil || errors.Is(err, ErrInvalidHarnessConfig) {
		t.Errorf("malformed yaml: %v", err)
	}
}
