package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hollis-labs/nanite/internal/fsutil"
)

// makeBootDir creates the ephemeral $TMPDIR/nanite-boot-<provider>-<sessID>-r<runID>-XXXXXX/
// directory and returns its absolute path. The XXXXXX suffix is generated
// by os.MkdirTemp so concurrent boots don't collide.
func makeBootDir(provider string, params SetupParams) (string, error) {
	prefix := fmt.Sprintf("nanite-boot-%s-%s-r%s-", provider, params.SessionID, defaultIfEmpty(params.RunID, "0"))
	dir, err := os.MkdirTemp("", prefix+"*")
	if err != nil {
		return "", fmt.Errorf("agent: mkdir boot dir for %s: %w", provider, err)
	}
	return dir, nil
}

// plantSandboxFiles writes the .sandbox/agent-context.md and
// .sandbox/envelope-schema.md files shared across every nanite-managed
// boot dir. Returns the .sandbox/ subdir path on success.
func plantSandboxFiles(bootDir string, params SetupParams) error {
	sandboxDir := filepath.Join(bootDir, ".sandbox")
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		return fmt.Errorf("agent: mkdir .sandbox/: %w", err)
	}

	if err := fsutil.AtomicWriteFile(
		filepath.Join(sandboxDir, "agent-context.md"),
		[]byte(BuildAgentContext(params.AgentProfile, nil)),
		0o644,
	); err != nil {
		return fmt.Errorf("agent: write .sandbox/agent-context.md: %w", err)
	}

	if err := fsutil.AtomicWriteFile(
		filepath.Join(sandboxDir, "envelope-schema.md"),
		[]byte(envelopeSchemaContent),
		0o644,
	); err != nil {
		return fmt.Errorf("agent: write .sandbox/envelope-schema.md: %w", err)
	}

	return nil
}

// plantBootMD writes the boot.md kickoff target into bootDir.
func plantBootMD(bootDir string, params SetupParams) error {
	if err := fsutil.AtomicWriteFile(
		filepath.Join(bootDir, "boot.md"),
		[]byte(params.BootContent),
		0o644,
	); err != nil {
		return fmt.Errorf("agent: write boot.md: %w", err)
	}
	return nil
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
