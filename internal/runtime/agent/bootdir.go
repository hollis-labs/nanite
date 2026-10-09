package agent

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/harness/adapters/registry"
	plant "github.com/hollis-labs/substrate/harness/agentlaunch/planting"
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

	// Populate retains an existing bound root and returns typed unavailable.
	// Saved ownership metadata and between-turn timing do not prove that a
	// provider has stopped reading its binding. Fresh Setup has a custody port.
	Populate(bootDir string, params SetupParams) (plant.PlantResult, error)

	// RegenerateSystemPromptSlot returns typed unavailable without changing
	// the current instructions or binding under the inactive-only contract.
	RegenerateSystemPromptSlot(bootDir string, params SetupParams) error

	// AmendEnv merges provider-specific env additions onto the base env
	// map composed by composeEnv.
	AmendEnv(base map[string]string, bootDir string) map[string]string

	// SpawnWorkdir returns the working directory for the spawn. claude /
	// codex spawn from the boot dir; opencode spawns from the project dir
	// (falling back to the boot dir when projectDir is empty — see
	// TASKS/agent-host-acp/18) with OPENCODE_CONFIG_DIR pointing at the
	// boot dir. MUST NOT return "" — the caller feeds this straight into
	// wrapper.Config.Workdir, which wrapper.Wrapper.Run hard-requires to
	// be non-empty.
	SpawnWorkdir(bootDir, projectDir string) string

	// BootPrompt returns the system-prompt payload threaded into
	// legacy layout consumers. Boot uses the planted instruction file;
	// slot regeneration requires a fresh binding under the custody contract.
	BootPrompt(profile *store.AgentProfile, opts Options) string

	// BootMode returns the boot-prompt delivery mode threaded into
	// legacy layout consumers. Boot does not consult this hint;
	// native headless runtimes use planted files and framed turn input.
	BootMode() string
}

// LayoutFor returns the Layout for the named provider. Exposed so
// composition-root adapters (recovery.BootDirOps) can resolve a provider
// from a persisted runtime row without re-implementing the dispatch table.
func LayoutFor(provider string) Layout {
	return bootdirLayoutFor(provider)
}

// HasBootdirLayout reports whether provider (after CLI-alias
// normalization) has a real, implemented bootdir Layout — claude, codex,
// and opencode, the headless CLI runtimes agent.Boot can actually
// materialize a boot dir for. Everything else, including every plain HTTP
// API provider (anthropic, openai, gemini-api, openrouter, ...) and every
// CLI tool without a Layout yet (gemini, copilot, aider, junie), resolves
// to unsupportedLayout and would fail Setup with a "not yet implemented"
// error.
//
// Exposed so a caller deciding whether to even attempt a CLI-boot
// recovery/replacement session can skip it cleanly for a provider that
// structurally can never succeed, rather than dispatching, hitting
// unsupportedLayout.Setup's error, and having that read as a genuine
// per-session recovery failure (CW-20260815-0024: this was happening on
// every HTTP-provider chat-stream error, with the recovery broker's own
// breadcrumbs recording a guaranteed "permanent failure" outcome that was
// never actually assessable — there was nothing to retry).
func HasBootdirLayout(provider string) bool {
	switch normalizeProviderName(provider) {
	case "claude", "claude-code", "claudecode", "codex", "opencode":
		return true
	default:
		return false
	}
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
		// CW-20260910-0015: the boot-dir hook set. DefaultBootDirHooks is
		// empty, so this plants nothing today — the point of wiring it
		// here anyway is that the mechanism is REACHABLE from the real
		// boot path rather than only from tests. CW-20260910-0016 decides
		// what, if anything, that slice should contain; when it does, no
		// plumbing has to change.
		Hooks: DefaultBootDirHooks,
	}
	params.CLIWritableRoots = effectiveCLIWritableRoots(deps, sessID, opts.Workdir)
	if deps != nil {
		// No root offered to an agent (the work root, dev_tools_allowed_paths,
		// path grants) may be, or sit inside, a control-plane directory
		// (CW-20261001-0143). Codex also has the roots that contain one split
		// around it, because its own sandbox, not Nanite's, enforces them.
		_, codex := layout.(codexLayout)
		params.CLIWritableRoots = rootsOutsideProtected(params.CLIWritableRoots,
			deps.ControlPlane.protectedFor(opts.Workdir, naniteHomeDir()), codex)
		params.Skills = deps.Skills
		params.SkillVendor = deps.SkillVendor
	}
	return layout, params
}

// PathMentionLaunchRootsEnv is the kill switch for CW-20261001-0232. Set to
// "1", the session's and its lineage's path grants feed a CLI launch's
// writable roots again, as they did before. Default off.
const PathMentionLaunchRootsEnv = "NANITE_PATH_MENTION_LAUNCH_ROOTS"

// PathMentionLaunchRootsEnabled reports whether the kill switch is on.
func PathMentionLaunchRootsEnabled() bool {
	return os.Getenv(PathMentionLaunchRootsEnv) == "1"
}

