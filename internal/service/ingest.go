package service

// J7 (CW-20260421-0011): skills/agents DB ingestion.
//
// This file lives in the service package so it can import both store and
// agent/skill without creating an import cycle (store → agent/skill →
// store is the cycle; service sits above both).
//
// Auto-ingestion is called from NewContainer after Discover() returns.
// It makes the DB the runtime source of truth: files are the import path,
// DB is where the runtime reads from.
//
// TASKS/phase-1/08 ("Kill the file-reingest-on-boot pattern, in full"):
// AutoIngestAgents/AutoIngestSkills run unconditionally on every boot, but
// once a def has already been ingested into a DB row, that row's content is
// frozen against further boot-time file-parse passes -- the file stays the
// *first-ingest* path, not a standing sync. The one exception is a genuine
// provenance transition (existing.Source != the incoming def's Source,
// e.g. the historical builtin->internal migration flip, CW-20260512-0111)
// -- that's a deliberate, one-time reclassification, not an ordinary
// repeated boot, so it still content-syncs once. A real, deliberate
// re-import (an agent edited through the managed-agent write path,
// AgentConfigService.Update/writeManaged/SaveManagedAgentProfile, all of
// which call IngestAgentDefinition directly, not through the boot-time
// AutoIngestAgents pass) is unaffected by the freeze -- see upsertAgentDef's
// bootPass parameter.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	agentpkg "github.com/hollis-labs/nanite/internal/agent"
	skillpkg "github.com/hollis-labs/nanite/internal/skill"
	"github.com/hollis-labs/nanite/internal/store"
)

// AutoIngestAgents upserts all discovered agent definitions into the DB.
// Called once at container startup. Errors per-definition are logged and
// skipped; the function returns the count of successful ingestions.
//
// H1 trust integration (CW-20260421-0014):
//   - def.Source == "user"   → default_trust_tier = "untrusted"
//   - def.Source == "plugin" → default_trust_tier = "untrusted"
//   - other sources          → default_trust_tier = "normal" (preserved)
//
// A per-definition failure leaves that agent visible via file discovery
// (agentServiceImpl.List/Get read fileDefs directly) but with no backing
// agent_profiles row — a real gap, not a cosmetic one: anything that needs
// the DB row (durable-agent apply, copy-to-managed, FK children) breaks for
// it. CW-20260815-0009 found exactly this happen with zero operator-visible
// signal beyond a per-item slog.Warn easy to miss in startup noise. In
// addition to that per-item Warn, emit one aggregate slog.Error naming every
// failed slug + reason when any occur, so "N of M agent files failed to
// ingest" is discoverable from logs alone — no DB query required.
//
// knownTools, when non-nil, is the set of currently-registered tool names
// (builtins + live MCP discovery) used to validate every def.RoleTools /
// def.Tools entry (CW-20260815-0013): a profile declaring a plausible-
// looking but non-existent tool name (e.g. `bash_run` instead of `dev_bash`)
// previously failed silently — the tool simply never showed up at selection
// time, and the only way to discover why was a live agent failing and a
// manual grep. Unknown names are collected and logged the same
// loudly-aggregated way as ingest failures above; they do NOT fail the
// ingest itself (the profile row is still valid; only the specific tool
// reference is wrong) and passing nil skips this check entirely (tests that
// don't care about it, or callers with no tool catalog available yet).
// Caveat: some MCP servers (e.g. plugin-provided tools) register AFTER
// AutoIngestAgents runs during startup (see discoverAndLoadPlugins in
// cmd/nanite/main.go) — a name that's genuinely valid but supplied by a
// not-yet-loaded plugin can show up here as a false positive. This is a
// diagnostic warning, not a hard gate, so that tradeoff is acceptable.
func AutoIngestAgents(st *store.Store, defs []*agentpkg.Definition, knownTools map[string]bool) int {
	count := 0
	considered := 0 // defs actually attempted, excluding nil/empty-slug skips
	var failures []string
	var unknownToolRefs []string
	var hardcodedModels []string
	for _, def := range defs {
		if def == nil || def.Slug == "" {
			continue
		}
		considered++
		if err := upsertAgentDef(st, def, true /* bootPass: freeze already-ingested rows */); err != nil {
			slog.Warn("service: auto-ingest agent", "slug", def.Slug, "err", err)
			failures = append(failures, fmt.Sprintf("%s: %v", def.Slug, err))
			continue
		}
		count++
		if knownTools != nil {
			if bad := unknownDeclaredTools(def, knownTools); len(bad) > 0 {
				slog.Warn("service: agent profile declares unregistered tool name(s) — these will silently never be selectable until fixed",
					"slug", def.Slug, "unknown_tools", bad)
				unknownToolRefs = append(unknownToolRefs, fmt.Sprintf("%s: %v", def.Slug, bad))
			}
		}
		if def.Model != "" {
			slog.Warn("service: agent profile hardcodes a model, opting out of the system default (ResolveProviderAndModel, CW-20260526-0003) — this profile will silently break the day this model ID is retired (see CW-20260815-0021); leave `model:` blank unless there is a deliberate, documented reason to pin it",
				"slug", def.Slug, "hardcoded_model", def.Model)
			hardcodedModels = append(hardcodedModels, fmt.Sprintf("%s: %s", def.Slug, def.Model))
		}
	}
	if len(failures) > 0 {
		slog.Error("service: agent auto-ingest failed for one or more files — these agents are file-discoverable but have no working agent_profiles row until fixed and the service is restarted",
			"failed", len(failures), "considered", considered, "succeeded", count, "discovered", len(defs), "failures", failures)
	}
	if len(unknownToolRefs) > 0 {
		slog.Error("service: one or more agent profiles declare tool names that are not in the registered tool catalog — check for typos or renamed tools",
			"profiles_affected", len(unknownToolRefs), "details", unknownToolRefs)
	}
	if len(hardcodedModels) > 0 {
		slog.Warn("service: one or more agent profiles hardcode a model ID instead of inheriting the system default — each one is a future instance of CW-20260815-0021 waiting for its pinned model to be retired",
			"profiles_affected", len(hardcodedModels), "details", hardcodedModels)
	}
	return count
}

