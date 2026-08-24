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
	"github.com/hollis-labs/nanite/internal/storetest"
)

func newConfigTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cfg.db")
	st, err := storetest.New(t, context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close(context.Background()) })
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
	if err := st.CreateAgent(context.Background(), existing); err != nil {
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

// TestAgentConfig_Create_RejectsSlugTraversal is GO-AGENT-001's Create-path
// regression: a crafted slug must be rejected before any filesystem call,
// not silently joined into a path outside the managed root.
func TestAgentConfig_Create_RejectsSlugTraversal(t *testing.T) {
	st := newConfigTestStore(t)
	root := t.TempDir()
	svc := NewAgentConfigService(st, agent.NewClassification(root, ""), root, nil)

	for _, malicious := range []string{
		"../evil",
		"../../etc/passwd",
		"a/b",
		"UPPER",
		"with space",
	} {
		if _, err := svc.Create(&store.AgentProfile{Name: "Evil", Slug: malicious, SystemPrompt: "x"}, nil); err == nil {
			t.Errorf("slug %q: expected rejection, got nil error", malicious)
		}
	}
	// Nothing escaped the managed root (one level above configRoot/agents,
	// and configRoot itself).
	if _, err := os.Stat(filepath.Join(root, "evil.md")); !os.IsNotExist(err) {
		t.Fatalf("traversal wrote inside configRoot but outside agents/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil.md")); !os.IsNotExist(err) {
		t.Fatalf("traversal wrote above configRoot: %v", err)
	}
}

// TestAgentConfig_Update_DBOnlyMaterialize_RejectsSlugTraversal covers the
// DB-only-materialize branch (existing.SourceRef == "", the un-guarded
// sibling of Create GO-AGENT-001 flags as worse than Create) — it routes
// through the same agent.ManagedAgentPath as Create, so the fix inside that
// function is the backstop for this branch too.
func TestAgentConfig_Update_DBOnlyMaterialize_RejectsSlugTraversal(t *testing.T) {
	st := newConfigTestStore(t)
	root := t.TempDir()
	classification := agent.NewClassification(root, "")
	svc := NewAgentConfigService(st, classification, root, nil)

	// A DB-only operator agent: no on-disk file yet (SourceRef == "").
	existing := &store.AgentProfile{Name: "Ghost", Slug: "ghost", SystemPrompt: "x", Source: "user"}
	if err := st.CreateAgent(context.Background(), existing); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if existing.SourceRef != "" {
		t.Fatalf("precondition: expected no SourceRef, got %q", existing.SourceRef)
	}

	for _, malicious := range []string{"../evil", "../../etc/passwd", "a/b"} {
		updated := *existing
		updated.Slug = malicious
		if _, err := svc.Update(existing, &updated, nil, ""); err == nil {
			t.Errorf("slug %q: expected rejection on DB-only-materialize branch", malicious)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil.md")); !os.IsNotExist(err) {
		t.Fatalf("traversal wrote above configRoot: %v", err)
	}
}

// TestAgentConfig_Update_RenameBranch_RejectsSlugTraversal covers the
// second, worse gap GO-AGENT-001 calls out by name: the rename branch
// (existing.SourceRef != "") bypasses agent.ManagedAgentPath entirely and
// does its own raw join — it needs its own explicit validator +
// pathsafe.ResolveUnder call, which this pins.
func TestAgentConfig_Update_RenameBranch_RejectsSlugTraversal(t *testing.T) {
	st := newConfigTestStore(t)
	root := t.TempDir()
	classification := agent.NewClassification(root, "")
	svc := NewAgentConfigService(st, classification, root, nil)

	created, err := svc.Create(&store.AgentProfile{Name: "Atlas", Slug: "atlas", SystemPrompt: "x"}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	for _, malicious := range []string{"../evil", "../../etc/evil", "a/b", "UPPER"} {
		updated := *created.Profile
		updated.Slug = malicious
		if _, err := svc.Update(created.Profile, &updated, nil, created.Revision); err == nil {
			t.Errorf("slug %q: expected rejection on rename branch", malicious)
		}
	}
	// The original file must survive every rejected rename attempt, and
	// nothing must have escaped agents/ into configRoot.
	if _, err := os.Stat(created.Profile.SourceRef); err != nil {
		t.Fatalf("original managed file missing after rejected rename attempts: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "evil.md")); !os.IsNotExist(err) {
		t.Fatalf("traversal escaped agents/ into configRoot: %v", err)
	}
}

// TestAgentConfig_Update_RenameBranch_SameSlugNoOp is a correctness
// regression for the fix's own implementation: an update that does not
// change the slug must not be misclassified as a rename (which would
// delete the file this very call just wrote — see agent_config.go's
// "No rename" comment on the branch this pins).
func TestAgentConfig_Update_RenameBranch_SameSlugNoOp(t *testing.T) {
	st := newConfigTestStore(t)
	root := t.TempDir()
	classification := agent.NewClassification(root, "")
	svc := NewAgentConfigService(st, classification, root, nil)

	created, err := svc.Create(&store.AgentProfile{Name: "Atlas", Slug: "atlas", SystemPrompt: "x"}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated := *created.Profile
	updated.Description = "same slug, different field"
	res, err := svc.Update(created.Profile, &updated, nil, created.Revision)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if res.Profile.SourceRef != created.Profile.SourceRef {
		t.Fatalf("SourceRef changed on a non-rename edit: %q -> %q", created.Profile.SourceRef, res.Profile.SourceRef)
	}
	if _, err := os.Stat(created.Profile.SourceRef); err != nil {
		t.Fatalf("file must still exist after a same-slug edit: %v", err)
	}
	if got, err := st.GetAgentBySlug(context.Background(), "atlas"); err != nil || got.Description != "same slug, different field" {
		t.Fatalf("edit did not persist: %+v err=%v", got, err)
	}
}

// TestAgentConfig_Update_RenameBranch_SameSlugNoOp_LegacySourceRef is a
// stricter variant of TestAgentConfig_Update_RenameBranch_SameSlugNoOp that
// actually pins the same-slug short-circuit against the most realistic
// failure mode: editing an agent whose managed file predates this fix.
//
// The sibling test above seeds SourceRef via svc.Create(), which already
// routes the path through pathsafe.ResolveUnder — so re-resolving it a
// second time is idempotent and does not exercise the hazard the
// short-circuit exists to prevent. Every real, currently-deployed managed
// agent row was created before this fix shipped, i.e. its persisted
// SourceRef is a plain filepath.Join result (agent.ManagedAgentPath's old
// shape), never ResolveUnder'd. This test seeds SourceRef exactly that way:
// filepath.Join(managedRoot, "agents", slug+".md") through a directory that
// itself contains a symlink component (a stand-in for any real-world case
// where the managed root is reached through a symlink — macOS
// /var -> /private/var, a symlinked project checkout, a bind mount — the
// same shape ResolveUnder's own doc comment calls out as the reason it
// normalizes symlinks). ResolveUnder canonicalizes that symlink; a raw
// filepath.Join does not — so the two disagree on the exact string for the
// very same on-disk file, which is precisely the mismatch the short-circuit
// guards against.
func TestAgentConfig_Update_RenameBranch_SameSlugNoOp_LegacySourceRef(t *testing.T) {
	st := newConfigTestStore(t)

	realRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(realRoot, "agents"), 0o755); err != nil {
		t.Fatalf("mkdir real agents dir: %v", err)
	}
	// aliasRoot is a symlink to realRoot. A plain filepath.Join through
	// aliasRoot never gets canonicalized; pathsafe.ResolveUnder always does.
	aliasRoot := filepath.Join(t.TempDir(), "legacy-alias")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Fatalf("symlink alias: %v", err)
	}
	legacyAgentsDir := filepath.Join(aliasRoot, "agents")

	// Seed the DB row the way a real, already-ingested managed agent looks.
	seed := &store.AgentProfile{Name: "Atlas", Slug: "atlas", SystemPrompt: "x", Source: "user"}
	if err := st.CreateAgent(context.Background(), seed); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	legacyPath := writeRawAgentFile(t, legacyAgentsDir, "atlas",
		"---\nid: "+seed.ID+"\nname: Atlas\nslug: atlas\n---\nlegacy body\n")
	// This is the "pre-fix" / "already-deployed" shape: a plain
	// filepath.Join result, never run through pathsafe.ResolveUnder.
	seed.SourceRef = legacyPath

	svc := NewAgentConfigService(st, agent.NewClassification(realRoot, ""), realRoot, nil)

	updated := *seed
	updated.Description = "same slug, legacy sourceref"
	if _, err := svc.Update(seed, &updated, nil, ""); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy-style managed file must survive a same-slug edit: %v", err)
	}
	if got, err := st.GetAgentBySlug(context.Background(), "atlas"); err != nil || got.Description != "same slug, legacy sourceref" {
		t.Fatalf("edit did not persist: %+v err=%v", got, err)
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
	if !errors.Is(err, ErrAgentRevisionConflict) {
		t.Fatalf("expected ErrAgentRevisionConflict, got %v", err)
	}
}
