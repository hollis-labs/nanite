package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

type harnessChatStub struct {
	handleMessageFn          func(context.Context, string, string) (string, error)
	cancelActiveGenerationFn func(string) bool
}

func (s *harnessChatStub) HandleMessage(ctx context.Context, sessionID, content string) (string, error) {
	return s.handleMessageFn(ctx, sessionID, content)
}
func (s *harnessChatStub) RetryLastMessage(context.Context, string) (string, error) {
	panic("RetryLastMessage not used in harness tests")
}
func (s *harnessChatStub) SendAgentMessage(context.Context, string, string, string) (string, error) {
	panic("SendAgentMessage not used in harness tests")
}
func (s *harnessChatStub) DelegateTask(context.Context, chat.DelegationRequest) (*chat.DelegationResult, error) {
	panic("DelegateTask not used in harness tests")
}
func (s *harnessChatStub) DelegateAndAggregate(context.Context, string, string, string) (*chat.OrchestrationResult, error) {
	panic("DelegateAndAggregate not used in harness tests")
}
func (s *harnessChatStub) GetStream(string) (<-chan chat.StreamEvent, bool) {
	panic("GetStream not used in harness tests")
}
func (s *harnessChatStub) CancelActiveGeneration(sessionID string) bool {
	return s.cancelActiveGenerationFn(sessionID)
}
func (s *harnessChatStub) RebootSessionAgent(context.Context, string) (service.RebootResult, error) {
	panic("RebootSessionAgent not used in harness tests")
}
func (s *harnessChatStub) RecoverSession(context.Context, string) (service.RebootResult, error) {
	panic("RecoverSession not used in harness tests")
}
func (s *harnessChatStub) Shutdown() {}

func TestHarnessV1InitializeAndCapabilities(t *testing.T) {
	_, mux := newTestAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/harness/v1/initialize", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("initialize = %d body=%s", w.Code, w.Body.String())
	}
	var initResp harnessV1InitializeResponse
	if err := json.NewDecoder(w.Body).Decode(&initResp); err != nil {
		t.Fatalf("decode initialize: %v", err)
	}
	if initResp.ProtocolVersion != "v1" || initResp.RoutePrefix != harnessV1RoutePrefix {
		t.Fatalf("initialize = %+v", initResp)
	}
	if initResp.PermissionRequests.SupportLevel != "approval_request_stream_event" {
		t.Fatalf("permission support = %+v", initResp.PermissionRequests)
	}
	if !containsString(initResp.SupportedEventTypes, "approval_request") {
		t.Fatalf("supported events = %+v", initResp.SupportedEventTypes)
	}
	if !containsString(initResp.Operations, "sessions.recover") {
		t.Fatalf("operations should advertise sessions.recover: %+v", initResp.Operations)
	}
	if initResp.RouteHints.SessionRecover == "" {
		t.Fatalf("route hints should include session_recover: %+v", initResp.RouteHints)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/harness/v1/capabilities", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("capabilities = %d body=%s", w.Code, w.Body.String())
	}
	var caps harnessV1CapabilitiesResponse
	if err := json.NewDecoder(w.Body).Decode(&caps); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if !containsEnum(caps.SessionActivityStates, "pending_action") {
		t.Fatalf("session activity states = %+v", caps.SessionActivityStates)
	}
	if !containsEnum(caps.SupportedEventTypes, "plugin_envelope") {
		t.Fatalf("supported event types = %+v", caps.SupportedEventTypes)
	}
	if !containsString(caps.SessionCreateFields.Unsupported, "runtime_kind") {
		t.Fatalf("session create fields = %+v", caps.SessionCreateFields)
	}
}

