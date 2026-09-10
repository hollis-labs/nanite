package agent

import (
	"context"
	"fmt"
	"os"

	"github.com/hollis-labs/go-agent-wrapper/plant"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/store"
)

// codexLayout plants the codex-specific shape:
//
//	<bootDir>/
//	├── AGENTS.md                    # codex auto-loads from cwd (provider.AgentsMD shape)
//	├── boot.md
//	├── config.toml                  # provider config: approval_policy + sandbox_mode (sourced from go-providers BootDirSpec)
//	├── .sandbox/agent-context.md
//	├── .sandbox/envelope-schema.md
//	└── .mcp.json
//
// Spawn cwd: <bootDir>; project access via codex's --cd flag.
//
// TASKS/agent-host-acp/04: the bootdir file-set is declared as a
// plant.Spec (github.com/hollis-labs/go-agent-wrapper/plant) and planted
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
// path (including auth.json, forced to 0o600 via fileModeOverrides);
// Spec.MCPConfig lands at ".mcp.json"; Spec.ProviderSettings["codex"]
// lands at "config.toml", also at 0o600. See bootdir_plant.go's
// plantSpec for the shared write routine.
type codexPlanter struct{}

var _ plant.Planter = codexPlanter{}

func (codexPlanter) Plant(ctx context.Context, bootDir string, spec plant.Spec) (plant.Result, error) {
	return plantSpec(ctx, bootDir, spec, plantConfig{
		provider:             "codex",
		providerSettingsPath: "config.toml",
		providerSettingsMode: codexConfigFileMode,
		fileModeOverrides:    map[string]os.FileMode{"auth.json": codexConfigFileMode},
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
// plant.Spec.
//
// config.toml and auth.json are provider CONFIG files sourced from
// go-providers' CodexAdapter.BootDirSpec (not hand-rolled). config.toml
// carries approval_policy + sandbox_mode — the fix for the headless-codex
// approval deadlock — and rides Spec.ProviderSettings["codex"]; auth.json
// carries the user's codex auth, planted because CODEX_HOME (set by
// AmendEnv) redirects codex's auth lookup into the boot dir, and rides
// Spec.Files (codexPlanter forces its mode to codexConfigFileMode via
// fileModeOverrides — Spec.Files carries no per-entry mode of its own).
// See bootdir_provider_config.go.
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
func codexPlantSpec(params SetupParams) (plant.Spec, error) {
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
		return plant.Spec{}, hooksUnsupportedError("codex",
			"no verified declaration mechanism; go-providers reports hooks as explicit-effect but Nanite has not confirmed the config shape")
	}
	configTOML, err := codexConfigTOMLContent(params.CLIWritableRoots)
	if err != nil {
		return plant.Spec{}, err
	}
	authJSON, err := codexAuthJSONContent()
	if err != nil {
		return plant.Spec{}, err
	}

	files := map[string][]byte{
		"AGENTS.md": []byte(codexAgentsMD(params)),
		"boot.md":   []byte(params.BootContent),
		"auth.json": []byte(authJSON),
	}
	for relPath, content := range sandboxFiles(params) {
		files[relPath] = content
	}

	mcp, err := mcpConfigBytes(params)
	if err != nil {
		return plant.Spec{}, err
	}

	return plant.Spec{
		Files:            files,
		MCPConfig:        mcp,
		ProviderSettings: map[string][]byte{"codex": []byte(configTOML)},
	}, nil
}

func (l codexLayout) Setup(params SetupParams) (string, error) {
	bootDir, err := makeBootDir("codex", params)
	if err != nil {
		return "", err
	}
	if _, err := l.Populate(bootDir, params); err != nil {
		_ = os.RemoveAll(bootDir)
		return "", err
	}
	return bootDir, nil
}

// Populate writes the codex boot-dir shape into bootDir. Idempotent.
//
// Layout.Populate has no context.Context parameter (see bootdir.go
// and claudeLayout.Populate's comment for why codexPlanter.Plant is
// called with context.Background() here).
func (codexLayout) Populate(bootDir string, params SetupParams) (plant.Result, error) {
	if params.AgentProfile == nil {
		return plant.Result{}, fmt.Errorf("agent: codexLayout.Populate: AgentProfile is required")
	}
	spec, err := codexPlantSpec(params)
	if err != nil {
		return plant.Result{}, err
	}
	return codexPlanter{}.Plant(context.Background(), bootDir, spec)
}

// RegenerateSystemPromptSlot rewrites only AGENTS.md, leaving the rest
// of the sandbox dir intact.
func (codexLayout) RegenerateSystemPromptSlot(bootDir string, params SetupParams) error {
	if params.AgentProfile == nil {
		return fmt.Errorf("agent: codexLayout.RegenerateSystemPromptSlot: AgentProfile is required")
	}
	spec := plant.Spec{
		Files: map[string][]byte{
			"AGENTS.md": []byte(codexAgentsMD(params)),
		},
	}
	_, err := codexPlanter{}.Plant(context.Background(), bootDir, spec)
	return err
}

// AmendEnv sets CODEX_HOME=<bootDir>. Codex reads its config (config.toml)
// and auth (auth.json) from $CODEX_HOME; pointing it at the boot dir makes
// the planted config.toml the one codex actually consults — without this
// codex would merge ~/.codex/config.toml instead and the planted
// approval_policy / sandbox_mode (the headless-deadlock fix) would never
// take effect. codexPlantSpec plants auth.json alongside so the
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
