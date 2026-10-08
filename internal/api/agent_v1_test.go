package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

type agentChatStub struct {
	handleMessageFn          func(context.Context, string, string) (string, error)
	cancelActiveGenerationFn func(string) bool
}

func (s *agentChatStub) HandleMessage(ctx context.Context, sessionID, content string) (string, error) {
	return s.handleMessageFn(ctx, sessionID, content)
}
func (s *agentChatStub) SubmitCognitiveTurn(ctx context.Context, viewID, content string) (string, error) {
	return s.HandleMessage(ctx, viewID, content)
}
func (s *agentChatStub) RetryLastMessage(context.Context, string) (string, error) {
	panic("RetryLastMessage not used in agent API tests")
}
func (s *agentChatStub) SendAgentMessage(context.Context, string, string, string) (string, error) {
	panic("SendAgentMessage not used in agent API tests")
}
func (s *agentChatStub) DelegateTask(context.Context, chat.DelegationRequest) (*chat.DelegationResult, error) {
	panic("DelegateTask not used in agent API tests")
}
func (s *agentChatStub) DelegateAndAggregate(context.Context, string, string, string) (*chat.OrchestrationResult, error) {
	panic("DelegateAndAggregate not used in agent API tests")
}
func (s *agentChatStub) GetStream(string) (<-chan chat.StreamEvent, bool) {
	panic("GetStream not used in agent API tests")
}
func (s *agentChatStub) CancelActiveGeneration(sessionID string) bool {
	return s.cancelActiveGenerationFn(sessionID)
}
func (s *agentChatStub) RebootSessionAgent(context.Context, string) (service.RebootResult, error) {
	panic("RebootSessionAgent not used in agent API tests")
}
func (s *agentChatStub) RecoverSession(context.Context, string) (service.RebootResult, error) {
	panic("RecoverSession not used in agent API tests")
}
func (s *agentChatStub) Shutdown() error { return nil }

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func allowTestNativeModel(a *testAPI) {
	a.Services.CognitiveViews.Models = service.ModelAuthorizerFunc(func(context.Context, service.DefinitionRef, *service.ModelSelection) (service.ModelSelection, error) {
		return service.ModelSelection{Provider: "fixture", Model: "fixture-model"}, nil
	})
}
func TestAgentV1InitializeAndCapabilities(t *testing.T) {
	_, mux := newTestAPI(t)
	for _, path := range []string{"initialize", "capabilities"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", agentV1RoutePrefix+"/"+path, nil))
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["session_stream_supported"] != false {
			t.Fatal("session stream claimed")
		}
		if strings.Contains(w.Body.String(), "session_takeover") || strings.Contains(w.Body.String(), "message_id={") {
			t.Fatal("legacy discovery")
		}
		if !strings.Contains(w.Body.String(), "chatstream/v1") || !strings.Contains(w.Body.String(), "definition_ref") {
			t.Fatal("missing implemented contract")
		}
	}
}
func TestAgentV1CreateAndLoadSession(t *testing.T) {
	a, mux := newTestAPI(t)
	allowTestNativeModel(a)
	pin := a.Services.CognitiveViews.DefaultDefinitionRef
	body, _ := json.Marshal(agentV1CreateSessionRequest{DefinitionRef: pin, Title: "Pinned view", Metadata: map[string]any{"agent_id": "no authority", "harness_profile": "yolo"}})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", agentV1RoutePrefix+"/sessions", bytes.NewReader(body)))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var created agentV1SessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.DefinitionRef == nil || *created.DefinitionRef != pin || created.Session.Provider != "fixture" || created.Session.Title != "Pinned view" {
		t.Fatalf("%+v", created)
	}
	var actors int
	if err := a.store.DB.QueryRow(`SELECT COUNT(*) FROM durable_agent_instances`).Scan(&actors); err != nil {
		t.Fatal(err)
	}
	if actors != 0 {
		t.Fatal("cognitive create enrolled an actor")
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", created.RouteHints.Self, nil))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var loaded agentV1SessionResponse
	_ = json.Unmarshal(w.Body.Bytes(), &loaded)
	if loaded.DefinitionRef == nil || *loaded.DefinitionRef != pin || loaded.HostSubject != nil || loaded.CurrentTurnID != nil {
		t.Fatalf("%+v", loaded)
	}
}
func TestAgentV1CreateRefusalsPrecedeRows(t *testing.T) {
	a, mux := newTestAPI(t)
	allowTestNativeModel(a)
	pin := a.Services.CognitiveViews.DefaultDefinitionRef
	good, _ := json.Marshal(agentV1CreateSessionRequest{DefinitionRef: pin})
	mismatch := pin
	mismatch.SemanticDigest = "sha256:" + strings.Repeat("a", 64)
	wrong, _ := json.Marshal(agentV1CreateSessionRequest{DefinitionRef: mismatch})
	cases := []struct {
		body   []byte
		status int
	}{{[]byte(`{}`), 400}, {[]byte(`{"agent_id":"retained"}`), 400}, {[]byte(`{"runtime_kind":"pty"}`), 400}, {wrong, 409}}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", agentV1RoutePrefix+"/sessions", bytes.NewReader(tc.body)))
		if w.Code != tc.status {
			t.Fatalf("body=%s status=%d response=%s", tc.body, w.Code, w.Body.String())
		}
	}
	a.Services.CognitiveViews.Models = service.ModelAuthorizerFunc(func(context.Context, service.DefinitionRef, *service.ModelSelection) (service.ModelSelection, error) {
		return service.ModelSelection{}, service.ErrUnsupportedModel
	})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", agentV1RoutePrefix+"/sessions", bytes.NewReader(good)))
	if w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
	var views int
	_ = a.store.DB.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&views)
	if views != 0 {
		t.Fatal("rejected create wrote views", views)
	}
}
func TestAgentV1SendTurnAndRemovedRoutes(t *testing.T) {
	a, mux := newTestAPI(t)
	sess := &store.Session{Provider: "fixture", Model: "model"}
	if err := a.store.CreateSession(t.Context(), sess); err != nil {
		t.Fatal(err)
	}
	var content string
	a.Services.Chat = &agentChatStub{handleMessageFn: func(_ context.Context, id, text string) (string, error) {
		if id != sess.ID {
			t.Fatal(id)
		}
		content = text
		return "turn-1", nil
	}}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", agentV1RoutePrefix+"/sessions/"+sess.ID+"/turns", strings.NewReader(`{"content":[{"kind":"text","text":"a"},{"kind":"text","text":"b"}],"delivery":"at_idle","effort":"high","delta_mode":"live"}`)))
	if w.Code != 202 || content != "ab" {
		t.Fatal(w.Code, w.Body.String(), content)
	}
	var turn agentV1TurnResponse
	_ = json.Unmarshal(w.Body.Bytes(), &turn)
	if turn.TurnID != "turn-1" || turn.RunID != "turn-1" || turn.MessageID != "turn-1" || turn.Links.Events != turn.StreamURL || turn.InitialActivityState != "submitted" {
		t.Fatalf("%+v", turn)
	}
	for _, tc := range []struct{ method, path string }{{"POST", agentV1RoutePrefix + "/sessions/" + sess.ID + "/cancel"}, {"GET", agentV1RoutePrefix + "/sessions/" + sess.ID + "/events"}, {"POST", "/api/messages"}, {"GET", "/api/harness/v1/initialize"}} {
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != 404 && w.Code != 405 {
			t.Fatalf("removed %s %s=%d", tc.method, tc.path, w.Code)
		}
	}
}
