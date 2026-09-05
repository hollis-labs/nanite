package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// newTestAPIWithLoomCurator provisions the profile and durable instance through
// the normal database-backed service path before boot. That creation path also
// supplies the canonical builtin schedule; the fixture never inserts it by
// hand and deliberately has no config-file input.
func newTestAPIWithLoomCurator(t *testing.T) (*API, *http.ServeMux) {
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
		Store: s, Providers: provider.NewRegistry(), WorkingDir: root, DisableEmbeddedTesseract: true,
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

func TestLoomCuratorInstanceProvisionedInDatabase(t *testing.T) {
	a, _ := newTestAPIWithLoomCurator(t)

	inst, err := a.Services.Store.GetDurableAgentInstanceBySlug(context.Background(), "loom-curator")
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

// TestLoomCuratorDatabaseScheduleRuns proves the provisioned schedule is keyed
// correctly (agent_id = the *profile* ID, not the instance ID —
// ListDue/ListSchedules in durable_wake.go look it up via inst.ProfileID),
// that ListDue/RunDue find it due at an appropriate simulated time, and that
// firing it does not error. This is the other half of CW-20260816-0020's "no
// scheduling mechanism exists yet" gap that CW-20260816-0021 was scoped to
// close.
func TestLoomCuratorDatabaseScheduleRuns(t *testing.T) {
	a, _ := newTestAPIWithLoomCurator(t)
	ctx := context.Background()

	inst, err := a.Services.Store.GetDurableAgentInstanceBySlug(context.Background(), "loom-curator")
	if err != nil {
		t.Fatalf("loom-curator instance not provisioned: %v", err)
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

// TestLoomCuratorWake_InstanceNotProvisioned checks the 503 path on a plain
// container where no operator provisioned the Loom Curator database rows.
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
