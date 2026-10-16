package store

import (
	"context"
)

// ExistingPluginReflexSeed names a host-selected system definition to transfer.
// It never carries replacement trigger/action text or plugin-selected SQL.
type ExistingPluginReflexSeed struct{ SeedID, AgentID, LegacyName string }

// BindExistingPluginReflexSeeds atomically changes ownership and installs stable
// bindings. Definitions keep their IDs, names, edits, history and opt-outs.
// Missing source definitions become tombstones rather than recreated defaults.
// Call only after all accepted manifest registrations have succeeded.
func (s *Store) BindExistingPluginReflexSeeds(ctx context.Context, owner string, seeds []ExistingPluginReflexSeed) ([]AgentReflex, error) {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return nil, ErrImmutableAgentProfile
}
