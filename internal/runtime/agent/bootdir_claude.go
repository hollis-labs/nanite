package agent

import (
	"fmt"
	"os"

	"github.com/hollis-labs/go-agent-launch/agentlaunch"
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
//	│   └── settings.json            # {mcpServers: {}, approvedTools: []} stub (no global ~/.claude.json bleed)
//	└── .mcp.json                    # nanite MCP subprocess descriptor
//
// Spawn cwd: <bootDir>; project access: claude --add-dir <projectDir>.
//
// CW-20260515-0025: the bootdir file-set is now declared as an
// agentlaunch.InjectionSpec and written through the shared planting
// routine (plantInjectionSpec) — see bootdir_plant.go for why Nanite
// uses the shared InjectionSpec primitives rather than providerplant.Plant.
type claudeLayout struct{}

const claudeSettingsJSONStub = `{
  "mcpServers": {},
  "approvedTools": []
}
`

// claudeInjectionSpec assembles the full claude bootdir file-set as a
// shared agentlaunch.InjectionSpec. Provider files (CLAUDE.md,
// .claude/settings.json) and the Nanite app-extras (.sandbox/* docs,
// boot.md) ride as NativeFiles; .mcp.json rides as a BootDirOverlay
// entry so it plants last (overlay-wins-last, per the providerplant
// ordering contract).
func claudeInjectionSpec(params SetupParams) (agentlaunch.InjectionSpec, error) {
	native := []agentlaunch.NativeFile{
		nativeFileRaw("CLAUDE.md",
			BuildCLAUDEMD(params.AgentProfile.Name, params.AgentProfile.Description), 0o644),
		bootMDNativeFile(params),
		nativeFileRaw(".claude/settings.json", claudeSettingsJSONStub, 0o644),
	}
	native = append(native, sandboxNativeFiles(params)...)

	overlay, err := mcpOverlay(params)
	if err != nil {
		return agentlaunch.InjectionSpec{}, err
	}
	return agentlaunch.InjectionSpec{NativeFiles: native, BootDirOverlay: overlay}, nil
}

func (l claudeLayout) Setup(params SetupParams) (string, error) {
	bootDir, err := makeBootDir("claude", params)
	if err != nil {
		return "", err
	}
	if err := l.Populate(bootDir, params); err != nil {
		// On any post-mkdir failure, clean up the partial boot dir so
		// callers don't leak $TMPDIR entries.
		_ = os.RemoveAll(bootDir)
		return "", err
	}
	return bootDir, nil
}

// Populate writes the claude boot-dir shape into bootDir. Idempotent —
// every file is replaced via the shared planting routine (atomic
// temp-file + rename, no read of prior state); mkdir calls use MkdirAll
// so a partially-populated dir converges. Used by both Setup (post-mkdir)
// and the recovery BootDirOps adapter (against an existing dir).
func (claudeLayout) Populate(bootDir string, params SetupParams) error {
	if params.AgentProfile == nil {
		return fmt.Errorf("agent: claudeLayout.Populate: AgentProfile is required")
	}
	spec, err := claudeInjectionSpec(params)
	if err != nil {
		return err
	}
	return plantInjectionSpec(bootDir, spec)
}

// RegenerateSystemPromptSlot rewrites only CLAUDE.md, leaving the rest
// of the sandbox dir intact. Used by recovery.BootDirOps.RegenerateCLAUDEMD
// for watchdog_kill remediation.
func (claudeLayout) RegenerateSystemPromptSlot(bootDir string, params SetupParams) error {
	if params.AgentProfile == nil {
		return fmt.Errorf("agent: claudeLayout.RegenerateSystemPromptSlot: AgentProfile is required")
	}
	return plantInjectionSpec(bootDir, agentlaunch.InjectionSpec{
		NativeFiles: []agentlaunch.NativeFile{
			nativeFileRaw("CLAUDE.md",
				BuildCLAUDEMD(params.AgentProfile.Name, params.AgentProfile.Description), 0o644),
		},
	})
}

func (claudeLayout) AmendEnv(base map[string]string, _ string) map[string]string { return base }

// SpawnWorkdir returns the boot dir; CLAUDE.md auto-loads from cwd.
func (claudeLayout) SpawnWorkdir(bootDir, _ string) string { return bootDir }

// BootPrompt is the system prompt payload for the PTY runtime.
// Sourced from resolveBootPrompt (prompt.go), which honors
// Options.BootPromptOverride (CW-20260514-0048) and falls back to
// composeSystemPrompt(role, profile, mode) otherwise.
func (claudeLayout) BootPrompt(profile *store.AgentProfile, opts Options) string {
	return resolveBootPrompt(profile, opts)
}

// BootMode is "stdin" for PTY claude; the runtime writes the boot prompt
// onto the master PTY at process start.
func (claudeLayout) BootMode() string { return "stdin" }
