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
func AutoIngestAgents(st *store.Store, defs []*agentpkg.Definition) int {
	count := 0
	for _, def := range defs {
		if def == nil || def.Slug == "" {
			continue
		}
		if err := upsertAgentDef(st, def); err != nil {
			slog.Warn("service: auto-ingest agent", "slug", def.Slug, "err", err)
			continue
		}
		count++
	}
	return count
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

	existing, err := st.GetAgentBySlug(def.Slug)
	if err != nil {
		// GetAgentBySlug wraps sql.ErrNoRows as an error string; treat as not-found.
		existing = nil
	}

	if existing == nil {
		// Strip the deterministic file-ID prefix — DB rows get real UUIDs.
		// CreateAgent will assign a new UUID if ID is empty.
		profile.ID = ""
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
	return nil
}

// upsertSkillDef inserts or updates one skills row from a Definition.
// Uses ToStoreSkill() for field mapping; sets ingestion metadata.
func upsertSkillDef(st *store.Store, def *skillpkg.Definition) error {
	now := time.Now().UTC().Format(time.RFC3339)

	source := def.Source
	if source == "" {
		source = "user"
	}

	// ToStoreSkill() maps Definition → store.Skill.
	sk := def.ToStoreSkill()

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
	contentChanged := existing.Prompt != def.Prompt || existing.ToolBindings != sk.ToolBindings
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
