package service

import (
	"context"

	agentpkg "github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/agentimport"
	"github.com/hollis-labs/nanite/internal/store"
)

// SeedImportedAgentChildren is the agentimport.ChildSeeder for this codebase:
// it writes an imported definition's relational children (procedures, and the
// agent_known_tools roster plus agent_tools grants derived from roleTools)
// through the same two seeders AutoIngestAgents uses.
//
// It exists as an injected function rather than as code inside
// internal/agentimport so the import pipeline reuses these seeders instead of
// growing a second copy of them, and so internal/agentimport stays free of a
// dependency on this package.
func SeedImportedAgentChildren(st *store.Store) agentimport.ChildSeeder {
	return func(ctx context.Context, agentID string, def *agentpkg.Definition) {
		if def == nil || agentID == "" {
			return
		}
		if len(def.Procedures) > 0 {
			seedProcedures(ctx, st, agentID, def.Procedures)
		}
		if len(def.RoleTools) > 0 {
			seedRoleToolsFromIngest(ctx, st, agentID, def.RoleTools)
		}
	}
}
