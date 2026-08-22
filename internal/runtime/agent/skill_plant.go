package agent

// skill_plant.go — TASKS/skills/10
// (TASKS/skills/10-cli-hosted-native-skill-delivery-boot-dir-planting.md):
// docs/engineering/architecture/20-skills.md's "Delivery" section — CLI-
// hosted agents (Claude/Codex/OpenCode) get a granted skill's vendored
// package copied into the agent's own native skill location inside its
// boot dir, so the agent's own native skill mechanism can pick it up
// unmodified. Nanite's job stops at planting real files at the path the
// runtime already expects — no Nanite-specific rendering, no self-tool,
// nothing added to composeSystemPrompt/ResolveSystemPrompt for this
// delivery path.
//
// # Reuse, not reimplementation
//
// bootdir_plant.go's writePlantedFile/plantSpec is the existing, already-
// production primitive for "build a map[relPath][]byte, then write it
// atomically through the shared path-safety gate" — this file only builds
// that map for a skill's vendored file tree; it never touches the
// filesystem itself and never bypasses writePlantedFile's
// agentlaunch.ValidateBootDirRelPath gate.
//
// # Why this package can't import internal/skill
//
// internal/skill/resolver.go already imports this package
// (runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent",
// for ResolveContextBlocks) — importing internal/skill back from here
// would cycle. SkillCatalogStore/SkillGrantStore below are therefore this
// package's own, identically-shaped local interfaces rather than a reuse
// of internal/skill/resolver.go's SkillIndexStore or
// internal/skill/gate.go's AgentKnownSkillStore — *store.Store satisfies
// all of them without any adapter code.
//
// # The trust check this file applies before planting
//
// Mirrors internal/skill/gate.go's Gate.authorize check exactly (task 09's
// own execution-time gate), applied here to the planting operation instead
// of the execution operation: a skill is plantable only when its
// agent_known_skills grant row has a non-empty ApprovedContentHash (a bare,
// ungranted assignment — store.AgentKnownSkill.IsBareAssignment — is never
// plantable) AND that hash still matches the skill catalog row's current
// ContentHash (a stale approval, from before a since-superseded
// re-install/re-sync, is never plantable either — planting an old approval
// against new vendored content would be exactly the trust-boundary bypass
// task 09 was built to prevent on the execution side). Gate.authorize does
// not additionally check store.Skill.Enabled, so neither does
// ResolvePlantableSkills below — the two checks stay in lockstep by
// design, per this task's own instruction to mirror task 09's logic.
//
// # Per-provider native conventions (What-to-do item 3)
//
// Investigated directly rather than assumed identical across providers:
//
//   - Claude Code: real, documented native skill mechanism —
//     .claude/skills/<slug>/SKILL.md, auto-discovered from cwd. Nanite's
//     claude boot dir IS cwd (claudeLayout.SpawnWorkdir), so
//     "<bootDir>/.claude/skills/<slug>/..." is the correct, unambiguous
//     destination.
//   - OpenCode: also a real, documented native skill mechanism, but with
//     two candidate destination conventions and genuine published-doc
//     ambiguity about which one Nanite's own OPENCODE_CONFIG_DIR env
//     amendment (opencodeLayout.AmendEnv) actually reaches:
//     project-local ".opencode/skills/<name>/SKILL.md" (relative to cwd),
//     and a custom-config-dir convention docs describe as "search[ing]
//     ...just like the standard .opencode directory... should follow the
//     same structure" (which — per that same doc's own subdirectory list,
//     "agents/, commands/, modes/, plugins/, skills/, tools/, and
//     themes/" — plausibly includes "skills/" directly under the custom
//     dir, though the prose enumerating what a custom config dir searches
//     names only "agents, commands, modes, and plugins" and doesn't
//     explicitly re-list skills). Given real ambiguity and zero
//     functional cost to covering both (extra, unused files are inert;
//     nothing else in opencodePlantSpec's Files map uses either
//     top-level name), this file plants BOTH candidate destinations —
//     opencodeSkillDestPrefixes below. opencodeLayout.SpawnWorkdir's own
//     doc comment already establishes that real chat sessions call it
//     with projectDir="" today, so cwd == bootDir in practice, making the
//     project-local ".opencode/skills/" destination land in the same real
//     directory as the config-dir-relative "skills/" destination for
//     every live session this ships against.
//   - Codex: NO native skill mechanism, confirmed by (1) go-providers'
//     CodexAdapter.BootDirSpec having no skills-related planted file or
//     env amendment at all (bootdir_codex.go, vendored go-providers
//     v0.24.0), (2) a direct read of the OpenAI Codex CLI's own
//     docs/config.md reference (github.com/openai/codex) turning up zero
//     mentions of "skill"/"skills" anywhere, and (3) the CLI's README
//     likewise. This is a legitimate, documented terminal outcome per
//     20-skills.md's own Context ("they may not have an equivalent
//     native-skill mechanism at all... that provider simply gets no skill
//     planting") — codexPlantSpec deliberately has no skill-files
//     contribution, and skillFilesForProvider below returns (nil, nil)
//     for "codex" rather than guessing at a path.
//
// # Additive-only; no removal-on-revoke (documented scope boundary)
//
// Every write in this codebase's boot-dir mechanism (writePlantedFile,
// Populate, plantSpec) is additive/idempotent-by-overwrite — none of them
// ever delete a previously-planted file that's no longer needed. This file
// follows the same convention: a skill that stops being plantable
// (ungranted, or a stale re-approval) simply stops being included in a
// future plant/replant call's map — its previously-planted files are left
// on disk in the boot dir rather than actively removed. This is a real,
// deliberate scope boundary (not a Done-means gap): this task's own
// Done-means only requires that an unapproved/stale skill "is not
// planted" (verified as "the current plant/replant call omits it," which
// this achieves) and that a newly-granted skill "appears... without a
// full session restart" (also achieved, via PlantAgentSkillFiles below) —
// it does not require active removal of a since-revoked skill's stale
// files from an already-running session's boot dir. A CLI agent's own
// native skill mechanism re-reads its skill directory on each turn, not
// once at process start, so a stale planted-but-revoked skill remains
// technically invocable by the agent's own runtime until the boot dir
// itself is torn down — a real, narrow residual-trust window, logged here
// rather than silently left undiscoverable.

