package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/fsutil"
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
type opencodeLayout struct{}

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
func (l opencodeLayout) Populate(bootDir string, params SetupParams) error {
	if params.AgentProfile == nil {
		return fmt.Errorf("agent: opencodeLayout.Populate: AgentProfile is required")
	}

	if err := l.RegenerateSystemPromptSlot(bootDir, params); err != nil {
		return err
	}

	slug := agentSlug(params)
	agentsJSON := map[string]any{
		"agents": []map[string]any{
			{
				"name":              slug,
				"description":       params.AgentProfile.Description,
				"instructions_file": fmt.Sprintf("./agents/%s.md", slug),
			},
		},
	}
	agentsJSONBytes, err := json.MarshalIndent(agentsJSON, "", "  ")
	if err != nil {
		return fmt.Errorf("agent: marshal agents.json: %w", err)
	}
	if err := fsutil.AtomicWriteFile(
		filepath.Join(bootDir, "agents.json"),
		agentsJSONBytes,
		0o644,
	); err != nil {
		return fmt.Errorf("agent: write agents.json: %w", err)
	}

	opencodeJSON := map[string]any{
		"agent": map[string]any{
			slug: map[string]any{
				"prompt": fmt.Sprintf("{file:./agents/%s.md}", slug),
			},
		},
	}
	opencodeJSONBytes, err := json.MarshalIndent(opencodeJSON, "", "  ")
	if err != nil {
		return fmt.Errorf("agent: marshal opencode.json: %w", err)
	}
	if err := fsutil.AtomicWriteFile(
		filepath.Join(bootDir, "opencode.json"),
		opencodeJSONBytes,
		0o644,
	); err != nil {
		return fmt.Errorf("agent: write opencode.json: %w", err)
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

// RegenerateSystemPromptSlot rewrites only agents/<slug>.md, leaving the
// rest of the sandbox dir intact. Opencode's system-prompt-bearing slot
// is the per-slug agent file (analogous to claude's CLAUDE.md).
func (opencodeLayout) RegenerateSystemPromptSlot(bootDir string, params SetupParams) error {
	if params.AgentProfile == nil {
		return fmt.Errorf("agent: opencodeLayout.RegenerateSystemPromptSlot: AgentProfile is required")
	}
	slug := agentSlug(params)
	agentsDir := filepath.Join(bootDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		return fmt.Errorf("agent: mkdir agents/: %w", err)
	}
	agentMD := fmt.Sprintf("# %s\n\n%s\n", params.AgentProfile.Name, params.SystemPrompt)
	if err := fsutil.AtomicWriteFile(
		filepath.Join(agentsDir, slug+".md"),
		[]byte(agentMD),
		0o644,
	); err != nil {
		return fmt.Errorf("agent: regenerate agents/%s.md: %w", slug, err)
	}
	return nil
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
	return composeSystemPrompt(opts.Role, profile, opts.Mode)
}

func (opencodeLayout) BootMode() string { return "" }
