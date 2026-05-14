package agent

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/fsutil"
	"github.com/hollis-labs/nanite/internal/store"
)

// codexLayout plants the codex-specific shape:
//
//	<bootDir>/
//	├── AGENTS.md                    # codex auto-loads from cwd (provider.AgentsMD shape)
//	├── boot.md
//	├── .sandbox/agent-context.md
//	├── .sandbox/envelope-schema.md
//	└── .mcp.json
//
// Spawn cwd: <bootDir>; project access via codex's --cd flag.
type codexLayout struct{}

func (l codexLayout) Setup(params SetupParams) (string, error) {
	bootDir, err := makeBootDir("codex", params)
	if err != nil {
		return "", err
	}
	if err := l.Populate(bootDir, params); err != nil {
		_ = os.RemoveAll(bootDir)
		return "", err
	}
	return bootDir, nil
}

// Populate writes the codex boot-dir shape into bootDir. Idempotent.
func (l codexLayout) Populate(bootDir string, params SetupParams) error {
	if params.AgentProfile == nil {
		return fmt.Errorf("agent: codexLayout.Populate: AgentProfile is required")
	}

	if err := l.RegenerateSystemPromptSlot(bootDir, params); err != nil {
		return err
	}

	if err := plantBootMD(bootDir, params); err != nil {
		return err
	}

	if err := plantSandboxFiles(bootDir, params); err != nil {
		return err
	}

	if err := writeMCPJSON(bootDir, params.MCPConfig, params.SessionID); err != nil {
		return err
	}

	return nil
}

// RegenerateSystemPromptSlot rewrites only AGENTS.md, leaving the rest
// of the sandbox dir intact. Codex's system-prompt-bearing slot is
// AGENTS.md (the cwd-loaded file analogous to claude's CLAUDE.md).
func (codexLayout) RegenerateSystemPromptSlot(bootDir string, params SetupParams) error {
	if params.AgentProfile == nil {
		return fmt.Errorf("agent: codexLayout.RegenerateSystemPromptSlot: AgentProfile is required")
	}
	agentsBody := provider.AgentsMD(provider.AgentInfo{
		Name:         params.AgentProfile.Name,
		Role:         "", // codex flow doesn't carry a separate role taxonomy yet
		Description:  params.AgentProfile.Description,
		SystemPrompt: params.SystemPrompt,
	}, "")
	if err := fsutil.AtomicWriteFile(
		filepath.Join(bootDir, "AGENTS.md"),
		[]byte(agentsBody),
		0o644,
	); err != nil {
		return fmt.Errorf("agent: regenerate AGENTS.md: %w", err)
	}
	return nil
}

func (codexLayout) AmendEnv(base map[string]string, _ string) map[string]string { return base }

func (codexLayout) SpawnWorkdir(bootDir, _ string) string { return bootDir }

func (codexLayout) BootPrompt(profile *store.AgentProfile, opts Options) string {
	return resolveBootPrompt(profile, opts)
}

// BootMode is empty for the subprocess-per-turn codex path; the boot
// prompt threads as the first user message.
func (codexLayout) BootMode() string { return "" }
