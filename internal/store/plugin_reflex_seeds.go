package store

import (
	"context"
	"strings"
)

// PluginReflexSeed binds one validated, explicit-agent default to its durable
// definition. Binding identities remain stable across bundle updates.
type PluginReflexSeed struct {
	SeedID     string
	Definition AgentReflex
}

// BindPluginReflexSeeds creates absent defaults atomically and returns the
// surviving definitions. Existing definitions, edits, history and deletion
// tombstones are never overwritten by defaults.
func (s *Store) BindPluginReflexSeeds(ctx context.Context, owner string, seeds []PluginReflexSeed) ([]AgentReflex, error) {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return nil, ErrImmutableAgentProfile
}

// SetPluginReflexGate publishes a live-source predicate. It must not access
// the database: candidate enumeration holds the single SQLite connection.
// Catalog/editor reads continue to return inactive plugin definitions.
func (s *Store) SetPluginReflexGate(gate func(AgentReflex) bool) {
	if gate == nil {
		s.pluginReflexGate.Store(nil)
		return
	}
	s.pluginReflexGate.Store(&gate)
}
func (s *Store) pluginReflexAvailable(row AgentReflex) bool {
	if row.ProvenanceTier != "plugin" && !strings.HasPrefix(row.CreatedBy, "plugin:") {
		return true
	}
	gate := s.pluginReflexGate.Load()
	return gate != nil && (*gate)(row)
}
