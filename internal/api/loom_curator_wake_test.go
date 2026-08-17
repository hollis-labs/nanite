package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// newTestAPIWithLoomCurator boots a fresh container the same way newTestAPI
// does, but first copies the repo's real .nanite/agents/loom-curator.md and
// .nanite/durable-agents/loom-curator.yaml into the temp WorkingDir /
// ManagedConfigRoot before calling service.NewContainer. That exercises the
// exact boot pipeline a real Cerberus deploy+reload runs (agent.Discover ->
// ReconcileManagedAgentIDs -> AutoIngestAgents -> SyncManagedDurableAgentConfigs,
// see container.go), proving a plain file drop + restart is sufficient to
// produce a real, wakeable durable_agent_instances row — no migration or
// extra manual step required (CW-20260816-0020's seeding-gap trace).
func newTestAPIWithLoomCurator(t *testing.T) (*API, *http.ServeMux) {
	t.Helper()
	root := t.TempDir()

	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	copyFixture := func(rel string) {
		t.Helper()
		src := filepath.Join(repoRoot, rel)
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read fixture %s: %v", src, err)
		}
		dst := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", dst, err)
		}
		if err := os.WriteFile(dst, raw, 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", dst, err)
		}
	}
	copyFixture(".nanite/agents/loom-curator.md")
	copyFixture(".nanite/durable-agents/loom-curator.yaml")

	dbPath := filepath.Join(root, "test.db")
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	// A real boot (cmd/nanite/main.go) calls Seed() right after store.New,
	// which is what creates the "default" workspace row the wake handler's
	// hardcoded WorkspaceID depends on (see loom_curator_wake.go). newTestAPI
	// (api_test.go) skips this, so it must be done explicitly here for the
	// session-creation FK constraint to resolve the same way it does in
	// production.
	if err := s.Seed(); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	svc, err := service.NewContainer(service.ContainerConfig{
		Store:             s,
		Providers:         provider.NewRegistry(),
		WorkingDir:        root,
		ManagedConfigRoot: filepath.Join(root, ".nanite"),
	})
	if err != nil {
		t.Fatalf("service.NewContainer: %v", err)
	}

	a := New(svc)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)
	return a, mux
}

// TestLoomCuratorInstanceSeededFromFileDrop confirms the two managed config
// files alone (no migration, no API call) are enough for container boot to
// produce a real durable_agent_instances row for Loom Curator.
func TestLoomCuratorInstanceSeededFromFileDrop(t *testing.T) {
	a, _ := newTestAPIWithLoomCurator(t)

	inst, err := a.Services.Store.GetDurableAgentInstanceBySlug("loom-curator")
	if err != nil {
		t.Fatalf("loom-curator instance not seeded: %v", err)
	}
	if inst.ID == "" {
		t.Fatal("seeded instance has empty ID")
	}
	if inst.LifecycleClass != store.DurableAgentClassProcess {
		t.Fatalf("lifecycle_class = %q, want %q", inst.LifecycleClass, store.DurableAgentClassProcess)
	}
	if inst.Status != store.DurableAgentStatusSleeping {
		t.Fatalf("status = %q, want %q (fresh instance default)", inst.Status, store.DurableAgentStatusSleeping)
	}
	t.Logf("loom-curator seeded instance id = %s", inst.ID)
}