// unknownDeclaredTools returns every entry in def.RoleTools and def.Tools
// that is not a key in knownTools, deduplicated, in first-seen order.
func unknownDeclaredTools(def *agentpkg.Definition, knownTools map[string]bool) []string {
	seen := make(map[string]bool)
	var bad []string
	check := func(name string) {
		if name == "" || seen[name] || knownTools[name] {
			return
		}
		seen[name] = true
		bad = append(bad, name)
	}
	for _, name := range def.RoleTools {
		check(name)
	}
	for _, name := range def.Tools {
		check(name)
	}
	return bad
}

// IngestAgentDefinition is the explicit, deliberate reimport path -- called
// by AgentConfigService.writeManaged/SaveManagedAgentProfile immediately
// after a managed agent's file is written, so the edit that was just made
// takes effect in the DB right away. Unlike AutoIngestAgents' boot-time bulk
// pass, this always content-syncs the row (bootPass=false) -- it is the one
// legitimate "pull this file's content into the DB" action TASKS/phase-1/08
// preserves, not the standing every-boot sync it kills.
func IngestAgentDefinition(st *store.Store, def *agentpkg.Definition) error {
	if def == nil || def.Slug == "" {
		return fmt.Errorf("definition slug is required")
	}
	return upsertAgentDef(st, def, false /* bootPass: explicit reimport always syncs */)
}

// AutoIngestSkills upserts all discovered skill definitions into the DB.
// Called once at container startup, and again every subsequent boot. Errors
// per-definition are logged and skipped; the function returns the count of
// successful ingestions.
//
// TASKS/phase-1/08: a skill row, once ingested under its current source, is
// frozen against this boot-time pass -- see upsertSkillDef's freeze for the
// exact rule (and the provenance-transition exception).
func AutoIngestSkills(st *store.Store, defs []*skillpkg.Definition) int {
	count := 0
	for _, def := range defs {
		if def == nil || def.Slug == "" {
			continue
		}
		if err := upsertSkillDef(st, def); err != nil {
			slog.Warn("service: auto-ingest skill", "slug", def.Slug, "err", err)
			continue
		}
		count++
	}
	return count
}