// effectiveCLIWritableRoots lists the directories a CLI agent may write
// beyond its boot dir: the session's work root first (CW-20261001-0020 —
// the boot dir stays the cwd, so without this the project is outside the
// agent's sandbox), then the configured dev_tools_allowed_paths roots.
// Duplicates are dropped, and so is any root that is not an existing
// directory. A nil deps still yields the work root.
//
// Only configuration widens the roots. Path grants are minted from free
// text in a turn, which any loopback client can supply (CW-20261001-0232),
// so they stay with the in-process dev_* tools and are not folded in here,
// unless the PathMentionLaunchRootsEnv kill switch asks for the old
// behavior. Even then they are only grants that survived the mention
// policy (permission.MentionPolicy).
func effectiveCLIWritableRoots(deps *Dependencies, sessionID, workRoot string) []string {
	seen := make(map[string]struct{})
	var out []string
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
		// A root that is not an existing directory is never handed to a CLI:
		// Codex's sandbox binds every writable root, and one that is missing
		// ("bwrap: Can't bind mount ... No such file or directory") makes every
		// command of the turn fail.
		if info, err := os.Stat(clean); err != nil || !info.IsDir() {
			slog.Debug("agent: dropping CLI writable root that is not an existing directory", "root", clean)
			return
		}
		out = append(out, clean)
	}

	add(workRoot)
	if deps == nil {
		return out
	}
	for _, root := range deps.CLIWritableRoots {
		add(root)
	}

	if !PathMentionLaunchRootsEnabled() || deps.PathGrants == nil || sessionID == "" {
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

// grantAsWritableRoot is the directory a grant stands for: the grant itself
// when it is a directory, its directory when it is a file, and nothing when it
// does not exist. A mention of a file to be created registers its parent
// directory as a grant of its own (permission Q2), so a path that is missing
// is not widened to its parent here: that would turn a mention of
// ~/newdir/x.txt into a root of $HOME.
func grantAsWritableRoot(grant string) string {
	if grant == "" {
		return ""
	}
	clean := filepath.Clean(grant)
	info, err := os.Stat(clean)
	if err != nil {
		return ""
	}
	if info.IsDir() {
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

	// Skills resolves AgentProfile's plantable skill set (grant +
	// catalog lookups) for skill_plant.go's CLI-hosted native skill
	// delivery (TASKS/skills/10). nil disables skill planting entirely
	// (skillFilesForProvider's own "nothing to plant" contract) — a
	// composition root that hasn't wired skill support sees no skill
	// files planted, not an error.
	Skills SkillStore

	// SkillVendor reads a plantable skill's vendored file tree
	// (internal/skillvendor.Store.ReadFiles) for skill_plant.go. nil
	// disables skill planting, same as a nil Skills.
	SkillVendor SkillVendorReader

	// Hooks is the hook set planted into the boot dir and declared in the
	// provider's settings so the harness actually runs it
	// (CW-20260910-0015, bootdir_hooks.go). Empty — the zero value and
	// the DefaultBootDirHooks default — plants nothing and leaves the
	// planted settings byte-identical to a no-hook boot.
	//
	// A hook is a MECHANICALLY TRIGGERED steer, which is the whole reason
	// the field exists: agent-setup's gate inventory §4 finds that prose
	// an agent has to remember does not work, while something that fires
	// at the event does. Which hooks Nanite ships by default, if any, is
	// CW-20260910-0016 — not a decision this field makes.
	//
	// Only claude has verified wiring. Supplying hooks for codex or
	// opencode is an error rather than a silent no-op; see
	// bootdir_hooks.go's header for why.
	Hooks []BootDirHook
}

// bootdirLayoutFor returns the Layout for the named provider. Unsupported
// providers return a clear-error stub that fails Setup; callers iterate
// PlantedFiles only when the provider's BootDirSpec.Notes is empty per
// go-providers v0.8.0 guidance.
//
// Not a CLI-vs-API routing decision — this only ever runs once the CLI
// path has already been chosen upstream (Phase 2 item 01,
// TASKS/phase-2/01-wire-runtime-kind-routing.md's runtime_kind field).
// Its own dispatch answers a narrower, WHICH-CLI-adapter question
// (claude vs. codex vs. opencode) that runtime_kind alone can't answer —
// it genuinely needs the provider name string for that.
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
//
// Post-decision string-shape helper only (Phase 2 item 01,
// TASKS/phase-2/01-wire-runtime-kind-routing.md) — deriving the bare
// adapter name once CLI routing is already known, not deciding CLI-vs-API
// itself.
//
// After the prefix is stripped, a name the go-providers registry resolves
// (case-insensitively, with its aliases: "Claude", "claude-code",
// "open-code", "agy") becomes the registry's canonical id
// (CW-20260930-0113). Every name-keyed decision in this package (the
// boot-dir layout, Claude framing, workRootArgs, skill planting) then agrees
// with the runtime selectRuntime launched and CanLaunch routed. A name the
// registry does not carry is returned as stripped.
func normalizeProviderName(name string) string {
	stripped := name
	switch {
	case name == "pty":
		stripped = "claude"
	case len(name) > 4 && name[:4] == "pty-":
		stripped = name[4:]
	case len(name) > 4 && name[:4] == "sub-":
		stripped = name[4:]
	}
	if d, ok := registry.Lookup(stripped); ok {
		return string(d.ID)
	}
	return stripped
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

func (u unsupportedLayout) Populate(string, SetupParams) (plant.PlantResult, error) {
	return plant.PlantResult{}, fmt.Errorf("agent: bootdir Populate for provider %q is not yet implemented", u.name)
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
