package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// HomeSkillsDir returns the absolute path to ~/.nanite/skills/. homeDir
// overrides os.UserHomeDir() for testing; pass "" to use the real home dir.
func HomeSkillsDir(homeDir string) (string, error) {
	home := homeDir
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("skill: resolve home dir: %w", err)
		}
	}
	return filepath.Join(home, ".nanite", "skills"), nil
}

// WriteUserSkillFile writes a markdown skill file at ~/.nanite/skills/<slug>.md.
// The directory is created on demand. Returns the resolved absolute file path.
// E1 (CW-20260428-0016): used by the dev-mode "fork to user override" flow,
// which produces a mutable copy of an internal skill in the user folder.
func WriteUserSkillFile(homeDir, slug, body string) (string, error) {
	if slug == "" {
		return "", fmt.Errorf("skill: slug is required")
	}
	// Reject any slug that could escape ~/.nanite/skills/ via path traversal.
	// The dev-mode fork endpoint reaches this path with caller-supplied data,
	// so anything a filesystem could interpret as a separator or parent ref
	// has to be rejected before joining.
	if strings.ContainsAny(slug, `/\`) ||
		strings.Contains(slug, "..") ||
		filepath.Base(slug) != slug ||
		filepath.IsAbs(slug) {
		return "", fmt.Errorf("skill: invalid slug %q", slug)
	}
	dir, err := HomeSkillsDir(homeDir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("skill: ensure user skills dir: %w", err)
	}
	target := filepath.Join(dir, slug+".md")
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		return "", fmt.Errorf("skill: write %s: %w", target, err)
	}
	return target, nil
}
