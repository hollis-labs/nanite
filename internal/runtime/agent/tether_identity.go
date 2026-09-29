package agent

import (
	"fmt"

	"github.com/hollis-labs/nanite/internal/a2a"
)

// tetherIdentityFile returns the planted Tether-identity file content for
// params, or nil when the agent profile carries no TetherURN -- a true
// no-op for every profile that hasn't been registered in Tether. Gated on
// TetherURN (the value), not TetherManaged (the intent) -- a profile
// flipped tether_managed but not yet minted has nothing to plant yet
// either way.
//
// TetherManaged/TetherURN (migration 162, internal/store/agents.go) are
// plain agent_profiles columns: TetherManaged defaults false and is an
// explicit per-agent opt-in; TetherURN is minted by an ordinary agent tool
// call to tether_registry_register (not by this package, and not by any
// framework automation) and set directly via the agent API once minted.
//
// Both values this function plants are already resolved before Populate
// ever runs: the agent URN was minted once, at registration time, long
// before any boot; SessionID is already known to the caller before boot-dir
// materialization starts. Planting it here is therefore a pure, local,
// synchronous string operation -- no client, no network call, consistent
// with Populate's own contract (bootdir_claude.go: "a synchronous
// filesystem write with no cancellation point today"). See
// docs/adding-an-agent.md, "Materialize, then boot."
func tetherIdentityFile(params SetupParams) []byte {
	if params.AgentProfile == nil || params.AgentProfile.TetherURN == "" {
		return nil
	}

	sessionURN := ""
	if params.SessionID != "" {
		sessionURN = a2a.NewSessionAddress(params.SessionID).URN()
	}

	return []byte(fmt.Sprintf(
		"# Tether identity\n\nThis session's addressable identity for cross-agent messaging.\n\n"+
			"- Agent URN: `%s`\n- Session URN: `%s`\n",
		params.AgentProfile.TetherURN, sessionURN,
	))
}
