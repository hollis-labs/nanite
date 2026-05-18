package agent

import (
	"fmt"

	"github.com/hollis-labs/go-providers/provider"
)

// bootdir_provider_config.go sources the headless-agent CONFIG files
// (codex config.toml, claude .claude/settings.json) from go-providers'
// BootDirSpec instead of hand-rolling them Nanite-side.
//
// # Why only these two files
//
// Design-lock D5 keeps per-harness CONTENT (CLAUDE.md, AGENTS.md,
// .mcp.json, agent-context, envelope schema) consumer-owned — those stay
// hand-rolled in bootdir_claude.go / bootdir_codex.go / bootdir_plant.go.
// The provider CONFIG files are different: their schema (codex's
// approval_policy / sandbox_mode, claude's permissions.defaultMode) is
// owned by the CLI vendor, and go-providers v0.20.0 added first-class
// adapter knobs (CodexAdapter.ApprovalPolicy / .SandboxMode,
// ClaudeAdapter.PermissionMode) plus the renderers that emit the correct
// shape. Hand-rolling these risks drift with the CLI's evolving schema —
// and the codex case was a live bug (no config.toml planted at all → a
// headless codex with no non-interactive approval policy blocks forever
// on its first tool-approval request).
//
// # Headless-safe policy values (the deliberate choices)
//
// Nanite's boot dir is headless: the subordinate-dispatch path spawns the
// CLI with no TTY. A config that lets the CLI fall back to its
// interactive default would deadlock — there is no human to answer an
// approval prompt.
//
//   - codex: approval_policy = "never", sandbox_mode = "workspace-write".
//     "never" never prompts for approval; "workspace-write" lets the
//     agent write its workspace + /tmp (an agent that cannot write its
//     workspace cannot do work) while keeping network gated. This pair is
//     the orchestrated-run equivalent of codex's `--full-auto`, and it is
//     exactly go-providers' own resolveCodexExecPolicy headless-safe
//     default — we get it by leaving CodexAdapter.ApprovalPolicy /
//     .SandboxMode empty. "danger-full-access" was rejected as reckless;
//     "read-only" would let codex run but silently fail every write;
//     "untrusted" / "on-request" / "on-failure" can all surface an
//     approval request the headless runtime cannot answer.
//
//   - claude: permissions.defaultMode = "acceptEdits". This auto-accepts
//     file edits (the common headless tool action) without a prompt,
//     while still surfacing the explicit-confirmation path for the
//     riskier operations claude gates separately. "default" was rejected
//     because it prompts on the first tool use and the streaming-stdio
//     runtime (`claude -p ...`) has no TTY to answer it.
//     "bypassPermissions" — the settings-schema equivalent of
//     --dangerously-skip-permissions — was rejected as the production
//     default: it disables every permission gate, which is the reckless
//     end of the vocabulary.
//
// # Dev mode parity — argv wins, so the layout stays stateless
//
// In dev mode the runtime CLIAdapter is the Dev* constructor
// (NewClaudeAdapterDevStreamingStdio sets SkipPermissions=true →
// --dangerously-skip-permissions on argv). That CLI flag is strictly more
// permissive than any settings.json permissions.defaultMode and overrides
// it, so a planted "acceptEdits" is a no-op under dev mode and the dev
// experience is unchanged. Production (no argv flag) gets "acceptEdits"
// from the planted file. The layout therefore plants "acceptEdits"
// unconditionally and does NOT need to know dev mode — keeping
// claudeLayout stateless and the bootdir layer free of a dev-mode plumb.
//
// claudeDefaultPermissionMode is the single production knob; change it
// here if the headless-safe policy ever needs revisiting.
//
// # Render purity — no ~/.claude.json side effect
//
// go-providers' .claude/settings.json PlantedFile.Render performs a
// side effect (seedClaudeWorkspaceTrust writes ~/.claude.json) that is
// GATED on PlantContext.BootDir != "". Nanite spawns claude via the
// streaming-stdio runtime (`claude -p ...`), which is non-interactive —
// the first-run workspace-trust dialog is auto-skipped in non-interactive
// mode, so Nanite does not need the trust seed. We therefore render with
// PlantContext.BootDir = "" to keep the render PURE: the returned content
// is identical, and ~/.claude.json is left untouched. This also keeps
// Populate-against-an-existing-dir (crash recovery) free of global state
// mutation.

// codexConfigTOMLContent renders the codex config.toml body via
// go-providers' CodexAdapter.BootDirSpec(). The adapter is constructed
// with empty ApprovalPolicy / SandboxMode so go-providers' headless-safe
// resolveCodexExecPolicy defaults ("never" / "workspace-write") apply.
//
// writableRoots, when non-empty, sets CodexAdapter.WritableRoots so the
// planted config.toml carries a [sandbox_workspace_write] table widening
// the codex sandbox beyond the throwaway boot dir cwd. Under the default
// "workspace-write" SandboxMode codex confines writes to its cwd; without
// these roots a CLI-launch agent asked to write a real project path is
// silently confined to its boot dir (CW-20260518-0075). The list is
// sourced from the same nanite config setting (dev_tools_allowed_paths)
// that scopes the in-process dev_* tools — see resolveDevToolsAllowedPaths.
// Empty → no table is emitted and the config.toml is unchanged.
//
// PlantContext.MCPLoopbackURL is left empty deliberately. Nanite's MCP
// transport is subprocess-spawn-based — the planted .mcp.json names the
// nanite binary as a stdio child; there is no HTTP loopback URL to emit
// into an [mcp_servers.loopback] block. Wiring codex's MCP discovery
// (codex reads MCP servers from config.toml, NOT from the Nanite
// .mcp.json sidecar) is a separate, out-of-scope follow-up — see the
// report.
func codexConfigTOMLContent(writableRoots []string) (string, error) {
	adapter := provider.NewCodexAdapter()
	adapter.WritableRoots = writableRoots
	return renderProviderConfigFile(adapter, "config.toml", provider.PlantContext{})
}

