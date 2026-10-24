package agent

import (
	"context"
	"fmt"
	"os"

	"github.com/hollis-labs/libs/harness/core/adapters/provider"
	"github.com/hollis-labs/libs/harness/core/agentlaunch"
	plant "github.com/hollis-labs/libs/harness/core/agentlaunch/planting"
	"github.com/hollis-labs/nanite/internal/store"
)

// agyLayout plants the Antigravity CLI (agy) shape, as go-providers'
// AntigravityAdapter.BootDirSpec describes it:
//
//	<bootDir>/
//	├── AGENTS.md                              # agent instructions
//	├── boot.md
//	├── .sandbox/agent-context.md
//	├── .sandbox/envelope-schema.md
//	└── .agents/plugins/tether/
//	    ├── plugin.json                        # marks the workspace plugin
//	    └── mcp_config.json                    # the Nanite MCP server (0600)
//
// Spawn cwd: <bootDir>; the work root and other granted directories are
// attached with --add-dir (addDirArgs). agy has no config-dir variable and its
// global config is shared with the Antigravity desktop app, so nothing is
// written there and no env is amended. Global MCP servers and skills still load
// alongside the planted ones: agy's isolation is not measured, so unlike Claude
// there is no strict-MCP guarantee for it.
type agyLayout struct{}

// agyMCPConfigPath is where agy discovers a workspace plugin's MCP servers. The
// plugin is always named "tether" (go-providers) because agy caches each plugin
// server's tool schemas by plugin and server name.
const agyMCPConfigPath = ".agents/plugins/tether/mcp_config.json"

type agyPlanter struct {
	authorize agentlaunch.ArtifactAuthorizer
}

var _ plant.Planter = agyPlanter{}

func (p agyPlanter) Plant(ctx context.Context, bootDir string, spec plant.PlantSpec) (plant.PlantResult, error) {
	return plantSpec(ctx, bootDir, spec, plantConfig{
		authorize: p.authorize, provider: "antigravity",
		// The MCP config carries the agent credential for `nanite mcp`.
		fileModeOverrides: map[string]os.FileMode{agyMCPConfigPath: 0o600},
	})
}

func agyAgentsMD(params SetupParams) string {
	return provider.AgentsMD(provider.AgentInfo{
		Name:         params.AgentProfile.Name,
		Description:  params.AgentProfile.Description,
		SystemPrompt: params.SystemPrompt,
	}, "")
}

func agyPlantSpec(params SetupParams) (plant.PlantSpec, error) {
	if len(params.Hooks) > 0 {
		return plant.PlantSpec{}, hooksUnsupportedError("antigravity",
			"no verified hook declaration mechanism for agy")
	}
	files := map[string][]byte{
		"AGENTS.md":                          []byte(agyAgentsMD(params)),
		"boot.md":                            []byte(params.BootContent),
		".agents/plugins/tether/plugin.json": []byte(`{"name":"tether"}` + "\n"),
	}
	for relPath, content := range sandboxFiles(params) {
		files[relPath] = content
	}
	// Skills go where the shared layout table puts them for this runtime.
	skillFiles, err := skillFilesForProvider(context.Background(), "antigravity", params)
	if err != nil {
		return plant.PlantSpec{}, err
	}
	for relPath, content := range skillFiles {
		files[relPath] = content
	}
	// The definition's pinned reference files, under agent/.
	for relPath, content := range definitionFilesFor("antigravity", params) {
		files[relPath] = content
	}

	// agy's mcp_config.json has the shape of .mcp.json for a stdio server
	// ({"mcpServers": {name: {command, args, env}}}).
	mcp, err := mcpConfigBytes(params)
	if err != nil {
		return plant.PlantSpec{}, err
	}
	if mcp != nil {
		files[agyMCPConfigPath] = mcp
	}
	return plant.PlantSpec{Files: files}, nil
}

func (l agyLayout) Setup(params SetupParams) (string, error) {
	bootDir, authorize, seal, err := makeAuthorizedBootDir("antigravity", params)
	if err != nil {
		return "", err
	}
	defer seal()
	result, err := l.populateInactive(bootDir, params, authorize)
	bootArtifactEvidence.Store(bootDir, result)
	if err != nil {
		return "", &BootArtifactFailure{BootDir: bootDir, Result: result, Cause: err}
	}
	return bootDir, nil
}

func (agyLayout) Populate(bootDir string, params SetupParams) (plant.PlantResult, error) {
	return plant.PlantResult{}, &ArtifactRefreshUnavailable{Provider: "antigravity", Operation: "populate bound root"}
}

func (agyLayout) populateInactive(bootDir string, params SetupParams, authorize agentlaunch.ArtifactAuthorizer) (plant.PlantResult, error) {
	if params.AgentProfile == nil {
		return plant.PlantResult{}, fmt.Errorf("agent: agyLayout.Populate: AgentProfile is required")
	}
	spec, err := agyPlantSpec(params)
	if err != nil {
		return plant.PlantResult{}, err
	}
	return (agyPlanter{authorize: authorize}).Plant(context.Background(), bootDir, spec)
}

func (agyLayout) RegenerateSystemPromptSlot(bootDir string, params SetupParams) error {
	return &ArtifactRefreshUnavailable{Provider: "antigravity", Operation: "refresh system prompt"}
}

// AmendEnv leaves the environment alone: agy has no config-dir variable.
func (agyLayout) AmendEnv(base map[string]string, _ string) map[string]string { return base }

// SpawnWorkdir is the boot dir; the project is attached with --add-dir.
func (agyLayout) SpawnWorkdir(bootDir, _ string) string { return bootDir }

func (agyLayout) BootPrompt(profile *store.AgentProfile, opts Options) string {
	return resolveBootPrompt(profile, opts)
}

func (agyLayout) BootMode() string { return "" }
