package mcp

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// newAutoDiscoverTestStore opens a scratch SQLite DB for AutoDiscover
// skills-table verification. Isolated per-test via t.TempDir() — never a
// real tracked path.
func newAutoDiscoverTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestAutoDiscover_NeverWritesSkillsTable is TASKS/skills/01's "Done means"
// verification: mcp.Manager.AutoDiscover's writes into the skills table
// (one row per newly visible tool, category="auto-discovered", plus a
// "removed":true flag for tools that disappear) are cut in full per
// docs/engineering/architecture/20-skills.md's "Scope: skills are authored
// packages only" section ("mcp.Manager.AutoDiscover's writes into the
// skills table stop entirely."). ListSkills() must return exactly what the
// test explicitly seeded — nothing added, nothing altered — after a fresh
// AutoDiscover run against a scratch DB with tools present.
func TestAutoDiscover_NeverWritesSkillsTable(t *testing.T) {
	st := newAutoDiscoverTestStore(t)

	// Seed one pre-existing skills row directly (mirroring what a real DB
	// might already carry from before this cut landed) so the test also
	// proves AutoDiscover doesn't mutate an existing auto-discovered row,
	// not just that it skips creating new ones.
	preexisting := &store.Skill{
		Name:         "Old Tool",
		Slug:         "old-tool",
		Category:     "auto-discovered",
		ToolBindings: `["old_tool"]`,
		Settings:     `{"server":"srv","auto_discovered":true}`,
	}
	if err := st.CreateSkill(preexisting); err != nil {
		t.Fatalf("seed preexisting skill: %v", err)
	}

	before, err := st.ListSkills()
	if err != nil {
		t.Fatalf("ListSkills (before): %v", err)
	}

	mgr := NewManager()
	tools := []Tool{
		{Name: "brand_new_tool", Description: "a tool AutoDiscover has never seen before"},
		{Name: "another_new_tool", Description: "a second never-seen tool"},
	}
	if err := mgr.AddServer("srv", &fakeTieredTransport{tools: tools}, TierThirdPartyHTTP); err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	diff, err := mgr.AutoDiscover(context.Background(), st)
	if err != nil {
		t.Fatalf("AutoDiscover: %v", err)
	}

	// The diffing logic itself is unchanged — new tools still get reported
	// via diff.Added even though no row gets created for them.
	if len(diff.Added) != 2 {
		t.Errorf("diff.Added: got %v, want 2 entries", diff.Added)
	}

	after, err := st.ListSkills()
	if err != nil {
		t.Fatalf("ListSkills (after): %v", err)
	}

	if len(after) != len(before) {
		t.Fatalf("ListSkills row count changed: before=%d after=%d — AutoDiscover must never write to the skills table", len(before), len(after))
	}
	for i := range before {
		if before[i].UpdatedAt != after[i].UpdatedAt || before[i].Settings != after[i].Settings {
			t.Errorf("existing skill row %q was mutated by AutoDiscover: before=%+v after=%+v", before[i].Slug, before[i], after[i])
		}
	}

	// Explicitly confirm neither newly-discovered tool got a skill row.
	for _, slug := range []string{"brand-new-tool", "another-new-tool"} {
		sk, err := st.GetSkillBySlug(slug)
		if err != nil {
			t.Fatalf("GetSkillBySlug(%q): %v", slug, err)
		}
		if sk != nil {
			t.Errorf("AutoDiscover created a skill row for %q — writes into the skills table must stop entirely", slug)
		}
	}
}
