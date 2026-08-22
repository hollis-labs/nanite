package skillinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// fakeGraphIndex is a narrow, in-memory IndexStore fake for testing
// checkDependencyGraph in isolation from a real *store.Store — lets
// these tests assert exact call counts (e.g. "zero dependencies means
// zero lookups") and construct graph shapes (transitive chains, a
// pre-existing unrelated cycle) that would be verbose to build through
// full Installer.Install calls.
type fakeGraphIndex struct {
	skills map[string]*store.Skill
	calls  int
}

func (f *fakeGraphIndex) GetSkillBySlug(ctx context.Context, slug string) (*store.Skill, error) {
	f.calls++
	return f.skills[slug], nil
}
func (f *fakeGraphIndex) CreateSkill(ctx context.Context, sk *store.Skill) error { return nil }
func (f *fakeGraphIndex) UpdateSkill(ctx context.Context, sk *store.Skill) error { return nil }

func (f *fakeGraphIndex) put(slug string, deps ...string) {
	if f.skills == nil {
		f.skills = map[string]*store.Skill{}
	}
	depsJSON := "[]"
	if len(deps) > 0 {
		b := "["
		for i, d := range deps {
			if i > 0 {
				b += ","
			}
			b += `"` + d + `"`
		}
		b += "]"
		depsJSON = b
	}
	f.skills[slug] = &store.Skill{Slug: slug, DeclaredDependencies: depsJSON}
}

func TestCheckDependencyGraph_NoDeps_NoIndexCalls(t *testing.T) {
	idx := &fakeGraphIndex{}
	if err := checkDependencyGraph(idx, "new-skill", nil, DefaultMaxDependencyDepth); err != nil {
		t.Fatalf("checkDependencyGraph: %v", err)
	}
	if idx.calls != 0 {
		t.Errorf("expected zero Index lookups for a dependency-free package, got %d", idx.calls)
	}
}

func TestCheckDependencyGraph_DanglingReference_NotAnError(t *testing.T) {
	idx := &fakeGraphIndex{} // "other-skill" is not installed anywhere.
	if err := checkDependencyGraph(idx, "new-skill", []string{"other-skill"}, DefaultMaxDependencyDepth); err != nil {
		t.Fatalf("checkDependencyGraph: unexpected error for a dangling (not-yet-installed) dependency reference: %v", err)
	}
}

func TestCheckDependencyGraph_DirectCycle(t *testing.T) {
	idx := &fakeGraphIndex{}
	idx.put("b", "a") // b already declares a dependency on a.

	err := checkDependencyGraph(idx, "a", []string{"b"}, DefaultMaxDependencyDepth)
	if err == nil {
		t.Fatal("expected a cycle error installing a -> b -> a")
	}
	var cerr *CycleError
	if !errors.As(err, &cerr) {
		t.Fatalf("expected *CycleError, got %T: %v", err, err)
	}
	if cerr.Slug != "a" {
		t.Errorf("Slug = %q, want %q", cerr.Slug, "a")
	}
	want := []string{"a", "b", "a"}
	if !reflect.DeepEqual(cerr.Cycle, want) {
		t.Errorf("Cycle = %v, want %v", cerr.Cycle, want)
	}
}

func TestCheckDependencyGraph_TransitiveCycle(t *testing.T) {
	idx := &fakeGraphIndex{}
	idx.put("b", "c")
	idx.put("c", "a") // c -> a, a doesn't exist yet — being installed now.

	err := checkDependencyGraph(idx, "a", []string{"b"}, DefaultMaxDependencyDepth)
	if err == nil {
		t.Fatal("expected a cycle error installing a -> b -> c -> a")
	}
	var cerr *CycleError
	if !errors.As(err, &cerr) {
		t.Fatalf("expected *CycleError, got %T: %v", err, err)
	}
	want := []string{"a", "b", "c", "a"}
	if !reflect.DeepEqual(cerr.Cycle, want) {
		t.Errorf("Cycle = %v, want %v", cerr.Cycle, want)
	}
}

func TestCheckDependencyGraph_WithinLimit_NoError(t *testing.T) {
	idx := &fakeGraphIndex{}
	idx.put("lvl1")
	idx.put("lvl2", "lvl1")
	idx.put("lvl3", "lvl2")
	idx.put("lvl4", "lvl3")
	idx.put("lvl5", "lvl4")

	// root -> lvl5 -> lvl4 -> lvl3 -> lvl2 -> lvl1 is exactly 5 levels
	// deep from root — within DefaultMaxDependencyDepth (5).
	if err := checkDependencyGraph(idx, "root", []string{"lvl5"}, DefaultMaxDependencyDepth); err != nil {
		t.Fatalf("checkDependencyGraph: unexpected error at exactly the depth limit: %v", err)
	}
}

func TestCheckDependencyGraph_RecursionLimitExceeded(t *testing.T) {
	idx := &fakeGraphIndex{}
	idx.put("lvl1")
	idx.put("lvl2", "lvl1")
	idx.put("lvl3", "lvl2")
	idx.put("lvl4", "lvl3")
	idx.put("lvl5", "lvl4")
	idx.put("lvl6", "lvl5")

	// root -> lvl6 -> lvl5 -> lvl4 -> lvl3 -> lvl2 -> lvl1 is 6 levels
	// deep from root — one past DefaultMaxDependencyDepth (5), and not
	// a cycle.
	err := checkDependencyGraph(idx, "root", []string{"lvl6"}, DefaultMaxDependencyDepth)
	if err == nil {
		t.Fatal("expected a recursion-limit error for a 6-level-deep, acyclic composition")
	}
	var rerr *RecursionLimitError
	if !errors.As(err, &rerr) {
		t.Fatalf("expected *RecursionLimitError, got %T: %v", err, err)
	}
	if rerr.Slug != "root" {
		t.Errorf("Slug = %q, want %q", rerr.Slug, "root")
	}
	if rerr.Limit != DefaultMaxDependencyDepth {
		t.Errorf("Limit = %d, want %d", rerr.Limit, DefaultMaxDependencyDepth)
	}
	want := []string{"root", "lvl6", "lvl5", "lvl4", "lvl3", "lvl2", "lvl1"}
	if !reflect.DeepEqual(rerr.Path, want) {
		t.Errorf("Path = %v, want %v", rerr.Path, want)
	}
}

