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
	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// newTestAPIWithLoomCurator boots a fresh container against a DB that
// already carries a real loom-curator agent_profiles row, then copies the
// repo's real .nanite/durable-agents/loom-curator.yaml into the temp
// ManagedConfigRoot before calling service.NewContainer.
//
// TASKS/phase-1/08 ("Kill the file-reingest-on-boot pattern, in full") cut
// the project-tier (.nanite/agents/) directory scan that this helper used to
// rely on to auto-provision the agent_profiles row from a dropped
// .nanite/agents/loom-curator.md file at boot — that mechanism no longer
// exists, by design (files are not agent storage going forward, except
// builtin/seed content). What SyncManagedDurableAgentConfigs actually needs
// — an agent_profiles row for slug "loom-curator" to already exist before it
// resolves .nanite/durable-agents/loom-curator.yaml against it — is
// unaffected by that cut: in production, Loom Curator's real DB row was
// already ingested under the old mechanism and (per this task's own
// overwrite-freeze, still in force) stays exactly as-is on every subsequent
// boot regardless of whether the file is ever scanned again. Here, the
// equivalent of "the agent already exists" is reproduced explicitly via
// IngestAgentDefinition — the one deliberate reimport path this task
// preserves (the same one AgentConfigService's managed-agent write flow
// uses) — rather than relying on the now-cut automatic first-ingest-at-boot
// path. Everything downstream (SyncManagedDurableAgentConfigs, the wake
// endpoint, schedule seeding) is exercised exactly as before.
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
	agentFixturePath := filepath.Join(repoRoot, ".nanite", "agents", "loom-curator.md")
	copyFixture(".nanite/durable-agents/loom-curator.yaml")

	dbPath := filepath.Join(root, "test.db")
	prepareAPIStoreDB(t, dbPath)
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() {
		s.Close(context.
			// A real boot (cmd/nanite/main.go) calls Seed() right after store.New,
			// which is what creates the "default" workspace row the wake handler's
			// hardcoded WorkspaceID depends on (see loom_curator_wake.go). newTestAPI
			// (api_test.go) skips this, so it must be done explicitly here for the
			// session-creation FK constraint to resolve the same way it does in
			// production.
			Background())
	})

	if err := s.Seed(context.Background()); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	// Reproduce "loom-curator's agent_profiles row already exists" — the
	// state every real boot finds it in — via the one explicit reimport path
	// TASKS/phase-1/08 preserves, standing in for however the row first got
	// created (in production: the old boot-time discovery mechanism, before
	// this task cut it; going forward: an explicit create/import action).
	def, err := agent.ParseMDFile(agentFixturePath)
	if err != nil {
		t.Fatalf("parse loom-curator.md fixture: %v", err)
	}
	def.Source = "project"
	if err := service.IngestAgentDefinition(s, def); err != nil {
		t.Fatalf("ingest loom-curator agent definition: %v", err)
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
	t.Cleanup(func() { svc.Shutdown() })

	a := New(svc)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)
	return a, mux
}

// TestLoomCuratorInstanceSeededFromDurableConfigDrop confirms that once an
// agent_profiles row exists for loom-curator (see newTestAPIWithLoomCurator —
// no longer auto-provisioned by a boot-time file scan, per TASKS/phase-1/08),
// dropping .nanite/durable-agents/loom-curator.yaml alone (no migration, no
// API call) is enough for container boot's SyncManagedDurableAgentConfigs
// pass to produce a real durable_agent_instances row for it.
func TestLoomCuratorInstanceSeededFromDurableConfigDrop(t *testing.T) {
	a, _ := newTestAPIWithLoomCurator(t)

	inst, err := a.Services.Store.GetDurableAgentInstanceBySlug(context.Background(), "loom-curator")
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

	inst, err := a.Services.Store.GetDurableAgentInstanceBySlug(context.Background(), "loom-curator")
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}
	if inst.Status != store.DurableAgentStatusActive {
		t.Fatalf("instance status after wake = %q, want %q", inst.Status, store.DurableAgentStatusActive)
	}
}

