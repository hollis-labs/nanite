package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gosched "github.com/hollis-labs/go-scheduler"

	"github.com/hollis-labs/nanite/internal/scheduler"
	"github.com/hollis-labs/nanite/internal/store"
)

func createScheduleTestAgent(t *testing.T, a *API, slug string) *store.AgentProfile {
	t.Helper()
	agent := &store.AgentProfile{Name: "Schedule Agent " + slug, Slug: slug, SystemPrompt: "x", Class: "advisor"}
	if err := a.Services.Store.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return agent
}

// TestSchedulesAPI_CreateGetPatchDeleteLifecycle exercises all five CRUD
// endpoints against a real store, proving the documented request/response
// shapes round-trip correctly end to end.
func TestSchedulesAPI_CreateGetPatchDeleteLifecycle(t *testing.T) {
	a, mux := newTestAPI(t)
	agent := createScheduleTestAgent(t, a, "sched-lifecycle")

	createBody := fmt.Sprintf(`{
		"agent_id": %q,
		"name": "nightly-wake",
		"schedule_kind": "cron",
		"schedule_spec": "0 9 * * *",
		"body": "good morning",
		"max_retries": 5,
		"on_fail": "notify",
		"job_type": "durable_agent_wake"
	}`, agent.ID)

	req := httptest.NewRequest(http.MethodPost, "/api/schedules", bytes.NewBufferString(createBody))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create schedule = %d body=%s", w.Code, w.Body.String())
	}
	var created store.AgentSchedule
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("created schedule has empty id")
	}
	if created.AgentID != agent.ID {
		t.Fatalf("created.AgentID = %q, want %q", created.AgentID, agent.ID)
	}
	if created.MaxRetries != 5 || created.OnFail != store.ScheduleOnFailNotify {
		t.Fatalf("retry policy not persisted: %+v", created)
	}
	if created.NextRun == "" {
		t.Fatalf("created schedule has no next_run -- would never fire under the CAS-claim engine")
	}
	if created.Status != store.ScheduleStatusActive {
		t.Fatalf("created.Status = %q, want active (default)", created.Status)
	}

	// GET
	req = httptest.NewRequest(http.MethodGet, "/api/schedules/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get schedule = %d body=%s", w.Code, w.Body.String())
	}

	// PATCH: status -> paused, priority, schedule_spec.
	patchBody := `{"status":"paused","priority":9,"schedule_spec":"0 10 * * *"}`
	req = httptest.NewRequest(http.MethodPatch, "/api/schedules/"+created.ID, bytes.NewBufferString(patchBody))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("patch schedule = %d body=%s", w.Code, w.Body.String())
	}
	var patched store.AgentSchedule
	if err := json.NewDecoder(w.Body).Decode(&patched); err != nil {
		t.Fatalf("decode patched: %v", err)
	}
	if patched.Status != store.ScheduleStatusPaused {
		t.Fatalf("patched.Status = %q, want paused", patched.Status)
	}
	if patched.Priority != 9 {
		t.Fatalf("patched.Priority = %d, want 9", patched.Priority)
	}
	if patched.ScheduleSpec != "0 10 * * *" {
		t.Fatalf("patched.ScheduleSpec = %q, want %q", patched.ScheduleSpec, "0 10 * * *")
	}
	// Immutable-through-PATCH fields: agent_id/schedule_kind/job_type are
	// simply absent from schedulePatchRequest, so attempting to smuggle
	// them through is silently ignored (matching agent_reflexes.
	// provenance_tier's own non-patchable precedent) rather than erroring.
	otherAgent := createScheduleTestAgent(t, a, "sched-lifecycle-other")
	smuggleBody := fmt.Sprintf(`{"agent_id":%q,"schedule_kind":"one_shot","job_type":"command_run"}`, otherAgent.ID)
	req = httptest.NewRequest(http.MethodPatch, "/api/schedules/"+created.ID, bytes.NewBufferString(smuggleBody))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("patch (smuggle attempt) = %d body=%s", w.Code, w.Body.String())
	}
	var afterSmuggle store.AgentSchedule
	if err := json.NewDecoder(w.Body).Decode(&afterSmuggle); err != nil {
		t.Fatalf("decode afterSmuggle: %v", err)
	}
	if afterSmuggle.AgentID != agent.ID {
		t.Fatalf("agent_id changed via PATCH: got %q, want unchanged %q", afterSmuggle.AgentID, agent.ID)
	}
	if afterSmuggle.ScheduleKind != store.ScheduleKindCron {
		t.Fatalf("schedule_kind changed via PATCH: got %q, want unchanged %q", afterSmuggle.ScheduleKind, store.ScheduleKindCron)
	}
	if afterSmuggle.JobType != store.ScheduleJobTypeDurableAgentWake {
		t.Fatalf("job_type changed via PATCH: got %q, want unchanged %q", afterSmuggle.JobType, store.ScheduleJobTypeDurableAgentWake)
	}

	// DELETE
	req = httptest.NewRequest(http.MethodDelete, "/api/schedules/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete schedule = %d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/schedules/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", w.Code)
	}
}