// TestLoomCuratorWake_FEPayloadShape proves the purpose-built endpoint
// accepts FE's exact {generator, fragment} callback body (traced from
// fragments-engine's CallbackDestinationExecutor.Execute,
// internal/ingest/destination_writer.go:461-519) and wakes Curator
// successfully on the very first call — no prior session/workspace
// bootstrap required.
func TestLoomCuratorWake_FEPayloadShape(t *testing.T) {
	a, mux := newTestAPIWithLoomCurator(t)

	body := `{
		"generator": "wiki_page",
		"fragment": {
			"id": "frag-123",
			"source": "claude-code",
			"source_type": "chat",
			"source_id": "sess-abc",
			"title": "Nanite envelope system",
			"canonical_path": "nanite/envelope-system"
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/loom/curator-wake", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("wake = %d body=%s", w.Code, w.Body.String())
	}

	var resp service.DurableAgentWakeResult
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if resp.Skipped {
		t.Fatalf("wake was skipped: %s (first-ever wake must not skip on 'workspace unavailable')", resp.SkipReason)
	}
	if resp.WakeReason != "callback:wiki_page" {
		t.Fatalf("wake_reason = %q, want callback:wiki_page", resp.WakeReason)
	}

	inst, err := a.Services.Store.GetDurableAgentInstanceBySlug("loom-curator")
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}
	if inst.Status != store.DurableAgentStatusActive {
		t.Fatalf("instance status after wake = %q, want %q", inst.Status, store.DurableAgentStatusActive)
	}
}

// TestLoomCuratorWake_MissingFragmentID checks the 400 validation path.
func TestLoomCuratorWake_MissingFragmentID(t *testing.T) {
	_, mux := newTestAPIWithLoomCurator(t)

	req := httptest.NewRequest(http.MethodPost, "/api/loom/curator-wake", bytes.NewBufferString(`{"generator":"wiki_page","fragment":{}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestLoomCuratorScheduleSeededFromFileDrop proves CW-20260816-0021's
// schedule-seeding mechanism end to end: the structured `schedule:` block
// in .nanite/durable-agents/loom-curator.yaml produces a real
// agent_schedules row once container boot resolves Loom Curator's real
// profile ID (syncManagedDurableAgentConfig, called from
// SyncManagedDurableAgentConfigs — the same boot path
// TestLoomCuratorInstanceSeededFromFileDrop above already proves seeds the
// durable_agent_instances row). It also proves the row is keyed correctly
// (agent_id = the *profile* ID, not the instance ID — ListDue/ListSchedules
// in durable_wake.go look it up via inst.ProfileID), that ListDue/RunDue
// find it due at an appropriate simulated time, and that firing it does
// not error. This is the other half of CW-20260816-0020's "no scheduling
// mechanism exists yet" gap that CW-20260816-0021 was scoped to close.
func TestLoomCuratorScheduleSeededFromFileDrop(t *testing.T) {
	a, _ := newTestAPIWithLoomCurator(t)
	ctx := context.Background()

	inst, err := a.Services.Store.GetDurableAgentInstanceBySlug("loom-curator")
	if err != nil {
		t.Fatalf("loom-curator instance not seeded: %v", err)
	}
	if inst.ProfileID == "" {
		t.Fatal("seeded instance has empty ProfileID")
	}

	schedules, err := a.Services.Store.ListAgentSchedules(ctx, inst.ProfileID)
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

	// wakeScheduleDue's cron branch (durable_wake.go) falls back to
	// ref = now.Add(-15*time.Minute) whenever LastFiredAt is empty and
	// CreatedAt fails to RFC3339-parse — which it always does here,
	// since InsertAgentSchedule defaults created_at to SQLite's native
	// datetime('now') format, not RFC3339. So a 15-minute window after
	// each daily "0 3 * * *" boundary is due, on any calendar date;
	// 03:05 UTC sits comfortably inside it.
	simulatedNow := time.Date(2027, time.January, 4, 3, 5, 0, 0, time.UTC)

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

// TestLoomCuratorWake_InstanceNotProvisioned checks the 503 path on a plain
// container that never had the loom-curator config files dropped into it.
func TestLoomCuratorWake_InstanceNotProvisioned(t *testing.T) {
	_, mux := newTestAPI(t)

	req := httptest.NewRequest(http.MethodPost, "/api/loom/curator-wake", bytes.NewBufferString(`{"generator":"wiki_page","fragment":{"id":"f1"}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}
