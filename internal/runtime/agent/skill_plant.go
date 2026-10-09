package agent

// Skill source resolution preserves catalog hash approval and provider-native
// paths. Fresh private Setup includes the resolved tree in its materialization.
// PlantAgentSkillFiles against an existing bound root returns typed unavailable
// when there are files to refresh; no-grant and unsupported-provider empty
// trees remain no-ops. A later grant requires a fresh boot until a fenced
// provider reload/binding contract exists. Previously planted files remain in
// the old binding; this source change performs no live removal or backfill.

import (
	"context"
	"fmt"
	"log/slog"
	"path"

	"github.com/hollis-labs/nanite/internal/skillvendor"
	"github.com/hollis-labs/nanite/internal/store"
)

// SkillCatalogStore is the narrow slice of *store.Store's API this file
// depends on for looking up a skill's current catalog row (for its
// current vendored ContentHash). See this file's package doc for why this
// is a local interface rather than a reuse of internal/skill/resolver.go's
// identically-shaped SkillIndexStore.
type SkillCatalogStore interface {
	GetSkillBySlug(ctx context.Context, slug string) (*store.Skill, error)
}

// SkillGrantStore is the narrow slice of *store.Store's API this file
// depends on for listing an agent's granted skills. See this file's
// package doc for why this is a local interface rather than a reuse of
// internal/skill/gate.go's per-lookup AgentKnownSkillStore (planting needs
// an agent's whole granted set at once, not one skill's grant at a time).
type SkillGrantStore interface {
	ListAgentKnownSkills(ctx context.Context, agentID string) ([]store.AgentKnownSkill, error)
}

// SkillStore combines SkillGrantStore and SkillCatalogStore — the whole
// slice of *store.Store's API this file depends on. Production always
// backs both halves through the same *store.Store value; this file keeps
// them as separate narrow interfaces above (matching this package's own
// existing AgentProfiles/RuntimeStore DI-for-testability convention) but
// SetupParams/Dependencies carry a single combined field so callers don't
// have to wire the same store value into two separate struct fields.
type SkillStore interface {
	SkillGrantStore
	SkillCatalogStore
}

// SkillVendorReader is the narrow slice of *skillvendor.Store's API this
// file depends on for reading a skill's vendored file tree.
type SkillVendorReader interface {
	ReadFiles(address string) (skillvendor.FileMap, error)
}

// PlantableSkill is one of an agent's plantable skills — a granted,
// currently-approved skill ready to be planted into a CLI-hosted agent's
// boot dir. See this file's package doc, "The trust check this file
// applies before planting," for exactly what "plantable" means.
type PlantableSkill struct {
	Slug        string
	ContentHash string
}

// ResolvePlantableSkills returns agentID's plantable skill set: every
// agent_known_skills grant row (via SkillGrantStore.ListAgentKnownSkills)
// whose ApprovedContentHash is non-empty and currently matches the skill
// catalog's own ContentHash for that slug (via
// SkillCatalogStore.GetSkillBySlug). A grant row naming a skill slug no
// longer present in the catalog is silently excluded, matching
// store.ListAgentSkills' own dangling-reference tolerance. Returns
// (nil, nil) when grants, catalog, or agentID is empty/nil — a caller with
// no skill wiring configured (e.g. a test SetupParams) sees "nothing to
// plant," not an error.
func ResolvePlantableSkills(ctx context.Context, grants SkillGrantStore, catalog SkillCatalogStore, agentID string) ([]PlantableSkill, error) {
	if grants == nil || catalog == nil || agentID == "" {
		return nil, nil
	}
	known, err := grants.ListAgentKnownSkills(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("agent: resolve plantable skills: list known skills: %w", err)
	}

	out := make([]PlantableSkill, 0, len(known))
	for _, k := range known {
		if k.ApprovedContentHash == "" {
			// Ungranted / never-approved bare assignment — never plantable.
			continue
		}
		sk, err := catalog.GetSkillBySlug(ctx, k.SkillName)
		if err != nil {
			return nil, fmt.Errorf("agent: resolve plantable skills: look up skill %q: %w", k.SkillName, err)
		}
		if sk == nil {
			// Dangling grant against a since-deleted catalog row.
			continue
		}
		if k.ApprovedContentHash != sk.ContentHash {
			// Stale approval — a re-install/re-sync happened since this
			// grant was approved. Never plantable until re-approved.
			continue
		}
		out = append(out, PlantableSkill{Slug: sk.Slug, ContentHash: sk.ContentHash})
	}
	return out, nil
}

