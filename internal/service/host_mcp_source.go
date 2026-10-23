package service

import (
	"context"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// storeHostMCP is the launch-time source of a CLI host's MCP servers (the
// registry) and the tool grants that gate them. Grants go through the same
// NativeGrants decider the API runtime uses, so the submitting credential's
// standing and tools scope and the turn's narrowing apply here too. It holds
// the read seam only: the authority writer is built in container.go.
type storeHostMCP struct {
	st     *store.Store
	grants *NativeGrants
}

var _ agent.HostMCPSource = storeHostMCP{}

func (h storeHostMCP) MCPServer(ctx context.Context, name string) (*store.MCPServerConfig, error) {
	return h.st.GetMCPServer(ctx, name)
}

// GrantedTools returns what the turn's execution capability may use now. A
// launch with no capability in ctx (no submitting credential, or a turn that is
// not a native turn) or one admitted under a different host revision gets
// nothing: the servers are planted and every call is refused.
func (h storeHostMCP) GrantedTools(ctx context.Context, hostSettingsID, revision string) ([]string, error) {
	c, ok := executionCapabilityFrom(ctx)
	if !ok || h.grants == nil {
		slog.Warn("host mcp: no execution capability for this launch; no tool is granted", "host_settings", hostSettingsID)
		return nil, nil
	}
	if c.hostSettingsID != hostSettingsID || c.admittedRevision != revision {
		slog.Warn("host mcp: capability is for another host revision; no tool is granted", "host_settings", hostSettingsID)
		return nil, nil
	}
	return h.grants.Offered(ctx, NativeSubject{HostSettingsID: c.hostSettingsID, AdmittedRevision: c.admittedRevision, SubmittedBy: c.submittedBy}, c.narrowing), nil
}
