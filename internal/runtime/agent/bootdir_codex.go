package agent

import (
	"context"
	"fmt"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/harness/adapters/provider"
	"github.com/hollis-labs/substrate/harness/agentlaunch"
	plant "github.com/hollis-labs/substrate/harness/agentlaunch/planting"
)

// codexLayout plants the codex-specific shape:
//
//	<bootDir>/
//	├── AGENTS.md                    # codex auto-loads from cwd (provider.AgentsMD shape)
//	├── boot.md
//	├── config.toml                  # provider config: approval_policy + sandbox_mode (sourced from go-providers BootDirSpec)
//	├── .sandbox/agent-context.md
//	├── .sandbox/envelope-schema.md
//	├── .mcp.json
//	└── auth.json -> host login      # symlink to $CODEX_HOME/auth.json or ~/.codex/auth.json (bootdir_codex_auth.go)
//
// Spawn cwd: <bootDir>; project access via config.toml writable_roots,
// which carries the session's work root (CW-20261001-0020).
//
// TASKS/agent-host-acp/04: the bootdir file-set is declared as a
// plant.PlantSpec (github.com/hollis-labs/substrate/harness/agentlaunch/planting) and planted
// through codexPlanter, which implements plant.Planter — see
// bootdir_plant.go for why Nanite uses go-agent-wrapper's Planter
// contract rather than agentkit/agentlaunch/providerplant.Plant.
//
// S5 Phase B: config.toml is now planted (it was previously missing
// entirely). Codex's load-bearing config lives in $CODEX_HOME/config.toml;
// with no config.toml a headless codex falls back to its interactive
// approval default and BLOCKS FOREVER waiting for an approval no one can
// give (live bug: codex-via-dispatch hangs). The file is sourced from
// go-providers' CodexAdapter.BootDirSpec so it carries approval_policy +
// sandbox_mode. See bootdir_provider_config.go for the policy choice.
//
// NOTE: codex isolates per-task config via CODEX_HOME pointing at the
// boot dir — see AmendEnv below. Without that env amendment codex reads
// ~/.codex/config.toml and the planted config.toml is never consulted.
type codexLayout struct{}

// codexPlanter implements plant.Planter for the codex bootdir shape.
// Plant destinations: Spec.Files entries land verbatim at their map-key
// path; Spec.MCPConfig lands at ".mcp.json"; Spec.ProviderSettings["codex"]
// lands at "config.toml" at 0o600. See bootdir_plant.go's plantSpec for
// the shared write routine. auth.json is not planted through here — see
// codexLayout.Populate.
type codexPlanter struct {
	authorize agentlaunch.ArtifactAuthorizer
}

var _ plant.Planter = codexPlanter{}

func (p codexPlanter) Plant(ctx context.Context, bootDir string, spec plant.PlantSpec) (plant.PlantResult, error) {
	return plantSpec(ctx, bootDir, spec, plantConfig{
		authorize: p.authorize, provider: "codex",
		providerSettingsPath: "config.toml",
		providerSettingsMode: codexConfigFileMode,
	})
}

// codexAgentsMD renders the AGENTS.md system-prompt body. Codex's
// system-prompt-bearing slot is AGENTS.md (the cwd-loaded file analogous
// to claude's CLAUDE.md). The body shape comes from go-providers'
// provider.AgentsMD renderer.
func codexAgentsMD(params SetupParams) string {
	return provider.AgentsMD(provider.AgentInfo{
		Name:         params.AgentProfile.Name,
		Role:         "", // codex flow doesn't carry a separate role taxonomy yet
		Description:  params.AgentProfile.Description,
		SystemPrompt: params.SystemPrompt,
	}, "")
}

