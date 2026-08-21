package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/go-agent-wrapper/plant"
	"github.com/hollis-labs/nanite/internal/fsutil"
)

// bootdir_plant.go is the convergence point onto go-agent-wrapper's
// plant.Planter contract (TASKS/agent-host-acp/04). It previously
// converged onto agentkit/agentlaunch's InjectionSpec (CW-20260515-0025);
// this migration replaces that InjectionSpec-based write path with
// plant.Planter implementations, one per provider (claudePlanter in
// bootdir_claude.go, codexPlanter in bootdir_codex.go, opencodePlanter in
// bootdir_opencode.go).
//
// # Why plant.Planter, and why NOT providerplant.Plant
//
// The shared planter agentkit/agentlaunch/providerplant.Plant operates on
// a fully Compiled + Prepared agentlaunch.LaunchPlan: it resolves a
// go-providers adapter for the launch's provider×runtime pair, renders
// that adapter's BootDirSpec, and rewires a PreparedLaunch's Env/Argv/
// Workdir in place. Nanite's runtime boot path (agent.Boot, the Layout
// interface) does NOT flow through a LaunchPlan — agent.Boot works
// directly off a store.AgentProfile, and synthesizing a LaunchPlan purely
// to satisfy providerplant.Plant would reshape the runtime. Two further
// mismatches still rule it out: the Layout contract needs slot-only
// re-planting (RegenerateSystemPromptSlot) and Populate-against-an-
// existing-dir for crash recovery, while providerplant.Plant is a
// one-shot planter that also appends argv on each call; and go-providers'
// own BootDirSpec renderers produce materially different CLAUDE.md/
// envelope/agent-context content than Nanite's — that content is app
// business logic and stays Nanite-side.
//
// go-agent-wrapper's plant.Planter (github.com/hollis-labs/go-agent-
// wrapper/plant) is a different, much thinner contract than
// providerplant.Plant: a single method, Plant(ctx, bootDir, Spec)
// (Result, error), callable any number of times against an existing
// directory, with no LaunchPlan or Compiled/Prepared launch in scope.
// That removes both objections above — Nanite still builds every file's
// CONTENT itself (CLAUDE.md/AGENTS.md/opencode prompt files, the provider
// config renderers in bootdir_provider_config.go, the app-extra
// .sandbox/ docs); plant.Planter only supplies the destination-agnostic
// Spec vocabulary (Files/MCPConfig/ProviderSettings/Hooks/RecoveryPrompt)
// and a Result reporting shape. Nanite's own claudePlanter/codexPlanter/
// opencodePlanter decide where each Spec field lands, reusing the same
// writePlantedFile primitive (path-safety gate + atomic write) the prior
// InjectionSpec-based mechanism used.
//
// # What did NOT move onto Planter
//
// Layout.SpawnWorkdir / Layout.BootMode / Layout.BootPrompt are
// deliberately NOT part of this migration — see the Layout interface doc
// in bootdir.go and TASKS/agent-host-acp/04's Context. plant.Planter's
// contract is file-planting only; it has no concept of workdir selection
// or boot-mode signaling, and forcing those two concerns into it would
// misrepresent them as file-planting mechanics when they are actually
// lifecycle policy (where a process runs, how the boot prompt is
// delivered) — Nanite-owned per the agent-host boundary doc
// (docs/engineering/architecture/16-agent-host.md). BootPrompt (the
// prompt STRING) is product content (agent roles/skills) and stays
// entirely Nanite-owned for the same reason; only the FILE that carries
// it (CLAUDE.md/AGENTS.md/agents/<slug>.md) is planted through Planter.
//
// # Lifecycle / cleanup ownership
//
// Cleanup stays entirely Nanite-app-owned, exactly as before: makeBootDir
// rolls the forensic $TMPDIR path, Layout.Setup os.RemoveAll's it on any
// post-mkdir failure, and agent.Boot's deferred cleanup removes it on any
// later failure (the pre-Start leak guard).

// writePlantedFile validates relPath through the shared bootdir
// path-safety gate, creates any intermediate directories, and atomically
// writes content. mode 0 falls back to 0o644.
func writePlantedFile(bootDir, relPath, content string, mode os.FileMode) error {
	if err := agentlaunch.ValidateBootDirRelPath(relPath); err != nil {
		return fmt.Errorf("agent: bootdir plant %q: %w", relPath, err)
	}
	if mode == 0 {
		mode = 0o644
	}
	path := filepath.Join(bootDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("agent: bootdir plant %q: mkdir: %w", relPath, err)
	}
	if err := fsutil.AtomicWriteFile(path, []byte(content), mode); err != nil {
		return fmt.Errorf("agent: bootdir plant %q: %w", relPath, err)
	}
	return nil
}

// plantConfig is the per-provider destination knowledge a Nanite
// plant.Planter implementation supplies — which of Spec's generic fields
// map to which bootdir-relative path, and with what file mode. Encoding
// this per concrete Planter type (rather than in plant.Spec itself, which
// deliberately stays destination-agnostic) keeps go-agent-wrapper's
// contract thin while letting Nanite's file-planting mechanics — e.g.
// codex's config.toml/auth.json needing 0o600, not the 0o644 default —
// live where they always have: Nanite-side.
type plantConfig struct {
	// provider is the Spec.ProviderSettings map key this Planter reads.
	provider string
	// providerSettingsPath is the bootdir-relative path
	// Spec.ProviderSettings[provider] is written to. Empty means this
	// provider has no ProviderSettings destination (opencode: its
	// agents.json/opencode.json descriptors are hand-rolled Nanite
	// content, not a go-providers-sourced settings file, so they ride
	// Files instead).
	providerSettingsPath string
	// providerSettingsMode is the file mode for providerSettingsPath.
	// 0 falls back to 0o644.
	providerSettingsMode os.FileMode
	// fileModeOverrides sets a non-default mode for specific Files
	// entries (codex's auth.json needs 0o600 alongside config.toml,
	// same as go-providers' BootDirSpec declares).
	fileModeOverrides map[string]os.FileMode
}

