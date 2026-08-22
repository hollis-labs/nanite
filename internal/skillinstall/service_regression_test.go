package skillinstall

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/skill"
)

// TestInstall_ResultRoundTripsThroughSkillService is the regression test for
// the "installed skills got a permanent file-<slug> ID" bug (fresh reviewer,
// 2026-08-21, TASKS/skills/04's "Fix required" section). It goes one layer
// above the direct store.Store assertions the rest of this package's tests
// make: install a real package through Installer.Install, then round-trip
// the resulting row through service.SkillService.Update and .Delete — the
// actual live REST-endpoint-facing service layer (internal/service/skill.go,
// wired to PUT/DELETE /api/skills/{id}) — against the *same* underlying
// *store.Store the Installer's Index field used, so this exercises the same
// rows the pipeline actually created, not two disconnected stores.
//
// Before the fix, def.ToStoreSkill()'s unconditional "file-<slug>" ID was
// passed straight into a real CreateSkill call, and skill.IsFileBasedID
// (skillServiceImpl.Update/Delete's guard, reserved for the old pre-redesign
// virtual/ephemeral file-based rows) matched it — so both calls hard-rejected
// every installed skill with "cannot update/delete file-based skill ... edit/
// remove the .md file instead". This test fails on that rejection if the bug
// regresses.
func TestInstall_ResultRoundTripsThroughSkillService(t *testing.T) {
	inst, _, idx := newTestInstaller(t)

	result, err := inst.Install(context.Background(), Source{Path: filepath.Join(fixturesDir, "sample-skill")})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	if skill.IsFileBasedID(result.Skill.ID) {
		t.Fatalf("installed skill got a file-based sentinel ID: %q — should be a real UUID", result.Skill.ID)
	}

	svc := service.NewSkillService(service.SkillServiceConfig{Skills: idx, FileSkills: nil})

	installed, err := svc.Get(context.Background(), result.Skill.ID)
	if err != nil {
		t.Fatalf("SkillService.Get: %v", err)
	}
	if installed == nil {
		t.Fatal("SkillService.Get returned nil for the just-installed skill")
	}

	installed.Description = "updated via SkillService round-trip"
	if err := svc.Update(context.Background(), installed); err != nil {
		t.Fatalf("SkillService.Update rejected an installed skill: %v", err)
	}

	if err := svc.Delete(context.Background(), installed.ID); err != nil {
		t.Fatalf("SkillService.Delete rejected an installed skill: %v", err)
	}

	// Confirm the delete actually took, at the same store the Installer used.
	sk, err := idx.GetSkillBySlug(context.Background(), "sample-skill")
	if err != nil {
		t.Fatalf("GetSkillBySlug after delete: %v", err)
	}
	if sk != nil {
		t.Error("expected the skill row to be gone after SkillService.Delete")
	}
}