// upsertAgentDef inserts or updates one agent_profiles row from a Definition.
// Uses ToProfile() for field mapping; applies H1 trust tier; sets ingestion metadata.
//
// bootPass distinguishes the two legitimate callers (TASKS/phase-1/08):
//   - true  (AutoIngestAgents' boot-time bulk pass): once a row already
//     exists under its current source, content sync (UpdateAgent, plus the
//     secondary seedProcedures/seedRoleToolsFromIngest passes) is skipped --
//     the DB is authoritative, the file is not re-synced on every process
//     start. The one exception is a genuine provenance transition (the
//     existing row's source differs from this def's source) -- that's a
//     deliberate one-time reclassification (e.g. the historical
//     builtin->internal migration flip), not an ordinary repeated boot, so
//     it still syncs once.
//   - false (IngestAgentDefinition's explicit reimport): always syncs,
//     regardless of whether a row already exists -- this is the deliberate
//     "the operator/API just edited this file, commit it" action.
func upsertAgentDef(st *store.Store, def *agentpkg.Definition, bootPass bool) error {
	now := time.Now().UTC().Format(time.RFC3339)

	// H1 trust: user/plugin-dropped files are untrusted until promoted.
	trustTier := "normal"
	if def.Source == "user" || def.Source == "plugin" {
		trustTier = "untrusted"
	}

	// ToProfile() maps Definition → store.AgentProfile including all v2 fields.
	profile := def.ToProfile()
	// Override fields set by the ingestion layer.
	profile.Source = def.Source
	if profile.Source == "" {
		profile.Source = "user"
	}
	profile.ImportedAt = now
	profile.OriginSystem = "nanite"
	profile.Format = "markdown"

	// Identity resolution. A managed file stamped with a UUID (`id:`) owns a
	// stable identity that survives slug renames — look it up by ID first so a
	// renamed file updates the existing row (and its FK children) instead of
	// colliding on a fresh insert. Fall back to slug for unstamped/internal
	// definitions, whose runtime identity stays "file-<slug>".
	var existing *store.AgentProfile
	if def.ID != "" && !agentpkg.IsFileBasedID(def.ID) {
		if row, err := st.GetAgent(def.ID); err == nil {
			existing = row
		}
	}
	if existing == nil {
		if row, err := st.GetAgentBySlug(def.Slug); err == nil {
			existing = row
		}
	}

	if existing == nil {
		// Unstamped/internal definitions (CanonicalID == "file-<slug>") get a
		// minted DB UUID while the harness keeps using the deterministic
		// "file-<slug>" runtime identity. A managed file stamped with a real
		// UUID uses that UUID as the DB row PK so reflex/known-tool/boot-plan
		// FKs resolve correctly.
		if agentpkg.IsFileBasedID(profile.ID) {
			profile.ID = ""
		}
		profile.Kind = "internal"
		profile.CapabilitiesJSON = "[]"
		profile.LimitsJSON = "{}"
		if err := st.CreateAgent(profile); err != nil {
			return fmt.Errorf("create: %w", err)
		}
	} else {
		// Preserve existing ID, created_at, version counter.
		profile.ID = existing.ID
		profile.AgentHash = existing.AgentHash
		profile.Version = existing.Version
		profile.Kind = existing.Kind
		if profile.Kind == "" {
			profile.Kind = "internal"
		}
		if profile.CapabilitiesJSON == "" {
			profile.CapabilitiesJSON = existing.CapabilitiesJSON
		}
		if profile.LimitsJSON == "" {
			profile.LimitsJSON = existing.LimitsJSON
		}
		// TASKS/phase-1/08: on a boot-time pass, a row that's already been
		// ingested under its current source is frozen -- skip the content
		// sync so a DB-side edit (however it landed) survives the next
		// restart. sourceChanged (below) carves out the one legitimate
		// exception: a genuine provenance transition still syncs once.
		if !(bootPass && existing.Source == profile.Source) {
			if err := st.UpdateAgent(profile); err != nil {
				return fmt.Errorf("update: %w", err)
			}
		}
	}
	// freshContent is true when this call actually created the row or (on a
	// boot pass) just completed a provenance-transition sync -- the two
	// cases where the secondary seed passes below (procedures, role tools)
	// should also run. It mirrors the UpdateAgent gate above so a frozen
	// boot-time reingest doesn't re-stomp a GUI/API customization to either
	// child table either.
	freshContent := existing == nil || !bootPass || existing.Source != profile.Source

	// Apply H1 trust tier. Always reconcile — if a file was promoted to trusted
	// and then the source changed (e.g., file moved to ~/.nanite/agents/), re-ingest
	// should not silently leave it trusted. We only override when the tier is
	// deterministic from Source; workspace overrides (workspace_role_trust) are
	// not touched — they remain independent per the H1 design.
	if _, err := st.DB.Exec(
		`UPDATE agent_profiles SET default_trust_tier = ? WHERE slug = ?`,
		trustTier, def.Slug,
	); err != nil {
		return fmt.Errorf("set trust tier: %w", err)
	}

	if freshContent && len(def.Procedures) > 0 {
		row, err := st.GetAgentBySlug(def.Slug)
		if err == nil && row != nil {
			seedProcedures(context.Background(), st, row.ID, def.Procedures)
		}
	}
	if freshContent && len(def.RoleTools) > 0 {
		row, err := st.GetAgentBySlug(def.Slug)
		if err == nil && row != nil {
			seedRoleToolsFromIngest(context.Background(), st, row.ID, def.RoleTools)
		}
	}
	return nil
}

func seedRoleToolsFromIngest(ctx context.Context, st *store.Store, agentID string, tools []string) {
	for i, name := range tools {
		if name == "" {
			continue
		}
		if err := st.InsertAgentKnownTool(ctx, store.AgentKnownTool{
			AgentID:   agentID,
			ToolName:  name,
			Pinned:    true,
			SortOrder: int64(i + 1),
			Reason:    "role_seed",
		}); err != nil {
			slog.Warn("service: seed role tool (ingest)", "agent_id", agentID, "tool", name, "err", err)
		}
	}
}

