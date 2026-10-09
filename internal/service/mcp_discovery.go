package service

import (
	"context"
	"sync"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// newMCPDiscoverySync serializes discovery and catalog publication across
// server writes and explicit refreshes. It snapshots the unfiltered registry:
// load preferences affect model selection, not an operator's ability to grant
// a discovered tool. Catalog refresh never derives agent grants.
func newMCPDiscoverySync(st *store.Store, manager *mcp.Manager, client *toolclient.ToolClient) func(context.Context) (*mcp.DiscoveryDiff, error) {
	if manager == nil {
		return nil
	}
	var mu sync.Mutex
	return func(ctx context.Context) (*mcp.DiscoveryDiff, error) {
		mu.Lock()
		defer mu.Unlock()
		diff, err := manager.AutoDiscover(ctx, st)
		if err != nil {
			return nil, err
		}
		catalog := manager.GetAllToolsUnfiltered()
		var isBuiltin func(string) bool
		if client != nil {
			catalog = client.GetAllToolsUnfiltered()
			isBuiltin = client.IsBuiltinTool
		}
		catalog = append(catalog, toolclient.RequestToolsMetaTool())
		result := SyncKnownTools(ctx, st, catalog, isBuiltin)
		return diff, result.Err
	}
}
