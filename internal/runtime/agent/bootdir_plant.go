package agent

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/artifact"
	"github.com/hollis-labs/go-agent-wrapper/plant"
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
// opencodePlanter decide where each Spec field lands.
//
// # The write mechanics: agentkit's shared materialization engine
//
// CW-20260910-0020: the actual bytes-to-disk step is no longer a
// hand-rolled per-file loop. bootDirArtifactTree below converts the
// app-level plant.Spec into an agentkit artifact.Tree — carrying Nanite's
// OWN per-provider destinations and modes, from plantConfig — and
// plantSpec hands that tree to plant.SharedPlanter, which routes it
// through agentlaunch.MaterializeArtifacts into agentkit's shared
// materialize.Engine. That engine owns the atomic temp-file + rename
// write (via os.Root, so the write cannot escape the boot dir) and
// returns a materialize.Handle carrying the manifest and per-entry
// change data, which Layout.Populate now surfaces to its caller.
//
// Building Spec.Artifacts here is deliberate and is the whole point:
// SharedPlanter ALSO accepts the legacy Spec.Files/MCPConfig/
// ProviderSettings fields, but converts them on its own fixed
// conventions — Files 0644, MCPConfig 0600, ProviderSettings to its own
// providerSettingsPath(provider), Hooks 0700. Nanite's destinations and
// modes differ (codex's config.toml/auth.json at 0600, opencode's
// hand-rolled descriptors riding Files rather than ProviderSettings), so
// leaning on that legacy conversion would silently relocate files and
// change modes. Nothing in this package populates the legacy fields on
// the Spec it hands to SharedPlanter.
//
// # The ownership rule this buys, and the trap it creates
//
// The shared engine tracks what it owns in a manifest it writes into the
// boot dir at .agentkit/materialize-manifest.json. Reconcile (the
// operation Nanite uses — see plantSpec) classifies a desired path that
// EXISTS ON DISK but is ABSENT FROM THE MANIFEST as an ownership
// conflict, and refuses the whole plant before writing anything. The
// ConflictOverwrite policy does NOT rescue that case; it is only
// consulted for paths the manifest already knows.
//
// Consequence, and it is load-bearing: once the engine owns a boot dir,
// EVERY writer into that dir must go through the engine. A file dropped
// in by a direct os.WriteFile becomes a landmine for the next plant that
// wants to own its path. This is why PlantAgentSkillFiles (skill_plant.go),
// which plants newly-granted skills into a LIVE session's boot dir, was
// migrated onto this same path in CW-20260910-0020 rather than left on a
// direct write loop — a mid-session skill planted outside the manifest
// would have made the next crash-recovery Repopulate fail outright.
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

// plantedFileMode is the mode a planted file gets when neither
// plantConfig.fileModeOverrides nor plantConfig.providerSettingsMode
// names one. Matches the pre-CW-20260910-0020 writePlantedFile default.
const plantedFileMode os.FileMode = 0o644

// mcpConfigFileMode is the mode for the planted ".mcp.json" descriptor.
// Deliberately 0o644 — the mode Nanite has always written it at — and
// deliberately NOT go-agent-wrapper's legacy MCPConfig convention of
// 0o600. The boot dir itself is 0o700 (makeBootDir's os.MkdirTemp), so
// the descriptor is not readable outside the owning user regardless.
// Tightening it is a security-posture decision on its own merits, not a
// side effect this migration gets to make silently.
const mcpConfigFileMode os.FileMode = 0o644

// bootDirOwnershipGroup is the materialization ownership group every
// Nanite-planted boot-dir entry carries. A single group is correct here:
// Nanite never uses materialize.Selection to plant a subset by group,
// and never sets ReconcilePolicy.RemoveOwned, so the group exists to
// mark "Nanite planted this" rather than to partition the tree.
//
// Per-entry EntryIDs are derived from the destination path, which is
// what makes a boot-time plant and a later mid-session re-plant of the
// same path agree on ownership instead of colliding.
const bootDirOwnershipGroup = "nanite:bootdir"

