package service

import (
	"context"

	"github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// storeHostMCP is the launch-time source of a CLI host's MCP servers (the
// registry) and the tool grants that gate them (the native authority).
type storeHostMCP struct{ st *store.Store }

var _ agent.HostMCPSource = storeHostMCP{}

func (h storeHostMCP) MCPServer(ctx context.Context, name string) (*store.MCPServerConfig, error) {
	return h.st.GetMCPServer(ctx, name)
}

func (h storeHostMCP) GrantedTools(ctx context.Context, hostSettingsID, revision string) ([]string, error) {
	return h.st.LocalAuthority().ResolveToolNames(ctx, hostSettingsID, revision)
}
