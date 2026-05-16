package agent

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/hollis-labs/go-agent-launch/agentlaunch"
	"github.com/hollis-labs/nanite/internal/store"
)

// opencodeLayout plants opencode's config-dir shape:
//
//	<bootDir>/
//	├── agents/
//	│   └── <agentSlug>.md           # role-aware system prompt; referenced from agents.json + opencode.json
//	├── agents.json                  # {agents: [{name, description, instructions_file}]}
//	├── opencode.json                # {agent: {<agentSlug>: {prompt: "{file:./agents/<agentSlug>.md}"}}}
//	├── boot.md                      # task-specific kickoff
//	├── .sandbox/agent-context.md
//	├── .sandbox/envelope-schema.md
//	└── .mcp.json
//
// Spawn: opencode run --agent <agentSlug> ... "Boot @./boot.md".
// Spawn cwd: <projectDir> (NOT bootDir). The boot dir is the *config* dir,
// surfaced via OPENCODE_CONFIG_DIR=<bootDir>.
//
// CW-20260515-0025: the bootdir file-set is declared as an
// agentlaunch.InjectionSpec and written via plantInjectionSpec — see
// bootdir_plant.go.
type opencodeLayout struct{}

// opencodeAgentMD renders the agents/<slug>.md system-prompt body.
// Opencode's system-prompt-bearing slot is the per-slug agent file
// (analogous to claude's CLAUDE.md).
func opencodeAgentMD(params SetupParams) string {
	return fmt.Sprintf("# %s\n\n%s\n", params.AgentProfile.Name, params.SystemPrompt)
}

// opencodeAgentsJSON renders the agents.json descriptor body.
func opencodeAgentsJSON(params SetupParams, slug string) (string, error) {
	agentsJSON := map[string]any{
		"agents": []map[string]any{
			{
				"name":              slug,
				"description":       params.AgentProfile.Description,
				"instructions_file": fmt.Sprintf("./agents/%s.md", slug),
			},
		},
	}
	b, err := json.MarshalIndent(agentsJSON, "", "  ")
	if err != nil {
		return "", fmt.Errorf("agent: marshal agents.json: %w", err)
	}
	return string(b), nil
}

// opencodeJSON renders the opencode.json descriptor body.
func opencodeJSON(slug string) (string, error) {
	opencodeJSON := map[string]any{
		"agent": map[string]any{
			slug: map[string]any{
				"prompt": fmt.Sprintf("{file:./agents/%s.md}", slug),
			},
		},
	}
	b, err := json.MarshalIndent(opencodeJSON, "", "  ")
	if err != nil {
		return "", fmt.Errorf("agent: marshal opencode.json: %w", err)
	}
	return string(b), nil
}

// opencodeInjectionSpec assembles the full opencode bootdir file-set as a
// shared agentlaunch.InjectionSpec.
func opencodeInjectionSpec(params SetupParams) (agentlaunch.InjectionSpec, error) {
	slug := agentSlug(params)

	agentsJSONBody, err := opencodeAgentsJSON(params, slug)
	if err != nil {
		return agentlaunch.InjectionSpec{}, err
	}
	opencodeJSONBody, err := opencodeJSON(slug)
	if err != nil {
		return agentlaunch.InjectionSpec{}, err
	}

	native := []agentlaunch.NativeFile{
		nativeFileRaw(fmt.Sprintf("agents/%s.md", slug), opencodeAgentMD(params), 0o644),
		nativeFileRaw("agents.json", agentsJSONBody, 0o644),
		nativeFileRaw("opencode.json", opencodeJSONBody, 0o644),
		bootMDNativeFile(params),
	}
	native = append(native, sandboxNativeFiles(params)...)

	overlay, err := mcpOverlay(params)
	if err != nil {
		return agentlaunch.InjectionSpec{}, err
	}
	return agentlaunch.InjectionSpec{NativeFiles: native, BootDirOverlay: overlay}, nil
}

func (l opencodeLayout) Setup(params SetupParams) (string, error) {
	bootDir, err := makeBootDir("opencode", params)
	if err != nil {
		return "", err
	}
	if err := l.Populate(bootDir, params); err != nil {
		_ = os.RemoveAll(bootDir)
		return "", err
	}
	return bootDir, nil
}

// Populate writes the opencode boot-dir shape into bootDir. Idempotent.
func (opencodeLayout) Populate(bootDir string, params SetupParams) error {
	if params.AgentProfile == nil {
		return fmt.Errorf("agent: opencodeLayout.Populate: AgentProfile is required")
	}
	spec, err := opencodeInjectionSpec(params)
	if err != nil {
		return err
	}
	return plantInjectionSpec(bootDir, spec)
}

// RegenerateSystemPromptSlot rewrites only agents/<slug>.md, leaving the
// rest of the sandbox dir intact.
func (opencodeLayout) RegenerateSystemPromptSlot(bootDir string, params SetupParams) error {
	if params.AgentProfile == nil {
		return fmt.Errorf("agent: opencodeLayout.RegenerateSystemPromptSlot: AgentProfile is required")
	}
	slug := agentSlug(params)
	return plantInjectionSpec(bootDir, agentlaunch.InjectionSpec{
		NativeFiles: []agentlaunch.NativeFile{
			nativeFileRaw(fmt.Sprintf("agents/%s.md", slug), opencodeAgentMD(params), 0o644),
		},
	})
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

func (opencodeLayout) BootPrompt(profile *store.AgentProfile, opts Options) string {
	return resolveBootPrompt(profile, opts)
}

func (opencodeLayout) BootMode() string { return "" }