func TestHarnessV1CreateAndLoadSession(t *testing.T) {
	a, mux := newTestAPI(t)
	agent := &store.AgentProfile{Name: "Harness Agent", Slug: "harness-agent", SystemPrompt: "x"}
	if err := a.Services.Store.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	body, _ := json.Marshal(harnessV1CreateSessionRequest{
		Provider: "anthropic",
		Model:    "claude-sonnet-4",
		AgentID:  agent.ID,
		Title:    "Harness Session",
		Metadata: map[string]any{"client": "external"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/harness/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session = %d body=%s", w.Code, w.Body.String())
	}
	var created harnessV1SessionResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Session == nil || created.Session.Title != "Harness Session" {
		t.Fatalf("created session = %+v", created)
	}
	if created.Details.Session.ID != created.Session.ID || created.Details.ActivityState != "idle" {
		t.Fatalf("created details = %+v", created.Details)
	}
	if !strings.Contains(created.RouteHints.Events, created.Session.ID) {
		t.Fatalf("route hints = %+v", created.RouteHints)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/harness/v1/sessions/"+created.Session.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get session = %d body=%s", w.Code, w.Body.String())
	}
	var loaded harnessV1SessionResponse
	if err := json.NewDecoder(w.Body).Decode(&loaded); err != nil {
		t.Fatalf("decode load: %v", err)
	}
	if loaded.Details.Session.Metadata == "" || loaded.Details.PrimaryAgent == nil || loaded.Details.PrimaryAgent.ID != agent.ID {
		t.Fatalf("loaded details = %+v", loaded.Details)
	}
}

func TestHarnessV1CreateSessionRejectsUnsupportedRuntime(t *testing.T) {
	_, mux := newTestAPI(t)
	body, _ := json.Marshal(harnessV1CreateSessionRequest{
		RuntimeKind: "pty",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/harness/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unsupported runtime_kind = %d body=%s", w.Code, w.Body.String())
	}
}

func TestHarnessV1TurnCancelAndEvents(t *testing.T) {
	a, mux := newTestAPI(t)
	sess := &store.Session{Provider: "anthropic", Model: "claude-sonnet-4", Status: "active"}
	if err := a.Services.Store.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	var gotSessionID, gotContent string
	a.Services.Chat = &harnessChatStub{
		handleMessageFn: func(_ context.Context, sessionID, content string) (string, error) {
			gotSessionID, gotContent = sessionID, content
			return "msg-harness", nil
		},
		cancelActiveGenerationFn: func(sessionID string) bool {
			return sessionID == sess.ID
		},
	}

	body, _ := json.Marshal(harnessV1TurnRequest{Content: "hello harness", CycleKind: "wake", Effort: "high"})
	req := httptest.NewRequest(http.MethodPost, "/api/harness/v1/sessions/"+sess.ID+"/turns", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("turn send = %d body=%s", w.Code, w.Body.String())
	}
	var turnResp harnessV1TurnResponse
	if err := json.NewDecoder(w.Body).Decode(&turnResp); err != nil {
		t.Fatalf("decode turn: %v", err)
	}
	if gotSessionID != sess.ID || gotContent != "hello harness" {
		t.Fatalf("handleMessage got session=%q content=%q", gotSessionID, gotContent)
	}
	if turnResp.StreamURL != harnessV1RoutePrefix+"/sessions/"+sess.ID+"/events?message_id=msg-harness" || turnResp.InitialActivityState != "working" {
		t.Fatalf("turn response = %+v", turnResp)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/harness/v1/sessions/"+sess.ID+"/cancel", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("cancel = %d body=%s", w.Code, w.Body.String())
	}
	var cancelResp harnessV1CancelResponse
	if err := json.NewDecoder(w.Body).Decode(&cancelResp); err != nil {
		t.Fatalf("decode cancel: %v", err)
	}
	if cancelResp.Status != "cancelled" {
		t.Fatalf("cancel response = %+v", cancelResp)
	}

	a.Services.Chat = &harnessChatStub{
		handleMessageFn:          func(context.Context, string, string) (string, error) { return "msg-harness", nil },
		cancelActiveGenerationFn: func(string) bool { return false },
	}
	req = httptest.NewRequest(http.MethodPost, "/api/harness/v1/sessions/"+sess.ID+"/cancel", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("idle cancel = %d body=%s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&cancelResp); err != nil {
		t.Fatalf("decode idle cancel: %v", err)
	}
	if cancelResp.Status != "idle" {
		t.Fatalf("idle cancel response = %+v", cancelResp)
	}

	if err := a.Services.Store.CreateMessage(&store.Message{
		ID:        "msg-stream",
		SessionID: sess.ID,
		Role:      "assistant",
		Content:   "existing",
	}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	stream := a.Services.Streams.CreateStream("msg-stream", sess.ID)
	stream <- chat.StreamEvent{Type: "status", Content: "warming up"}
	close(stream)

	req = httptest.NewRequest(http.MethodGet, "/api/harness/v1/sessions/"+sess.ID+"/events?message_id=msg-stream", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("events = %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "event: status") {
		t.Fatalf("events body = %s", w.Body.String())
	}
}

func TestHarnessV1DurableWrappers(t *testing.T) {
	a, mux := newTestAPI(t)
	profile := &store.AgentProfile{Name: "Harness Durable", Slug: "harness-durable", SystemPrompt: "x"}
	if err := a.Services.Store.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	advisor := &store.DurableAgentInstance{
		Name:             "Advisor",
		Slug:             "advisor-harness",
		ProfileID:        profile.ID,
		Provider:         "anthropic",
		Model:            "model-a",
		RuntimeKind:      "api",
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
	}
	if err := a.Services.Store.CreateDurableAgentInstance(advisor); err != nil {
		t.Fatalf("CreateDurableAgentInstance advisor: %v", err)
	}
	process := &store.DurableAgentInstance{
		Name:             "Process",
		Slug:             "process-harness",
		ProfileID:        profile.ID,
		LifecycleClass:   store.DurableAgentClassProcess,
		Provider:         "anthropic",
		Model:            "model-a",
		RuntimeKind:      "api",
		LaunchSourceType: store.DurableAgentLaunchProcessTick,
	}
	if err := a.Services.Store.CreateDurableAgentInstance(process); err != nil {
		t.Fatalf("CreateDurableAgentInstance process: %v", err)
	}
	scopeSession := &store.Session{Provider: "anthropic", Model: "model-a"}
	if err := a.Services.Store.CreateSession(scopeSession); err != nil {
		t.Fatalf("CreateSession scope: %v", err)
	}
	if err := a.Services.Store.AttachDurableAgentInstanceSession(process.ID, scopeSession.ID, store.DurableAgentSessionRelationWake); err != nil {
		t.Fatalf("AttachDurableAgentInstanceSession process: %v", err)
	}

	startBody, _ := json.Marshal(DurableAgentStartRequest{})
	req := httptest.NewRequest(http.MethodPost, "/api/harness/v1/durable-agents/"+advisor.ID+"/start", bytes.NewReader(startBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("durable start = %d body=%s", w.Code, w.Body.String())
	}
	var started service.DurableAgentLaunchResult
	if err := json.NewDecoder(w.Body).Decode(&started); err != nil {
		t.Fatalf("decode durable start: %v", err)
	}
	if started.Session == nil || started.Instance.CurrentSessionID == "" {
		t.Fatalf("started = %+v", started)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/harness/v1/durable-agents/"+advisor.ID+"/resume", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("durable resume = %d body=%s", w.Code, w.Body.String())
	}
	var resumed service.DurableAgentLaunchResult
	if err := json.NewDecoder(w.Body).Decode(&resumed); err != nil {
		t.Fatalf("decode durable resume: %v", err)
	}
	if !resumed.ReusedSession || resumed.Session == nil || resumed.Session.ID != started.Session.ID {
		t.Fatalf("resumed = %+v started=%+v", resumed, started)
	}

	wakeBody, _ := json.Marshal(DurableAgentStartRequest{
		WakePayload: DurableAgentWakePayloadRequest{Reason: service.DurableAgentWakeManual},
	})
	req = httptest.NewRequest(http.MethodPost, "/api/harness/v1/durable-agents/"+process.ID+"/wake", bytes.NewReader(wakeBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("durable wake = %d body=%s", w.Code, w.Body.String())
	}
	var wakeResult service.DurableAgentWakeResult
	if err := json.NewDecoder(w.Body).Decode(&wakeResult); err != nil {
		t.Fatalf("decode durable wake: %v", err)
	}
	if wakeResult.Skipped || wakeResult.LaunchResult == nil || wakeResult.LaunchResult.Session == nil {
		t.Fatalf("wake result = %+v", wakeResult)
	}
}

// TestHarnessV1RecoverSession verifies the harness-v1 recover route (Slice 5):
// it arms session recovery and 404s an unknown session.
func TestHarnessV1RecoverSession(t *testing.T) {
	a, mux := newTestAPI(t)
	sess := &store.Session{Provider: "anthropic", Model: "claude-sonnet-4", Status: "active"}
	if err := a.Services.Store.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/harness/v1/sessions/"+sess.ID+"/recover", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("recover = %d body=%s", w.Code, w.Body.String())
	}
	var resp harnessV1RecoverResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode recover: %v", err)
	}
	// No live runtime in a fresh test process → recovery is a no-op that still
	// leaves the next turn to cold-boot into auto-recovery.
	if resp.SessionID != sess.ID || resp.Recovered || resp.Status != "no_active_agent" {
		t.Fatalf("recover response = %+v", resp)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/harness/v1/sessions/does-not-exist/recover", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("recover unknown session = %d body=%s", w.Code, w.Body.String())
	}
}

// TestHarnessV1CreateSessionAgentResolution verifies that -agent flag resolution
// (CW-20260815-0026) works correctly for both slugs and IDs, and fails loudly
// when the agent doesn't exist instead of silently falling back to default.
func TestHarnessV1CreateSessionAgentResolution(t *testing.T) {
	a, mux := newTestAPI(t)

	// Create a test agent with known ID and slug
	agent := &store.AgentProfile{
		Name:         "Test Agent",
		Slug:         "test-agent",
		SystemPrompt: "test prompt",
	}
	if err := a.Services.Store.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// Test 1: Resolution by ID should work
	body, _ := json.Marshal(harnessV1CreateSessionRequest{
		AgentID: agent.ID,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/harness/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session by ID: got %d, want %d; body=%s", w.Code, http.StatusCreated, w.Body.String())
	}
	var created1 harnessV1SessionResponse
	if err := json.NewDecoder(w.Body).Decode(&created1); err != nil {
		t.Fatalf("decode create by ID: %v", err)
	}
	if created1.Details.PrimaryAgent == nil || created1.Details.PrimaryAgent.ID != agent.ID {
		t.Fatalf("session agent by ID: got %+v, want ID=%s", created1.Details.PrimaryAgent, agent.ID)
	}

	// Test 2: Resolution by slug should work
	body, _ = json.Marshal(harnessV1CreateSessionRequest{
		AgentID: "test-agent", // slug, not ID
	})
	req = httptest.NewRequest(http.MethodPost, "/api/harness/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session by slug: got %d, want %d; body=%s", w.Code, http.StatusCreated, w.Body.String())
	}
	var created2 harnessV1SessionResponse
	if err := json.NewDecoder(w.Body).Decode(&created2); err != nil {
		t.Fatalf("decode create by slug: %v", err)
	}
	if created2.Details.PrimaryAgent == nil || created2.Details.PrimaryAgent.ID != agent.ID {
		t.Fatalf("session agent by slug: got %+v, want ID=%s", created2.Details.PrimaryAgent, agent.ID)
	}

	// Test 3: Nonexistent agent should fail loudly (not silently fall back)
	body, _ = json.Marshal(harnessV1CreateSessionRequest{
		AgentID: "does-not-exist",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/harness/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create session with bogus agent: got %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	bodyStr := w.Body.String()
	if !strings.Contains(bodyStr, "does-not-exist") || !strings.Contains(bodyStr, "not found") {
		t.Fatalf("error message should mention agent not found, got: %s", bodyStr)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
