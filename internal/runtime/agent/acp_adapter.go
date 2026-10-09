package agent

import (
	"fmt"

	"github.com/hollis-labs/substrate/harness/adapters"
	"github.com/hollis-labs/substrate/harness/adapters/launch"
)

// newACPAdapter selects the shipped protocol adapter through
// go-agent-wrapper's launch.Select, by registry runtime and ACP mode
// (acp-stdio, or acp-tcp for Copilot's daemon). Wrapper.Run obtains a fresh
// client through acp.ClientAdapter and owns initialize, resume/new, prompt,
// cancellation, close, event drain, provider identity, and cleanup. Kept as
// the production half of Dependencies.ACPAdapterFactory's signature.
func newACPAdapter(providerName string, transport adapters.Transport) (adapters.Adapter, error) {
	d, ok := resolveRuntime(providerName)
	if !ok {
		return nil, fmt.Errorf("agent: protocol=acp: %w: %q", errUnknownRuntime, providerName)
	}
	return launch.Select(launch.Selection{Runtime: string(d.ID), Mode: acpMode(d, transport)})
}
