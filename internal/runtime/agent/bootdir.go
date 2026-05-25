package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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

	// Populate writes the per-provider files into an existing boot dir.
	// Idempotent — every Atomic*WriteFile call replaces the prior file
	// without reading prior state. Used by recovery.BootDirOps.Repopulate
	// to rewrite a partially-truncated sandbox dir without re-rolling the
	// $TMPDIR path. The caller is responsible for owning bootDir's
	// lifecycle (cleanup on terminal-failure remains with Setup).
	Populate(bootDir string, params SetupParams) error

	// RegenerateSystemPromptSlot rewrites only the system-prompt-bearing
	// file in bootDir (CLAUDE.md for claude, AGENTS.md for codex,
	// agents/<slug>.md for opencode), leaving the rest of the sandbox
	// intact. Used by recovery.BootDirOps.RegenerateCLAUDEMD when a
	// watchdog_kill suggests a stuck agent that needs a fresh prompt.
	RegenerateSystemPromptSlot(bootDir string, params SetupParams) error

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

// LayoutFor returns the Layout for the named provider. Exposed so
// composition-root adapters (recovery.BootDirOps) can resolve a provider
// from a persisted runtime row without re-implementing the dispatch table.
func LayoutFor(provider string) Layout {
	return bootdirLayoutFor(provider)
}

// composeBootdirParams projects the inputs Boot already has on hand into
// a (Layout, SetupParams) pair. Pulled out so composition-root adapters
// (recovery.BootDirOps via ResolveBootdirParams) can rebuild the same
// params from a persisted runtime row without duplicating the
// system-prompt / boot-content / MCP-config plumbing.
func composeBootdirParams(deps *Dependencies, opts Options, profile *store.AgentProfile, sessID string) (Layout, SetupParams) {
	mcp := MCPConfig{}
	if deps != nil {
		mcp = deps.MCPConfig
	}
	layout := bootdirLayoutFor(effectiveProvider(opts, profile))
	params := SetupParams{
		SessionID:    sessID,
		RunID:        opts.RunID,
		AgentProfile: profile,
		Mode:         opts.Mode,
		// CW-20260516-0007: resolveBootPrompt (not bare composeSystemPrompt)
		// so SetupParams.SystemPrompt carries the AUTHORITATIVE boot prompt
		// — it honors Options.BootPromptOverride, which bootprofile-driven
		// launches set to a catalog-authored prompt. claudeLayout plants
		// this into CLAUDE.md; for non-bootprofile sessions resolveBootPrompt
		// is identical to the prior composeSystemPrompt result.
		SystemPrompt: resolveBootPrompt(profile, opts),
		BootContent:  composeBootContent(opts),
		ProjectDir:   opts.Workdir,
		MCPConfig:    mcp,
	}
	if deps != nil {
		params.CLIWritableRoots = effectiveCLIWritableRoots(deps, sessID)
	}
	return layout, params
}

