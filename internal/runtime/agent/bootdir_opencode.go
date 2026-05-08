package agent

import "errors"

// opencodeLayout plants opencode's config-dir shape:
//
//	<bootDir>/
//	├── agents/
//	│   └── <agentName>.md           # role-aware system prompt; referenced from agents.json + opencode.json
//	├── agents.json                  # {agents: [{name, description, instructions_file}]}
//	├── opencode.json                # {agent: {<agentName>: {prompt: "{file:./agents/<agentName>.md}"}}}
//	├── boot.md                      # task-specific kickoff
//	├── .sandbox/agent-context.md
//	├── .sandbox/envelope-schema.md
//	└── .mcp.json
//
// Spawn: opencode run --agent <agentName> --format json "Boot @./boot.md"
// Spawn cwd: <projectDir> (NOT bootDir). The boot dir is the *config* dir,
// surfaced via OPENCODE_CONFIG_DIR=<bootDir>.
type opencodeLayout struct{}

func (opencodeLayout) Setup(params SetupParams) (string, error) {
	_ = params
	return "", errors.New("agent.opencodeLayout.Setup: not yet implemented (skeleton — phase 2)")
}

// AmendEnv injects OPENCODE_CONFIG_DIR=<bootDir>.
func (opencodeLayout) AmendEnv(base map[string]string, bootDir string) map[string]string {
	out := make(map[string]string, len(base)+1)
	for k, v := range base {
		out[k] = v
	}
	out["OPENCODE_CONFIG_DIR"] = bootDir
	return out
}

// SpawnWorkdir returns the project dir (boot dir is the config dir, not cwd).
func (opencodeLayout) SpawnWorkdir(_, projectDir string) string { return projectDir }

func (opencodeLayout) BootPrompt(profile AgentProfile, opts Options) string {
	return composeSystemPrompt(opts.Role, profile, opts.Mode)
}

func (opencodeLayout) BootMode() string { return "" }
