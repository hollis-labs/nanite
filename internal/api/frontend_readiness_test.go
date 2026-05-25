package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestStartSurfaceCapabilities(t *testing.T) {
	a, mux := newTestAPI(t)
	profile := &store.AgentProfile{Name: "Start Surface Agent", Slug: "start-surface-agent", SystemPrompt: "x"}
	if err := a.Services.Store.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:             "Start Surface Durable",
		Slug:             "start-surface-durable",
		ProfileID:        profile.ID,
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
	}
	if err := a.Services.Store.CreateDurableAgentInstance(inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/start-surface/capabilities", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("capabilities = %d body=%s", w.Code, w.Body.String())
	}
	var body startSurfaceCapabilities
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if body.SchemaVersion != 1 || len(body.Recipes) < 10 || len(body.RuntimeKinds) == 0 {
		t.Fatalf("capabilities = %+v", body)
	}
	if len(body.Recipes[0].Inputs) == 0 {
		t.Fatalf("recipe inputs missing from capabilities: %+v", body.Recipes[0])
	}
	if !containsEnum(body.LifecycleClasses, store.DurableAgentClassAdvisor) ||
		!containsEnum(body.AttachmentRelations, store.DurableAgentSessionRelationWake) {
		t.Fatalf("missing canonical enums: %+v %+v", body.LifecycleClasses, body.AttachmentRelations)
	}
	if !containsDurableAgent(body.DurableAgents, inst.ID) {
		t.Fatalf("durable agents = %+v", body.DurableAgents)
	}
}

func containsDurableAgent(items []store.DurableAgentInstance, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func TestSessionDetailsContract(t *testing.T) {
	a, mux := newTestAPI(t)
	profile := &store.AgentProfile{Name: "Details Agent", Slug: "details-agent", SystemPrompt: "x"}
	if err := a.Services.Store.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	sess := &store.Session{WorkspaceID: "", Provider: "anthropic", Model: "model-a"}
	if err := a.Services.Store.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := a.Services.Store.EnsureSessionAgent(sess.ID, profile.ID, "default", true); err != nil {
		t.Fatalf("EnsureSessionAgent: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:             "Details Durable",
		Slug:             "details-durable",
		ProfileID:        profile.ID,
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
	}
	if err := a.Services.Store.CreateDurableAgentInstance(inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}
	if err := a.Services.Store.AttachDurableAgentInstanceSession(inst.ID, sess.ID, store.DurableAgentSessionRelationPrimary); err != nil {
		t.Fatalf("AttachDurableAgentInstanceSession: %v", err)
	}
	if err := a.Services.Store.CreateDurableAgentEvent(&store.DurableAgentEvent{
		InstanceID:   inst.ID,
		EventType:    store.DurableAgentEventStartSucceeded,
		StatusBefore: store.DurableAgentStatusStarting,
		StatusAfter:  store.DurableAgentStatusActive,
		SessionID:    sess.ID,
		Message:      "Session attached and ready.",
	}); err != nil {
		t.Fatalf("CreateDurableAgentEvent: %v", err)
	}
	if err := a.Services.Store.CreateAgentRuntimeRow(&store.AgentRuntimeRow{
		ID:              "runtime-a",
		AgentProfile:    profile.ID,
		Provider:        "claude",
		RuntimeKind:     "streaming-stdio",
		Mode:            "long_lived",
		Workdir:         "/tmp/boot",
		State:           "running",
		PID:             1234,
		ParentSessionID: sess.ID,
		StartedAt:       time.Now(),
	}); err != nil {
		t.Fatalf("CreateAgentRuntimeRow: %v", err)
	}
	if err := a.Services.Store.RecordUsage(sess.ID, "message-1", "model-a", 10, 12, 0, 0, 3); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/sessions/"+sess.ID+"/details", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("details = %d body=%s", w.Code, w.Body.String())
	}
	var details sessionDetailsResponse
	if err := json.NewDecoder(w.Body).Decode(&details); err != nil {
		t.Fatalf("decode details: %v", err)
	}
	if details.Session.ID != sess.ID || details.PrimaryAgent == nil || details.PrimaryAgent.ID != profile.ID {
		t.Fatalf("details session/agent = %+v", details)
	}
	if details.CurrentDurableAgent == nil || details.CurrentDurableAgent.ID != inst.ID {
		t.Fatalf("durable details = %+v", details.CurrentDurableAgent)
	}
	if details.Runtime.State != "running" || details.Runtime.RuntimeKind != "streaming-stdio" || details.Runtime.PID != 1234 {
		t.Fatalf("runtime details = %+v", details.Runtime)
	}
	if details.ActivityState != "online" || details.Usage == nil || details.Usage.TotalTokens != 22 {
		t.Fatalf("observability details = %+v", details)
	}
	if len(details.RecentDurableEvents) != 1 || details.RecentDurableEvents[0].EventType != store.DurableAgentEventStartSucceeded {
		t.Fatalf("recent durable events = %+v", details.RecentDurableEvents)
	}
	if details.BootSource != "api_default" || len(details.ImmutableStartFields) == 0 {
		t.Fatalf("boot/immutability = %q %+v", details.BootSource, details.ImmutableStartFields)
	}
}

func TestSessionDetailsContract_NoRuntimeAndHalted(t *testing.T) {
	a, mux := newTestAPI(t)
	sess := &store.Session{Provider: "anthropic", Model: "model-a", Status: "active"}
	if err := a.Services.Store.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := a.Services.Store.MarkSessionHalted(sess.ID, "detector_B: repeated empty output"); err != nil {
		t.Fatalf("MarkSessionHalted: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/sessions/"+sess.ID+"/details", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("details = %d body=%s", w.Code, w.Body.String())
	}

	var details sessionDetailsResponse
	if err := json.NewDecoder(w.Body).Decode(&details); err != nil {
		t.Fatalf("decode details: %v", err)
	}
	if details.Runtime.State != "none" || !details.Halt.IsHalted || details.ActivityState != "halted" {
		t.Fatalf("halted details = %+v", details)
	}
	if details.Usage != nil || len(details.RecentDurableEvents) != 0 {
		t.Fatalf("unexpected usage/events = %+v %+v", details.Usage, details.RecentDurableEvents)
	}
}

func TestUpdateSessionProviderModelImmutableAfterMessages(t *testing.T) {
	a, mux := newTestAPI(t)
	sess := &store.Session{Provider: "anthropic", Model: "model-a"}
	if err := a.Services.Store.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := a.Services.Store.CreateMessage(&store.Message{SessionID: sess.ID, Role: "user", Content: "hello"}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	body, _ := json.Marshal(UpdateSessionRequest{Provider: ptrString("openai")})
	req := httptest.NewRequest("PUT", "/api/sessions/"+sess.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("provider mutation = %d body=%s", w.Code, w.Body.String())
	}
	body, _ = json.Marshal(UpdateSessionRequest{Model: ptrString("model-b")})
	req = httptest.NewRequest("PUT", "/api/sessions/"+sess.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("model mutation = %d body=%s", w.Code, w.Body.String())
	}
}

func containsEnum(options []enumOption, value string) bool {
	for _, option := range options {
		if option.Value == value {
			return true
		}
	}
	return false
}

func ptrString(v string) *string {
	return &v
}
