package agent

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/fsutil"
	"github.com/hollis-labs/nanite/internal/store"
)

// claudeLayout plants nanite's claude-specific boot dir shape:
//
//	<bootDir>/
//	├── CLAUDE.md                    # role-aware system prompt + envelope rules
//	├── boot.md                      # task-specific kickoff (Boot @./boot.md target)
//	├── .sandbox/
//	│   ├── agent-context.md         # nanite agent identity + tool catalog reminders
//	│   └── envelope-schema.md       # SSE envelope schema reference
//	├── .claude/
//	│   └── settings.json            # {mcpServers: {}, approvedTools: []} stub (no global ~/.claude.json bleed)
//	└── .mcp.json                    # nanite MCP subprocess descriptor
//
// Spawn cwd: <bootDir>; project access: claude --add-dir <projectDir>.
type claudeLayout struct{}

const claudeSettingsJSONStub = `{
  "mcpServers": {},
  "approvedTools": []
}
`

func (l claudeLayout) Setup(params SetupParams) (string, error) {
	bootDir, err := makeBootDir("claude", params)
	if err != nil {
		return "", err
	}
	if err := l.Populate(bootDir, params); err != nil {
		// On any post-mkdir failure, clean up the partial boot dir so
		// callers don't leak $TMPDIR entries.
		_ = os.RemoveAll(bootDir)
		return "", err
	}
	return bootDir, nil
}

// Populate writes the claude boot-dir shape into bootDir. Idempotent —
// every file is replaced via fsutil.AtomicWriteFile; mkdir calls use
// MkdirAll so a partially-populated dir converges. Used by both Setup
// (post-mkdir) and the recovery BootDirOps adapter (against an existing
// dir).
func (claudeLayout) Populate(bootDir string, params SetupParams) error {
	if params.AgentProfile == nil {
		return fmt.Errorf("agent: claudeLayout.Populate: AgentProfile is required")
	}

	if err := fsutil.AtomicWriteFile(
		filepath.Join(bootDir, "CLAUDE.md"),
		[]byte(BuildCLAUDEMD(params.AgentProfile.Name, params.AgentProfile.Description)),
		0o644,
	); err != nil {
		return fmt.Errorf("agent: write CLAUDE.md: %w", err)
	}

	if err := plantBootMD(bootDir, params); err != nil {
		return err
	}

	if err := plantSandboxFiles(bootDir, params); err != nil {
		return err
	}

	claudeDir := filepath.Join(bootDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return fmt.Errorf("agent: mkdir .claude/: %w", err)
	}
	if err := fsutil.AtomicWriteFile(
		filepath.Join(claudeDir, "settings.json"),
		[]byte(claudeSettingsJSONStub),
		0o644,
	); err != nil {
		return fmt.Errorf("agent: write .claude/settings.json: %w", err)
	}

	if err := writeMCPJSON(bootDir, params.MCPConfig, params.SessionID); err != nil {
		return err
	}

	return nil
}

// RegenerateSystemPromptSlot rewrites only CLAUDE.md, leaving the rest
// of the sandbox dir intact. Used by recovery.BootDirOps.RegenerateCLAUDEMD
// for watchdog_kill remediation.
func (claudeLayout) RegenerateSystemPromptSlot(bootDir string, params SetupParams) error {
	if params.AgentProfile == nil {
		return fmt.Errorf("agent: claudeLayout.RegenerateSystemPromptSlot: AgentProfile is required")
	}
	if err := fsutil.AtomicWriteFile(
		filepath.Join(bootDir, "CLAUDE.md"),
		[]byte(BuildCLAUDEMD(params.AgentProfile.Name, params.AgentProfile.Description)),
		0o644,
	); err != nil {
		return fmt.Errorf("agent: regenerate CLAUDE.md: %w", err)
	}
	return nil
}

func (claudeLayout) AmendEnv(base map[string]string, _ string) map[string]string { return base }

// SpawnWorkdir returns the boot dir; CLAUDE.md auto-loads from cwd.
func (claudeLayout) SpawnWorkdir(bootDir, _ string) string { return bootDir }

// BootPrompt is the system prompt payload for the PTY runtime.
// Sourced from resolveBootPrompt (prompt.go), which honors
// Options.BootPromptOverride (CW-20260514-0048) and falls back to
// composeSystemPrompt(role, profile, mode) otherwise.
func (claudeLayout) BootPrompt(profile *store.AgentProfile, opts Options) string {
	return resolveBootPrompt(profile, opts)
}

// BootMode is "stdin" for PTY claude; the runtime writes the boot prompt
// onto the master PTY at process start.
func (claudeLayout) BootMode() string { return "stdin" }
