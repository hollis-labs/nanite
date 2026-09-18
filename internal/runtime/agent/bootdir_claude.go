package agent

import (
	"context"
	"fmt"
	"os"

	"github.com/hollis-labs/go-agent-wrapper/plant"
	"github.com/hollis-labs/go-materialize/artifact"
	"github.com/hollis-labs/nanite/internal/store"
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
// plant.Spec (github.com/hollis-labs/go-agent-wrapper/plant) and planted
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
type claudePlanter struct{}

var _ plant.Planter = claudePlanter{}

func (claudePlanter) Plant(ctx context.Context, bootDir string, spec plant.Spec) (plant.Result, error) {
	return plantSpec(ctx, bootDir, spec, plantConfig{
		provider:             "claude",
		providerSettingsPath: ".claude/settings.json",
	})
}

// claudePlantSpec assembles the full claude bootdir file-set as a
// plant.Spec. The Nanite-owned CONTENT files (CLAUDE.md, .sandbox/* docs,
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
func claudePlantSpec(bootDir string, params SetupParams) (plant.Spec, error) {
	hookSettings, err := claudeHookSettings(bootDir, params.Hooks)
	if err != nil {
		return plant.Spec{}, err
	}
	settings, err := claudeProviderConfigContent(params.CLIWritableRoots, hookSettings)
	if err != nil {
		return plant.Spec{}, err
	}
	hookEntries, err := hookArtifactEntries("claude", params.Hooks)
	if err != nil {
		return plant.Spec{}, err
	}

	files := map[string][]byte{
		"CLAUDE.md": []byte(BuildCLAUDEMD(params.AgentProfile.Name, params.AgentProfile.Description, params.SystemPrompt)),
		"boot.md":   []byte(params.BootContent),
	}
	for relPath, content := range sandboxFiles(params) {
		files[relPath] = content
	}

	// TASKS/skills/10: plant this agent's plantable skill set at claude's
	// native .claude/skills/<slug>/ convention. No-op when params carries
	// no skill wiring (params.Skills / params.SkillVendor nil) or the
	// agent has no plantable skills — see skill_plant.go.
	skillFiles, err := skillFilesForProvider(context.Background(), "claude", params)
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

	return plant.Spec{
		Files:            files,
		MCPConfig:        mcp,
		ProviderSettings: map[string][]byte{"claude": []byte(settings)},
		Artifacts:        artifact.Tree{Entries: hookEntries},
	}, nil
}

func (l claudeLayout) Setup(params SetupParams) (string, error) {
	bootDir, err := makeBootDir("claude", params)
	if err != nil {
		return "", err
	}
	if _, err := l.Populate(bootDir, params); err != nil {
		// On any post-mkdir failure, clean up the partial boot dir so
		// callers don't leak $TMPDIR entries.
		_ = os.RemoveAll(bootDir)
		return "", err
	}
	return bootDir, nil
}

// Populate writes the claude boot-dir shape into bootDir. Idempotent —
// claudePlanter's underlying writePlantedFile calls use atomic
// temp-file + rename with no read of prior state; mkdir calls use
// MkdirAll so a partially-populated dir converges. Used by both Setup
// (post-mkdir) and the recovery BootDirOps adapter (against an existing
// dir).
//
// Layout.Populate has no context.Context parameter (see bootdir.go); it
// predates context threading and its external callers
// (internal/service/agent_bootdir_adapter.go, agent.Boot) are unchanged
// by this migration, so claudePlanter.Plant is called with
// context.Background() here — this is a synchronous filesystem write
// with no cancellation point today.
func (claudeLayout) Populate(bootDir string, params SetupParams) (plant.Result, error) {
	if params.AgentProfile == nil {
		return plant.Result{}, fmt.Errorf("agent: claudeLayout.Populate: AgentProfile is required")
	}
	spec, err := claudePlantSpec(bootDir, params)
	if err != nil {
		return plant.Result{}, err
	}
	return claudePlanter{}.Plant(context.Background(), bootDir, spec)
}

// RegenerateSystemPromptSlot rewrites only CLAUDE.md, leaving the rest
// of the sandbox dir intact. Used by recovery.BootDirOps.RegenerateCLAUDEMD
// for watchdog_kill remediation.
func (claudeLayout) RegenerateSystemPromptSlot(bootDir string, params SetupParams) error {
	if params.AgentProfile == nil {
		return fmt.Errorf("agent: claudeLayout.RegenerateSystemPromptSlot: AgentProfile is required")
	}
	spec := plant.Spec{
		Files: map[string][]byte{
			"CLAUDE.md": []byte(BuildCLAUDEMD(params.AgentProfile.Name, params.AgentProfile.Description, params.SystemPrompt)),
		},
	}
	_, err := claudePlanter{}.Plant(context.Background(), bootDir, spec)
	return err
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

// BootPrompt is the system prompt payload for the PTY runtime.
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

// BootMode is "stdin" for PTY claude; the runtime writes the boot prompt
// onto the master PTY at process start.
//
// Nanite-owned, unchanged by TASKS/agent-host-acp/04: boot-prompt
// delivery mode is lifecycle policy, not file-planting — see
// SpawnWorkdir's comment above for the same rationale.
func (claudeLayout) BootMode() string { return "stdin" }
