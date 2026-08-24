package skillinstall

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/skill"
	"github.com/hollis-labs/nanite/internal/skillvendor"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

const fixturesDir = "testdata/fixtures"

func newTestInstaller(t *testing.T) (*Installer, *skillvendor.Store, *store.Store) {
	t.Helper()

	vendorRoot := filepath.Join(t.TempDir(), "vendor")
	vendor, err := skillvendor.New(vendorRoot)
	if err != nil {
		t.Fatalf("skillvendor.New: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	idx, err := storetest.New(t, context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { idx.Close(context.Background()) })

	inst := &Installer{Vendor: vendor, Index: idx}
	return inst, vendor, idx
}

// copyFixture copies a testdata fixture directory into a fresh, mutable
// temp directory so a re-sync test can edit its content in place without
// touching the tracked fixture on disk.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join(fixturesDir, name)
	dst := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, p)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy fixture %s: %v", name, err)
	}
	return dst
}

func TestInstall_EndToEnd(t *testing.T) {
	inst, vendor, idx := newTestInstaller(t)

	result, err := inst.Install(context.Background(), Source{Path: filepath.Join(fixturesDir, "sample-skill")})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if result.Reused {
		t.Error("first install should not be Reused")
	}
	if result.Skill.Slug != "sample-skill" {
		t.Errorf("Slug = %q, want %q", result.Skill.Slug, "sample-skill")
	}
	if result.Skill.Version != 1 {
		t.Errorf("Version = %d, want 1", result.Skill.Version)
	}
	if result.Skill.ContentHash != result.Address {
		t.Errorf("ContentHash = %q, want %q", result.Skill.ContentHash, result.Address)
	}
	if result.Skill.DeclaredDependencies != `["other-skill"]` {
		t.Errorf("DeclaredDependencies = %q, want %q", result.Skill.DeclaredDependencies, `["other-skill"]`)
	}
	if !result.Skill.Enabled {
		t.Error("Enabled should be true")
	}
	if inst.State() != StateReady {
		t.Errorf("State() = %q, want %q", inst.State(), StateReady)
	}

	// Vendored copy is present and byte-correct.
	files, err := vendor.ReadFiles(result.Address)
	if err != nil {
		t.Fatalf("vendor.ReadFiles: %v", err)
	}
	for _, want := range []string{"SKILL.md", "scripts/run.sh", "references/notes.md", "assets/logo.txt"} {
		if _, ok := files[want]; !ok {
			t.Errorf("vendored package missing %q", want)
		}
	}

	// Index row is present and matches.
	sk, err := idx.GetSkillBySlug(context.Background(), "sample-skill")
	if err != nil {
		t.Fatalf("GetSkillBySlug: %v", err)
	}
	if sk == nil {
		t.Fatal("expected an indexed skill row, got nil")
	}
	if sk.ContentHash != result.Address {
		t.Errorf("indexed ContentHash = %q, want %q", sk.ContentHash, result.Address)
	}

	// InputSchema was built from the declared parameters (task 04's
	// convert.go extension), not left at the bare "{}" default.
	if sk.InputSchema == "{}" || sk.InputSchema == "" {
		t.Errorf("InputSchema should reflect declared parameters, got %q", sk.InputSchema)
	}
}

func TestInstall_MalformedPackage_MissingScript(t *testing.T) {
	inst, vendor, idx := newTestInstaller(t)

	_, err := inst.Install(context.Background(), Source{Path: filepath.Join(fixturesDir, "malformed-missing-script")})
	if err == nil {
		t.Fatal("expected an error for a scripts: entry pointing at a nonexistent file")
	}
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected a *ValidationError, got %T: %v", err, err)
	}
	if inst.State() != StateFailed {
		t.Errorf("State() = %q, want %q", inst.State(), StateFailed)
	}

	// No partial vendoring: nothing to look up an address for since
	// validation ran before any Vendor.Write, but assert the store's own
	// root has no published (non-staging) entries at all.
	entries, err := os.ReadDir(vendor.Root())
	if err != nil {
		t.Fatalf("read vendor root: %v", err)
	}
	for _, e := range entries {
		if e.Name() == ".staging" {
			continue
		}
		t.Errorf("unexpected vendored entry after a validation failure: %q", e.Name())
	}

	// No partial indexing.
	sk, err := idx.GetSkillBySlug(context.Background(), "malformed-missing-script")
	if err != nil {
		t.Fatalf("GetSkillBySlug: %v", err)
	}
	if sk != nil {
		t.Error("expected no index row for a package that failed validation")
	}
}

