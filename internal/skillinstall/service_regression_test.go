package skillinstall

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
)

// TestInstall_ResultRoundTripsThroughSkillService proves an installed package
// receives a durable database identity and goes one layer
// above the direct store.Store assertions the rest of this package's tests
// make: install a real package through Installer.Install, then round-trip
// the resulting row through service.SkillService.Update and .Delete — the
// actual live REST-endpoint-facing service layer (internal/service/skill.go,
// wired to PUT/DELETE /api/skills/{id}) — against the *same* underlying
// *store.Store the Installer's Index field used, so this exercises the same
// rows the pipeline actually created, not two disconnected stores.
func TestInstall_ResultRoundTripsThroughSkillService(t *testing.T) {
	inst, _, idx := newTestInstaller(t)

	result, err := inst.Install(context.Background(), Source{Path: filepath.Join(fixturesDir, "sample-skill")})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	if result.Skill.ID == "" {
		t.Fatal("installed skill did not receive a database identity")
	}

	svc := service.NewSkillService(service.SkillServiceConfig{Skills: idx})

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
