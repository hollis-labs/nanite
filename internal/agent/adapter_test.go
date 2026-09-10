package agent

import (
	"fmt"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// stubAdapter is a minimal CLIAgentAdapter for testing.
type stubAdapter struct {
	name      string
	priority  int
	defs      []Definition
	importErr error
}

func (s *stubAdapter) Name() string  { return s.name }
func (s *stubAdapter) Priority() int { return s.priority }
func (s *stubAdapter) Import(path string) ([]Definition, error) {
	if s.importErr != nil {
		return nil, s.importErr
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

// TestRegistry_ImportAllFirstNonEmptyWins pins CW-20260910-0012's change to
// the aggregate semantics. DiscoverAll used to merge every adapter's results
// and dedup across them; ImportAll takes the FIRST non-empty result and stops.
//
// The reason is that a path has one format. Under the old merge, two adapters
// both claiming the same path produced a roster assembled from two readings
// of it — one of which was necessarily a guess.
func TestRegistry_ImportAllFirstNonEmptyWins(t *testing.T) {
	reg := NewAdapterRegistry()

	// Lower priority, and it claims the path.
	reg.Register(&stubAdapter{
		name:     "first",
		priority: 10,
		defs: []Definition{
			{Slug: "agent-x", Name: "First X", Source: "first"},
			{Slug: "agent-y", Name: "First Y", Source: "first"},
		},
	})
	// Also claims it. Never consulted, because "first" already answered.
	reg.Register(&stubAdapter{
		name:     "second",
		priority: 20,
		defs: []Definition{
			{Slug: "agent-x", Name: "Second X", Source: "second"},
			{Slug: "agent-z", Name: "Second Z", Source: "second"},
		},
	})

	name, defs, err := reg.ImportAll("/tmp/project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "first" {
		t.Errorf("winning adapter = %q, want %q", name, "first")
	}
	if len(defs) != 2 {
		t.Fatalf("expected only the winning adapter's 2 definitions, got %d: %+v", len(defs), defs)
	}
	for _, d := range defs {
		if d.Source != "first" {
			t.Errorf("definition %q came from %q; results must not be merged across adapters", d.Slug, d.Source)
		}
	}
}

// TestRegistry_ImportAllSkipsAdaptersThatDecline — (nil, nil) means "not my
// format," so the registry moves on rather than returning nothing.
func TestRegistry_ImportAllSkipsAdaptersThatDecline(t *testing.T) {
	reg := NewAdapterRegistry()
	reg.Register(&stubAdapter{name: "declines", priority: 10})
	reg.Register(&stubAdapter{
		name:     "claims",
		priority: 20,
		defs:     []Definition{{Slug: "agent-x", Source: "claims"}},
	})

	name, defs, err := reg.ImportAll("/tmp/project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "claims" || len(defs) != 1 {
		t.Errorf("winner = %q with %d defs, want %q with 1", name, len(defs), "claims")
	}
}

// TestRegistry_ImportAllDedupWithinWinner — dedup-by-slug survives the change,
// now scoped to the winning adapter's own result.
func TestRegistry_ImportAllDedupWithinWinner(t *testing.T) {
	reg := NewAdapterRegistry()
	reg.Register(&stubAdapter{
		name:     "dupes",
		priority: 10,
		defs: []Definition{
			{Slug: "agent-x", Name: "kept"},
			{Slug: "agent-x", Name: "dropped"},
			{Slug: "agent-y", Name: "kept too"},
		},
	})

	_, defs, err := reg.ImportAll("/tmp/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("expected 2 after dedup, got %d", len(defs))
	}
	if defs[0].Name != "kept" {
		t.Errorf("first slug wins: got %q", defs[0].Name)
	}
}

// TestRegistry_ImportAllNoAdapterClaims returns an empty winner name rather
// than an error — nothing recognized the path, which the caller reports.
func TestRegistry_ImportAllNoAdapterClaims(t *testing.T) {
	reg := NewAdapterRegistry()
	reg.Register(&stubAdapter{name: "declines", priority: 10})

	name, defs, err := reg.ImportAll("/tmp/project")
	if err != nil || name != "" || defs != nil {
		t.Errorf("got (%q, %v, %v), want (\"\", nil, nil)", name, defs, err)
	}
}

func TestRegistry_ImportAllError(t *testing.T) {
	reg := NewAdapterRegistry()

	reg.Register(&stubAdapter{
		name:      "broken",
		priority:  10,
		importErr: fmt.Errorf("import failed"),
	})

	if _, _, err := reg.ImportAll("/tmp/project"); err == nil {
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

// fakeAdapter is a minimal CLIAgentAdapter for testing the registry.
type fakeAdapter struct {
	name     string
	priority int
	synced   *bool
}

func (f *fakeAdapter) Name() string                          { return f.name }
func (f *fakeAdapter) Priority() int                         { return f.priority }
func (f *fakeAdapter) Import(_ string) ([]Definition, error) { return nil, nil }
func (f *fakeAdapter) PopulateSandbox(_ string, _ store.AgentProfile, _ SandboxContext) error {
	return nil
}
func (f *fakeAdapter) SyncProjectRoot(_ string, _ []store.AgentProfile) error {
	if f.synced != nil {
		*f.synced = true
	}
	return nil
}

func TestSyncAllProjectRootsFiltered_OnlyAllowedRun(t *testing.T) {
	var claudeRan, codexRan, geminiRan, nativeRan bool
	reg := NewAdapterRegistry()
	reg.Register(&fakeAdapter{name: "claude", priority: 60, synced: &claudeRan})
	reg.Register(&fakeAdapter{name: "codex", priority: 70, synced: &codexRan})
	reg.Register(&fakeAdapter{name: "gemini", priority: 80, synced: &geminiRan})
	reg.Register(&fakeAdapter{name: "nanite-native", priority: 10, synced: &nativeRan})

	if err := reg.SyncAllProjectRootsFiltered("/tmp/x", nil, []string{"claude"}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !claudeRan {
		t.Error("claude should have run")
	}
	if codexRan {
		t.Error("codex should NOT have run")
	}
	if geminiRan {
		t.Error("gemini should NOT have run")
	}
	if !nativeRan {
		t.Error("nanite-native should always run")
	}
}

func TestSyncAllProjectRootsFiltered_EmptyAllowedListStillRunsNative(t *testing.T) {
	var claudeRan, nativeRan bool
	reg := NewAdapterRegistry()
	reg.Register(&fakeAdapter{name: "claude", priority: 60, synced: &claudeRan})
	reg.Register(&fakeAdapter{name: "nanite-native", priority: 10, synced: &nativeRan})

	if err := reg.SyncAllProjectRootsFiltered("/tmp/x", nil, []string{}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if claudeRan {
		t.Error("claude should NOT have run with empty allowed list")
	}
	if !nativeRan {
		t.Error("nanite-native should always run, even with empty allowed list")
	}
}