func TestInstall_MalformedFrontmatter_FailsCleanly(t *testing.T) {
	inst, _, idx := newTestInstaller(t)

	_, err := inst.Install(context.Background(), Source{Path: filepath.Join(fixturesDir, "malformed-frontmatter")})
	if err == nil {
		t.Fatal("expected an error for frontmatter missing the required name field")
	}

	sk, err := idx.GetSkillBySlug(context.Background(), "malformed-frontmatter")
	if err != nil {
		t.Fatalf("GetSkillBySlug: %v", err)
	}
	if sk != nil {
		t.Error("expected no index row for a package with malformed frontmatter")
	}
}

func TestInstall_Resync_IdenticalContent_IsIdempotent(t *testing.T) {
	inst, _, idx := newTestInstaller(t)
	dir := copyFixture(t, "sample-skill")

	first, err := inst.Install(context.Background(), Source{Path: dir})
	if err != nil {
		t.Fatalf("first Install: %v", err)
	}

	second, err := inst.Install(context.Background(), Source{Path: dir})
	if err != nil {
		t.Fatalf("second Install (resync): %v", err)
	}

	if !second.Reused {
		t.Error("resyncing unchanged content should report Reused=true")
	}
	if second.Address != first.Address {
		t.Errorf("Address changed on an unchanged resync: %q vs %q", second.Address, first.Address)
	}
	if second.Skill.Version != 1 {
		t.Errorf("Version should not bump on an unchanged resync, got %d", second.Skill.Version)
	}

	sk, err := idx.GetSkillBySlug(context.Background(), "sample-skill")
	if err != nil {
		t.Fatalf("GetSkillBySlug: %v", err)
	}
	if sk.Version != 1 {
		t.Errorf("indexed Version = %d, want 1", sk.Version)
	}
}

func TestInstall_Resync_ChangedContent_NewAddressAndVersionBump(t *testing.T) {
	inst, vendor, idx := newTestInstaller(t)
	dir := copyFixture(t, "sample-skill")

	first, err := inst.Install(context.Background(), Source{Path: dir})
	if err != nil {
		t.Fatalf("first Install: %v", err)
	}

	// Mutate the source package in place — same slug, same source path,
	// different content.
	notesPath := filepath.Join(dir, "references", "notes.md")
	if err := os.WriteFile(notesPath, []byte("# Notes\n\nUpdated content for the resync test.\n"), 0o644); err != nil {
		t.Fatalf("mutate fixture: %v", err)
	}

	second, err := inst.Install(context.Background(), Source{Path: dir})
	if err != nil {
		t.Fatalf("second Install (resync): %v", err)
	}

	if second.Reused {
		t.Error("resyncing genuinely changed content should not report Reused=true")
	}
	if second.Address == first.Address {
		t.Error("changed content should produce a new vendored address")
	}
	if second.Skill.Version != 2 {
		t.Errorf("Version = %d, want 2 after a real content change", second.Skill.Version)
	}
	if second.Skill.ID != first.Skill.ID {
		t.Errorf("resync should update the existing row, not create a new one: %q vs %q", second.Skill.ID, first.Skill.ID)
	}

	sk, err := idx.GetSkillBySlug(context.Background(), "sample-skill")
	if err != nil {
		t.Fatalf("GetSkillBySlug: %v", err)
	}
	if sk.ContentHash != second.Address {
		t.Errorf("indexed ContentHash = %q, want the new address %q", sk.ContentHash, second.Address)
	}

	// Immutability: the OLD vendored copy is still present and unmodified.
	oldFiles, err := vendor.ReadFiles(first.Address)
	if err != nil {
		t.Fatalf("old vendored address should still be readable after resync: %v", err)
	}
	if string(oldFiles["references/notes.md"]) != "# Notes\n\nReference material for the sample skill fixture.\n" {
		t.Errorf("old vendored content was mutated in place: %q", oldFiles["references/notes.md"])
	}
}

