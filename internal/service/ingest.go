package service

// J7 (CW-20260421-0011): agent DB ingestion.
//
// This file lives in the service package so it can import both store and
// agent without creating an import cycle (store → agent → store is the
// cycle; service sits above both).
//
// Auto-ingestion is called from NewContainer for compiled-in seed definitions
// and any definitions supplied by an adapter extension. It makes the DB the
// runtime source of truth.
//
// TASKS/phase-1/08 ("Kill the file-reingest-on-boot pattern, in full"):
// AutoIngestAgents runs unconditionally on every boot, but once a def has
// already been ingested into a DB row, that row's content is frozen against
// further boot-time seed passes. The one exception is a genuine provenance
// transition (existing.Source != the incoming def's Source, e.g. the
// historical builtin->internal migration flip, CW-20260512-0111) -- that's
// a deliberate, one-time reclassification, not an ordinary repeated boot,
// so it still content-syncs once. Operator edits use AgentConfigService's
// direct database path and never re-import a definition file.
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
	"log/slog"

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
// A per-definition failure leaves that seed absent from agent_profiles. In
// addition to the per-item warning, emit one aggregate error naming every
// failed slug and reason.
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
	// Old seed formats are historical inputs, never fresh runtime definitions.
	return 0
}

// sourceTransitionSyncs decides whether a boot pass may content-sync an
// already-ingested row whose stored provenance differs from the incoming
// seed's.
//
// The base rule is TASKS/phase-1/08's: a row already ingested under its
// current source is frozen, and only a genuine provenance transition (the
// historical builtin -> internal reclassification, CW-20260512-0111) still
// syncs once.
//
// The carve-out below is the guard that rule needs now that agents can also
// arrive by explicit import (CW-20260910-0009, internal/agentimport). An
// imported row carries external provenance, which never matches a
// compiled-in seed's "internal" — so the transition rule alone would treat
// every boot as a legitimate one-time reclassification and overwrite it.
// Probed 2026-09-10: importing under a slug a compiled-in seed also uses
// (`reviewer`, `planner`, `worker`, `researcher` and `backend` are all real
// seed slugs) let the very next boot replace the imported content and flip
// source back to "internal", silently.
//
// A boot pass may never transition a row AWAY from external provenance. That
// is not what the carve-out was for: builtin -> internal is a rename of
// Nanite's own seeds, while external -> internal would be Nanite claiming
// ownership of something an operator deliberately imported. agentimport
// holds the same boundary in the other direction, refusing to write over a
// slug already held by an internal, managed or plugin profile — between them
// the two directions of the collision are closed.
func sourceTransitionSyncs(existingSource, incomingSource string) bool {
	if existingSource == incomingSource {
		return false
	}
	if agentpkg.NewClassification().Classify(existingSource) == agentpkg.ManageClassExternal {
		return false
	}
	return true
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

// upsertAgentDef inserts or updates one agent_profiles row from a Definition.
// Uses ToProfile() for field mapping; applies H1 trust tier; sets ingestion metadata.
//
// Once a row exists under the same source, the seed pass leaves its content
// and capability children untouched. A source transition syncs once for
// historical migrations such as builtin -> internal.
func upsertAgentDef(st *store.Store, def *agentpkg.Definition) error {
	retired, err := st.ProfileIngestionRetired(context.Background(), def.ID, def.Slug)
	if err != nil {
		return err
	}
	if retired {
		return store.ErrProfileIngestionRetired
	}
	return store.ErrImmutableAgentProfile
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
	seedRoleToolRoster(ctx, st, agentID, tools, true)
}

func seedRoleToolRoster(ctx context.Context, st *store.Store, agentID string, tools []string, grant bool) {
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

		if !grant {
			continue
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