// SkillPlantFiles reads each of skills' vendored file trees from vendor and
// returns the combined map[relPath][]byte, with every file nested under
// each of destPrefixes(skill.Slug)'s returned relative-path prefixes (a
// skill may be planted under more than one destination prefix for the same
// boot dir — see this file's package doc for why OpenCode gets two).
// Returns (nil, nil) when vendor is nil or skills is empty.
//
// destPrefixes returns (prefixes, false) when slug's per-provider
// destination(s) would escape their own intended subtree (see this file's
// package doc, "Path-traversal guard on the skill slug") — that skill is
// skipped entirely (a logged warning, no partial/mangled plant, never a
// silent redirect to some other path) rather than merged into the
// returned map.
//
// Every relPath this produces still passes through the shared
// ValidateBootDirRelPath path-safety gate at the point each provider's
// Planter actually writes it (plantSpec/claudePlantSpec etc.) — this
// function only builds the map; it never touches the filesystem itself.
func SkillPlantFiles(ctx context.Context, skills []PlantableSkill, vendor SkillVendorReader, destPrefixes func(slug string) ([]string, bool)) (map[string][]byte, error) {
	if vendor == nil || len(skills) == 0 {
		return nil, nil
	}
	out := make(map[string][]byte)
	for _, sk := range skills {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		prefixes, safe := destPrefixes(sk.Slug)
		if !safe {
			// Path-traversal guard tripped — see this file's package doc.
			// Skip this skill entirely; never plant a partial/mangled
			// version of it and never redirect it to a "sanitized"
			// fallback path. skillFilesForProvider's caller wraps
			// destPrefixes with agent/provider-identifying logging before
			// it reaches here.
			continue
		}
		if len(prefixes) == 0 {
			continue
		}
		files, err := vendor.ReadFiles(sk.ContentHash)
		if err != nil {
			return nil, fmt.Errorf("agent: plant skill %q: read vendored files: %w", sk.Slug, err)
		}
		for _, prefix := range prefixes {
			for relPath, content := range files {
				out[path.Join(prefix, relPath)] = content
			}
		}
	}
	return out, nil
}

// skillDestPrefixSafe joins root and slug (root+"/"+slug, path.Clean'd —
// exactly what claudeSkillDestPrefixes/opencodeSkillDestPrefixes actually
// plant under) and reports whether the result genuinely stays under
// root's own per-skill subtree, rather than a ".."-laden slug canceling
// part or all of root itself via path.Join's internal path.Clean. See
// this file's package doc, "Path-traversal guard on the skill slug," for
// the full reasoning and the ESCALATIONS.md finding this closes.
//
// ok is false for an empty slug (never a valid destination) or whenever
// the cleaned path.Join result differs from the literal, UNCLEANED
// concatenation root+"/"+slug — the signature of a slug that ate into
// root via ".."/"." segments.
func skillDestPrefixSafe(root, slug string) (dest string, ok bool) {
	if slug == "" {
		return "", false
	}
	dest = path.Join(root, slug)
	literal := root + "/" + slug
	if dest != literal {
		return "", false
	}
	return dest, true
}

// claudeSkillDestPrefixes is claude's native skill-delivery convention —
// see this file's package doc. Returns (nil, false) when slug would
// escape ".claude/skills/"'s own per-skill subtree.
func claudeSkillDestPrefixes(slug string) ([]string, bool) {
	dest, ok := skillDestPrefixSafe(".claude/skills", slug)
	if !ok {
		return nil, false
	}
	return []string{dest}, true
}

// opencodeSkillDestPrefixes is opencode's native skill-delivery
// convention. Plants to BOTH candidate destinations given real,
// unresolved doc ambiguity about which one Nanite's own
// OPENCODE_CONFIG_DIR env amendment reaches — see this file's package doc
// for the full investigation. Returns (nil, false) — no partial plant to
// only the destination(s) that happen to check out — when EITHER
// candidate destination would escape its own per-skill subtree (the
// shorter "skills/" root is exactly the case ESCALATIONS.md's 2026-08-22
// finding calls out: a bare ".." slug alone is enough to escape it).
func opencodeSkillDestPrefixes(slug string) ([]string, bool) {
	configRelative, okA := skillDestPrefixSafe("skills", slug)         // OPENCODE_CONFIG_DIR-relative convention
	projectLocal, okB := skillDestPrefixSafe(".opencode/skills", slug) // project-local convention (== cwd == bootDir today)
	if !okA || !okB {
		return nil, false
	}
	return []string{configRelative, projectLocal}, true
}

