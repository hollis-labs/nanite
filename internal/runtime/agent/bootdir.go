package agent

import (
	"fmt"

	"github.com/hollis-labs/nanite/internal/store"
)

// Layout abstracts per-provider boot-dir population. Each provider's
// Layout owns the planted file shapes (CLAUDE.md / AGENTS.md /
// agents/<name>.md), env amendments (OPENCODE_CONFIG_DIR for opencode),
// the choice of cwd vs. project dir, and the kickoff message form
// (Boot @./boot.md vs raw content).
type Layout interface {
	// Setup materializes the boot dir at $TMPDIR/nanite-boot-<provider>-<sessionID>-r<runID>-XXXXXX/
	// and plants the per-provider files. Returns the absolute path of the
	// resulting boot dir; the caller defers cleanup to session stop.
	Setup(params SetupParams) (string, error)

	// AmendEnv merges provider-specific env additions onto the base env
	// map composed by composeEnv.
	AmendEnv(base map[string]string, bootDir string) map[string]string

	// SpawnWorkdir returns the working directory for the spawn. claude /
	// codex spawn from the boot dir; opencode spawns from the project dir
	// with OPENCODE_CONFIG_DIR pointing at the boot dir.
	SpawnWorkdir(bootDir, projectDir string) string

	// BootPrompt returns the system-prompt payload threaded into
	// agentsessions.StartOptions.BootPrompt. PTY claude consumes this on
	// process start; subsequent slot regeneration writes to <bootDir>/CLAUDE.md.
	BootPrompt(profile *store.AgentProfile, opts Options) string

	// BootMode returns the boot-prompt delivery mode threaded into
	// agentsessions.StartOptions.BootMode. PTY runtimes use "stdin"; legacy
	// subprocess-per-turn paths use the empty string (first-turn delivery).
	BootMode() string
}

// SetupParams aggregates the inputs Setup needs. Kept stable so individual
// Layout implementations can extend over time without rippling signatures.
type SetupParams struct {
	SessionID    string
	RunID        string
	AgentProfile *store.AgentProfile
	Mode         Mode
	SystemPrompt string
	BootContent  string
	ProjectDir   string
	// MCPConfig is the per-session MCP subprocess descriptor planted as
	// .mcp.json in the boot dir. Nanite MCP transport is subprocess-spawn-
	// based: the planted config names the nanite binary and the
	// per-session args ("mcp --db <db> --session <sessID>"). Zero-value
	// MCPConfig disables MCP planting.
	MCPConfig MCPConfig
}

// bootdirLayoutFor returns the Layout for the named provider. Unsupported
// providers return a clear-error stub that fails Setup; callers iterate
// PlantedFiles only when the provider's BootDirSpec.Notes is empty per
// go-providers v0.8.0 guidance.
func bootdirLayoutFor(provider string) Layout {
	switch provider {
	case "claude", "claude-code", "claudecode":
		return claudeLayout{}
	case "codex":
		return codexLayout{}
	case "opencode":
		return opencodeLayout{}
	case "gemini":
		return unsupportedLayout{name: "gemini"}
	case "copilot":
		return unsupportedLayout{name: "copilot"}
	case "aider":
		return unsupportedLayout{name: "aider"}
	case "junie":
		return unsupportedLayout{name: "junie"}
	default:
		return unsupportedLayout{name: provider}
	}
}

// unsupportedLayout is the stub for providers without a verified boot-dir
// shape. Setup returns a clear error; the other methods return zero values
// so callers don't have to special-case before invoking Setup.
type unsupportedLayout struct {
	name string
}

func (u unsupportedLayout) Setup(SetupParams) (string, error) {
	return "", fmt.Errorf("agent: bootdir for provider %q is not yet implemented (awaiting go-providers BootDirSpec coverage)", u.name)
}

func (u unsupportedLayout) AmendEnv(base map[string]string, _ string) map[string]string {
	return base
}

func (u unsupportedLayout) SpawnWorkdir(_, projectDir string) string         { return projectDir }
func (u unsupportedLayout) BootPrompt(*store.AgentProfile, Options) string   { return "" }
func (u unsupportedLayout) BootMode() string                                 { return "" }