import (
	"context"
	"fmt"
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
	GetSkillBySlug(slug string) (*store.Skill, error)
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
		sk, err := catalog.GetSkillBySlug(k.SkillName)
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
// Every relPath this produces still passes through the shared
// writePlantedFile path-safety gate at the point each provider's Planter
// actually writes it (plantSpec/claudePlantSpec etc.) — this function only
// builds the map; it never touches the filesystem itself.
func SkillPlantFiles(ctx context.Context, skills []PlantableSkill, vendor SkillVendorReader, destPrefixes func(slug string) []string) (map[string][]byte, error) {
	if vendor == nil || len(skills) == 0 {
		return nil, nil
	}
	out := make(map[string][]byte)
	for _, sk := range skills {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		files, err := vendor.ReadFiles(sk.ContentHash)
		if err != nil {
			return nil, fmt.Errorf("agent: plant skill %q: read vendored files: %w", sk.Slug, err)
		}
		for _, prefix := range destPrefixes(sk.Slug) {
			for relPath, content := range files {
				out[path.Join(prefix, relPath)] = content
			}
		}
	}
	return out, nil
}

// claudeSkillDestPrefixes is claude's native skill-delivery convention —
// see this file's package doc.
func claudeSkillDestPrefixes(slug string) []string {
	return []string{path.Join(".claude/skills", slug)}
}

// opencodeSkillDestPrefixes is opencode's native skill-delivery
// convention. Plants to BOTH candidate destinations given real,
// unresolved doc ambiguity about which one Nanite's own
// OPENCODE_CONFIG_DIR env amendment reaches — see this file's package doc
// for the full investigation.
func opencodeSkillDestPrefixes(slug string) []string {
	return []string{
		path.Join("skills", slug),          // OPENCODE_CONFIG_DIR-relative convention
		path.Join(".opencode/skills", slug), // project-local convention (== cwd == bootDir today)
	}
}

// skillFilesForProvider returns providerName's plant.Spec-ready
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

	var destPrefixes func(slug string) []string
	switch normalizeProviderName(providerName) {
	case "claude", "claude-code", "claudecode":
		destPrefixes = claudeSkillDestPrefixes
	case "opencode":
		destPrefixes = opencodeSkillDestPrefixes
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
	for relPath, content := range files {
		if err := writePlantedFile(bootDir, relPath, string(content), 0); err != nil {
			return err
		}
	}
	return nil
}
