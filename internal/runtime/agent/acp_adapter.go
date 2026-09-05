package agent

import (
	"fmt"

	"github.com/hollis-labs/go-agent-wrapper/adapters"
	"github.com/hollis-labs/go-agent-wrapper/adapters/claudeacp"
	"github.com/hollis-labs/go-agent-wrapper/adapters/codexacp"
	"github.com/hollis-labs/go-agent-wrapper/adapters/copilotacp"
	"github.com/hollis-labs/go-agent-wrapper/adapters/opencodeacp"
	"github.com/hollis-labs/go-agent-wrapper/adapters/piacp"
)

var acpSupportedProviders = map[string]bool{
	"opencode": true,
	"copilot":  true,
	"claude":   true,
	"codex":    true,
	"pi":       true,
}

// newACPAdapter selects the shipped protocol adapter. Wrapper.Run obtains a
// fresh client through acp.ClientAdapter and owns initialize, resume/new,
// prompt, cancellation, close, event drain, provider identity, and cleanup.
func newACPAdapter(providerName string, transport adapters.Transport) (adapters.Adapter, error) {
	switch normalizeProviderName(providerName) {
	case "opencode":
		return opencodeacp.New(), nil
	case "copilot":
		return copilotacp.New(copilotacp.WithAdapterTransport(transport)), nil
	case "claude":
		return claudeacp.New(), nil
	case "codex":
		return codexacp.New(), nil
	case "pi":
		return piacp.New(), nil
	default:
		return nil, fmt.Errorf(
			"agent: protocol=acp is not supported for provider %q yet (supported: opencode, copilot, claude, codex, pi)",
			providerName,
		)
	}
}
