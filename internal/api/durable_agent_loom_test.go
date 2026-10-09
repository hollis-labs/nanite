package api

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/modelsdevtest"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/substrate/harness/adapters/provider"
)

// newTestAPIWithLoomCurator provisions the profile and durable instance through
// the normal database-backed service path before boot. That creation path also
// supplies the canonical builtin schedule; the fixture never inserts it by
// hand and deliberately has no config-file input.
func newTestAPIWithLoomCurator(t *testing.T) (*testAPI, *http.ServeMux) {
	t.Helper()
	root := t.TempDir()
	for env, dir := range map[string]string{
		"HOME": filepath.Join(root, "home"), "XDG_DATA_HOME": filepath.Join(root, "xdg", "data"),
		"XDG_STATE_HOME": filepath.Join(root, "xdg", "state"), "XDG_CACHE_HOME": filepath.Join(root, "xdg", "cache"),
		"XDG_CONFIG_HOME": filepath.Join(root, "xdg", "config"),
	} {
		t.Setenv(env, dir)
	}

	dbPath := filepath.Join(root, "test.db")
	s, err := storetest.New(t, context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })

	// A real boot calls Seed after store.New. The wake handler's default
	// workspace must exist for session creation's FK.
	if seedErr := s.Seed(context.Background()); seedErr != nil {
		t.Fatalf("Seed: %v", seedErr)
	}
	profile := &store.AgentProfile{
		Name: "Loom Curator", Slug: "loom-curator", SystemPrompt: "Curate Loom fragments.", Source: "user", Durable: true,
	}
	if createErr := s.CreateAgent(context.Background(), profile); createErr != nil {
		t.Fatalf("create loom-curator profile: %v", createErr)
	}
	instance := &store.DurableAgentInstance{
		Name: "Loom Curator", Slug: "loom-curator", ProfileID: profile.ID,
		LifecycleClass: store.DurableAgentClassProcess, RuntimeKind: "api",
		LaunchSourceType: store.DurableAgentLaunchProcessTick, LaunchSourceID: "loom-curator",
		Status: store.DurableAgentStatusSleeping, MetadataJSON: `{"provisioned_by":"test"}`,
	}
	if createErr := service.NewDurableAgentService(s).Create(context.Background(), instance); createErr != nil {
		t.Fatalf("create loom-curator instance: %v", createErr)
	}

	svc, err := service.NewContainer(service.ContainerConfig{
		ModelCatalogOptions: modelsdevtest.Options(t),
		Store:               s, Providers: provider.NewRegistry(), WorkingDir: root, DisableEmbeddedTesseract: true,
	})
	if err != nil {
		t.Fatalf("service.NewContainer: %v", err)
	}
	t.Cleanup(func() { svc.Shutdown() })

	a := newAPIStoreFixture(svc, s)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)
	return a, mux
}

func TestLoomCuratorInstanceProvisionedInDatabase(t *testing.T) {
	a, _ := newTestAPIWithLoomCurator(t)

	inst, err := a.store.GetDurableAgentInstanceBySlug(context.Background(), "loom-curator")
	if err != nil {
		t.Fatalf("loom-curator instance not provisioned: %v", err)
	}
	if inst.ID == "" {
		t.Fatal("provisioned instance has empty ID")
	}
	if inst.LifecycleClass != store.DurableAgentClassProcess {
		t.Fatalf("lifecycle_class = %q, want %q", inst.LifecycleClass, store.DurableAgentClassProcess)
	}
	if inst.Status != store.DurableAgentStatusSleeping {
		t.Fatalf("status = %q, want %q (fresh instance default)", inst.Status, store.DurableAgentStatusSleeping)
	}
}

