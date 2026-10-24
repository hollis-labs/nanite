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
// attached with --add-dir (addDirArgs). agy's global config is shared with the
// Antigravity desktop app and is never written. Instead the launch runs with
// HOME=<bootDir>/agy-home, a symlink farm of the real home with a private
// .gemini whose settings.json carries the per-tool MCP allow rules from the
// agent's grants (bootdir_agy_home.go, agy_mcp_allow.go). The real ~/.agents
// is not linked, so the operator's global plugins and skills do not load.
//
//	<bootDir>/agy-home/                        HOME (farm + .gemini link)
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
	result, err := (agyPlanter{authorize: authorize}).Plant(context.Background(), bootDir, spec)
	if err != nil {
		return result, err
	}
	// The launch's private HOME and settings: outside the planter's file set
	// (symlinks, and a persistent dir outside the boot dir).
	if err := plantAgyHome(bootDir, params); err != nil {
		return result, err
	}
	return result, nil
}

func (agyLayout) RegenerateSystemPromptSlot(bootDir string, params SetupParams) error {
	return &ArtifactRefreshUnavailable{Provider: "antigravity", Operation: "refresh system prompt"}
}

// AmendEnv points HOME at the launch's private home farm (bootdir_agy_home.go):
// agy has no config-dir variable, but it honours $HOME, and the farm's private
// .gemini is the only place Nanite can put the MCP allow rules without writing
// the operator's real one. Everything else of the real home resolves through
// the farm's links; NANITE_* and the rest of the env are untouched.
func (agyLayout) AmendEnv(base map[string]string, bootDir string) map[string]string {
	home := agyHomePath(bootDir)
	if home == "" {
		return base
	}
	out := make(map[string]string, len(base)+1)
	for k, v := range base {
		out[k] = v
	}
	out["HOME"] = home
	return out
}

// SpawnWorkdir is the boot dir; the project is attached with --add-dir.
func (agyLayout) SpawnWorkdir(bootDir, _ string) string { return bootDir }

func (agyLayout) BootPrompt(profile *store.AgentProfile, opts Options) string {
	return resolveBootPrompt(profile, opts)
}

func (agyLayout) BootMode() string { return "" }
