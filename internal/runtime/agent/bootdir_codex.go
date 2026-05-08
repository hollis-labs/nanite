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

func (codexLayout) Setup(params SetupParams) (string, error) {
	bootDir, err := makeBootDir("codex", params)
	if err != nil {
		return "", err
	}

	cleanup := func(setupErr error) (string, error) {
		_ = os.RemoveAll(bootDir)
		return "", setupErr
	}

	if params.AgentProfile == nil {
		return cleanup(fmt.Errorf("agent: codexLayout.Setup: AgentProfile is required"))
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
		return cleanup(fmt.Errorf("agent: write AGENTS.md: %w", err))
	}

	if err := plantBootMD(bootDir, params); err != nil {
		return cleanup(err)
	}

	if err := plantSandboxFiles(bootDir, params); err != nil {
		return cleanup(err)
	}

	if err := writeMCPJSON(bootDir, params.MCPConfig, params.SessionID); err != nil {
		return cleanup(err)
	}

	return bootDir, nil
}

func (codexLayout) AmendEnv(base map[string]string, _ string) map[string]string { return base }

func (codexLayout) SpawnWorkdir(bootDir, _ string) string { return bootDir }

func (codexLayout) BootPrompt(profile *store.AgentProfile, opts Options) string {
	return composeSystemPrompt(opts.Role, profile, opts.Mode)
}

// BootMode is empty for the subprocess-per-turn codex path; the boot
// prompt threads as the first user message.
func (codexLayout) BootMode() string { return "" }
