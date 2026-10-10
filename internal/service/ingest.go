package service

import (
	"context"

	agentpkg "github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// AutoIngestAgents is retained as an explicit retired-source boundary. Legacy
// profile documents never populate the fresh definition, host or actor graph.
// New embedded definitions are installed separately as immutable artifacts.
func AutoIngestAgents(_ *store.Store, _ []*agentpkg.Definition, _ map[string]bool) int { return 0 }

// upsertAgentDef refuses old profile documents before any dependent effects.
// Suppression remains distinguishable for audited historical retirement callers.
func upsertAgentDef(st *store.Store, def *agentpkg.Definition) error {
	if def == nil {
		return store.ErrImmutableAgentProfile
	}
	retired, err := st.ProfileIngestionRetired(context.Background(), def.ID, def.Slug)
	if err != nil {
		return err
	}
	if retired {
		return store.ErrProfileIngestionRetired
	}
	return store.ErrImmutableAgentProfile
}
