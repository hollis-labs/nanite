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
func AutoIngestAgents(st *store.Store, defs []*agentpkg.Definition) int {
	count := 0
	var failures []string
	for _, def := range defs {
		if def == nil || def.Slug == "" {
			continue
		}
		if err := upsertAgentDef(st, def); err != nil {
			slog.Warn("service: auto-ingest agent", "slug", def.Slug, "err", err)
			failures = append(failures, fmt.Sprintf("%s: %v", def.Slug, err))
			continue
		}
		count++
	}
	if len(failures) > 0 {
		slog.Error("service: agent auto-ingest failed for one or more files — these agents are file-discoverable but have no working agent_profiles row until fixed and the service is restarted",
			"failed", len(failures), "total", len(defs), "succeeded", count, "failures", failures)
	}
	return count
}

func IngestAgentDefinition(st *store.Store, def *agentpkg.Definition) error {
	if def == nil || def.Slug == "" {
		return fmt.Errorf("definition slug is required")
	}
	return upsertAgentDef(st, def)
}

// AutoIngestSkills upserts all discovered skill definitions into the DB.
// Called once at container startup. Errors per-definition are logged and
// skipped; the function returns the count of successful ingestions.
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
func upsertAgentDef(st *store.Store, def *agentpkg.Definition) error {
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
		if err := st.UpdateAgent(profile); err != nil {
			return fmt.Errorf("update: %w", err)
		}
	}

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

	if len(def.Procedures) > 0 {
		row, err := st.GetAgentBySlug(def.Slug)
		if err == nil && row != nil {
			seedProcedures(context.Background(), st, row.ID, def.Procedures)
		}
	}
	if len(def.RoleSkills) > 0 {
		row, err := st.GetAgentBySlug(def.Slug)
		if err == nil && row != nil {
			seedRoleSkills(context.Background(), st, row.ID, def.RoleSkills)
		}
	}
	if len(def.RoleTools) > 0 {
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

func seedRoleSkills(ctx context.Context, st *store.Store, agentID string, slugs []string) {
	for _, slug := range slugs {
		if slug == "" {
			continue
		}
		if err := st.InsertAgentKnownSkill(ctx, store.AgentKnownSkill{
			AgentID:   agentID,
			SkillName: slug,
			Pinned:    true,
			Reason:    "role_seed",
		}); err != nil {
			slog.Warn("service: seed role skill", "agent_id", agentID, "skill_name", slug, "err", err)
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

// resolveSkillModeIDs translates a list of mode slugs to mode IDs by
// querying the modes table. Unresolved slugs are dropped silently (logged
// as a warning). Empty input → "[]" (back-compat: skill is available in
// every mode). The result is the JSON-array string written to
// skills.mode_ids.
func resolveSkillModeIDs(st *store.Store, slugs []string) string {
	if len(slugs) == 0 {
		return "[]"
	}
	ids := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		mode, err := st.GetModeBySlug(slug)
		if err != nil {
			slog.Warn("service: resolve skill mode slug", "slug", slug, "err", err)
			continue
		}
		if mode == nil {
			slog.Warn("service: skill mode slug not found", "slug", slug)
			continue
		}
		ids = append(ids, mode.ID)
	}
	return store.MarshalSkillModeIDs(ids)
}
