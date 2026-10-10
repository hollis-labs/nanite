package service

import (
	"testing"

	agentpkg "github.com/hollis-labs/nanite/internal/agent"
)

func TestAutoIngestAgentsRetiredWithoutCatalogOrDatabaseEffects(t *testing.T) {
	// A nil database deliberately proves even valid old documents cannot reach
	// row, role, procedure or declaration writers during a boot pass.
	defs := []*agentpkg.Definition{{Name: "Old seed", Slug: "old-seed", Source: "internal", SystemPrompt: "Old behavior", RoleTools: []string{"all"}}, nil}
	if got := AutoIngestAgents(nil, defs, map[string]bool{"all": true}); got != 0 {
		t.Fatalf("retired boot ingestion reported effects: %d", got)
	}
}
