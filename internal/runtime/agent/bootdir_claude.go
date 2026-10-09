package agent

import (
	"context"
	"fmt"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/harness/agentlaunch"
	plant "github.com/hollis-labs/substrate/harness/agentlaunch/planting"
	"github.com/hollis-labs/substrate/harness/workspace/materialize/artifact"
)

// claudeLayout plants nanite's claude-specific boot dir shape:
//
//	<bootDir>/
//	├── CLAUDE.md                    # role-aware system prompt + envelope rules
//	├── boot.md                      # task-specific kickoff (Boot @./boot.md target)
//	├── .sandbox/
//	│   ├── agent-context.md         # nanite agent identity + tool catalog reminders
//	│   └── envelope-schema.md       # SSE envelope schema reference
//	├── .claude/
//	│   └── settings.json            # provider config: permissions.defaultMode (sourced from go-providers BootDirSpec)
//	└── .mcp.json                    # nanite MCP subprocess descriptor
//
// Spawn cwd: <bootDir>; project access: claude --add-dir <projectDir>.
//
// TASKS/agent-host-acp/04: the bootdir file-set is declared as a
// plant.PlantSpec (github.com/hollis-labs/substrate/harness/agentlaunch/planting) and planted
// through claudePlanter, which implements plant.Planter — see
// bootdir_plant.go for why Nanite uses go-agent-wrapper's Planter
// contract rather than agentkit/agentlaunch/providerplant.Plant, and for
// why SpawnWorkdir/BootMode/BootPrompt below are NOT part of this
// migration.
//
// S5 Phase B: .claude/settings.json is no longer a hand-rolled
// {mcpServers,approvedTools} stub — it is sourced from go-providers'
// ClaudeAdapter.BootDirSpec so it carries permissions.defaultMode (the
// CLI-vendor-owned permission knob). A headless claude with no
// permissions.defaultMode silently denies tool calls it cannot get
// approval for; "acceptEdits" lets file edits proceed. See
// bootdir_provider_config.go for the rationale and the mode choice. The
// legacy approvedTools/mcpServers keys are dropped — current Claude Code
// ignores both.
type claudeLayout struct{}

// claudePlanter implements plant.Planter for the claude bootdir shape.
// Plant destinations: Spec.Files entries land verbatim at their map-key
// path; Spec.MCPConfig lands at ".mcp.json"; Spec.ProviderSettings["claude"]
// lands at ".claude/settings.json". See bootdir_plant.go's plantSpec for
// the shared write routine.
type claudePlanter struct {
	authorize agentlaunch.ArtifactAuthorizer
}

var _ plant.Planter = claudePlanter{}

func (p claudePlanter) Plant(ctx context.Context, bootDir string, spec plant.PlantSpec) (plant.PlantResult, error) {
	return plantSpec(ctx, bootDir, spec, plantConfig{
		authorize: p.authorize, provider: "claude",
		providerSettingsPath: ".claude/settings.json",
	})
}

// claudePlantSpec assembles the full claude bootdir file-set as a
// plant.PlantSpec. The Nanite-owned CONTENT files (CLAUDE.md, .sandbox/* docs,
// boot.md) ride as Files entries; the provider CONFIG file
// .claude/settings.json rides as ProviderSettings["claude"] — its content
// is sourced from go-providers (claudeProviderConfigContent), not
// hand-rolled; .mcp.json rides as Spec.MCPConfig.
//
// CW-20260910-0015: hook scripts ride Spec.Artifacts rather than Files,
// because they need a mode (0700, executable) and their own directory —
// neither of which the flat Files map can carry. Their DECLARATION is
// merged into the settings document in the same call, since a planted
// hook script that nothing declares never runs.
//
// bootDir is a parameter for exactly that reason: the declaration names
// the script by absolute path, so the spec cannot be built before the
// destination is known. It is unused when params.Hooks is empty.
func claudePlantSpec(bootDir string, params SetupParams) (plant.PlantSpec, error) {
	hookSettings, err := claudeHookSettings(bootDir, params.Hooks)
	if err != nil {
		return plant.PlantSpec{}, err
	}
	settings, err := claudeProviderConfigContent(params.CLIWritableRoots, hookSettings)
	if err != nil {
		return plant.PlantSpec{}, err
	}
	hookEntries, err := hookArtifactEntries("claude", params.Hooks)
	if err != nil {
		return plant.PlantSpec{}, err
	}

	files := map[string][]byte{
		"CLAUDE.md": []byte(BuildCLAUDEMD(params.AgentProfile.Name, params.AgentProfile.Description, params.SystemPrompt)),
		"boot.md":   []byte(params.BootContent),
	}
	for relPath, content := range sandboxFiles(params) {
		files[relPath] = content
	}

	// Tether identity: no-op unless params.AgentProfile is registered in
	// Tether (settings.tether_urn set). See tether_identity.go — pure,
	// local, no network call.
	if identity := tetherIdentityFile(params); identity != nil {
		files[".sandbox/tether-identity.md"] = identity
	}

	// TASKS/skills/10: plant this agent's plantable skill set at claude's
	// native .claude/skills/<slug>/ convention. No-op when params carries
	// no skill wiring (params.Skills / params.SkillVendor nil) or the
	// agent has no plantable skills — see skill_plant.go.
	skillFiles, err := skillFilesForProvider(context.Background(), "claude", params)
	if err != nil {
		return plant.PlantSpec{}, err
	}
	for relPath, content := range skillFiles {
		files[relPath] = content
	}

	mcp, err := mcpConfigBytes(params)
	if err != nil {
		return plant.PlantSpec{}, err
	}

	return plant.PlantSpec{
		Files:            files,
		MCPConfig:        mcp,
		ProviderSettings: map[string][]byte{"claude": []byte(settings)},
		Artifacts:        artifact.Tree{Entries: hookEntries},
	}, nil
}

