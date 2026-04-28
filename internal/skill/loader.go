package skill

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnsureHomeDirs creates the user-level skill directory (~/.nanite/skills/)
// on first run if it does not yet exist. homeDir overrides os.UserHomeDir()
// for testing; pass "" to use the real home directory.
//
// This is a no-op when the directory already exists. The function is called
// once at container startup so that dropping a skill file into the directory
// immediately works on the next load without any install ceremony.
func EnsureHomeDirs(homeDir string) error {
	home := homeDir
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("skill: resolve home dir: %w", err)
		}
	}

	skillsDir := filepath.Join(home, ".nanite", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return fmt.Errorf("skill: ensure ~/.nanite/skills/: %w", err)
	}
	return nil
}
