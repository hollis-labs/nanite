package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

func newConfigTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cfg.db")
	st, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func writeRawAgentFile(t *testing.T, dir, slug, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, slug+".md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// TestReconcileManagedAgentIDs_StampsAndIsIdempotent verifies the boot pass
// mints a UUID for an unstamped writable managed file, writes it back into the
// frontmatter, and that re-running maps to the same identity (idempotent).
func TestReconcileManagedAgentIDs_StampsAndIsIdempotent(t *testing.T) {
	st := newConfigTestStore(t)
	root := t.TempDir()
	agentsDir := filepath.Join(root, "agents")
	path := writeRawAgentFile(t, agentsDir, "atlas", "---\nname: Atlas\nslug: atlas\n---\nYou curate.\n")

	classification := agent.NewClassification(root, "")
	def, err := agent.ParseMDFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	def.Source = "project"
	if def.ID != "" {
		t.Fatalf("precondition: file should be unstamped, got id %q", def.ID)
	}

	ReconcileManagedAgentIDs(st, []*agent.Definition{def}, classification)
	if def.ID == "" {
		t.Fatalf("expected a minted UUID, got %q", def.ID)
	}
	stamped := def.ID

	// File now carries the id.
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "id: "+stamped) {
		t.Fatalf("file not stamped:\n%s", raw)
	}

	// Re-parse + reconcile again → same identity, no churn.
	def2, _ := agent.ParseMDFile(path)
	def2.Source = "project"
	if def2.ID != stamped {
		t.Fatalf("re-parse id = %q, want %q", def2.ID, stamped)
	}
	ReconcileManagedAgentIDs(st, []*agent.Definition{def2}, classification)
	if def2.ID != stamped {
		t.Fatalf("reconcile not idempotent: %q != %q", def2.ID, stamped)
	}
}

// TestReconcileManagedAgentIDs_AdoptsExistingProjection verifies that an
// unstamped file whose slug already has a DB row adopts that row's UUID
// (no orphaned children, no migration needed).
func TestReconcileManagedAgentIDs_AdoptsExistingProjection(t *testing.T) {
	st := newConfigTestStore(t)
	root := t.TempDir()
	agentsDir := filepath.Join(root, "agents")

	existing := &store.AgentProfile{Name: "Atlas", Slug: "atlas", SystemPrompt: "x", Source: "user"}
	if err := st.CreateAgent(existing); err != nil {
		t.Fatalf("seed existing: %v", err)
	}
	path := writeRawAgentFile(t, agentsDir, "atlas", "---\nname: Atlas\nslug: atlas\n---\nYou curate.\n")
	def, _ := agent.ParseMDFile(path)
	def.Source = "project"

	ReconcileManagedAgentIDs(st, []*agent.Definition{def}, agent.NewClassification(root, ""))
	if def.ID != existing.ID {
		t.Fatalf("adopt failed: def.ID = %q, want existing %q", def.ID, existing.ID)
	}
}

// TestReconcileManagedAgentIDs_SkipsInternal verifies embedded internal
// definitions are never written back (they can't be — go:embed).
func TestReconcileManagedAgentIDs_SkipsInternal(t *testing.T) {
	st := newConfigTestStore(t)
	def := &agent.Definition{Slug: "default", Name: "Chat", Source: "internal", SourceRef: "embedded:profiles/default.md"}
	ReconcileManagedAgentIDs(st, []*agent.Definition{def}, agent.NewClassification(t.TempDir(), ""))
	if def.ID != "" {
		t.Fatalf("internal def should stay unstamped, got %q", def.ID)
	}
}

