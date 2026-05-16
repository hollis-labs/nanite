package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/hollis-labs/go-agent-launch/agentlaunch"
	"github.com/hollis-labs/nanite/internal/fsutil"
)

// bootdir_plant.go is the convergence point onto shared go-agent-launch
// bootdir machinery for CW-20260515-0025.
//
// # Why this shape, and why NOT providerplant.Plant
//
// The shared planter providerplant.Plant operates on a fully Compiled +
// Prepared agentlaunch.LaunchPlan: it resolves a go-providers adapter for
// the launch's provider×runtime pair, renders that adapter's BootDirSpec,
// and rewires a PreparedLaunch's Env/Argv/Workdir in place. Phase 5
// (Torque) adopted it because Torque launches already flow through the
// LaunchPlan → launcher.Compile → launcher.Prepare pipeline.
//
// Nanite's runtime boot path (agent.Boot, the Layout interface) does NOT.
// agent.Boot works directly off a store.AgentProfile; there is no
// LaunchPlan in scope at Setup time, and the boot-profile bridge
// (LaunchSpec.ToLaunchPlan, CW-0024) only exists on the dropdown-driven
// path, not the runtime. Synthesizing a LaunchPlan per boot purely to
// satisfy providerplant.Plant would reshape the runtime — the task rule
// is explicit that only mechanical assembly moves, not app semantics.
//
// Two further mismatches make providerplant.Plant a poor fit here:
//
//   - The Layout contract needs slot-only re-planting
//     (RegenerateSystemPromptSlot) and Populate-against-an-existing-dir
//     for crash recovery (recovery.BootDirOps). providerplant.Plant is a
//     one-shot planter that also appends argv on each call.
//   - go-providers' own BootDirSpec renderers (renderClaudeMD,
//     renderMCPJSON, the codex config.toml/auth.json files) produce
//     materially different content from Nanite's planted files — swapping
//     to them would regress the "functionally equivalent" acceptance bar.
//     Nanite's CLAUDE.md/envelope/agent-context content is app business
//     logic and stays Nanite-side per the task's "keep a thin adapter"
//     rule.
//
// # What this file moves onto shared machinery
//
// The genuinely mechanical, app-agnostic part of bootdir assembly is the
// file-set model and the path-safe write loop. Each Layout now declares
// its complete bootdir contents as an agentlaunch.InjectionSpec
// (NativeFiles for files at caller-known paths, BootDirOverlay for the
// flat path→content escape hatch). plantInjectionSpec then writes that
// spec using the same shared primitives providerplant uses internally:
//
//   - agentlaunch.NativeFile / agentlaunch.InjectionSpec — the shared
//     vocabulary for "extra files planted into a bootdir".
//   - agentlaunch.ValidateBootDirRelPath — the shared bootdir path-safety
//     gate (rejects "..", absolute paths, reserved names like .git/.ssh).
//   - The documented planting order (provider/native files first, then
//     the overlay, overlay-wins-last) mirrors providerplant's contract.
//
// Nanite's app-extra files (the .sandbox/ envelope schema + agent-context
// docs) ride this exact path as NativeFile entries — they are NOT pushed
// into any shared package, satisfying the task's "KEEP as Nanite
// app-extra planted files" rule while still being represented in the
// shared InjectionSpec type.
//
// # Lifecycle / cleanup ownership
//
// Cleanup stays entirely Nanite-app-owned, exactly as before: makeBootDir
// rolls the forensic $TMPDIR path, Layout.Setup os.RemoveAll's it on any
// post-mkdir failure, and agent.Boot's deferred cleanup removes it on any
// later failure (the pre-Start leak guard). No go-agent-sessions
// AutoPlantBootDir is involved, so there is no double-planting risk.

// nativeFileRaw is a small constructor for an agentlaunch.NativeFile of
// the "raw" kind — a file written verbatim at a caller-known
// bootdir-relative path. Mode 0 falls back to 0o644 at plant time.
func nativeFileRaw(relPath, content string, mode os.FileMode) agentlaunch.NativeFile {
	return agentlaunch.NativeFile{
		Kind:    agentlaunch.NativeFileRaw,
		RelPath: relPath,
		Content: content,
		Mode:    mode,
	}
}

// plantInjectionSpec materializes an agentlaunch.InjectionSpec into
// bootDir. It is the shared-machinery write routine the per-provider
// Layouts route through instead of hand-coding their own
// os.MkdirAll + WriteFile loops.
//
// Planting order mirrors providerplant.Plant's documented contract:
//
//  1. InjectionSpec.NativeFiles — written in declaration order. Nanite
//     only uses the NativeFileRaw kind (caller-known paths); the
//     NativeFileSkill kind is validated but unused here.
//  2. InjectionSpec.BootDirOverlay — the flat path→content map, written
//     last in sorted key order so an overlay entry deterministically
//     wins over a native file at the same path.
//
// Every target path is re-validated through agentlaunch.ValidateBootDirRelPath
// (defence in depth: the same gate providerplant applies at plant phase)
// so a buggy renderer cannot escape the bootdir. Writes use
// fsutil.AtomicWriteFile to preserve Nanite's idempotent-Populate
// guarantee (temp-file + rename, no read of prior state).
func plantInjectionSpec(bootDir string, spec agentlaunch.InjectionSpec) error {
	for i := range spec.NativeFiles {
		nf := spec.NativeFiles[i]
		if err := nf.Validate(); err != nil {
			return fmt.Errorf("agent: bootdir native file: %w", err)
		}
		if nf.Kind != agentlaunch.NativeFileRaw {
			// Nanite has no provider-native skill files in the bootdir;
			// reject anything else loudly rather than silently mis-pathing.
			return fmt.Errorf("agent: bootdir native file %q: unsupported kind %q", nf.RelPath, nf.Kind)
		}
		if err := writePlantedFile(bootDir, nf.RelPath, nf.Content, nf.Mode); err != nil {
			return err
		}
	}

	if len(spec.BootDirOverlay) > 0 {
		keys := make([]string, 0, len(spec.BootDirOverlay))
		for k := range spec.BootDirOverlay {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := writePlantedFile(bootDir, k, spec.BootDirOverlay[k], 0); err != nil {
				return err
			}
		}
	}
	return nil
}

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

// sandboxNativeFiles returns the Nanite app-extra .sandbox/ files as
// agentlaunch.NativeFile entries. These are deliberately NOT pushed into
// a shared package — the envelope schema and agent-context doc are Nanite
// product surface — but they ride the shared InjectionSpec planting path
// as raw native files (the task's "represent app extras via
// InjectionSpec.NativeFiles" rule).
func sandboxNativeFiles(params SetupParams) []agentlaunch.NativeFile {
	return []agentlaunch.NativeFile{
		nativeFileRaw(".sandbox/agent-context.md", BuildAgentContext(params.AgentProfile, nil), 0o644),
		nativeFileRaw(".sandbox/envelope-schema.md", envelopeSchemaContent, 0o644),
	}
}

// bootMDNativeFile returns the boot.md kickoff target as a raw native
// file (the "Boot @./boot.md" include target).
func bootMDNativeFile(params SetupParams) agentlaunch.NativeFile {
	return nativeFileRaw("boot.md", params.BootContent, 0o644)
}

// mcpOverlay returns the .mcp.json descriptor as a single-entry overlay
// map, or an empty map when MCP planting is disabled (zero-value DBPath).
// .mcp.json is modelled as a BootDirOverlay entry rather than a
// NativeFile so it is planted last and wins over any same-path provider
// file — matching providerplant's overlay-wins-last ordering.
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