func effectiveCLIWritableRoots(deps *Dependencies, sessionID string) []string {
	if deps == nil {
		return nil
	}

	seen := make(map[string]struct{})
	out := make([]string, 0, len(deps.CLIWritableRoots))
	add := func(path string) {
		if path == "" {
			return
		}
		clean := filepath.Clean(path)
		if clean == "." || clean == "" {
			return
		}
		if _, dup := seen[clean]; dup {
			return
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}

	for _, root := range deps.CLIWritableRoots {
		add(root)
	}

	if deps.PathGrants == nil || sessionID == "" {
		return out
	}

	for _, grant := range deps.PathGrants.ListGrants(sessionID) {
		add(grantAsWritableRoot(grant))
	}
	for _, grant := range deps.PathGrants.ListLineageGrants(sessionID) {
		add(grantAsWritableRoot(grant))
	}
	return out
}

func grantAsWritableRoot(grant string) string {
	if grant == "" {
		return ""
	}
	clean := filepath.Clean(grant)
	if info, err := os.Stat(clean); err == nil && info.IsDir() {
		return clean
	}
	return filepath.Dir(clean)
}

// ResolveBootdirParams is the composition-root entry point recovery's
// BootDirOps adapter uses to rebuild a SetupParams pair for an existing
// session. The adapter holds the original Options it captured at Boot
// time; this helper handles profile resolution + system-prompt
// composition without re-implementing the dispatch table.
//
// Returns an error when deps.Agents is unwired or profile resolution
// fails. Empty sessID is rejected — callers (recovery adapter) always
// have a sessionID on hand.
func ResolveBootdirParams(deps *Dependencies, opts Options, sessID string) (Layout, SetupParams, error) {
	if deps == nil {
		return nil, SetupParams{}, errors.New("agent.ResolveBootdirParams: nil Dependencies")
	}
	if deps.Agents == nil {
		return nil, SetupParams{}, errors.New("agent.ResolveBootdirParams: Dependencies.Agents is required")
	}
	if sessID == "" {
		return nil, SetupParams{}, errors.New("agent.ResolveBootdirParams: empty sessID")
	}
	profile, err := deps.Agents.GetOrDefault(opts.AgentProfile)
	if err != nil {
		return nil, SetupParams{}, fmt.Errorf("agent.ResolveBootdirParams: resolve profile: %w", err)
	}
	if profile == nil {
		return nil, SetupParams{}, errors.New("agent.ResolveBootdirParams: profile resolution returned nil")
	}
	layout, params := composeBootdirParams(deps, opts, profile, sessID)
	return layout, params, nil
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

	// CLIWritableRoots is the allow-list of directories a CLI-launch
	// agent (codex / claude) may write to beyond its throwaway boot dir.
	// It threads into the planted provider config: codex's
	// [sandbox_workspace_write] writable_roots and claude's
	// permissions.additionalDirectories. Sourced from the nanite
	// dev_tools_allowed_paths config setting (CW-20260518-0075). Empty
	// leaves the agent confined to its boot dir cwd.
	CLIWritableRoots []string
}

// bootdirLayoutFor returns the Layout for the named provider. Unsupported
// providers return a clear-error stub that fails Setup; callers iterate
// PlantedFiles only when the provider's BootDirSpec.Notes is empty per
// go-providers v0.8.0 guidance.
//
// CW-20260514-0045: prefixed CLI aliases ("pty", "pty-claude",
// "pty-codex", "pty-opencode", "sub-<x>") normalize to their bare
// adapter names before dispatch. This protects the bootdir layer when an
// older agent profile row carries a dropdown-shape default_provider; the
// chat-side normalization in chat_generate.go is the primary fix, this
// is the belt-and-suspenders guard at the runtime boundary.
func bootdirLayoutFor(provider string) Layout {
	provider = normalizeProviderName(provider)
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

// normalizeProviderName maps prefixed CLI aliases to bare adapter names.
// Duplicates chat.NormalizeCLIProvider's rules locally so the
// internal/runtime/agent package stays free of an internal/chat
// dependency (runtime is below chat in the layering — chat imports
// runtime, not the other way around). The two functions MUST stay in
// lock-step; see chat.NormalizeCLIProvider for the canonical rule set.
func normalizeProviderName(name string) string {
	switch {
	case name == "pty":
		return "claude"
	case len(name) > 4 && name[:4] == "pty-":
		return name[4:]
	case len(name) > 4 && name[:4] == "sub-":
		return name[4:]
	default:
		return name
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

func (u unsupportedLayout) Populate(string, SetupParams) error {
	return fmt.Errorf("agent: bootdir Populate for provider %q is not yet implemented", u.name)
}

func (u unsupportedLayout) RegenerateSystemPromptSlot(string, SetupParams) error {
	return fmt.Errorf("agent: bootdir RegenerateSystemPromptSlot for provider %q is not yet implemented", u.name)
}

func (u unsupportedLayout) AmendEnv(base map[string]string, _ string) map[string]string {
	return base
}

func (u unsupportedLayout) SpawnWorkdir(_, projectDir string) string       { return projectDir }
func (u unsupportedLayout) BootPrompt(*store.AgentProfile, Options) string { return "" }
func (u unsupportedLayout) BootMode() string                               { return "" }