// codexPlantSpec assembles the full codex bootdir file-set as a
// plant.PlantSpec.
//
// config.toml is a provider CONFIG file sourced from go-providers'
// CodexAdapter.BootDirSpec (not hand-rolled). It carries approval_policy +
// sandbox_mode — the fix for the headless-codex approval deadlock — and
// rides Spec.ProviderSettings["codex"]. See bootdir_provider_config.go.
//
// auth.json is deliberately absent from the spec: CODEX_HOME (set by
// AmendEnv) redirects codex's auth lookup into the boot dir, and
// Populate answers that with a symlink to the host login, outside the
// materialize engine (bootdir_codex_auth.go, CW-20261001-0027).
//
// TASKS/skills/10: codex has NO native skill-loading mechanism — confirmed
// against go-providers' own CodexAdapter.BootDirSpec (no skills-related
// planted file or env amendment), the OpenAI Codex CLI's docs/config.md
// reference, and its README (zero mentions of "skill"/"skills" in either).
// See skill_plant.go's package doc for the full investigation. codexPlantSpec
// therefore has no skill-files contribution at all — this is the
// documented, legitimate "provider has no equivalent native-skill
// mechanism" outcome 20-skills.md's own Context anticipates, not an
// oversight.
func codexPlantSpec(params SetupParams) (plant.PlantSpec, error) {
	// CW-20260910-0015: codex has no verified hook wiring in Nanite.
	// go-providers' capability matrix reports FeatureHooks as
	// "explicit-effect" for codex — the provider is understood to have
	// the feature, but that package does not project it and Nanite has no
	// confirmed config shape for declaring one. Planting the scripts
	// anyway would produce executables nothing runs, which is the exact
	// failure bootdir_hooks.go exists to prevent. An error, not a silent
	// drop: a caller that asked for a gate should be told it did not get
	// one. See bootdir_hooks.go's header.
	if len(params.Hooks) > 0 {
		return plant.PlantSpec{}, hooksUnsupportedError("codex",
			"no verified declaration mechanism; go-providers reports hooks as explicit-effect but Nanite has not confirmed the config shape")
	}
	configTOML, err := codexConfigTOMLContent(params.CLIWritableRoots)
	if err != nil {
		return plant.PlantSpec{}, err
	}

	files := map[string][]byte{
		"AGENTS.md": []byte(codexAgentsMD(params)),
		"boot.md":   []byte(params.BootContent),
	}
	for relPath, content := range sandboxFiles(params) {
		files[relPath] = content
	}

	mcp, err := mcpConfigBytes(params)
	if err != nil {
		return plant.PlantSpec{}, err
	}

	return plant.PlantSpec{
		Files:            files,
		MCPConfig:        mcp,
		ProviderSettings: map[string][]byte{"codex": []byte(configTOML)},
	}, nil
}

func (l codexLayout) Setup(params SetupParams) (string, error) {
	bootDir, authorize, seal, err := makeAuthorizedBootDir("codex", params)
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

// Populate refuses refresh of an existing bound root under the published
// inactive-only artifact contract. Setup uses its private custody port instead.
func (codexLayout) Populate(bootDir string, params SetupParams) (plant.PlantResult, error) {
	return plant.PlantResult{}, &ArtifactRefreshUnavailable{Provider: "codex", Operation: "populate bound root"}
}

func (codexLayout) populateInactive(bootDir string, params SetupParams, authorize agentlaunch.ArtifactAuthorizer) (plant.PlantResult, error) {
	if params.AgentProfile == nil {
		return plant.PlantResult{}, fmt.Errorf("agent: codexLayout.Populate: AgentProfile is required")
	}
	spec, err := codexPlantSpec(params)
	if err != nil {
		return plant.PlantResult{}, err
	}
	result, err := (codexPlanter{authorize: authorize}).Plant(context.Background(), bootDir, spec)
	if err != nil {
		return result, err
	}
	if err := linkCodexHostAuth(bootDir); err != nil {
		return result, err
	}
	return result, nil
}

// RegenerateSystemPromptSlot retains the binding and returns typed unavailable
// until a provider acknowledged, fenced active-update contract is implemented.
func (codexLayout) RegenerateSystemPromptSlot(bootDir string, params SetupParams) error {
	return &ArtifactRefreshUnavailable{Provider: "codex", Operation: "refresh system prompt"}
}

// AmendEnv sets CODEX_HOME=<bootDir>. Codex reads its config (config.toml)
// and auth (auth.json) from $CODEX_HOME; pointing it at the boot dir makes
// the planted config.toml the one codex actually consults — without this
// codex would merge ~/.codex/config.toml instead and the planted
// approval_policy / sandbox_mode (the headless-deadlock fix) would never
// take effect. Populate links auth.json to the host login so the
// redirected auth lookup still resolves. Mirrors opencodeLayout.AmendEnv's
// OPENCODE_CONFIG_DIR=<bootDir> pattern.
//
// An empty bootDir leaves base unchanged (defensive — Setup/Populate
// always pass a real path).
//
// Nanite-owned, unchanged by TASKS/agent-host-acp/04: plant.Planter's
// contract is file-planting only and has no concept of env composition.
func (codexLayout) AmendEnv(base map[string]string, bootDir string) map[string]string {
	if bootDir == "" {
		return base
	}
	out := make(map[string]string, len(base)+1)
	for k, v := range base {
		out[k] = v
	}
	out["CODEX_HOME"] = bootDir
	return out
}

// SpawnWorkdir returns the boot dir.
//
// Nanite-owned, unchanged by TASKS/agent-host-acp/04 — see
// claudeLayout.SpawnWorkdir's comment for the rationale.
func (codexLayout) SpawnWorkdir(bootDir, _ string) string { return bootDir }

// BootPrompt is Nanite-owned, unchanged by TASKS/agent-host-acp/04 — see
// claudeLayout.BootPrompt's comment for the rationale.
func (codexLayout) BootPrompt(profile *store.AgentProfile, opts Options) string {
	return resolveBootPrompt(profile, opts)
}

// BootMode is empty for the subprocess-per-turn codex path; the boot
// prompt threads as the first user message. Nanite-owned, unchanged by
// TASKS/agent-host-acp/04 — see claudeLayout.BootMode's comment.
func (codexLayout) BootMode() string { return "" }
