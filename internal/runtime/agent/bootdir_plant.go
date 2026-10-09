package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/hollis-labs/substrate/harness/agentlaunch"
	plant "github.com/hollis-labs/substrate/harness/agentlaunch/planting"
	"github.com/hollis-labs/substrate/harness/workspace/materialize"
	"github.com/hollis-labs/substrate/harness/workspace/materialize/artifact"
)

// Nanite renders provider-specific artifacts and destinations. The published
// Harness planter owns materialization into a fresh private boot root whose
// custody was established by Setup. Returning the root seals that authority.
// Between-turn refresh and crash-recovery Populate return typed unavailable:
// neither a saved manifest nor next-turn timing proves reader quiescence.
// Materialization handles and partial roots remain available as evidence;
// no stale-manifest deletion or direct-write fallback grants ownership.

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

// bootDirArtifactTree converts an app-level plant.PlantSpec into the agentkit
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
func bootDirArtifactTree(spec plant.PlantSpec, cfg plantConfig) (artifact.Tree, error) {
	var entries []artifact.Entry

	// CW-20260910-0010: caller-supplied tree entries come first. This is
	// the path for anything the flat Files map cannot express — a
	// DIRECTORY as a directory, or a per-entry mode. Spec.Files is
	// map[relPath][]byte: it has nowhere to put either, which is why the
	// hook target (CW-20260910-0015, hook scripts at 0700 under
	// hooks/<provider>/) and imported-agent resource dirs
	// (CW-20260910-0012) need this rather than another Files entry.
	//
	// Ownership and provenance default to the same conventions the legacy
	// fields get, so a caller only sets them to say something different.
	// The path gate applies here exactly as it does everywhere else — a
	// caller-built tree is not a way around ValidateBootDirRelPath.
	for _, entry := range spec.Artifacts.Entries {
		if err := agentlaunch.ValidateBootDirRelPath(entry.Path); err != nil {
			return artifact.Tree{}, fmt.Errorf("agent: bootdir plant %q: %w", entry.Path, err)
		}
		planted := entry
		if planted.Ownership.EntryID == "" {
			planted.Ownership.EntryID = "nanite:artifact:" + planted.Path
		}
		if planted.Ownership.GroupID == "" {
			planted.Ownership.GroupID = bootDirOwnershipGroup
		}
		if planted.Provenance.Source == "" {
			planted.Provenance.Source = "nanite.bootdir." + cfg.provider
		}
		entries = append(entries, planted)
	}

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

// bootDirSourceLimits bounds a filesystem tree import. Deliberately
// conservative: a boot dir is an ephemeral per-session sandbox, not a
// place to stage a large tree, and an unbounded walk here would turn a
// mistaken source path into an OOM or a full $TMPDIR.
var bootDirSourceLimits = artifact.SourceLimits{
	MaxEntries: 2000,
	MaxBytes:   32 << 20, // 32 MiB
	MaxDepth:   16,
}

// bootDirTreeFromDir builds an artifact.Tree from an on-disk directory,
// destined for destPrefix inside the boot dir. This is CW-20260910-0010's
// "plant a directory as a directory" primitive.
//
// It is a thin adapter over agentkit's own resolver rather than a
// hand-rolled walk, because agentkit/artifact already ships the hardened
// version of exactly this: explicit EntryDirectory entries carrying their
// source modes, per-entry ValidateRelPath, content digests, the
// MaxEntries/MaxBytes/MaxDepth bounds above, and a symlink policy that
// defaults to reject and — under import-by-value — verifies the target
// resolves inside an allowed root and is a regular file. Re-implementing
// that Nanite-side would duplicate security-relevant code for no gain.
//
// Symlink policy is left at the resolver's default (reject). A boot dir
// is planted for a child agent process to read; importing a symlink by
// value would silently copy content from outside srcDir into it, and
// preserving the link would point the child at a path Nanite has not
// vetted. A caller that genuinely needs either should say so explicitly,
// at which point the decision is reviewable.
//
// The returned tree is passed to plantSpec as Spec.Artifacts, where every
// path is re-checked against ValidateBootDirRelPath — artifact's own
// validation does not carry the reserved-prefix denylist.
func bootDirTreeFromDir(ctx context.Context, srcDir, destPrefix, ownershipGroup string) (artifact.Tree, error) {
	if srcDir == "" {
		return artifact.Tree{}, fmt.Errorf("agent: bootdir tree from dir: empty source directory")
	}
	if ownershipGroup == "" {
		ownershipGroup = bootDirOwnershipGroup
	}
	tree, err := artifact.NewResolver(artifact.ResolverOptions{}).ResolveArtifacts(ctx, artifact.SourceRequest{
		Source: artifact.Source{
			Kind:       artifact.SourceFilesystemTree,
			Filesystem: &artifact.FilesystemSource{Root: srcDir},
		},
		DestinationPrefix: destPrefix,
		Limits:            bootDirSourceLimits,
		OwnershipGroup:    ownershipGroup,
	})
	if err != nil {
		return artifact.Tree{}, fmt.Errorf("agent: bootdir tree from dir %q: %w", srcDir, err)
	}
	return tree, nil
}

// plantConfig is the per-provider destination knowledge a Nanite
// plant.Planter implementation supplies — which of Spec's generic fields
// map to which bootdir-relative path, and with what file mode. Encoding
// this per concrete Planter type (rather than in plant.PlantSpec itself, which
// deliberately stays destination-agnostic) keeps go-agent-wrapper's
// contract thin while letting Nanite's file-planting mechanics — e.g.
// codex's config.toml needing 0o600, not the 0o644 default —
// live where they always have: Nanite-side.
type plantConfig struct {
	authorize agentlaunch.ArtifactAuthorizer
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
	// entries. No provider sets one today: codex's auth.json, its only
	// user, became a symlink to the host login (CW-20261001-0027).
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
// spec.Hooks stays rejected, and after CW-20260910-0015 that is an
// informed decision rather than a gap. Nanite DOES plant hooks — see
// bootdir_hooks.go — but not through this field, because plant.PlantHook
// carries only {Provider, Name, Payload}: no event, no matcher. A hook
// script written without a declaration never runs, so honoring
// spec.Hooks here would plant executables nothing executes. Nanite's own
// BootDirHook carries the declaration, and rides Spec.Artifacts for the
// script plus the provider's settings document for the wiring.
//
// spec.RecoveryPrompt likewise has no Nanite plant target; nothing in
// this codebase populates it.
//
// Both are rejected loudly rather than silently dropped, matching the
// "unsupported kind" guard the prior InjectionSpec-based mechanism used
// for non-raw NativeFiles.
func plantSpec(ctx context.Context, bootDir string, spec plant.PlantSpec, cfg plantConfig) (plant.PlantResult, error) {
	if len(spec.Hooks) > 0 {
		return plant.PlantResult{}, fmt.Errorf(
			"agent: bootdir Planter(%s): plant.PlantSpec.Hooks is not the hook path here — it carries no event or matcher, so its payloads would plant as executables nothing declares; use SetupParams.Hooks (bootdir_hooks.go)",
			cfg.provider)
	}
	if spec.RecoveryPrompt != "" {
		return plant.PlantResult{}, fmt.Errorf("agent: bootdir Planter(%s): RecoveryPrompt is not yet supported", cfg.provider)
	}

	tree, err := bootDirArtifactTree(spec, cfg)
	if err != nil {
		return plant.PlantResult{}, err
	}
	if len(tree.Entries) == 0 {
		return plant.PlantResult{}, nil
	}

	if cfg.authorize == nil {
		return plant.PlantResult{}, &ArtifactRefreshUnavailable{Provider: cfg.provider, Operation: "populate existing root"}
	}
	result, err := (plant.SharedPlanter{Authorize: cfg.authorize}).Plant(ctx, bootDir, plant.PlantSpec{Artifacts: tree})
	bootArtifactEvidence.Store(bootDir, result)
	if err != nil {
		return result, fmt.Errorf("agent: bootdir Planter(%s): %w", cfg.provider, err)
	}
	return result, nil
}

// legacyManifestRelPath is where agentkit before v0.7.0 kept the
// materialization manifest.
const legacyManifestRelPath = ".agentkit/materialize-manifest.json"

// staleBootDir reports whether bootDir was planted before the manifest
// moved: it carries the legacy manifest and no current one.
func staleBootDir(bootDir string) bool {
	if _, err := os.Stat(filepath.Join(bootDir, legacyManifestRelPath)); err != nil {
		return false
	}
	_, err := os.Stat(materialize.ManifestPath(bootDir))
	return os.IsNotExist(err)
}

// sandboxFiles returns the Nanite app-extra .sandbox/ files as
// plant.PlantSpec.Files entries. Deliberately NOT pushed into a shared
// package — the envelope schema and agent-context doc are Nanite product
// surface — but they ride the same Files vocabulary as every other
// planted file.
func sandboxFiles(params SetupParams) map[string][]byte {
	return map[string][]byte{
		".sandbox/agent-context.md":   []byte(BuildAgentContext(params.AgentProfile)),
		".sandbox/envelope-schema.md": []byte(envelopeSchemaContent()),
	}
}

// mcpConfigBytes renders the .mcp.json descriptor as plant.PlantSpec.MCPConfig
// bytes, or nil when MCP planting is disabled (zero-value MCPConfig).
// Delegates to mcpOverlay (below) for the actual render + Mode gating,
// lifting its single ".mcp.json" entry into the []byte shape
// plant.PlantSpec.MCPConfig expects.
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
// the single seam that applies the self-tool scope Mode gating below.
//
// Returns an error when the MCP config is internally inconsistent
// (BinaryPath required when DBPath is set) so a misconfigured boot fails
// fast rather than planting a broken descriptor.
func mcpOverlay(params SetupParams) (map[string]string, error) {
	// Every launch's `nanite mcp` forwards its self-tool calls to the live
	// harness (NANITE_API_URL), so none opens the database, whose directory
	// Nanite write-protects from agents (CW-20261001-0188). The harness's
	// full self-tool surface is a chat-agent affordance only (1b324a45):
	// subagent, background and one-shot launches keep the bare-store set
	// they had when they dispatched locally, named by SelfToolsScopeEnv.
	// ModeResume re-boots a crash-recovered chat agent, so it keeps the
	// full surface.
	storeScope := params.Mode != ModeLongLived && params.Mode != ModeResume
	body, err := renderMCPJSON(params.MCPConfig, params.SessionID, storeScope)
	if err != nil {
		return nil, err
	}
	if body == "" {
		return nil, nil
	}
	return map[string]string{".mcp.json": body}, nil
}
