package agent

import "errors"

// codexLayout plants the codex-specific shape:
//
//	<bootDir>/
//	├── AGENTS.md                    # codex auto-loads from cwd
//	├── boot.md
//	├── .sandbox/agent-context.md
//	├── .sandbox/envelope-schema.md
//	└── .mcp.json
//
// Spawn cwd: <bootDir>; project access via codex's --cd flag (per
// go-providers v0.8.0 BootDirSpec, verified once — may iterate).
type codexLayout struct{}

func (codexLayout) Setup(params SetupParams) (string, error) {
	_ = params
	return "", errors.New("agent.codexLayout.Setup: not yet implemented (skeleton — phase 2)")
}

func (codexLayout) AmendEnv(base map[string]string, _ string) map[string]string { return base }

func (codexLayout) SpawnWorkdir(bootDir, _ string) string { return bootDir }

func (codexLayout) BootPrompt(profile AgentProfile, opts Options) string {
	return composeSystemPrompt(opts.Role, profile, opts.Mode)
}

// BootMode is empty for the subprocess-per-turn codex path; the boot
// prompt threads as the first user message.
func (codexLayout) BootMode() string { return "" }