func (l claudeLayout) Setup(params SetupParams) (string, error) {
	bootDir, authorize, seal, err := makeAuthorizedBootDir("claude", params)
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
func (claudeLayout) Populate(bootDir string, params SetupParams) (plant.PlantResult, error) {
	return plant.PlantResult{}, &ArtifactRefreshUnavailable{Provider: "claude", Operation: "populate bound root"}
}

func (claudeLayout) populateInactive(bootDir string, params SetupParams, authorize agentlaunch.ArtifactAuthorizer) (plant.PlantResult, error) {
	if params.AgentProfile == nil {
		return plant.PlantResult{}, fmt.Errorf("agent: claudeLayout.Populate: AgentProfile is required")
	}
	spec, err := claudePlantSpec(bootDir, params)
	if err != nil {
		return plant.PlantResult{}, err
	}
	return (claudePlanter{authorize: authorize}).Plant(context.Background(), bootDir, spec)
}

// RegenerateSystemPromptSlot retains the binding and returns typed unavailable
// until a provider acknowledged, fenced active-update contract is implemented.
func (claudeLayout) RegenerateSystemPromptSlot(bootDir string, params SetupParams) error {
	return &ArtifactRefreshUnavailable{Provider: "claude", Operation: "refresh system prompt"}
}

func (claudeLayout) AmendEnv(base map[string]string, _ string) map[string]string { return base }

// SpawnWorkdir returns the boot dir; CLAUDE.md auto-loads from cwd.
//
// Nanite-owned, unchanged by TASKS/agent-host-acp/04: plant.Planter's
// contract is file-planting only and has no concept of workdir
// selection. Workdir choice is lifecycle policy — where a process
// actually runs — which docs/engineering/architecture/16-agent-host.md
// names as something a host does not own.
func (claudeLayout) SpawnWorkdir(bootDir, _ string) string { return bootDir }

// BootPrompt is the system prompt content for legacy layout consumers.
// Sourced from resolveBootPrompt (prompt.go), which honors
// Options.BootPromptOverride (CW-20260514-0048) and falls back to
// composeSystemPrompt(role, profile, mode) otherwise.
//
// Nanite-owned, unchanged by TASKS/agent-host-acp/04: the prompt STRING
// is product content (agent roles/skills), explicitly named in
// docs/engineering/architecture/16-agent-host.md as something a host
// does not own. Only the FILE that carries it (CLAUDE.md, planted via
// claudePlanter above) is host-owned file-planting mechanics.
func (claudeLayout) BootPrompt(profile *store.AgentProfile, opts Options) string {
	return resolveBootPrompt(profile, opts)
}

// BootMode is a legacy layout hint. Boot does not consume it: Claude
// reads the planted CLAUDE.md and receives NDJSON-framed turns over stdio.
//
// Nanite-owned, unchanged by TASKS/agent-host-acp/04: boot-prompt
// delivery mode is lifecycle policy, not file-planting — see
// SpawnWorkdir's comment above for the same rationale.
func (claudeLayout) BootMode() string { return "stdin" }