// skillFilesForProvider returns providerName's plant.PlantSpec-ready
// map[relPath][]byte for params.AgentProfile's plantable skill set, or
// (nil, nil) when the provider has no native skill mechanism (codex —
// see package doc) or params carries no skill wiring (SkillGrants /
// SkillVendor unset — e.g. a test SetupParams that doesn't exercise
// skills, or a composition root that hasn't wired Dependencies.Skills /
// Dependencies.SkillVendor).
func skillFilesForProvider(ctx context.Context, providerName string, params SetupParams) (map[string][]byte, error) {
	if params.Skills == nil || params.SkillVendor == nil || params.AgentProfile == nil || params.AgentProfile.ID == "" {
		return nil, nil
	}

	var rawDestPrefixes func(slug string) ([]string, bool)
	switch normalizeProviderName(providerName) {
	case "claude", "claude-code", "claudecode":
		rawDestPrefixes = claudeSkillDestPrefixes
	case "opencode":
		rawDestPrefixes = opencodeSkillDestPrefixes
	default:
		// codex (no native skill mechanism) and anything unrecognized: no
		// skill planting. Not an error — a legitimate terminal outcome.
		return nil, nil
	}

	skills, err := ResolvePlantableSkills(ctx, params.Skills, params.Skills, params.AgentProfile.ID)
	if err != nil {
		return nil, err
	}
	if len(skills) == 0 {
		return nil, nil
	}

	agentID := params.AgentProfile.ID
	// Wrap the raw per-provider dispatch with agent/provider-identifying
	// logging for the path-traversal guard (this file's package doc,
	// "Path-traversal guard on the skill slug") — SkillPlantFiles itself
	// only knows the skill's slug, not which agent/provider triggered the
	// plant.
	destPrefixes := func(slug string) ([]string, bool) {
		prefixes, ok := rawDestPrefixes(slug)
		if !ok {
			slog.Warn("agent: skipping skill plant — destination would escape its own per-skill subtree (adversarial or malformed slug)",
				"agent_id", agentID, "provider", providerName, "skill_slug", slug)
		}
		return prefixes, ok
	}
	return SkillPlantFiles(ctx, skills, params.SkillVendor, destPrefixes)
}

// PlantAgentSkillFiles (re)plants agentID's plantable skill set into an
// existing bootDir, using providerName's native skill-delivery convention.
// Used by both the initial boot-dir population (via each provider's
// <provider>PlantSpec) and the mid-session slot-regeneration path
// (internal/service/chat_boot_drive.go's regenerateBootDirSlots, via
// runtimeagent.PlantAgentSkillFiles) so a newly-granted skill appears in a
// live session's boot dir without a full session restart.
//
// Idempotent and additive-only — see this file's package doc, "Additive-
// only; no removal-on-revoke," for the documented scope boundary this
// carries. A provider with no native skill mechanism (codex) is a
// documented no-op, never an error. deps.Skills / deps.SkillVendor unset
// (a composition root that hasn't wired skill support) is likewise a
// no-op, not an error — mirrors skillFilesForProvider's own "nothing to
// plant" contract.
func PlantAgentSkillFiles(ctx context.Context, deps *Dependencies, bootDir, providerName, agentID string) error {
	if deps == nil {
		return fmt.Errorf("agent: PlantAgentSkillFiles: nil Dependencies")
	}
	if bootDir == "" {
		return fmt.Errorf("agent: PlantAgentSkillFiles: empty bootDir")
	}
	if agentID == "" {
		return fmt.Errorf("agent: PlantAgentSkillFiles: empty agentID")
	}

	files, err := skillFilesForProvider(ctx, providerName, SetupParams{
		AgentProfile: &store.AgentProfile{ID: agentID},
		Skills:       deps.Skills,
		SkillVendor:  deps.SkillVendor,
	})
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	return &ArtifactRefreshUnavailable{Provider: providerName, Operation: "refresh skill files"}
}
