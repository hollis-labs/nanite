package agent

import (
	"fmt"
	"os"

	"github.com/hollis-labs/go-agent-launch/agentlaunch"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/store"
)

// codexLayout plants the codex-specific shape:
//
//	<bootDir>/
//	├── AGENTS.md                    # codex auto-loads from cwd (provider.AgentsMD shape)
//	├── boot.md
//	├── .sandbox/agent-context.md
//	├── .sandbox/envelope-schema.md
//	└── .mcp.json
//
// Spawn cwd: <bootDir>; project access via codex's --cd flag.
//
// CW-20260515-0025: the bootdir file-set is declared as an
// agentlaunch.InjectionSpec and written via plantInjectionSpec — see
// bootdir_plant.go.
type codexLayout struct{}

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

// codexInjectionSpec assembles the full codex bootdir file-set as a
// shared agentlaunch.InjectionSpec.
func codexInjectionSpec(params SetupParams) (agentlaunch.InjectionSpec, error) {
	native := []agentlaunch.NativeFile{
		nativeFileRaw("AGENTS.md", codexAgentsMD(params), 0o644),
		bootMDNativeFile(params),
	}
	native = append(native, sandboxNativeFiles(params)...)

	overlay, err := mcpOverlay(params)
	if err != nil {
		return agentlaunch.InjectionSpec{}, err
	}
	return agentlaunch.InjectionSpec{NativeFiles: native, BootDirOverlay: overlay}, nil
}

func (l codexLayout) Setup(params SetupParams) (string, error) {
	bootDir, err := makeBootDir("codex", params)
	if err != nil {
		return "", err
	}
	if err := l.Populate(bootDir, params); err != nil {
		_ = os.RemoveAll(bootDir)
		return "", err
	}
	return bootDir, nil
}

// Populate writes the codex boot-dir shape into bootDir. Idempotent.
func (codexLayout) Populate(bootDir string, params SetupParams) error {
	if params.AgentProfile == nil {
		return fmt.Errorf("agent: codexLayout.Populate: AgentProfile is required")
	}
	spec, err := codexInjectionSpec(params)
	if err != nil {
		return err
	}
	return plantInjectionSpec(bootDir, spec)
}

// RegenerateSystemPromptSlot rewrites only AGENTS.md, leaving the rest
// of the sandbox dir intact.
func (codexLayout) RegenerateSystemPromptSlot(bootDir string, params SetupParams) error {
	if params.AgentProfile == nil {
		return fmt.Errorf("agent: codexLayout.RegenerateSystemPromptSlot: AgentProfile is required")
	}
	return plantInjectionSpec(bootDir, agentlaunch.InjectionSpec{
		NativeFiles: []agentlaunch.NativeFile{
			nativeFileRaw("AGENTS.md", codexAgentsMD(params), 0o644),
		},
	})
}

func (codexLayout) AmendEnv(base map[string]string, _ string) map[string]string { return base }

func (codexLayout) SpawnWorkdir(bootDir, _ string) string { return bootDir }

func (codexLayout) BootPrompt(profile *store.AgentProfile, opts Options) string {
	return resolveBootPrompt(profile, opts)
}

// BootMode is empty for the subprocess-per-turn codex path; the boot
// prompt threads as the first user message.
func (codexLayout) BootMode() string { return "" }
