package agent

import (
	"fmt"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// stubAdapter is a minimal CLIAgentAdapter for testing.
type stubAdapter struct {
	name     string
	priority int
	defs     []Definition
	discErr  error
}

func (s *stubAdapter) Name() string { return s.name }
func (s *stubAdapter) Priority() int { return s.priority }
func (s *stubAdapter) Discover(projectDir string) ([]Definition, error) {
	if s.discErr != nil {
		return nil, s.discErr
	}
	return s.defs, nil
}
func (s *stubAdapter) PopulateSandbox(sandboxDir string, agent store.AgentProfile, session SandboxContext) error {
	return nil
}
func (s *stubAdapter) SyncProjectRoot(projectDir string, agents []store.AgentProfile) error {
	return nil
}

func TestRegistry_PriorityOrdering(t *testing.T) {
	reg := NewAdapterRegistry()

	reg.Register(&stubAdapter{name: "c", priority: 30})
	reg.Register(&stubAdapter{name: "a", priority: 10})
	reg.Register(&stubAdapter{name: "b", priority: 20})

	adapters := reg.Adapters()
	if len(adapters) != 3 {
		t.Fatalf("expected 3 adapters, got %d", len(adapters))
	}
	if adapters[0].Name() != "a" || adapters[1].Name() != "b" || adapters[2].Name() != "c" {
		t.Errorf("expected order [a b c], got [%s %s %s]",
			adapters[0].Name(), adapters[1].Name(), adapters[2].Name())
	}
}

func TestRegistry_DiscoverAllDeduplication(t *testing.T) {
	reg := NewAdapterRegistry()

	// First adapter (lower priority) defines "agent-x".
	reg.Register(&stubAdapter{
		name:     "first",
		priority: 10,
		defs: []Definition{
			{Slug: "agent-x", Name: "First X", Source: "first"},
			{Slug: "agent-y", Name: "First Y", Source: "first"},
		},
	})
	// Second adapter also defines "agent-x" — should be deduplicated.
	reg.Register(&stubAdapter{
		name:     "second",
		priority: 20,
		defs: []Definition{
			{Slug: "agent-x", Name: "Second X", Source: "second"},
			{Slug: "agent-z", Name: "Second Z", Source: "second"},
		},
	})

	defs, err := reg.DiscoverAll("/tmp/project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(defs) != 3 {
		t.Fatalf("expected 3 definitions (dedup agent-x), got %d", len(defs))
	}

	// agent-x should come from "first" adapter (lower priority wins).
	for _, d := range defs {
		if d.Slug == "agent-x" && d.Source != "first" {
			t.Errorf("agent-x should come from 'first', got source=%q", d.Source)
		}
	}
}

func TestRegistry_DiscoverAllError(t *testing.T) {
	reg := NewAdapterRegistry()

	reg.Register(&stubAdapter{
		name:     "broken",
		priority: 10,
		discErr:  fmt.Errorf("discovery failed"),
	})

	_, err := reg.DiscoverAll("/tmp/project")
	if err == nil {
		t.Fatal("expected error from broken adapter")
	}
}

func TestRegistry_GetAdapter(t *testing.T) {
	reg := NewAdapterRegistry()
	reg.Register(&stubAdapter{name: "claude", priority: 10})

	// Found
	a, ok := reg.GetAdapter("claude")
	if !ok || a == nil {
		t.Fatal("expected to find adapter 'claude'")
	}
	if a.Name() != "claude" {
		t.Errorf("expected name 'claude', got %q", a.Name())
	}

	// Not found
	_, ok = reg.GetAdapter("nonexistent")
	if ok {
		t.Error("expected adapter 'nonexistent' to not be found")
	}
}

func TestRegistry_AdaptersReturnsCopy(t *testing.T) {
	reg := NewAdapterRegistry()
	reg.Register(&stubAdapter{name: "a", priority: 10})

	copy1 := reg.Adapters()
	copy1[0] = &stubAdapter{name: "mutated"}

	copy2 := reg.Adapters()
	if copy2[0].Name() != "a" {
		t.Error("Adapters() should return a copy; mutation leaked")
	}
}
