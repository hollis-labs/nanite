package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func TestDurableAgentsAPI_MissingProfileIsNotFound(t *testing.T) {
	_, mux := newTestAPI(t)
	body, _ := json.Marshal(CreateDurableAgentRequest{
		Name: "Missing", Slug: "missing", ProfileID: "does-not-exist",
	})
	req := httptest.NewRequest("POST", "/api/durable-agents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "create or import it through the agent API first") {
		t.Fatalf("missing profile = %d body=%s", w.Code, w.Body.String())
	}
}

// TestDurableAgentsAPI_UpdateRejectsSlugTraversal is the HTTP-level
// regression test for the durable-agent sibling of GO-AGENT-001: a crafted
// slug on PATCH/PUT /api/durable-agents/{id} (the rename-shaped update
// handleUpdateDurableAgent performs at durable_agents.go:139-141) must be
// rejected without changing the canonical database row or writing files.
func TestDurableAgentsAPI_UpdateRejectsSlugTraversal(t *testing.T) {
	a, mux := newTestAPI(t)
	profile := &store.AgentProfile{Name: "Traversal Profile", Slug: "traversal-profile", SystemPrompt: "x"}
	if err := storetest.PriorAuthorizedActor(t.Context(), a.store, profile); err != nil {
		t.Fatalf("PriorAuthorizedActor: %v", err)
	}

	created := store.DurableAgentInstance{
		Name: "Torque Supervisor", Slug: "torque-supervisor-traversal", ProfileID: profile.ID,
		LifecycleClass: store.DurableAgentClassProcess,
		Provider:       "anthropic", Model: "claude-sonnet-4", RuntimeKind: "api",
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
	}
	persistPriorAPIInstance(t, a.store, &created)
	before := durableAPIRefusalState(t, a)
	for _, malicious := range []string{"../evil", "../../etc/evil", "a/b", "UPPER"} {
		patch, _ := json.Marshal(UpdateDurableAgentRequest{Slug: &malicious})
		req := httptest.NewRequest("PATCH", "/api/durable-agents/"+created.ID, bytes.NewReader(patch))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("rename with slug %q = %d, want 400; body=%s", malicious, w.Code, w.Body.String())
		}
		assertDurableAPIRefusalState(t, a, before)
	}
	persisted, err := a.Services.DurableAgents.Get(context.Background(), created.ID)
	if err != nil || persisted.Slug != created.Slug {
		t.Fatalf("rejected rename changed DB row: %#v, %v", persisted, err)
	}
	if _, err := os.Stat(filepath.Join(a.Services.WorkingDir, ".nanite", "durable-agents")); !os.IsNotExist(err) {
		t.Fatalf("durable API produced a filesystem projection: %v", err)
	}
}

func TestDurableAgentsAPI_PriorInstanceGetPatchArchive(t *testing.T) {
	a, mux := newTestAPI(t)
	profile := &store.AgentProfile{Name: "Durable API Profile", Slug: "durable-api-profile", SystemPrompt: "x"}
	if err := storetest.PriorAuthorizedActor(t.Context(), a.store, profile); err != nil {
		t.Fatalf("PriorAuthorizedActor: %v", err)
	}

	created := store.DurableAgentInstance{
		Name: "Torque Supervisor", Slug: "torque-supervisor-api", ProfileID: profile.ID,
		LifecycleClass: store.DurableAgentClassProcess,
		Provider:       "anthropic", Model: "claude-sonnet-4", RuntimeKind: "api",
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor, WorkRoot: t.TempDir(),
	}
	persistPriorAPIInstance(t, a.store, &created)
	req := httptest.NewRequest("GET", "/api/durable-agents/"+created.ID, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET prior instance = %d body=%s", w.Code, w.Body.String())
	}
	var read store.DurableAgentInstance
	if err := json.NewDecoder(w.Body).Decode(&read); err != nil {
		t.Fatal(err)
	}
	if read.ID != created.ID || read.ProfileID != profile.ID || read.URN != profile.ID {
		t.Fatalf("prior instance read changed bound identity: %+v", read)
	}

	newName := "Torque Supervisor Renamed"
	patch, _ := json.Marshal(UpdateDurableAgentRequest{Name: &newName})
	req = httptest.NewRequest("PATCH", "/api/durable-agents/"+created.ID, bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH /api/durable-agents/{id} = %d body=%s", w.Code, w.Body.String())
	}
	var updated store.DurableAgentInstance
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if updated.Name != newName || updated.Provider != "anthropic" {
		t.Fatalf("updated = %+v", updated)
	}

	req = httptest.NewRequest("POST", "/api/durable-agents/"+created.ID+"/archive", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("archive = %d body=%s", w.Code, w.Body.String())
	}
	var archived store.DurableAgentInstance
	if err := json.NewDecoder(w.Body).Decode(&archived); err != nil {
		t.Fatalf("decode archive: %v", err)
	}
	if archived.Status != store.DurableAgentStatusArchived {
		t.Fatalf("archived status = %q", archived.Status)
	}
	if strings.Contains(archived.MetadataJSON, "managed_config_path") {
		t.Fatalf("durable API injected filesystem metadata: %s", archived.MetadataJSON)
	}
	if _, err := os.Stat(filepath.Join(a.Services.WorkingDir, ".nanite", "durable-agents")); !os.IsNotExist(err) {
		t.Fatalf("durable lifecycle produced a filesystem projection: %v", err)
	}
}

