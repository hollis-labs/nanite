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
//	├── config.toml                  # provider config: approval_policy + sandbox_mode (sourced from go-providers BootDirSpec)
//	├── .sandbox/agent-context.md
//	├── .sandbox/envelope-schema.md
//	└── .mcp.json
//
// Spawn cwd: <bootDir>; project access via codex's --cd flag.
//
// CW-20260515-0025: the bootdir file-set is declared as an
// agentlaunch.InjectionSpec and written via plantInjectionSpec — see
// bootdir_plant.go.
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
//
// config.toml and auth.json are provider CONFIG files sourced from
// go-providers' CodexAdapter.BootDirSpec (not hand-rolled). config.toml
// carries approval_policy + sandbox_mode — the fix for the headless-codex
// approval deadlock; auth.json carries the user's codex auth, planted
// because CODEX_HOME (set by AmendEnv) redirects codex's auth lookup into
// the boot dir. See bootdir_provider_config.go.
func codexInjectionSpec(params SetupParams) (agentlaunch.InjectionSpec, error) {
	configTOML, err := codexConfigTOMLContent(params.CLIWritableRoots)
	if err != nil {
		return agentlaunch.InjectionSpec{}, err
	}
	authJSON, err := codexAuthJSONContent()
	if err != nil {
		return agentlaunch.InjectionSpec{}, err
	}
	native := []agentlaunch.NativeFile{
		nativeFileRaw("AGENTS.md", codexAgentsMD(params), 0o644),
		bootMDNativeFile(params),
		nativeFileRaw("config.toml", configTOML, codexConfigFileMode),
		nativeFileRaw("auth.json", authJSON, codexConfigFileMode),
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

// AmendEnv sets CODEX_HOME=<bootDir>. Codex reads its config (config.toml)
// and auth (auth.json) from $CODEX_HOME; pointing it at the boot dir makes
// the planted config.toml the one codex actually consults — without this
// codex would merge ~/.codex/config.toml instead and the planted
// approval_policy / sandbox_mode (the headless-deadlock fix) would never
// take effect. codexInjectionSpec plants auth.json alongside so the
// redirected auth lookup still resolves. Mirrors opencodeLayout.AmendEnv's
// OPENCODE_CONFIG_DIR=<bootDir> pattern.
//
// An empty bootDir leaves base unchanged (defensive — Setup/Populate
// always pass a real path).
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

func (codexLayout) SpawnWorkdir(bootDir, _ string) string { return bootDir }

func (codexLayout) BootPrompt(profile *store.AgentProfile, opts Options) string {
	return resolveBootPrompt(profile, opts)
}

// BootMode is empty for the subprocess-per-turn codex path; the boot
// prompt threads as the first user message.
func (codexLayout) BootMode() string { return "" }