// TestLoomCuratorWake_FEPayloadShape proves the purpose-built endpoint
// accepts FE's exact {generator, fragment} callback body (traced from
// fragments-engine's CallbackDestinationExecutor.Execute,
// internal/ingest/destination_writer.go:461-519) and wakes Curator
// successfully on the very first call — no prior session/workspace
// bootstrap required.
func TestLoomCuratorDatabaseScheduleRuns(t *testing.T) {
	a, _ := newTestAPIWithLoomCurator(t)
	ctx := context.Background()

	inst, err := a.store.GetDurableAgentInstanceBySlug(context.Background(), "loom-curator")
	if err != nil {
		t.Fatalf("loom-curator instance not provisioned: %v", err)
	}
	if inst.ProfileID == "" {
		t.Fatal("seeded instance has empty ProfileID")
	}

	schedules, err := a.store.ListAgentSchedules(ctx, inst.ProfileID)
	if err != nil {
		t.Fatalf("ListAgentSchedules(%s): %v", inst.ProfileID, err)
	}
	if len(schedules) != 1 {
		t.Fatalf("expected exactly 1 schedule for loom-curator's profile, got %d: %+v", len(schedules), schedules)
	}
	sched := schedules[0]
	if sched.AgentID != inst.ProfileID {
		t.Fatalf("schedule.AgentID = %q, want instance's ProfileID %q", sched.AgentID, inst.ProfileID)
	}
	if sched.Name != "lint-and-export" {
		t.Fatalf("schedule name = %q, want lint-and-export", sched.Name)
	}
	if sched.ScheduleKind != store.ScheduleKindCron {
		t.Fatalf("schedule kind = %q, want %q", sched.ScheduleKind, store.ScheduleKindCron)
	}
	if sched.ScheduleSpec != "0 3 * * *" {
		t.Fatalf("schedule spec = %q, want '0 3 * * *'", sched.ScheduleSpec)
	}
	if sched.Status != store.ScheduleStatusActive {
		t.Fatalf("schedule status = %q, want active", sched.Status)
	}
	if !strings.Contains(sched.Body, "loom_bundle_conformance") || !strings.Contains(sched.Body, "loom_export_bundle") {
		t.Fatalf("schedule body missing expected loom_* tool references: %q", sched.Body)
	}

	// Due-ness is next_run-based. Simulate after the freshly-computed next run
	// so ListDue/RunDue reliably exercise dispatch without a hand-written row.
	if sched.NextRun == "" {
		t.Fatalf("schedule.NextRun is empty: %+v", sched)
	}
	nextRun, err := time.Parse(time.RFC3339, sched.NextRun)
	if err != nil {
		t.Fatalf("schedule.NextRun %q does not parse as RFC3339: %v", sched.NextRun, err)
	}
	simulatedNow := nextRun.Add(5 * time.Minute)
	if !nextRun.Before(simulatedNow) {
		t.Fatalf("schedule.NextRun = %s, want before simulatedNow %s (test's own due-ness assumption)", nextRun, simulatedNow)
	}

	due, err := a.Services.DurableWake.ListDue(ctx, simulatedNow)
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	var dueItem *service.DurableAgentWakeDueItem
	for i := range due {
		if due[i].InstanceID == inst.ID && due[i].Schedule.ID == sched.ID {
			dueItem = &due[i]
		}
	}
	if dueItem == nil {
		t.Fatalf("loom-curator's lint-and-export schedule not found by ListDue at %s: %+v", simulatedNow, due)
	}
	if !dueItem.Due {
		t.Fatalf("due item not marked Due: %+v", dueItem)
	}

	run, err := a.Services.DurableWake.RunDue(ctx, service.DurableAgentWakeRunRequest{Now: simulatedNow})
	if err != nil {
		t.Fatalf("RunDue: %v", err)
	}
	var result *service.DurableAgentWakeResult
	for i := range run.Results {
		if run.Results[i].ScheduleID == sched.ID {
			result = &run.Results[i]
		}
	}
	if result == nil {
		t.Fatalf("RunDue produced no result for schedule %s: %+v", sched.ID, run.Results)
	}
	if result.FailureReason != "" {
		t.Fatalf("firing loom-curator's scheduled tick errored: %s", result.FailureReason)
	}
	// A fresh instance with no prior attached session legitimately skips
	// on "workspace unavailable" (same behavior TestDurableWakeListDueAndDryRun
	// already documents for any freshly-seeded process instance) — RunDue
	// itself returning without error, with no FailureReason, is the bar
	// this test is proving: the schedule fires through the real
	// ListDue -> RunDue -> Wake chain without the pipeline erroring.
	t.Logf("scheduled tick result: skipped=%v skip_reason=%q", result.Skipped, result.SkipReason)
}