func TestCheckDependencyGraph_PreExistingUnrelatedCycle_DoesNotInfiniteLoop(t *testing.T) {
	idx := &fakeGraphIndex{}
	// b and c already form a cycle with each other, unrelated to "a" —
	// defensive coverage: this shouldn't be possible going forward (this
	// very check prevents it), but a graph edited outside the install
	// pipeline (or a bug in an earlier version of this check) must not
	// make a later, unrelated install hang.
	idx.put("b", "c")
	idx.put("c", "b")

	done := make(chan error, 1)
	go func() { done <- checkDependencyGraph(idx, "a", []string{"b"}, DefaultMaxDependencyDepth) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("checkDependencyGraph: unexpected error for a pre-existing cycle unrelated to the package being installed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("checkDependencyGraph did not return — likely stuck in an infinite loop on a pre-existing cycle")
	}
}

// writeMinimalPackage writes a bare-minimum, valid SKILL.md (name,
// slug, description, optional dependencies:) into dir — enough to pass
// DefaultValidator so these end-to-end tests exercise the real
// Installer.Install pipeline (parse -> validate -> [this task's own
// dependency-graph check] -> vendor -> index), not just
// checkDependencyGraph in isolation.
func writeMinimalPackage(t *testing.T, dir, slug string, deps []string) {
	t.Helper()
	fm := "---\n" +
		"name: " + slug + "\n" +
		"slug: " + slug + "\n" +
		"description: minimal package for a dependency-graph end-to-end test.\n"
	if len(deps) > 0 {
		fm += "dependencies:\n"
		for _, d := range deps {
			fm += "  - " + d + "\n"
		}
	}
	fm += "---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(fm), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

// installMinimal writes and installs a minimal package for slug with
// the given declared dependencies, against a real Installer (real
// *skillvendor.Store + real *store.Store, per newTestInstaller).
func installMinimal(t *testing.T, inst *Installer, slug string, deps []string) {
	t.Helper()
	dir := t.TempDir()
	writeMinimalPackage(t, dir, slug, deps)
	if _, err := inst.Install(context.Background(), Source{Path: dir}); err != nil {
		t.Fatalf("install %q: %v", slug, err)
	}
}

func TestInstall_DependencyCycle_RejectedAtInstallTime(t *testing.T) {
	inst, vendor, idx := newTestInstaller(t)
	installMinimal(t, inst, "b", []string{"a"}) // dangling ref to not-yet-installed "a" — allowed.

	dir := t.TempDir()
	writeMinimalPackage(t, dir, "a", []string{"b"})
	_, err := inst.Install(context.Background(), Source{Path: dir})
	if err == nil {
		t.Fatal("expected the real Installer.Install to reject a -> b -> a as a cycle")
	}
	var cerr *CycleError
	if !errors.As(err, &cerr) {
		t.Fatalf("expected *CycleError, got %T: %v", err, err)
	}
	if inst.State() != StateFailed {
		t.Errorf("State() = %q, want %q", inst.State(), StateFailed)
	}

	// No partial state: "a" was never indexed, and nothing beyond "b"'s
	// own already-vendored content exists.
	sk, err := idx.GetSkillBySlug(context.Background(), "a")
	if err != nil {
		t.Fatalf("GetSkillBySlug: %v", err)
	}
	if sk != nil {
		t.Error("expected no index row for a package rejected for a dependency cycle")
	}
	entries, err := os.ReadDir(vendor.Root())
	if err != nil {
		t.Fatalf("read vendor root: %v", err)
	}
	nonStaging := 0
	for _, e := range entries {
		if e.Name() != ".staging" {
			nonStaging++
		}
	}
	if nonStaging != 1 { // only "b"'s own vendored copy.
		t.Errorf("expected exactly 1 vendored entry (b's), got %d", nonStaging)
	}
}

func TestInstall_RecursionLimitExceeded_RejectedAtInstallTime(t *testing.T) {
	inst, _, idx := newTestInstaller(t)
	installMinimal(t, inst, "lvl1", nil)
	installMinimal(t, inst, "lvl2", []string{"lvl1"})
	installMinimal(t, inst, "lvl3", []string{"lvl2"})
	installMinimal(t, inst, "lvl4", []string{"lvl3"})
	installMinimal(t, inst, "lvl5", []string{"lvl4"})
	installMinimal(t, inst, "lvl6", []string{"lvl5"})

	dir := t.TempDir()
	writeMinimalPackage(t, dir, "root", []string{"lvl6"})
	_, err := inst.Install(context.Background(), Source{Path: dir})
	if err == nil {
		t.Fatal("expected the real Installer.Install to reject a 6-level-deep, acyclic composition")
	}
	var rerr *RecursionLimitError
	if !errors.As(err, &rerr) {
		t.Fatalf("expected *RecursionLimitError, got %T: %v", err, err)
	}

	sk, err := idx.GetSkillBySlug(context.Background(), "root")
	if err != nil {
		t.Fatalf("GetSkillBySlug: %v", err)
	}
	if sk != nil {
		t.Error("expected no index row for a package rejected for exceeding the recursion depth limit")
	}
}