func seedProcedures(ctx context.Context, st *store.Store, agentID string, procs []agentpkg.ProcedureDefinition) {
	for _, p := range procs {
		if p.Name == "" {
			slog.Warn("service: skip procedure with empty name", "agent_id", agentID)
			continue
		}
		if p.Body == "" {
			slog.Warn("service: skip procedure with empty body", "agent_id", agentID, "procedure", p.Name)
			continue
		}
		scope := p.Scope
		if scope == "" {
			scope = "agent"
		}
		if err := st.InsertAgentProcedure(ctx, store.AgentProcedure{
			AgentID: agentID,
			Name:    p.Name,
			Body:    p.Body,
			Scope:   scope,
		}); err != nil {
			slog.Warn("service: seed procedure", "agent_id", agentID, "procedure", p.Name, "err", err)
		}
	}
}

// upsertSkillDef inserts or updates one skills row from a Definition.
// Uses ToStoreSkill() for field mapping; sets ingestion metadata.
//
// E2 (CW-20260428-0017): translates def.Modes (slugs) → mode IDs and
// stores them as a JSON array in skills.mode_ids. Unresolved slugs are
// dropped silently — the file is the input, the DB is the runtime, and
// a typo upstream should not crash ingestion.
func upsertSkillDef(st *store.Store, def *skillpkg.Definition) error {
	now := time.Now().UTC().Format(time.RFC3339)

	source := def.Source
	if source == "" {
		source = "user"
	}

	// ToStoreSkill() maps Definition → store.Skill.
	sk := def.ToStoreSkill()
	sk.ModeIDs = resolveSkillModeIDs(st, def.Modes)

	existing, err := st.GetSkillBySlug(def.Slug)
	if err != nil {
		return fmt.Errorf("lookup: %w", err)
	}

	if existing == nil {
		// DB rows get real UUIDs; strip the deterministic file-ID prefix.
		sk.ID = ""
		sk.IsBuiltin = false
		sk.Source = source
		sk.ImportedAt = now
		sk.OriginSystem = "nanite"
		sk.Format = "markdown"
		sk.Version = 1
		if err := st.CreateSkill(sk); err != nil {
			return fmt.Errorf("create: %w", err)
		}
		return nil
	}

	// TASKS/phase-1/08: once a row already exists under its current source,
	// AutoIngestSkills' boot-time pass no longer content-syncs it -- the DB
	// is authoritative, the file is not re-parsed-and-overwritten on every
	// process start. upsertSkillDef has exactly one caller (AutoIngestSkills
	// -- confirmed via grep; the REST CRUD path's handleUpdateSkill writes
	// through st.UpdateSkill directly, never through this function), so
	// unlike upsertAgentDef there is no separate "explicit reimport" caller
	// to preserve a resync path for. The one exception is a genuine
	// provenance transition (the row's source differs from this def's
	// source) -- a deliberate one-time reclassification, not an ordinary
	// repeated boot, so it still syncs once and bumps the version if the
	// content also changed.
	if existing.Source == source {
		return nil
	}

	// Update existing row; bump version when content changes.
	contentChanged := existing.Prompt != def.Prompt ||
		existing.ToolBindings != sk.ToolBindings ||
		existing.ModeIDs != sk.ModeIDs
	newVersion := existing.Version
	if contentChanged {
		newVersion++
	}

	existing.Name = sk.Name
	existing.Description = sk.Description
	existing.Category = sk.Category
	existing.ToolBindings = sk.ToolBindings
	existing.Settings = sk.Settings
	existing.Prompt = def.Prompt
	existing.ModeIDs = sk.ModeIDs
	existing.Source = source
	existing.ImportedAt = now
	existing.OriginSystem = "nanite"
	existing.Format = "markdown"
	existing.Version = newVersion

	if err := st.UpdateSkill(existing); err != nil {
		return fmt.Errorf("update: %w", err)
	}
	return nil
}

// resolveSkillModeIDs marshals a skill definition's frontmatter `modes:`
// slugs into the JSON-array string written to skills.mode_ids. Empty input
// → "[]" (back-compat: skill is available in every mode).
//
// Phase 0 item 21 ("Cut Modes, in full") deleted the `modes` catalog table
// and store.GetModeBySlug — this function used to resolve each slug against
// that table and store the resolved row ID. There is no more catalog to
// resolve against, and skills.mode_ids' only remaining consumer
// (internal/skillbroker's mode-bound relevance bonus) only ever checks
// whether the column is non-empty, never a specific ID — so the slugs
// themselves are now stored directly as their own identity, unresolved.
func resolveSkillModeIDs(_ *store.Store, slugs []string) string {
	return store.MarshalSkillModeIDs(slugs)
}
