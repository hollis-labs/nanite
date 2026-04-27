package agent

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnsureHomeDirs creates the user-level agent directory (~/.nanite/agents/)
// on first run if it does not yet exist. homeDir overrides os.UserHomeDir()
// for testing; pass "" to use the real home directory.
//
// This is a no-op when the directory already exists. The function is called
// once at container startup so that dropping an agent file into the directory
// immediately works on the next load without any install ceremony.
//
// H1 trust note (CW-20260421-0014): agents discovered from this directory
// carry Source="user". When J7 (CW-20260421-0011) ingests them into the DB,
// it must set default_trust_tier="untrusted" per the H1 trust model decision.
// J7 should call this loader's Discover result and inspect Definition.Source
// to apply the correct tier before any INSERT into agent_profiles.
func EnsureHomeDirs(homeDir string) error {
	home := homeDir
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("agent: resolve home dir: %w", err)
		}
	}

	agentsDir := filepath.Join(home, ".nanite", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		return fmt.Errorf("agent: ensure ~/.nanite/agents/: %w", err)
	}
	return nil
}
