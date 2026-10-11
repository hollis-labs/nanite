package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// makeBootDir creates the ephemeral $TMPDIR/nanite-boot-<provider>-<sessID>-r<runID>-XXXXXX/
// directory and returns its absolute path. The XXXXXX suffix is generated
// by os.MkdirTemp so concurrent boots don't collide.
//
// CW-20260515-0025: the forensic naming scheme (provider + session + run
// id) is deliberately Nanite-owned and is NOT delegated to
// go-agent-launch's launcher.allocateBootDir, whose scheme keys on a
// plan hash instead. Operators correlate $TMPDIR entries to sessions by
// this prefix; keeping the scheme app-side preserves that affordance.
func makeBootDir(provider string, params SetupParams) (string, error) {
	prefix := fmt.Sprintf("nanite-boot-%s-%s-r%s-", provider, params.SessionID, defaultIfEmpty(params.RunID, "0"))
	dir, err := mkdirTempCanonical(prefix + "*")
	if err != nil {
		return "", fmt.Errorf("agent: mkdir boot dir for %s: %w", provider, err)
	}
	return dir, nil
}

// mkdirTempCanonical is os.MkdirTemp in the default temp directory, returning
// the symlink-resolved path. The Harness workspace authority (InspectRoot)
// refuses any root that is not already in physical form, so a TMPDIR reached
// through a symlink must not leak into the root it is handed.
func mkdirTempCanonical(pattern string) (string, error) {
	dir, err := os.MkdirTemp("", pattern)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		_ = os.Remove(dir)
		return "", err
	}
	return canonical, nil
}

// agentSlug returns a filesystem-safe identifier derived from the profile.
// Falls back to "agent" when the profile is nil or has no usable name.
var slugSanitize = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func agentSlug(params SetupParams) string {
	if params.AgentProfile != nil {
		if s := strings.TrimSpace(params.AgentProfile.Slug); s != "" {
			return slugSanitize.ReplaceAllString(strings.ToLower(s), "-")
		}
		if n := strings.TrimSpace(params.AgentProfile.Name); n != "" {
			return slugSanitize.ReplaceAllString(strings.ToLower(n), "-")
		}
	}
	return "agent"
}

func defaultIfEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
