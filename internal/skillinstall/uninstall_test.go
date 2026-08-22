package skillinstall

// uninstall_test.go — TASKS/skills/12's own regression suite for
// Uninstaller, the shared logic behind both DELETE /api/skills/{slug}
// (internal/api/skills.go) and the skill_delete self-tool (internal/
// selftools/self_tools_transport.go). newTestInstaller/copyFixture (this
// package's own install_test.go) are reused as-is for setup.

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestUninstall_RemovesVendorAndIndex(t *testing.T) {
	inst, vendor, idx := newTestInstaller(t)

	result, err := inst.Install(context.Background(), Source{Path: filepath.Join(fixturesDir, "sample-skill")})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	u := &Uninstaller{Vendor: vendor, Index: idx}
	uresult, err := u.Uninstall(&result.Skill)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if !uresult.VendorDeleted {
		t.Error("expected VendorDeleted = true for a skill with a real vendored copy")
	}
	if uresult.Skill.Slug != "sample-skill" {
		t.Errorf("Skill.Slug = %q, want %q", uresult.Skill.Slug, "sample-skill")
	}

	// Index row is gone.
	sk, err := idx.GetSkillBySlug("sample-skill")
	if err != nil {
		t.Fatalf("GetSkillBySlug: %v", err)
	}
	if sk != nil {
		t.Error("expected the index row to be removed")
	}

	// Vendored copy is gone (ReadFiles on a deleted address fails).
	if _, err := vendor.ReadFiles(result.Address); err == nil {
		t.Error("expected vendor.ReadFiles to fail for a deleted address")
	}
}

func TestUninstall_SkipsVendorDeletion_WhenContentHashEmpty(t *testing.T) {
	_, vendor, idx := newTestInstaller(t)

	// A bare admin-CRUD row (task 02's POST /api/skills) never went through
	// install/sync — ContentHash is empty, nothing vendored to delete.
	sk := &store.Skill{Name: "Bare Row", Slug: "bare-row", Description: "no content"}
	if err := idx.CreateSkill(sk); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}

	u := &Uninstaller{Vendor: vendor, Index: idx}
	result, err := u.Uninstall(sk)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if result.VendorDeleted {
		t.Error("expected VendorDeleted = false for a skill with no vendored content")
	}

	got, err := idx.GetSkillBySlug("bare-row")
	if err != nil {
		t.Fatalf("GetSkillBySlug: %v", err)
	}
	if got != nil {
		t.Error("expected the index row to be removed")
	}
}

func TestUninstall_NilSkill_ClearError(t *testing.T) {
	_, vendor, idx := newTestInstaller(t)
	u := &Uninstaller{Vendor: vendor, Index: idx}
	if _, err := u.Uninstall(nil); err == nil {
		t.Fatal("expected an error for a nil skill")
	}
}

func TestUninstall_ContentHashSetButVendorNil_ClearErrorNotPanic(t *testing.T) {
	_, _, idx := newTestInstaller(t)
	sk := &store.Skill{Name: "Ghost Vendor", Slug: "ghost-vendor", ContentHash: "skl-vendor-0000000000000000"}
	if err := idx.CreateSkill(sk); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}

	u := &Uninstaller{Vendor: nil, Index: idx}
	if _, err := u.Uninstall(sk); err == nil {
		t.Fatal("expected a clear error when Vendor is nil but the skill has a ContentHash")
	}

	// The index row must be left intact — a failed vendor-deletion attempt
	// aborts the whole call rather than deleting the index row anyway.
	got, err := idx.GetSkillBySlug("ghost-vendor")
	if err != nil {
		t.Fatalf("GetSkillBySlug: %v", err)
	}
	if got == nil {
		t.Error("expected the index row to still exist after an aborted uninstall")
	}
}

// fakeFailingVendor forces a Delete failure to confirm Uninstall aborts
// before ever touching the index row.
type fakeFailingVendor struct{}

func (fakeFailingVendor) Delete(string) error { return errors.New("boom") }

func TestUninstall_VendorDeleteFailure_AbortsBeforeIndexDelete(t *testing.T) {
	_, _, idx := newTestInstaller(t)
	sk := &store.Skill{Name: "Stubborn Vendor", Slug: "stubborn-vendor", ContentHash: "skl-vendor-0000000000000001"}
	if err := idx.CreateSkill(sk); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}

	u := &Uninstaller{Vendor: fakeFailingVendor{}, Index: idx}
	if _, err := u.Uninstall(sk); err == nil {
		t.Fatal("expected the vendor deletion failure to propagate")
	}

	got, err := idx.GetSkillBySlug("stubborn-vendor")
	if err != nil {
		t.Fatalf("GetSkillBySlug: %v", err)
	}
	if got == nil {
		t.Error("expected the index row to still exist after a failed vendor deletion")
	}
}
