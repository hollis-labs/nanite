package service

// Phase 1 item 04 (TASKS/phase-1/04-add-known-tools-and-agent-tools-fk.md):
// BackfillAgentToolsFromLegacyColumns carries the CURRENT value of every
// agent_profiles row's tools/role_tools columns into real agent_tools grant
// rows, once per agent -- so the follow-up that actually wires
// SelectForAgent to read agent_tools
// (TASKS/phase-4/05-wire-select-for-agent-to-read-agent-tools.md, which
// landed the SelectForAgent read path this file's comments below describe
// as "the follow-up" -- filterToolsByAllowlist referenced below no longer
// exists as of that task; it's retained here as the historical parity
// target this one-time backfill algorithm was built to replicate) starts
// from data that already matches today's live selection behavior instead
// of an empty table.
//
// Deliberately a ONE-TIME operation per agent, not a per-boot resync: once
// an agent has a row in agent_tools_legacy_backfill (an independent marker,
// not derived from whether any grant rows survive -- see that table's
// migration doc comment for why), this function skips it on every later
// boot. Without that guard, a later boot would silently re-derive (and
// re-assert) grants from the legacy JSON columns over any explicit change
// an operator later makes through agent_tools directly (e.g. task 09's
// picker UI) -- exactly the "file reingest silently reverts a GUI
// customization" anti-pattern architecture/01-agent-construction.md's
// "What's cut" section names as a real, already-fixed bug for agent
// profile files generally.
//
// role_tools is included in the candidate set alongside tools -- upgrading
// it from its current "best-effort agent_known_tools enrichment, not a
// contract the runtime enforces" status (migration 070's own doc comment)
// to a real agent_tools grant. This mirrors ingest.go's
// seedRoleToolsFromIngest, which this same task updates to grant agent_tools
// going forward for newly-ingested/re-ingested definitions -- see that
// function's doc comment for why this is a deliberate behavior upgrade, not
// a parity bug: SelectForAgent does not consult agent_tools yet (11 is
// still open), so nothing observable changes until that follow-up lands.

import (
	"context"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

const legacyBackfillProvenance = "legacy_backfill"

// BackfillAgentToolsFromLegacyColumns runs the one-time-per-agent backfill
// described above. Returns the number of agent_tools rows inserted across
// every agent. Must run after SyncKnownTools has populated known_tools for
// this boot (the candidate matching below is against the live catalog,
// not a blind replay of legacy JSON strings) and after AutoIngestAgents
// (so brand-new agents ingested this same boot are covered too).
func BackfillAgentToolsFromLegacyColumns(ctx context.Context, st *store.Store) (int, error) {
	available, err := st.ListAvailableKnownTools(ctx)
	if err != nil {
		return 0, err
	}
	idByName := make(map[string]string, len(available))
	allNames := make([]string, 0, len(available))
	for _, kt := range available {
		idByName[kt.Name] = kt.ID
		allNames = append(allNames, kt.Name)
	}

	agents, err := st.ListAgents()
	if err != nil {
		return 0, err
	}

	total := 0
	for _, agent := range agents {
		done, err := st.HasLegacyToolsBackfillRun(ctx, agent.ID)
		if err != nil {
			slog.Warn("service: backfill agent_tools — check guard", "agent", agent.ID, "err", err)
			continue
		}
		if done {
			continue
		}

		names := legacyGrantCandidates(agent, allNames)
		for _, name := range names {
			toolID, ok := idByName[name]
			if !ok {
				continue
			}
			if err := st.GrantAgentTool(ctx, agent.ID, toolID, legacyBackfillProvenance); err != nil {
				slog.Warn("service: backfill agent_tools — grant", "agent", agent.ID, "tool", name, "err", err)
				continue
			}
			total++
		}

		// Mark the agent as considered regardless of how many grants
		// resulted (including zero, for a genuinely empty legacy
		// configuration or a deny-everything policy) -- see
		// HasLegacyToolsBackfillRun's doc comment for why this must be an
		// independent marker, not derived from the grants themselves.
		if err := st.MarkLegacyToolsBackfillRun(ctx, agent.ID); err != nil {
			slog.Warn("service: backfill agent_tools — mark done", "agent", agent.ID, "err", err)
		}
	}
	return total, nil
}

// legacyGrantCandidates computes, for one agent, the set of currently-
// available tool names that today's live selection path
// (filterToolsByAllowlist, internal/service/tool.go) would consider
// selectable for it, given its current tools/role_tools column values.
//
//   - agent.Tools ("[]"/empty means no restriction, matching
//     filterToolsByAllowlist's own documented behavior) and agent.RoleTools
//     (upgraded to real selection input by this task, see the file doc
//     comment) are unioned into a pattern set. An empty pattern set means
//     "every currently-available tool" (mirrors the no-restriction case).
//   - The pattern set is expanded against allNames via
//     toolclient.MatchPattern (the same glob matcher filterToolsByAllowlist
//     already uses).
//
// TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md removed
// the second stage this used to run — narrowing the pattern-matched
// candidate set through agent.ToolPermissions' CheckPermission (allow_list
// / deny_list) — since tool_permissions no longer gates tool selection
// anywhere else in the system; replaying a narrowing step here that every
// other surface has stopped honoring would make this one-time backfill the
// LAST live consumer of a deny_list, silently under-granting relative to
// what agent_tools is now the sole, unconditional source of truth for. Any
// pre-existing agent's tool_permissions content (real, in the live DB for
// 14 of 35 seeded agents as of this task) is inert historical data now —
// see agent_profiles.ToolPermissions' doc comment (internal/store/agents.go).
func legacyGrantCandidates(agent store.AgentProfile, allNames []string) []string {
	patterns := dedupStrings(append(parseLegacyToolList(agent.Tools), parseLegacyToolList(agent.RoleTools)...))

	if len(patterns) == 0 {
		return allNames
	}
	var candidates []string
	for _, name := range allNames {
		for _, pattern := range patterns {
			if toolclient.MatchPattern(pattern, name) {
				candidates = append(candidates, name)
				break
			}
		}
	}
	return candidates
}

// parseLegacyToolList parses a JSON-array-of-strings column
// (agent_profiles.tools or .role_tools). Empty/"[]"/malformed all yield nil
// — the empty case is meaningful (see legacyGrantCandidates) and malformed
// input degrades to "no explicit patterns" rather than erroring, matching
// filterToolsByAllowlist's own tolerance of bad JSON.
func parseLegacyToolList(raw string) []string {
	if raw == "" || raw == "[]" {
		return nil
	}
	var out []string
	if err := parseJSONStrings(raw, &out); err != nil {
		return nil
	}
	return out
}

func dedupStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
