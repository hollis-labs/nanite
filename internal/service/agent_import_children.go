package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/agentimport"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// SeedImportedAgentChildren seeds authored content on import/sync. Tool
// declarations initialize grants only on creation; syncing content must never
// undo an operator's revocations. The independent backfill marker also prevents
// a later boot from replaying those declarations.
func SeedImportedAgentChildren(st *store.Store) agentimport.ChildSeeder {
	return func(ctx context.Context, agentID string, def *agent.Definition, created bool) error {
		if def == nil || agentID == "" {
			return nil
		}
		if len(def.Procedures) > 0 {
			seedProcedures(ctx, st, agentID, def.Procedures)
		}
		seedRoleToolRoster(ctx, st, agentID, def.RoleTools, false)
		var grants []store.InitialAgentToolGrant
		if created {
			available, err := st.ListAvailableKnownTools(ctx)
			if err != nil {
				return fmt.Errorf("list install tool catalog: %w", err)
			}
			matched := make(map[string]bool)
			for _, known := range available {
				via := ""
				for _, pattern := range def.Tools {
					if toolclient.MatchPattern(pattern, known.Name) {
						matched[pattern] = true
						via = "explicit"
					}
				}
				if via == "" {
					for _, name := range def.RoleTools {
						if name == known.Name {
							via = "role_seed"
							break
						}
					}
				}
				if via != "" {
					grants = append(grants, store.InitialAgentToolGrant{ToolID: known.ID, GrantedVia: via})
				}
			}
			for _, pattern := range def.Tools {
				if !matched[pattern] {
					slog.Warn("agent install: declared tool pattern has no available catalog match", "agent_id", agentID, "pattern", pattern)
				}
			}
		}
		return st.InitializeAgentToolGrants(ctx, agentID, grants)
	}
}