// TestLoomCuratorWake_DeliversRealTurn is the regression test for the
// CW-20260817 finding: CW-20260816-0020 was reported done, but the wake
// handler only ever populated WakePayload.Facts, which nothing downstream
// of Wake()/Start() reads — deliverWakePrompt (durable_agents.go) only fires
// a real ChatService.HandleMessage turn when WakePayload.Prompt is
// non-empty. That meant every callback wake created a session and marked
// the instance active without ever actually asking the agent to do
// anything: no message, no async generation, no classify/compile. This test
// asserts the fragment identity actually reaches the launched session as a
// real persisted user message (the same mechanism CW-20260816-0021's
// scheduled tick fix relies on), which is the only way Curator's
// classify_and_compile_fragment procedure can ever fire from this endpoint.
func TestLoomCuratorWake_DeliversRealTurn(t *testing.T) {
	a, mux := newTestAPIWithLoomCurator(t)

	body := `{
		"generator": "wiki_page",
		"fragment": {
			"id": "frag-456",
			"source": "claude-code",
			"source_type": "chat",
			"source_id": "sess-def",
			"title": "Envelope system notes",
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
	if resp.LaunchResult == nil || resp.LaunchResult.Session == nil {
		t.Fatalf("wake result missing launch_result/session: %+v", resp)
	}

	messages, err := a.Services.Store.ListMessages(context.Background(), resp.LaunchResult.Session.ID, 10)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var userMsg *store.Message
	for i := range messages {
		if messages[i].Role == "user" {
			userMsg = &messages[i]
		}
	}
	if userMsg == nil {
		t.Fatalf("no user message delivered into wake session %s — wake created a session but never actually asked Curator to do anything; messages=%+v", resp.LaunchResult.Session.ID, messages)
	}
	if !strings.Contains(userMsg.Content, "frag-456") {
		t.Fatalf("delivered turn missing fragment_id: %q", userMsg.Content)
	}
	if !strings.Contains(userMsg.Content, "classify_and_compile_fragment") {
		t.Fatalf("delivered turn doesn't point Curator at its classify_and_compile_fragment procedure: %q", userMsg.Content)
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

// TestLoomCuratorScheduleSeededFromDurableConfigDrop proves CW-20260816-0021's
// schedule-seeding mechanism end to end: the structured `schedule:` block
// in .nanite/durable-agents/loom-curator.yaml produces a real
// agent_schedules row once container boot resolves Loom Curator's real
// profile ID (syncManagedDurableAgentConfig, called from
// SyncManagedDurableAgentConfigs — the same boot path
// TestLoomCuratorInstanceSeededFromDurableConfigDrop above already proves
// seeds the durable_agent_instances row). It also proves the row is keyed
// correctly (agent_id = the *profile* ID, not the instance ID —
// ListDue/ListSchedules in durable_wake.go look it up via inst.ProfileID),
// that ListDue/RunDue find it due at an appropriate simulated time, and that
// firing it does not error. This is the other half of CW-20260816-0020's "no
// scheduling mechanism exists yet" gap that CW-20260816-0021 was scoped to
// close.
func TestLoomCuratorScheduleSeededFromDurableConfigDrop(t *testing.T) {
	a, _ := newTestAPIWithLoomCurator(t)
	ctx := context.Background()

	inst, err := a.Services.Store.GetDurableAgentInstanceBySlug(context.Background(), "loom-curator")
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

	// Due-ness is now next_run-based (scheduleDueByNextRun, durable_wake.go
	// — TASKS/scheduling/05-engine-wiring-and-full-replace.md removed the
	// old wakeScheduleDue 15-minute-lookback heuristic this comment used to
	// describe). syncManagedDurableAgentSchedule (managed_durable_configs.go)
	// computes next_run at first-sync time via store.
	// ComputeAgentScheduleNextRun("0 3 * * *", <real wall-clock time this
	// test process boots at>) — the real next occurrence of the schedule's
	// daily 3am UTC cron expression, not a fixed date. simulatedNow is
	// deliberately set far enough in the future (2027) that it is always
	// after whatever next_run really got computed to, regardless of what
	// day this test happens to run on, so ListDue/RunDue reliably see the
	// row as due without this test needing to compute the exact same
	// cron-next-occurrence math itself just to pick a "due" timestamp.
	// Asserted directly below (sched.NextRun) rather than only inferred
	// from ListDue's result, so a future regression in next_run's
	// computation fails loudly here instead of only downstream.
	simulatedNow := time.Date(2027, time.January, 4, 3, 5, 0, 0, time.UTC)
	if sched.NextRun == "" {
		t.Fatalf("schedule.NextRun is empty — syncManagedDurableAgentSchedule should have computed a real next_run at first sync: %+v", sched)
	}
	nextRun, err := time.Parse(time.RFC3339, sched.NextRun)
	if err != nil {
		t.Fatalf("schedule.NextRun %q does not parse as RFC3339: %v", sched.NextRun, err)
	}
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
