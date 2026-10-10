package service

import (
	"context"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/agentimport"
	"github.com/hollis-labs/nanite/internal/store"
)

// SeedImportedAgentChildren refuses the retired profile import format. A
// declaration or an old importer-created row cannot initialize actor authority.
func SeedImportedAgentChildren(_ *store.Store) agentimport.ChildSeeder {
	return func(context.Context, string, *agent.Definition, bool) error { return store.ErrImmutableAgentProfile }
}