// codexConfigFileMode is the file mode go-providers' codex BootDirSpec
// declares for config.toml and auth.json — 0o600, because both embed
// per-task secret-ish content (config.toml the loopback URL when present,
// auth.json the user's OAuth tokens / API key). Nanite honors it so the
// planted files are not world-readable.
const codexConfigFileMode = 0o600

// codexAuthJSONContent renders the codex auth.json body via go-providers'
// CodexAdapter.BootDirSpec().
//
// # Why this file is planted alongside config.toml
//
// Codex reads its config (config.toml) AND its auth (auth.json) from
// $CODEX_HOME. The codex layout sets CODEX_HOME=<bootDir> (see
// codexLayout.AmendEnv) so the planted config.toml is the one codex
// actually consults — without that env pointer codex reads
// ~/.codex/config.toml and the plant is inert. But once CODEX_HOME points
// at the boot dir, codex ALSO looks for <bootDir>/auth.json instead of
// ~/.codex/auth.json. Planting only config.toml would therefore fix the
// approval-hang but break auth ("Not logged in"). config.toml + auth.json
// + CODEX_HOME are a coherent set — go-providers' codex BootDirSpec
// declares all three together for exactly this reason.
//
// The render copies the user's ~/.codex/auth.json (honoring $CODEX_HOME
// in the PARENT env, matching codex's own discovery rule). If the user
// isn't logged in, it returns "" — the planted auth.json is empty and
// codex surfaces "Not logged in" at dispatch time, the same outcome as
// today's (CODEX_HOME-unset) path. A non-NotExist read error (permission
// denied, etc.) bubbles up so the operator sees an actionable message.
//
// PlantContext.BootDir is left empty; the auth.json Render has no
// BootDir-gated side effect (it only reads the source file).
func codexAuthJSONContent() (string, error) {
	adapter := provider.NewCodexAdapter()
	return renderProviderConfigFile(adapter, "auth.json", provider.PlantContext{})
}

// claudeDefaultPermissionMode is the permissions.defaultMode planted into
// every Nanite claude boot dir's .claude/settings.json. "acceptEdits"
// auto-accepts file edits without a prompt — required for the headless
// streaming-stdio runtime (`claude -p ...`, no TTY) — without the
// blanket gate-disable of "bypassPermissions". See the file header for
// the full rationale and the dev-mode-parity argument.
const claudeDefaultPermissionMode = "acceptEdits"

// claudeProviderConfigContent renders the .claude/settings.json body via
// go-providers' ClaudeAdapter.BootDirSpec(), planting
// permissions.defaultMode = claudeDefaultPermissionMode.
//
// additionalDirectories, when non-empty, sets
// ClaudeAdapter.AdditionalDirectories so the planted settings.json
// carries permissions.additionalDirectories — the directories claude
// may access beyond its boot dir cwd. This is claude's analogue of the
// codex WritableRoots widening; both are sourced from the same nanite
// config setting (dev_tools_allowed_paths) so a CLI-launch agent's
// file-write scope is consistent across providers (CW-20260518-0075).
// Empty → no additionalDirectories key is emitted.
//
// PlantContext.BootDir is left empty so the render stays pure (no
// ~/.claude.json trust seed — see file header).
func claudeProviderConfigContent(additionalDirectories []string) (string, error) {
	adapter := provider.NewClaudeAdapter()
	adapter.PermissionMode = claudeDefaultPermissionMode
	adapter.AdditionalDirectories = additionalDirectories
	return renderProviderConfigFile(adapter, ".claude/settings.json", provider.PlantContext{})
}

// renderProviderConfigFile obtains the named PlantedFile from a
// go-providers adapter's BootDirSpec and renders it with ctx. It is the
// single seam between Nanite's bootdir layouts and go-providers' config
// renderers — Nanite owns the planting (plantInjectionSpec), go-providers
// owns the config-file CONTENT.
//
// An adapter that does not implement BootDirProvider, or a BootDirSpec
// that does not declare relPath, is a programmer error (the go-providers
// API contract guarantees both for claude/codex) — surfaced as an error
// so a future go-providers bump that drops the file fails loudly here
// rather than silently planting nothing.
func renderProviderConfigFile(adapter provider.CLIAdapter, relPath string, ctx provider.PlantContext) (string, error) {
	bp, ok := adapter.(provider.BootDirProvider)
	if !ok {
		return "", fmt.Errorf("agent: provider adapter %T does not expose a BootDirSpec", adapter)
	}
	spec := bp.BootDirSpec()
	for _, pf := range spec.PlantedFiles {
		if pf.RelPath != relPath {
			continue
		}
		if pf.Render == nil {
			return "", fmt.Errorf("agent: BootDirSpec planted file %q has no Render", relPath)
		}
		content, err := pf.Render(ctx)
		if err != nil {
			return "", fmt.Errorf("agent: render provider config %q: %w", relPath, err)
		}
		return content, nil
	}
	return "", fmt.Errorf("agent: BootDirSpec does not declare planted file %q", relPath)
}
