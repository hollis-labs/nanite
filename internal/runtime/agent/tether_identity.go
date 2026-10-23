package agent

import (
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/a2a"
)

// tetherIdentityFile returns the planted Tether-identity file content for
// params, or nil when there is no address to name -- a true no-op for every
// session that is neither a bound durable actor nor a profile registered in
// Tether. The Agent URN is the bound durable actor's stable address
// (params.ActorURN) when there is one, because that is the address the inbound
// Tether bridge delivers to and so the one a reply must be sent from; the
// profile's own TetherURN is the fallback. Gated on the value, not
// TetherManaged (the intent) -- a profile flipped tether_managed but not yet
// minted has nothing to plant yet either way.
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
	agentURN := params.ActorURN
	if agentURN == "" && params.AgentProfile != nil {
		agentURN = params.AgentProfile.TetherURN
	}
	if agentURN == "" {
		return nil
	}

	sessionURN := ""
	if params.SessionID != "" {
		sessionURN = a2a.NewSessionAddress(params.SessionID).URN()
	}

	var b strings.Builder
	b.WriteString("# Tether identity\n\nThis session's addressable identity for cross-agent messaging.\n\n")
	fmt.Fprintf(&b, "- Agent URN: `%s`\n- Session URN: `%s`\n", agentURN, sessionURN)
	b.WriteString("\nThis is guidance, not a control: Tether attributes a send to the token holder only when it " +
		"enforces sender identity; while it only observes, `from` is whatever the caller passes.\n")
	if params.ActorURN != "" {
		fmt.Fprintf(&b, "\nThe Agent URN is this session's stable actor: Tether mail addressed to it is delivered "+
			"into this session, so use it as `from` when you send or reply (`in_reply_to` the message you answer).\n")
		if p := params.AgentProfile; p != nil && p.TetherURN != "" && p.TetherURN != params.ActorURN {
			fmt.Fprintf(&b, "\nThe profile's registered address `%s` is not this actor's delivery address; do not send from it.\n", p.TetherURN)
		}
	}
	return []byte(b.String())
}