func TestInstall_RequiresExplicitSourcePath(t *testing.T) {
	inst, vendor, idx := newTestInstaller(t)

	_, err := inst.Install(context.Background(), Source{})
	if err == nil {
		t.Fatal("expected an error for an empty Source.Path — there is no default/discovered target")
	}

	entries, err := os.ReadDir(vendor.Root())
	if err != nil {
		t.Fatalf("read vendor root: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("empty-Source install should touch nothing on disk, found %d entries", len(entries))
	}

	skills, err := idx.ListSkills(context.Background())
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("empty-Source install should create no index rows, found %d", len(skills))
	}
}

// fakeVendor lets tests force a Vendorer failure/behavior without a real
// filesystem store.
type fakeVendor struct {
	writeResult skillvendor.WriteResult
	writeErr    error
	deleted     []string
}

func (f *fakeVendor) Write(_ context.Context, _ skillvendor.FileMap) (skillvendor.WriteResult, error) {
	return f.writeResult, f.writeErr
}

func (f *fakeVendor) Delete(address string) error {
	f.deleted = append(f.deleted, address)
	return nil
}

// fakeIndex lets tests force an IndexStore failure on the Indexing step.
type fakeIndex struct {
	getErr    error
	createErr error
}

func (f *fakeIndex) GetSkillBySlug(ctx context.Context, _ string) (*store.Skill, error) {
	return nil, f.getErr
}

func (f *fakeIndex) CreateSkill(ctx context.Context, _ *store.Skill) error {
	return f.createErr
}

func (f *fakeIndex) UpdateSkill(ctx context.Context, _ *store.Skill) error {
	return errors.New("fakeIndex: UpdateSkill not expected in this test")
}

func TestInstall_IndexFailureAfterFreshVendorWrite_RollsBackVendoredAddress(t *testing.T) {
	fv := &fakeVendor{writeResult: skillvendor.WriteResult{Address: "skl-vendor-0000000000000000", Reused: false}}
	fi := &fakeIndex{createErr: errors.New("boom")}
	inst := &Installer{Vendor: fv, Index: fi}

	_, err := inst.Install(context.Background(), Source{Path: filepath.Join(fixturesDir, "sample-skill")})
	if err == nil {
		t.Fatal("expected the Indexing step's CreateSkill error to propagate")
	}
	if len(fv.deleted) != 1 || fv.deleted[0] != fv.writeResult.Address {
		t.Errorf("expected a rollback Delete of the fresh vendor write, got %v", fv.deleted)
	}
}

func TestInstall_IndexFailureAfterReusedVendorWrite_DoesNotDelete(t *testing.T) {
	fv := &fakeVendor{writeResult: skillvendor.WriteResult{Address: "skl-vendor-0000000000000000", Reused: true}}
	fi := &fakeIndex{createErr: errors.New("boom")}
	inst := &Installer{Vendor: fv, Index: fi}

	_, err := inst.Install(context.Background(), Source{Path: filepath.Join(fixturesDir, "sample-skill")})
	if err == nil {
		t.Fatal("expected the Indexing step's CreateSkill error to propagate")
	}
	if len(fv.deleted) != 0 {
		t.Errorf("a Reused vendor write must never be rolled back, but Delete was called for %v", fv.deleted)
	}
}

// Sanity: skill.PackageFiles and skillvendor.FileMap really are
// convertible (both map[string][]byte) — a compile-time guard, not a
// runtime assertion, that would fail to build if either type's underlying
// shape ever drifted.
var _ = func(f skill.PackageFiles) skillvendor.FileMap { return skillvendor.FileMap(f) }
