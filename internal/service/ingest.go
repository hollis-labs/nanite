package service

// J7 (CW-20260421-0011): agent DB ingestion.
//
// This file lives in the service package so it can import both store and
// agent without creating an import cycle (store → agent → store is the
// cycle; service sits above both).
//
// Auto-ingestion is called from NewContainer after Discover() returns.
// It makes the DB the runtime source of truth: files are the import path,
// DB is where the runtime reads from.
//
// TASKS/phase-1/08 ("Kill the file-reingest-on-boot pattern, in full"):
// AutoIngestAgents runs unconditionally on every boot, but once a def has
// already been ingested into a DB row, that row's content is frozen against
// further boot-time file-parse passes -- the file stays the *first-ingest*
// path, not a standing sync. The one exception is a genuine provenance
// transition (existing.Source != the incoming def's Source, e.g. the
// historical builtin->internal migration flip, CW-20260512-0111) -- that's
// a deliberate, one-time reclassification, not an ordinary repeated boot,
// so it still content-syncs once. A real, deliberate re-import (an agent
// edited through the managed-agent write path,
// AgentConfigService.Update/writeManaged/SaveManagedAgentProfile, all of
// which call IngestAgentDefinition directly, not through the boot-time
// AutoIngestAgents pass) is unaffected by the freeze -- see upsertAgentDef's
// bootPass parameter.
//
// TASKS/skills/01: this file used to carry the skill-side counterpart,
// AutoIngestSkills/upsertSkillDef/resolveSkillModeIDs, feeding
// skill.Discover()'s (always-empty) result plus the 8 embedded builtin
// skills into the DB. Both the discovery/builtin sources and this ingest
// pass are deleted in full per docs/engineering/architecture/20-skills.md's
// "Migration: clean slate, no carried-forward content" section — skills are
// authored packages installed explicitly (TASKS/skills/04-05), not a
// boot-time file-reingest target.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	agentpkg "github.com/hollis-labs/nanite/internal/agent"
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
	// colliding on a fresh insert. Fall back to slug for unstamped
	// definitions (e.g. an internal builtin seed profile with no `id:`
	// frontmatter, not yet ingested).
	var existing *store.AgentProfile
	if def.ID != "" {
		if row, err := st.GetAgent(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, def.ID); err == nil {
			existing = row
		}
	}
	if existing == nil {
		if row, err := st.GetAgentBySlug(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, def.Slug); err == nil {
			existing = row
		}
	}

	if existing == nil {
		// Unstamped definitions (def.ID == "", so profile.ID == "" too --
		// see Definition.ToProfile) get a minted DB UUID here, through the
		// exact same store.CreateAgent path any other newly created agent
		// goes through (TASKS/adhoc/01-eliminate-file-based-agent-runtime.md
		// -- this is now the one-time seed for the 9 internal builtin
		// profiles: no more parallel "file-<slug>" runtime identity, no
		// special-casing). A managed file stamped with a real UUID already
		// carries it in profile.ID and CreateAgent uses it as-is, so
		// reflex/known-tool/boot-plan FKs resolve correctly from creation.
		profile.Kind = "internal"
		profile.CapabilitiesJSON = "[]"
		profile.LimitsJSON = "{}"
		if err := st.CreateAgent(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, profile); err != nil {
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
		// role_id / consumer_id / model_id (Phase 1 items 02/03,
		// architecture/01-agent-construction.md's composition model) have
		// zero frontmatter representation -- def.ToProfile() always
		// returns their empty zero-value for a file-backed definition.
		// Without this, ANY reingest through this path -- every
		// managed-agent edit via AgentConfigService.Create/Update,
		// including ones with nothing to do with composition -- would
		// silently wipe a value set through TASKS/phase-5/01-build-
		// assignment-api.md's composition write path
		// (store.UpdateAgentComposition) back to NULL the next time the
		// agent's file was saved for an unrelated reason. Preserve the
		// existing row's values here -- "DB wins" on the write side, same
		// as the read side (agentServiceImpl.Get/GetBySlug/List just
		// return the row as-is now; see TASKS/adhoc/01-eliminate-file-
		// based-agent-runtime.md).
		// runtime_kind is deliberately NOT included -- unlike these
		// three, it already self-heals via applyMultiAgentDefaults'
		// inferRuntimeKind(a.DefaultProvider) whenever a caller leaves it
		// empty (called inside st.UpdateAgent below), which is the
		// desired behavior: a provider change on reingest SHOULD
		// re-derive cli/api classification, not freeze it.
		profile.RoleID = existing.RoleID
		profile.ConsumerID = existing.ConsumerID
		profile.ModelID = existing.ModelID
		// protocol / transport (TASKS/agent-host-acp/11-nanite-per-agent-
		// protocol-transport-config.md) are the identical shape: zero
		// frontmatter representation, written via the direct-DB
		// store.UpdateAgentACPConfig path (mirroring store.
		// UpdateAgentComposition above). Without this preservation line,
		// the very next unrelated managed-agent edit through this path
		// would silently wipe an operator's protocol="acp" configuration
		// back to "" (native) — same regression this file's RoleID/
		// ConsumerID/ModelID preservation exists to prevent.
		profile.Protocol = existing.Protocol
		profile.Transport = existing.Transport
		// TASKS/phase-1/08: on a boot-time pass, a row that's already been
		// ingested under its current source is frozen -- skip the content
		// sync so a DB-side edit (however it landed) survives the next
		// restart. sourceChanged (below) carves out the one legitimate
		// exception: a genuine provenance transition still syncs once.
		if !(bootPass && existing.Source == profile.Source) {
			if err := st.UpdateAgent(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, profile); err != nil {
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
		row, err := st.GetAgentBySlug(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, def.Slug)
		if err == nil && row != nil {
			seedProcedures(context.Background(), st, row.ID, def.Procedures)
		}
	}
	if freshContent && len(def.RoleTools) > 0 {
		row, err := st.GetAgentBySlug(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, def.Slug)
		if err == nil && row != nil {
			seedRoleToolsFromIngest(context.Background(), st, row.ID, def.RoleTools)
		}
	}
	return nil
}

// seedRoleToolsFromIngest seeds two things from a def's roleTools:
// frontmatter, per Phase 1 item 04
// (TASKS/phase-1/04-add-known-tools-and-agent-tools-fk.md):
//
//  1. agent_known_tools (unchanged, pre-existing behavior) — a pinned roster
//     row per name, reason='role_seed'. This is the live, per-agent
//     roster/telemetry table with its own REST CRUD and GUI; still not
//     touched by this task per its own Context section.
//  2. agent_tools (new) — a real grant row per name that resolves against
//     the known_tools catalog, granted_via='role_seed'. This is the
//     upgrade: role_tools used to only ever seed the best-effort
//     agent_known_tools roster ("NOT a contract the runtime enforces",
//     migration 070's own doc comment) with zero effect on real tool
//     selection. Now it also produces a real agent_tools grant, matching
//     architecture/01-agent-construction.md's statement that agent_tools
//     replaces roleTools: "entirely." A name with no matching known_tools
//     row (not yet live-synced, or a genuine typo — see
//     unknownDeclaredTools above) is skipped for (2) without failing the
//     ingest; (1) still records it regardless, preserving today's
//     tolerant behavior for that table.
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

		known, err := st.GetKnownToolByName(ctx, name)
		if err != nil {
			if !errors.Is(err, store.ErrKnownToolNotFound) {
				slog.Warn("service: seed role tool (ingest) — known_tools lookup", "agent_id", agentID, "tool", name, "err", err)
			}
			continue
		}
		if err := st.GrantAgentTool(ctx, agentID, known.ID, "role_seed"); err != nil {
			slog.Warn("service: seed role tool (ingest) — agent_tools grant", "agent_id", agentID, "tool", name, "err", err)
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

// TASKS/skills/01: upsertSkillDef and resolveSkillModeIDs (the
// AutoIngestSkills-only helpers that upserted a skills row from a
// file-parsed skill.Definition) are deleted along with AutoIngestSkills
// itself — see docs/engineering/architecture/20-skills.md's "Migration:
// clean slate, no carried-forward content" section. Nothing else called
// either function (confirmed via grep before deletion).
