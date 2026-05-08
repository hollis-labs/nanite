package agent

import (
	"errors"

	"github.com/hollis-labs/nanite/internal/store"
)

// claudeLayout plants nanite's claude-specific boot dir shape:
//
//	<bootDir>/
//	├── CLAUDE.md                    # role-aware system prompt
//	├── boot.md                      # task-specific kickoff (Boot @./boot.md target)
//	├── .sandbox/
//	│   ├── agent-context.md         # nanite agent identity + tool catalog reminders
//	│   └── envelope-schema.md       # SSE envelope schema reference
//	├── .claude/
//	│   └── settings.json            # {mcpServers: {}, approvedTools: []} stub (no global ~/.claude.json bleed)
//	└── .mcp.json                    # nanite MCP loopback URL
//
// Spawn cwd: <bootDir>; project access: claude --add-dir <projectDir>.
//
// Vocabulary (.sandbox/agent-context.md + .sandbox/envelope-schema.md) is
// nanite's portfolio differentiator — preserved per cross-app design §7.
//
// Concrete planting moves here from internal/plugin/builtin/adapter-claude/plugin.go:166-191
// in Phase 4c. Phase 2 ships the shape and a stubbed Setup.
type claudeLayout struct{}

func (claudeLayout) Setup(params SetupParams) (string, error) {
	_ = params
	return "", errors.New("agent.claudeLayout.Setup: not yet implemented (skeleton — phase 2)")
}

func (claudeLayout) AmendEnv(base map[string]string, _ string) map[string]string { return base }

// SpawnWorkdir returns the boot dir; CLAUDE.md auto-loads from cwd.
func (claudeLayout) SpawnWorkdir(bootDir, _ string) string { return bootDir }

// BootPrompt is the system prompt payload for the PTY runtime.
// Sourced from composeSystemPrompt(role, profile, mode); see prompt.go.
func (claudeLayout) BootPrompt(profile *store.AgentProfile, opts Options) string {
	return composeSystemPrompt(opts.Role, profile, opts.Mode)
}

// BootMode is "stdin" for PTY claude; the runtime writes the boot prompt
// onto the master PTY at process start.
func (claudeLayout) BootMode() string { return "stdin" }