// plantSpec is the shared write routine every per-provider Planter
// (claudePlanter/codexPlanter/opencodePlanter) delegates to. It writes
// spec.Files (sorted for deterministic Result.PlantedFiles order), then
// spec.MCPConfig (the ".mcp.json" shortcut), then
// spec.ProviderSettings[cfg.provider] at cfg.providerSettingsPath.
//
// spec.Hooks and spec.RecoveryPrompt have no Nanite plant target yet —
// nothing in this codebase populates either field today (see the
// claude/codex/opencode PlantSpec builders), so a non-empty value here
// can only mean a future caller expected behavior this Planter doesn't
// implement. Rejected loudly rather than silently dropped, matching the
// "unsupported kind" guard the prior InjectionSpec-based mechanism used
// for non-raw NativeFiles.
func plantSpec(bootDir string, spec plant.Spec, cfg plantConfig) (plant.Result, error) {
	if len(spec.Hooks) > 0 {
		return plant.Result{}, fmt.Errorf("agent: bootdir Planter(%s): hooks are not yet supported", cfg.provider)
	}
	if spec.RecoveryPrompt != "" {
		return plant.Result{}, fmt.Errorf("agent: bootdir Planter(%s): RecoveryPrompt is not yet supported", cfg.provider)
	}

	var planted []string

	keys := make([]string, 0, len(spec.Files))
	for relPath := range spec.Files {
		keys = append(keys, relPath)
	}
	sort.Strings(keys)
	for _, relPath := range keys {
		mode := cfg.fileModeOverrides[relPath]
		if err := writePlantedFile(bootDir, relPath, string(spec.Files[relPath]), mode); err != nil {
			return plant.Result{}, err
		}
		planted = append(planted, filepath.Join(bootDir, filepath.FromSlash(relPath)))
	}

	if len(spec.MCPConfig) > 0 {
		if err := writePlantedFile(bootDir, ".mcp.json", string(spec.MCPConfig), 0); err != nil {
			return plant.Result{}, err
		}
		planted = append(planted, filepath.Join(bootDir, ".mcp.json"))
	}

	if cfg.providerSettingsPath != "" {
		if content, ok := spec.ProviderSettings[cfg.provider]; ok {
			if err := writePlantedFile(bootDir, cfg.providerSettingsPath, string(content), cfg.providerSettingsMode); err != nil {
				return plant.Result{}, err
			}
			planted = append(planted, filepath.Join(bootDir, filepath.FromSlash(cfg.providerSettingsPath)))
		}
	}

	sort.Strings(planted)
	return plant.Result{PlantedFiles: planted}, nil
}

// sandboxFiles returns the Nanite app-extra .sandbox/ files as
// plant.Spec.Files entries. Deliberately NOT pushed into a shared
// package — the envelope schema and agent-context doc are Nanite product
// surface — but they ride the same Files vocabulary as every other
// planted file.
func sandboxFiles(params SetupParams) map[string][]byte {
	return map[string][]byte{
		".sandbox/agent-context.md":   []byte(BuildAgentContext(params.AgentProfile)),
		".sandbox/envelope-schema.md": []byte(envelopeSchemaContent),
	}
}

// mcpConfigBytes renders the .mcp.json descriptor as plant.Spec.MCPConfig
// bytes, or nil when MCP planting is disabled (zero-value MCPConfig).
// Delegates to mcpOverlay (below) for the actual render + Mode gating,
// lifting its single ".mcp.json" entry into the []byte shape
// plant.Spec.MCPConfig expects.
func mcpConfigBytes(params SetupParams) ([]byte, error) {
	overlay, err := mcpOverlay(params)
	if err != nil {
		return nil, err
	}
	body, ok := overlay[".mcp.json"]
	if !ok {
		return nil, nil
	}
	return []byte(body), nil
}

// mcpOverlay returns the .mcp.json descriptor as a single-entry map, or
// an empty map when MCP planting is disabled (zero-value DBPath). Kept
// as its own function (rather than folded into mcpConfigBytes) because
// sandbox_content_mcp_test.go exercises it directly, and because it is
// the single seam that applies the live-harness-proxy Mode gating below.
//
// Returns an error when the MCP config is internally inconsistent
// (BinaryPath required when DBPath is set) so a misconfigured boot fails
// fast rather than planting a broken descriptor.
func mcpOverlay(params SetupParams) (map[string]string, error) {
	cfg := params.MCPConfig
	// The live-harness self-tools proxy (NANITE_API_URL) is a chat-agent
	// affordance only. Subagent / background / one-shot launches follow the
	// standard boot and dispatch self-tools locally against their own store,
	// so strip the API URL for them — renderMCPJSON then omits the env.
	// ModeResume is kept proxied: it re-boots a crash-recovered chat agent.
	if cfg.APIBaseURL != "" && params.Mode != ModeLongLived && params.Mode != ModeResume {
		cfg.APIBaseURL = ""
	}
	body, err := renderMCPJSON(cfg, params.SessionID)
	if err != nil {
		return nil, err
	}
	if body == "" {
		return nil, nil
	}
	return map[string]string{".mcp.json": body}, nil
}