// TestAgentConfig_SlugRenamePreservesIdentityAndChildren verifies a slug
// rename keeps the same UUID (so FK children survive) and moves the file.
func TestAgentConfig_SlugRenamePreservesIdentityAndChildren(t *testing.T) {
	st := newConfigTestStore(t)
	root := t.TempDir()
	classification := agent.NewClassification(root, "")
	svc := NewAgentConfigService(st, classification, root, nil)

	created, err := svc.Create(&store.AgentProfile{Name: "Atlas", Slug: "atlas", SystemPrompt: "x"}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.Profile.ID
	oldPath := created.Profile.SourceRef

	// Attach an FK child (reflex) to the agent.
	if _, err := st.InsertAgentReflex(context.Background(), store.AgentReflex{
		AgentID:     id,
		Name:        "ground",
		TriggerKind: store.ReflexTriggerPredicate,
		TriggerSpec: `{"kind":"tool_calls_window","window":2,"op":"=","value":0}`,
		ActionKind:  store.ReflexActionInjectReminder,
		ActionSpec:  `{"body":"ground"}`,
	}); err != nil {
		t.Fatalf("insert reflex: %v", err)
	}

	// Rename via update (slug atlas -> atlas-curator).
	updated := *created.Profile
	updated.Slug = "atlas-curator"
	res, err := svc.Update(created.Profile, &updated, nil, created.Revision)
	if err != nil {
		t.Fatalf("rename update: %v", err)
	}
	if res.Profile.ID != id {
		t.Fatalf("identity changed on rename: %q -> %q", id, res.Profile.ID)
	}
	if res.Profile.Slug != "atlas-curator" {
		t.Fatalf("slug not applied: %q", res.Profile.Slug)
	}
	// Old file gone, new file present.
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old file not removed on rename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "atlas-curator.md")); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}
	// FK child still resolves under the same id.
	reflexes, err := st.ListAgentReflexesForAgent(context.Background(), id, "")
	if err != nil || len(reflexes) != 1 {
		t.Fatalf("reflex child lost on rename: %d err=%v", len(reflexes), err)
	}
}

// TestAgentConfig_CopyToManaged_NotIngestedSurfacesDistinctError is the
// silent-failure-visibility half of CW-20260815-0009: a profile that
// classifies as an already-managed config (Classify().Editable()) but has no
// backing agent_profiles row — i.e. AutoIngestAgents failed for its source
// file — must report ErrAgentNotIngested, not the misleading
// ErrAgentAlreadyManaged that made this bug look like a false success.
//
// No SourceRef + a non-internal/non-plugin Source classifies as
// ManageClassManaged per source_class.go's "DB-only operator agent" branch,
// the same as it would for a real project file whose parse succeeded but
// whose DB upsert didn't — so this stands in for that scenario without
// needing a real file on disk.
func TestAgentConfig_CopyToManaged_NotIngestedSurfacesDistinctError(t *testing.T) {
	st := newConfigTestStore(t)
	root := t.TempDir()
	classification := agent.NewClassification(root, "")
	svc := NewAgentConfigService(st, classification, root, nil)

	ghost := &store.AgentProfile{
		ID:     "11111111-1111-1111-1111-111111111111",
		Slug:   "ghost-agent",
		Source: "project",
		Name:   "Ghost",
	}

	_, err := svc.CopyToManaged(ghost, nil)
	if !errors.Is(err, ErrAgentNotIngested) {
		t.Fatalf("expected ErrAgentNotIngested, got %v", err)
	}
	if errors.Is(err, ErrAgentAlreadyManaged) {
		t.Fatal("must not report ErrAgentAlreadyManaged for an unpersisted profile — that's the false-success bug")
	}
}

// TestAgentConfig_CopyToManaged_AlreadyManagedWhenPersisted is the control
// case: a profile that's genuinely persisted (real agent_profiles row) still
// gets the ordinary ErrAgentAlreadyManaged, unaffected by the Persisted gate.
func TestAgentConfig_CopyToManaged_AlreadyManagedWhenPersisted(t *testing.T) {
	st := newConfigTestStore(t)
	root := t.TempDir()
	classification := agent.NewClassification(root, "")
	svc := NewAgentConfigService(st, classification, root, nil)

	created, err := svc.Create(&store.AgentProfile{Name: "Atlas", Slug: "atlas", SystemPrompt: "x"}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = svc.CopyToManaged(created.Profile, nil)
	if !errors.Is(err, ErrAgentAlreadyManaged) {
		t.Fatalf("expected ErrAgentAlreadyManaged for a genuinely persisted managed profile, got %v", err)
	}
}

// TestAgentConfig_RevisionConflict verifies the optimistic-concurrency guard.
func TestAgentConfig_RevisionConflict(t *testing.T) {
	st := newConfigTestStore(t)
	root := t.TempDir()
	svc := NewAgentConfigService(st, agent.NewClassification(root, ""), root, nil)

	created, err := svc.Create(&store.AgentProfile{Name: "Atlas", Slug: "atlas", SystemPrompt: "x"}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Simulate an out-of-band file change after the client loaded its revision.
	if err := os.WriteFile(created.Profile.SourceRef, []byte("---\nid: "+created.Profile.ID+"\nname: Atlas\nslug: atlas\n---\nhand edit\n"), 0o644); err != nil {
		t.Fatalf("hand edit: %v", err)
	}
	updated := *created.Profile
	updated.Description = "gui edit"
	_, err = svc.Update(created.Profile, &updated, nil, created.Revision)
	if err != ErrAgentRevisionConflict {
		t.Fatalf("expected ErrAgentRevisionConflict, got %v", err)
	}
}
