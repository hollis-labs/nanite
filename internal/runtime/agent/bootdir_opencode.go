package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/hollis-labs/go-agent-wrapper/plant"
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
// TASKS/agent-host-acp/04: the bootdir file-set is declared as a
// plant.Spec (github.com/hollis-labs/go-agent-wrapper/plant) and planted
// through opencodePlanter, which implements plant.Planter — see
// bootdir_plant.go for why Nanite uses go-agent-wrapper's Planter
// contract rather than agentkit/agentlaunch/providerplant.Plant.
type opencodeLayout struct{}

// opencodePlanter implements plant.Planter for the opencode bootdir
// shape. Plant destinations: Spec.Files entries land verbatim at their
// map-key path; Spec.MCPConfig lands at ".mcp.json". opencode has no
// go-providers-sourced ProviderSettings destination — agents.json and
// opencode.json are hand-rolled Nanite content and ride Spec.Files like
// every other planted file, so plantConfig.providerSettingsPath is left
// empty here. See bootdir_plant.go's plantSpec for the shared write
// routine.
type opencodePlanter struct{}

var _ plant.Planter = opencodePlanter{}

func (opencodePlanter) Plant(_ context.Context, bootDir string, spec plant.Spec) (plant.Result, error) {
	return plantSpec(bootDir, spec, plantConfig{provider: "opencode"})
}

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

// opencodePlantSpec assembles the full opencode bootdir file-set as a
// plant.Spec.
func opencodePlantSpec(params SetupParams) (plant.Spec, error) {
	slug := agentSlug(params)

	agentsJSONBody, err := opencodeAgentsJSON(params, slug)
	if err != nil {
		return plant.Spec{}, err
	}
	opencodeJSONBody, err := opencodeJSON(slug)
	if err != nil {
		return plant.Spec{}, err
	}

	files := map[string][]byte{
		fmt.Sprintf("agents/%s.md", slug): []byte(opencodeAgentMD(params)),
		"agents.json":                     []byte(agentsJSONBody),
		"opencode.json":                   []byte(opencodeJSONBody),
		"boot.md":                         []byte(params.BootContent),
	}
	for relPath, content := range sandboxFiles(params) {
		files[relPath] = content
	}

	// TASKS/skills/10: plant this agent's plantable skill set at opencode's
	// native skill convention(s) — see skill_plant.go's package doc for
	// why opencode gets two candidate destination prefixes. No-op when
	// params carries no skill wiring or the agent has no plantable
	// skills.
	skillFiles, err := skillFilesForProvider(context.Background(), "opencode", params)
	if err != nil {
		return plant.Spec{}, err
	}
	for relPath, content := range skillFiles {
		files[relPath] = content
	}

	mcp, err := mcpConfigBytes(params)
	if err != nil {
		return plant.Spec{}, err
	}

	return plant.Spec{Files: files, MCPConfig: mcp}, nil
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
//
// Layout.Populate has no context.Context parameter (see bootdir.go
// and claudeLayout.Populate's comment for why opencodePlanter.Plant is
// called with context.Background() here).
func (opencodeLayout) Populate(bootDir string, params SetupParams) error {
	if params.AgentProfile == nil {
		return fmt.Errorf("agent: opencodeLayout.Populate: AgentProfile is required")
	}
	spec, err := opencodePlantSpec(params)
	if err != nil {
		return err
	}
	_, err = opencodePlanter{}.Plant(context.Background(), bootDir, spec)
	return err
}

// RegenerateSystemPromptSlot rewrites only agents/<slug>.md, leaving the
// rest of the sandbox dir intact.
func (opencodeLayout) RegenerateSystemPromptSlot(bootDir string, params SetupParams) error {
	if params.AgentProfile == nil {
		return fmt.Errorf("agent: opencodeLayout.RegenerateSystemPromptSlot: AgentProfile is required")
	}
	slug := agentSlug(params)
	spec := plant.Spec{
		Files: map[string][]byte{
			fmt.Sprintf("agents/%s.md", slug): []byte(opencodeAgentMD(params)),
		},
	}
	_, err := opencodePlanter{}.Plant(context.Background(), bootDir, spec)
	return err
}

// AmendEnv injects OPENCODE_CONFIG_DIR=<bootDir>.
//
// Nanite-owned, unchanged by TASKS/agent-host-acp/04: plant.Planter's
// contract is file-planting only and has no concept of env composition.
func (opencodeLayout) AmendEnv(base map[string]string, bootDir string) map[string]string {
	out := make(map[string]string, len(base)+1)
	for k, v := range base {
		out[k] = v
	}
	out["OPENCODE_CONFIG_DIR"] = bootDir
	return out
}

// SpawnWorkdir returns the project dir (boot dir is the config dir, not
// cwd). Nanite-owned, unchanged by TASKS/agent-host-acp/04 — see
// claudeLayout.SpawnWorkdir's comment for the rationale. This is the
// provider where SpawnWorkdir's divergence from bootDir matters most:
// forcing it into plant.Planter's file-planting contract would have
// meant inventing a workdir concept the contract was never meant to
// carry.
//
// TASKS/agent-host-acp/18: projectDir falls back to bootDir when empty.
// Real chat sessions always call this with projectDir="" today —
// internal/service/chat_boot_drive.go's bootSessionWorkdir is a
// documented, intentional stub that never threads a resolved project
// path through (project-repo workdir threading is a separate, larger,
// still-deferred follow-up, not this fix's job). Pre-migration, an empty
// projectDir flowed into agentsessions.StartOptions.Workdir directly,
// which for exec.Cmd.Dir means "inherit the daemon's own cwd" — already
// a real, silent wrong-cwd bug, but not a crash. Post-migration,
// wrapper.Wrapper.Run hard-requires Config.Workdir non-empty
// (wrapper.go's Run: "Config.Workdir is required"), so returning
// projectDir verbatim turned that same empty value into an unconditional
// crash on the very first turn of every real OpenCode CLI session. The
// boot dir is always non-empty (Setup always creates it) and is at least
// a real, scoped, per-session directory — closer to claude/codexLayout's
// own SpawnWorkdir behavior (both always return bootDir) than to the
// pre-migration daemon-cwd fallback. This does NOT resolve the
// underlying wrong-cwd gap for OpenCode chat sessions (still bootDir, not
// a real project path) — it only stops the hard crash.
func (opencodeLayout) SpawnWorkdir(bootDir, projectDir string) string {
	if projectDir == "" {
		return bootDir
	}
	return projectDir
}

// BootPrompt is Nanite-owned, unchanged by TASKS/agent-host-acp/04 — see
// claudeLayout.BootPrompt's comment for the rationale.
func (opencodeLayout) BootPrompt(profile *store.AgentProfile, opts Options) string {
	return resolveBootPrompt(profile, opts)
}

// BootMode is Nanite-owned, unchanged by TASKS/agent-host-acp/04 — see
// claudeLayout.BootMode's comment.
func (opencodeLayout) BootMode() string { return "" }