// TestSchedulesAPI_ListFiltersByAgentID proves GET /api/schedules'
// agent_id filter mirrors store.ListAgentSchedules' own scoping, and that
// the unfiltered case returns every agent's rows.
func TestSchedulesAPI_ListFiltersByAgentID(t *testing.T) {
	a, mux := newTestAPI(t)
	agentA := createScheduleTestAgent(t, a, "sched-list-a")
	agentB := createScheduleTestAgent(t, a, "sched-list-b")

	for _, ag := range []*store.AgentProfile{agentA, agentB} {
		body := fmt.Sprintf(`{"agent_id":%q,"name":"n","schedule_kind":"one_shot","body":"b"}`, ag.ID)
		req := httptest.NewRequest(http.MethodPost, "/api/schedules", bytes.NewBufferString(body))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create schedule for %s = %d body=%s", ag.Slug, w.Code, w.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/schedules?agent_id="+agentA.ID, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list filtered = %d body=%s", w.Code, w.Body.String())
	}
	var filtered []store.AgentSchedule
	if err := json.NewDecoder(w.Body).Decode(&filtered); err != nil {
		t.Fatalf("decode filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].AgentID != agentA.ID {
		t.Fatalf("filtered list = %+v, want exactly one row scoped to %s", filtered, agentA.ID)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/schedules", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list unfiltered = %d body=%s", w.Code, w.Body.String())
	}
	var all []store.AgentSchedule
	if err := json.NewDecoder(w.Body).Decode(&all); err != nil {
		t.Fatalf("decode all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("unfiltered list has %d rows, want 2", len(all))
	}
}

// TestSchedulesAPI_CreateRejectsUnknownAgent proves a POST naming a
// nonexistent agent_id is rejected rather than inserting an orphaned row.
func TestSchedulesAPI_CreateRejectsUnknownAgent(t *testing.T) {
	_, mux := newTestAPI(t)
	body := `{"agent_id":"does-not-exist","name":"n","schedule_kind":"one_shot","body":"b"}`
	req := httptest.NewRequest(http.MethodPost, "/api/schedules", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with unknown agent_id = %d, want 400 body=%s", w.Code, w.Body.String())
	}
}

// TestSchedulesAPI_CreateRejectsInvalidScheduleKind is this task's
// required regression test: POST with an invalid schedule_kind is
// rejected with a clear error, not silently coerced or passed through to
// the DB's own CHECK constraint as an opaque SQL error.
func TestSchedulesAPI_CreateRejectsInvalidScheduleKind(t *testing.T) {
	a, mux := newTestAPI(t)
	agent := createScheduleTestAgent(t, a, "sched-bad-kind")
	body := fmt.Sprintf(`{"agent_id":%q,"name":"n","schedule_kind":"every_n_ticks","body":"b"}`, agent.ID)
	req := httptest.NewRequest(http.MethodPost, "/api/schedules", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with invalid schedule_kind = %d, want 400 body=%s", w.Code, w.Body.String())
	}
	var errResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp["error"] == "" {
		t.Fatalf("expected a non-empty clear error message, got %+v", errResp)
	}
}

// TestSchedulesAPI_CreateRejectsMalformedCronSpec is this task's other
// required regression test: a cron-kind schedule with an unparseable
// schedule_spec is rejected with a clear error at create time, rather than
// silently falling back to "due now" (which is what
// store.ComputeAgentScheduleNextRun does internally for a different,
// already-inserted-row use case -- this endpoint validates before that
// fallback ever gets a chance to mask the mistake).
func TestSchedulesAPI_CreateRejectsMalformedCronSpec(t *testing.T) {
	a, mux := newTestAPI(t)
	agent := createScheduleTestAgent(t, a, "sched-bad-cron")
	body := fmt.Sprintf(`{"agent_id":%q,"name":"n","schedule_kind":"cron","schedule_spec":"not a cron expr","body":"b"}`, agent.ID)
	req := httptest.NewRequest(http.MethodPost, "/api/schedules", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with malformed cron spec = %d, want 400 body=%s", w.Code, w.Body.String())
	}
	var errResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	const sharedRule = "schedule_spec is not a valid cron expression"
	if !strings.Contains(errResp["error"], sharedRule) {
		t.Fatalf("error = %q, want shared validator rule %q", errResp["error"], sharedRule)
	}
}

// TestSchedulesAPI_CreateRejectsMalformedJobPayload proves an
// invalid-JSON job_payload is rejected at create time.
func TestSchedulesAPI_CreateRejectsMalformedJobPayload(t *testing.T) {
	a, mux := newTestAPI(t)
	agent := createScheduleTestAgent(t, a, "sched-bad-payload")
	body := fmt.Sprintf(`{"agent_id":%q,"name":"n","schedule_kind":"one_shot","body":"b","job_type":"command_run","job_payload":"{not json"}`, agent.ID)
	req := httptest.NewRequest(http.MethodPost, "/api/schedules", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with malformed job_payload = %d, want 400 body=%s", w.Code, w.Body.String())
	}
}

// TestSchedulesAPI_PatchUnknownID404s and TestSchedulesAPI_DeleteUnknownID404s
// prove the not-found path for the id-addressed endpoints.
func TestSchedulesAPI_PatchUnknownID404s(t *testing.T) {
	_, mux := newTestAPI(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/schedules/does-not-exist", bytes.NewBufferString(`{"priority":1}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("patch unknown id = %d, want 404 body=%s", w.Code, w.Body.String())
	}
}

func TestSchedulesAPI_DeleteUnknownID404s(t *testing.T) {
	_, mux := newTestAPI(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/schedules/does-not-exist", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("delete unknown id = %d, want 404 body=%s", w.Code, w.Body.String())
	}
}

// TestSchedulesAPI_StatusEndpoint is this task's other required
// regression test: the status endpoint returns gosched.Engine.Status()'s
// real fields. Covers both the "not configured" case (every test
// container's default -- Services.Engine is nil, matching container.go's
// own "main.go-only wiring" doc comment) and a real gosched.Engine wired
// with the same production adapter types (internal/scheduler.StoreAdapter/
// RunnerAdapter, no fakes), proving the endpoint surfaces genuine
// Status() output rather than a hand-rolled shape that happens to look
// similar.
func TestSchedulesAPI_StatusEndpoint(t *testing.T) {
	a, mux := newTestAPI(t)

	// Not configured.
	req := httptest.NewRequest(http.MethodGet, "/api/schedules/status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status (unconfigured) = %d body=%s", w.Code, w.Body.String())
	}
	var unconfigured map[string]any
	if err := json.NewDecoder(w.Body).Decode(&unconfigured); err != nil {
		t.Fatalf("decode unconfigured status: %v", err)
	}
	if configured, _ := unconfigured["configured"].(bool); configured {
		t.Fatalf("unconfigured status response reports configured=true: %+v", unconfigured)
	}

	// Wire a real gosched.Engine over the same production adapters used
	// in cmd/nanite/main.go's own wiring.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	storeAdapter := &scheduler.StoreAdapter{Store: a.Services.Store, Logger: logger}
	runnerAdapter := &scheduler.RunnerAdapter{}
	engine := gosched.New(storeAdapter, runnerAdapter)
	a.Services.Engine = engine

	// TickNow runs a single tick synchronously, independent of the
	// engine's real (hardcoded, unexported, 1s) background tick interval
	// -- avoids a real-time sleep in this test while still exercising
	// genuine Status() bookkeeping (LastTickAt gets set by tick() whether
	// called via the background loop or TickNow).
	if err := engine.TickNow(context.Background()); err != nil {
		t.Fatalf("TickNow: %v", err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/schedules/status", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status (post-tick) = %d body=%s", w.Code, w.Body.String())
	}
	var afterTick map[string]any
	if err := json.NewDecoder(w.Body).Decode(&afterTick); err != nil {
		t.Fatalf("decode post-tick status: %v", err)
	}
	if configured, _ := afterTick["configured"].(bool); !configured {
		t.Fatalf("post-tick status response reports configured=false: %+v", afterTick)
	}
	if running, _ := afterTick["running"].(bool); running {
		t.Fatalf("running should still be false before Start(): %+v", afterTick)
	}
	lastTick, _ := afterTick["last_tick_at"].(string)
	if lastTick == "" {
		t.Fatalf("last_tick_at should be populated after TickNow: %+v", afterTick)
	}
	// dispatches/worker_errors must be present as real numeric fields
	// (zero is a legitimate, expected value here -- no due schedules
	// exist in this fresh store).
	if _, ok := afterTick["dispatches"]; !ok {
		t.Fatalf("dispatches field missing from status response: %+v", afterTick)
	}
	if _, ok := afterTick["worker_errors"]; !ok {
		t.Fatalf("worker_errors field missing from status response: %+v", afterTick)
	}

	// Start()/Stop() flips Running -- prove that field is live too, not a
	// static true once wired.
	engine.Start()
	req = httptest.NewRequest(http.MethodGet, "/api/schedules/status", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	engine.Stop()
	if w.Code != http.StatusOK {
		t.Fatalf("status (running) = %d body=%s", w.Code, w.Body.String())
	}
	var running map[string]any
	if err := json.NewDecoder(w.Body).Decode(&running); err != nil {
		t.Fatalf("decode running status: %v", err)
	}
	if r, _ := running["running"].(bool); !r {
		t.Fatalf("running should be true while the engine's background loop is Start()ed: %+v", running)
	}
}