func TestDurableAgentsAPI_LifecycleAndSessionAttachment(t *testing.T) {
	a, mux := newTestAPI(t)
	profile := &store.AgentProfile{Name: "Attach Profile", Slug: "attach-profile", SystemPrompt: "x"}
	if err := storetest.PriorAuthorizedActor(t.Context(), a.store, profile); err != nil {
		t.Fatalf("PriorAuthorizedActor: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:             "Attach Instance",
		Slug:             "attach-instance-api",
		ProfileID:        profile.ID,
		LaunchSourceType: store.DurableAgentLaunchAPIChat,
	}
	persistPriorAPIInstance(t, a.store, inst)
	sess := &store.Session{Title: "attached", Provider: "anthropic", Model: "model-a"}
	if err := a.store.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Attach the session first, then exercise start-request: RequestStart
	// (Phase 0 item 2 fix) now calls straight through to Start with an
	// empty DurableAgentStartRequest{} (no workspace_id — same shape as
	// RequestResume/Resume), so for it to actually launch (not just fail
	// with "workspace_id required"), this instance's advisor-class
	// ReuseLatestOrCreate policy needs an already-attached, reusable
	// session to find — the realistic "sleeping instance with a prior
	// session gets woken back up" scenario.
	attachBody, _ := json.Marshal(AttachDurableAgentSessionRequest{SessionID: sess.ID, Relation: store.DurableAgentSessionRelationPrimary})
	req := httptest.NewRequest("POST", "/api/durable-agents/"+inst.ID+"/sessions", bytes.NewReader(attachBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("attach session = %d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("POST", "/api/durable-agents/"+inst.ID+"/start-request", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("start request = %d body=%s", w.Code, w.Body.String())
	}
	var started store.DurableAgentInstance
	if err := json.NewDecoder(w.Body).Decode(&started); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if started.Status != store.DurableAgentStatusActive {
		t.Fatalf("status = %q, want active", started.Status)
	}
	if started.CurrentSessionID != sess.ID {
		t.Fatalf("current_session_id = %q, want %q", started.CurrentSessionID, sess.ID)
	}

	req = httptest.NewRequest("GET", "/api/durable-agents/"+inst.ID+"/sessions", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list sessions = %d body=%s", w.Code, w.Body.String())
	}
	var rels []store.DurableAgentInstanceSession
	if err := json.NewDecoder(w.Body).Decode(&rels); err != nil {
		t.Fatalf("decode rels: %v", err)
	}
	if len(rels) != 1 || rels[0].SessionID != sess.ID {
		t.Fatalf("relations = %+v", rels)
	}
}

func TestDurableAgentsAPI_StartAndResume(t *testing.T) {
	a, mux := newTestAPI(t)
	profile := &store.AgentProfile{Name: "Launch Profile", Slug: "launch-profile", SystemPrompt: "x"}
	if err := storetest.PriorAuthorizedActor(t.Context(), a.store, profile); err != nil {
		t.Fatalf("PriorAuthorizedActor: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:             "Launch Instance",
		Slug:             "launch-instance-api",
		ProfileID:        profile.ID,
		Provider:         "anthropic",
		Model:            "model-a",
		RuntimeKind:      "api",
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
	}
	persistPriorAPIInstance(t, a.store, inst)

	req := httptest.NewRequest("GET", "/api/durable-agents/"+inst.ID+"/launch-plan", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("launch plan = %d body=%s", w.Code, w.Body.String())
	}
	var plan service.DurableAgentLaunchPolicy
	if err := json.NewDecoder(w.Body).Decode(&plan); err != nil {
		t.Fatalf("decode launch plan: %v", err)
	}
	if plan.SessionPolicy != service.DurableAgentSessionPolicyReuseLatestOrCreate {
		t.Fatalf("plan = %+v", plan)
	}

	startBody, _ := json.Marshal(DurableAgentStartRequest{})
	req = httptest.NewRequest("POST", "/api/durable-agents/"+inst.ID+"/start", bytes.NewReader(startBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("start = %d body=%s", w.Code, w.Body.String())
	}
	var started service.DurableAgentLaunchResult
	if err := json.NewDecoder(w.Body).Decode(&started); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if !started.CreatedSession || started.Session == nil || started.Instance.CurrentSessionID != started.Session.ID {
		t.Fatalf("started = %+v", started)
	}

	req = httptest.NewRequest("POST", "/api/durable-agents/"+inst.ID+"/resume", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("resume = %d body=%s", w.Code, w.Body.String())
	}
	var resumed service.DurableAgentLaunchResult
	if err := json.NewDecoder(w.Body).Decode(&resumed); err != nil {
		t.Fatalf("decode resume: %v", err)
	}
	if !resumed.ReusedSession || resumed.Session.ID != started.Session.ID {
		t.Fatalf("resumed = %+v started_session=%s", resumed, started.Session.ID)
	}

	req = httptest.NewRequest("POST", "/api/durable-agents/"+inst.ID+"/pause-request", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("pause request = %d body=%s", w.Code, w.Body.String())
	}
	var paused store.DurableAgentInstance
	if err := json.NewDecoder(w.Body).Decode(&paused); err != nil {
		t.Fatalf("decode pause: %v", err)
	}
	if paused.Status != store.DurableAgentStatusPaused || paused.CurrentSessionID != started.Session.ID {
		t.Fatalf("paused = %+v", paused)
	}

	req = httptest.NewRequest("POST", "/api/durable-agents/"+inst.ID+"/resume-request", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("resume request = %d body=%s", w.Code, w.Body.String())
	}
	var resumeRequested store.DurableAgentInstance
	if err := json.NewDecoder(w.Body).Decode(&resumeRequested); err != nil {
		t.Fatalf("decode resume request: %v", err)
	}
	if resumeRequested.Status != store.DurableAgentStatusActive || resumeRequested.CurrentSessionID != started.Session.ID {
		t.Fatalf("resumeRequested = %+v", resumeRequested)
	}

	req = httptest.NewRequest("POST", "/api/durable-agents/"+inst.ID+"/stop-request", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("stop request = %d body=%s", w.Code, w.Body.String())
	}
	var stopped store.DurableAgentInstance
	if err := json.NewDecoder(w.Body).Decode(&stopped); err != nil {
		t.Fatalf("decode stop: %v", err)
	}
	if stopped.Status != store.DurableAgentStatusStopped || stopped.CurrentSessionID != started.Session.ID {
		t.Fatalf("stopped = %+v", stopped)
	}
}

func TestDurableAgentsAPI_Wake(t *testing.T) {
	a, mux := newTestAPI(t)
	profile := &store.AgentProfile{Name: "Wake Profile", Slug: "wake-profile", SystemPrompt: "x"}
	if err := storetest.PriorAuthorizedActor(t.Context(), a.store, profile); err != nil {
		t.Fatalf("PriorAuthorizedActor: %v", err)
	}

	process := &store.DurableAgentInstance{
		Name:             "Wake Process",
		Slug:             "wake-process-api",
		ProfileID:        profile.ID,
		LifecycleClass:   store.DurableAgentClassProcess,
		Provider:         "anthropic",
		Model:            "model-a",
		RuntimeKind:      "api",
		LaunchSourceType: store.DurableAgentLaunchProcessTick,
	}
	persistPriorAPIInstance(t, a.store, process)
	scopeSession := &store.Session{Provider: "anthropic", Model: "model-a"}
	if err := a.store.CreateSession(context.Background(), scopeSession); err != nil {
		t.Fatalf("CreateSession scope: %v", err)
	}
	if err := a.store.AttachDurableAgentInstanceSession(context.Background(), process.ID, scopeSession.ID, store.DurableAgentSessionRelationWake); err != nil {
		t.Fatalf("AttachDurableAgentInstanceSession process: %v", err)
	}

	wakeBody, _ := json.Marshal(DurableAgentStartRequest{
		WakePayload: DurableAgentWakePayloadRequest{Reason: service.DurableAgentWakeManual},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/durable-agents/"+process.ID+"/wake", bytes.NewReader(wakeBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("wake = %d body=%s", w.Code, w.Body.String())
	}
	var wakeResult service.DurableAgentWakeResult
	if err := json.NewDecoder(w.Body).Decode(&wakeResult); err != nil {
		t.Fatalf("decode wake: %v", err)
	}
	if wakeResult.InstanceID != process.ID || wakeResult.WakeReason != service.DurableAgentWakeManual {
		t.Fatalf("wake result = %+v", wakeResult)
	}
	if wakeResult.Skipped || wakeResult.LaunchResult == nil || wakeResult.LaunchResult.Session == nil {
		t.Fatalf("wake result = %+v", wakeResult)
	}
}

func TestDurableAgentsAPI_ListEventsLimitAndCap(t *testing.T) {
	a, mux := newTestAPI(t)
	profile := &store.AgentProfile{Name: "Events Profile", Slug: "events-profile", SystemPrompt: "x"}
	if err := storetest.PriorAuthorizedActor(t.Context(), a.store, profile); err != nil {
		t.Fatalf("PriorAuthorizedActor: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:             "Events Instance",
		Slug:             "events-instance-api",
		ProfileID:        profile.ID,
		LaunchSourceType: store.DurableAgentLaunchAPIChat,
	}
	persistPriorAPIInstance(t, a.store, inst)
	for i := 0; i < 205; i++ {
		if err := a.store.CreateDurableAgentEvent(context.Background(), &store.DurableAgentEvent{
			InstanceID:   inst.ID,
			EventType:    store.DurableAgentEventUpdated,
			StatusBefore: store.DurableAgentStatusSleeping,
			StatusAfter:  store.DurableAgentStatusSleeping,
		}); err != nil {
			t.Fatalf("CreateDurableAgentEvent %d: %v", i, err)
		}
	}

	req := httptest.NewRequest("GET", "/api/durable-agents/"+inst.ID+"/events?limit=2", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("events limit = %d body=%s", w.Code, w.Body.String())
	}
	var limited []store.DurableAgentEvent
	if err := json.NewDecoder(w.Body).Decode(&limited); err != nil {
		t.Fatalf("decode limited events: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("limited len = %d, want 2", len(limited))
	}

	req = httptest.NewRequest("GET", "/api/durable-agents/"+inst.ID+"/events?limit=999", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("events cap = %d body=%s", w.Code, w.Body.String())
	}
	var capped []store.DurableAgentEvent
	if err := json.NewDecoder(w.Body).Decode(&capped); err != nil {
		t.Fatalf("decode capped events: %v", err)
	}
	if len(capped) != 200 {
		t.Fatalf("capped len = %d, want 200", len(capped))
	}

	req = httptest.NewRequest("GET", "/api/durable-agents/missing/events", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing events = %d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/durable-agents/"+inst.ID+"/events?limit=bad", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad limit = %d body=%s", w.Code, w.Body.String())
	}
}

func TestDurableAgentsAPI_UnsupportedLaunchPolicy(t *testing.T) {
	a, mux := newTestAPI(t)
	profile := &store.AgentProfile{Name: "PTY Profile", Slug: "pty-profile", SystemPrompt: "x"}
	if err := storetest.PriorAuthorizedActor(t.Context(), a.store, profile); err != nil {
		t.Fatalf("PriorAuthorizedActor: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:             "PTY Instance",
		Slug:             "pty-instance-api",
		ProfileID:        profile.ID,
		RuntimeKind:      "pty",
		LaunchSourceType: store.DurableAgentLaunchCLIHarness,
	}
	persistPriorAPIInstance(t, a.store, inst)
	req := httptest.NewRequest("GET", "/api/durable-agents/"+inst.ID+"/launch-plan", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unsupported launch plan = %d body=%s", w.Code, w.Body.String())
	}
}

// A request for a known host projection still cannot issue an actor or an
// instance. These private fixtures distinguish retained history and already
// authorized instances from the unsupported production enrollment port.
func TestDurableAgentsAPI_CreateRequiresVerifiedAuthority(t *testing.T) {
	a, mux := newTestAPI(t)
	profile := &store.AgentProfile{Name: "Prior actor", Slug: "prior-create-control"}
	if err := storetest.PriorAuthorizedActor(t.Context(), a.store, profile); err != nil {
		t.Fatal(err)
	}
	prior := &store.DurableAgentInstance{Name: "Prior instance", Slug: "prior-instance-control", ProfileID: profile.ID}
	persistPriorAPIInstance(t, a.store, prior)
	seedDurableAPIHistory(t, a)
	var hostID string
	if err := a.store.DB.QueryRowContext(t.Context(), `SELECT host_settings_id FROM agent_actor_bindings WHERE actor_uri=?`, profile.ID).Scan(&hostID); err != nil {
		t.Fatal(err)
	}
	before := durableAPIRefusalState(t, a)
	claimed := &store.DurableAgentInstance{Name: "Claimed", Slug: "claimed-create", ProfileID: profile.ID, URN: profile.ID}
	if err := a.Services.DurableAgents.Create(t.Context(), claimed); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("production Create = %v, want verified-authority refusal", err)
	}
	if err := a.store.CreateDurableAgentInstance(t.Context(), claimed); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("production store Create = %v, want verified-authority refusal", err)
	}
	for _, id := range []string{"claimed-instance", prior.ID} {
		body, err := json.Marshal(CreateDurableAgentRequest{ID: id, Name: "Claimed", Slug: prior.Slug, ProfileID: hostID})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/durable-agents", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), store.ErrVerifiedActorRequired.Error()) {
			t.Fatalf("production POST = %d body=%s", w.Code, w.Body.String())
		}
		assertDurableAPIRefusalState(t, a, before)
	}
	if claimed.ID != "" || !claimed.CreatedAt.IsZero() {
		t.Fatalf("refused Create issued identity: %+v", claimed)
	}
	if _, err := os.Stat(filepath.Join(a.Services.WorkingDir, ".nanite", "durable-agents")); !os.IsNotExist(err) {
		t.Fatalf("refused Create produced filesystem projection: %v", err)
	}
}

func seedDurableAPIHistory(t *testing.T, a *testAPI) {
	t.Helper()
	profile := retiredAgentHistory(t, a, "durable-api-history")
	if _, err := a.store.DB.ExecContext(t.Context(), `INSERT INTO durable_agent_instances(id,name,slug,profile_id,urn) VALUES(?,?,?,?,?)`, "historical-api-instance", "Historical instance", "historical-api-instance", profile.ID, "msg://agent/historical/unverified"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.DB.ExecContext(t.Context(), `INSERT INTO durable_agent_events(id,instance_id,event_type,message) VALUES(?,?,?,?)`, "historical-api-event", "historical-api-instance", "updated", "retained historical event"); err != nil {
		t.Fatal(err)
	}
}

func durableAPIRefusalState(t *testing.T, a *testAPI) map[string][][]any {
	t.Helper()
	state := retiredAgentState(t, a)
	for _, table := range []string{"durable_agent_instances", "durable_agent_instance_sessions", "durable_agent_events", "sessions"} {
		rows, err := a.store.DB.QueryContext(t.Context(), fmt.Sprintf(`SELECT * FROM %q ORDER BY rowid`, table))
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		state[table] = make([][]any, 0)
		for rows.Next() {
			values := make([]any, len(columns))
			targets := make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatal(err)
			}
			for i, value := range values {
				if raw, ok := value.([]byte); ok {
					values[i] = string(raw)
				}
			}
			state[table] = append(state[table], values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return state
}

func assertDurableAPIRefusalState(t *testing.T, a *testAPI, before map[string][][]any) {
	t.Helper()
	after := durableAPIRefusalState(t, a)
	for table, rows := range before {
		if !reflect.DeepEqual(rows, after[table]) {
			t.Fatalf("refusal changed %s rows", table)
		}
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("refusal changed stored graph")
	}
}
