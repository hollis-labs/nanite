package service

// BackfillAgentToolsFromLegacyColumns copied each agent profile's legacy
// tools/role_tools selection into agent_tools when agent_tools became the live
// grant source. Candidate matching preserves the historical selection
// semantics so profiles first encountered after that cutover receive the same
// initial grants as profiles migrated at the time.
//
// Deliberately a ONE-TIME operation per agent, not a per-boot resync: once
// an agent has a row in agent_tools_legacy_backfill (an independent marker,
// not derived from whether any grant rows survive -- see that table's
// migration doc comment for why), this function skips it on every later
// boot. Without that guard, a later boot would silently re-derive (and
// re-assert) grants from the legacy JSON columns over any explicit change
// an operator later makes through agent_tools directly -- the same class of
// problem as file reingest silently reverting an operator customization.
//
// role_tools is included alongside tools because the agent_tools migration
// deliberately promoted that prior best-effort enrichment input into real
// grants. This mirrors seedRoleToolsFromIngest for newly ingested definitions.

import (
	"context"

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
// BackfillAgentToolsFromLegacyColumns is retained only as a retired entrypoint.
// Historical selection columns never issue or replay fresh actor grants.
func BackfillAgentToolsFromLegacyColumns(ctx context.Context, st *store.Store) (int, error) {
	return 0, store.ErrVerifiedActorRequired
}

// legacyGrantCandidates computes, for one agent, the set of currently
// available tool names that the historical tools/role_tools selection path
// would have considered selectable.
//
//   - agent.Tools ("[]"/empty means no restriction, matching
//     the historical behavior) and agent.RoleTools are unioned into a pattern
//     set. An empty pattern set means "every currently-available tool."
//   - The pattern set is expanded against allNames via
//     toolclient.MatchPattern, preserving the historical glob behavior.
//
// ToolPermissions is deliberately absent. Its retired allow/deny policy no
// longer gates tool selection; replaying it here would make this one-time
// backfill the last live deny-list consumer and silently under-grant relative
// to agent_tools, the sole grant source. Existing tool_permissions values are
// retained only as inert historical data.
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
// input degrades to "no explicit patterns" rather than erroring, matching the
// historical selection path's tolerance of bad JSON.
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