// bootDirArtifactTree converts an app-level plant.Spec into the agentkit
// artifact.Tree the shared materialization engine consumes, applying
// cfg's per-provider destinations and modes.
//
// Every destination path still passes through
// agentlaunch.ValidateBootDirRelPath, exactly as the prior
// writePlantedFile primitive did. That gate is NOT redundant with the
// engine's own artifact.ValidateRelPath: ValidateRelPath rejects
// absolute paths and traversal, but ValidateBootDirRelPath ALSO enforces
// a reserved-prefix denylist (".git/", ".ssh/", ".gnupg/", ".aws/" and
// their bare forms) that artifact does not carry. Dropping this call and
// leaning on the engine would silently retire that denylist.
func bootDirArtifactTree(spec plant.Spec, cfg plantConfig) (artifact.Tree, error) {
	var entries []artifact.Entry

	add := func(relPath string, content []byte, mode os.FileMode, entryID string) error {
		if err := agentlaunch.ValidateBootDirRelPath(relPath); err != nil {
			return fmt.Errorf("agent: bootdir plant %q: %w", relPath, err)
		}
		if mode == 0 {
			mode = plantedFileMode
		}
		// make+copy, NOT append([]byte(nil), content...): appending zero
		// elements to a nil slice yields nil, and artifact.Entry.Validate
		// rejects a file entry whose Bytes is nil. An empty planted file
		// is legitimate here (boot.md when the caller supplies no kickoff
		// content), so it must arrive as an empty-but-non-nil slice.
		body := make([]byte, len(content))
		copy(body, content)
		entries = append(entries, artifact.Entry{
			Path:  relPath,
			Kind:  artifact.EntryFile,
			Mode:  mode,
			Bytes: body,
			Ownership: artifact.Ownership{
				EntryID: "nanite:" + entryID,
				GroupID: bootDirOwnershipGroup,
			},
			Provenance: artifact.Provenance{Source: "nanite.bootdir." + cfg.provider},
		})
		return nil
	}

	// Sorted so tree construction is deterministic on its own terms.
	// artifact.Normalize sorts again downstream; this keeps the
	// pre-normalize tree reproducible for debugging and provenance.
	relPaths := make([]string, 0, len(spec.Files))
	for relPath := range spec.Files {
		relPaths = append(relPaths, relPath)
	}
	sort.Strings(relPaths)
	for _, relPath := range relPaths {
		if err := add(relPath, spec.Files[relPath], cfg.fileModeOverrides[relPath], "file:"+relPath); err != nil {
			return artifact.Tree{}, err
		}
	}

	if len(spec.MCPConfig) > 0 {
		if err := add(".mcp.json", spec.MCPConfig, mcpConfigFileMode, "mcp"); err != nil {
			return artifact.Tree{}, err
		}
	}

	if cfg.providerSettingsPath != "" {
		if content, ok := spec.ProviderSettings[cfg.provider]; ok {
			if err := add(cfg.providerSettingsPath, content, cfg.providerSettingsMode, "provider-settings:"+cfg.provider); err != nil {
				return artifact.Tree{}, err
			}
		}
	}

	return artifact.Tree{
		Entries:    entries,
		Provenance: artifact.Provenance{Source: "nanite.bootdir." + cfg.provider},
	}, nil
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
// (claudePlanter/codexPlanter/opencodePlanter) delegates to. It converts
// spec into an artifact.Tree carrying Nanite's own destinations and
// modes (bootDirArtifactTree), then hands that tree to
// plant.SharedPlanter for the actual write.
//
// # Why the default (reconcile) operation is the right one, verified
//
// SharedPlanter leaves Spec.Operation empty, which
// agentlaunch.MaterializeArtifacts resolves to
// materialize.OperationReconcile. That is the only operation that works
// against Nanite's boot-dir lifecycle, and it satisfies both Layout
// requirements — verified empirically against this exact call path in
// CW-20260910-0020, not inferred:
//
//   - Setup plants into a dir makeBootDir ALREADY created (os.MkdirTemp).
//     OperationCreate would fail with ErrTargetExists; it stages into a
//     sibling dir and renames, so it requires the target to be absent.
//   - Populate re-plants against an existing, possibly partially-
//     truncated dir for crash recovery. Reconcile overwrites the stale
//     content correctly.
//   - RegenerateSystemPromptSlot plants a ONE-ENTRY tree and must leave
//     the rest of the dir alone. Reconcile carries forward every manifest
//     entry not named in the desired tree, so .mcp.json and .sandbox/
//     survive.
//   - Re-planting an unchanged tree is a no-op (reported Unchanged, not
//     rewritten), which preserves Populate's documented idempotence.
//
// Note this works because of agentlaunch.MaterializeArtifacts, NOT
// materialize.Engine alone: that intermediate layer MkdirAlls the target,
// synthesizes a bootstrap manifest from the desired tree when the boot
// dir has none yet, and defaults the conflict policy to
// ConflictOverwrite. Calling materialize.Engine directly with reconcile
// returns ErrMissingManifest on a fresh dir. It is a narrow ledge; do not
// re-point this at the engine without re-establishing those three.
//
// spec.Hooks and spec.RecoveryPrompt have no Nanite plant target yet —
// nothing in this codebase populates either field today (see the
// claude/codex/opencode PlantSpec builders), so a non-empty value here
// can only mean a future caller expected behavior this Planter doesn't
// implement. Rejected loudly rather than silently dropped, matching the
// "unsupported kind" guard the prior InjectionSpec-based mechanism used
// for non-raw NativeFiles. (CW-20260910-0015 builds the Hooks target and
// lifts the first of these guards.)
func plantSpec(ctx context.Context, bootDir string, spec plant.Spec, cfg plantConfig) (plant.Result, error) {
	if len(spec.Hooks) > 0 {
		return plant.Result{}, fmt.Errorf("agent: bootdir Planter(%s): hooks are not yet supported", cfg.provider)
	}
	if spec.RecoveryPrompt != "" {
		return plant.Result{}, fmt.Errorf("agent: bootdir Planter(%s): RecoveryPrompt is not yet supported", cfg.provider)
	}

	tree, err := bootDirArtifactTree(spec, cfg)
	if err != nil {
		return plant.Result{}, err
	}
	if len(tree.Entries) == 0 {
		return plant.Result{}, nil
	}

	result, err := plant.SharedPlanter{}.Plant(ctx, bootDir, plant.Spec{Artifacts: tree})
	if err != nil {
		return result, fmt.Errorf("agent: bootdir Planter(%s): %w", cfg.provider, err)
	}
	return result, nil
}

// sandboxFiles returns the Nanite app-extra .sandbox/ files as
// plant.Spec.Files entries. Deliberately NOT pushed into a shared
// package — the envelope schema and agent-context doc are Nanite product
// surface — but they ride the same Files vocabulary as every other
// planted file.
func sandboxFiles(params SetupParams) map[string][]byte {
	return map[string][]byte{
		".sandbox/agent-context.md":   []byte(BuildAgentContext(params.AgentProfile)),
		".sandbox/envelope-schema.md": []byte(envelopeSchemaContent()),
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
