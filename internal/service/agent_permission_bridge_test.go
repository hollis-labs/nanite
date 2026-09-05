package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/permission"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
)

func TestBuildAgentDependencies_ACPApprovalUsesExistingSessionStream(t *testing.T) {
	streams := &StreamManager{}
	producer := streams.CreateStream("message-1", "session-1")
	t.Cleanup(func() { close(producer) })
	subscriber, _, found := streams.Subscribe("message-1", 0)
	if !found {
		t.Fatal("message stream not found")
	}

	engine := permission.NewEngine(permission.ModeDefault, nil)
	bundle, err := BuildAgentDependencies(AgentDepsConfig{
		Store:       newConfigTestStore(t),
		Streams:     streams,
		Permissions: engine,
		BinaryPath:  "/bin/true",
	})
	if err != nil {
		t.Fatalf("BuildAgentDependencies: %v", err)
	}
	if bundle.Deps.Permissions != engine || bundle.Deps.ApprovalRequestSink == nil {
		t.Fatal("existing permission engine and approval stream sink were not wired")
	}

	req := engine.RequestApproval("session-1", "shell", map[string]any{"command": "pwd"}, "provider requested permission")
	t.Cleanup(func() {
		engine.Respond(req.ID, permission.DecisionDeny, permission.ScopeOnce, "session-1")
		_ = engine.WaitForApproval(t.Context(), req)
	})
	bundle.Deps.ApprovalRequestSink(req)

	select {
	case event := <-subscriber:
		if event.Type != "approval_request" {
			t.Fatalf("event type = %q", event.Type)
		}
		var payload chat.ApprovalRequestPayload
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload.RequestID != req.ID || payload.Tool != "shell" || payload.Input["command"] != "pwd" {
			t.Fatalf("approval payload = %+v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approval_request was not delivered to the existing session SSE stream")
	}
}

func TestNewChatService_ReusesAgentDependenciesSessionManager(t *testing.T) {
	manager := runtimeagent.NewSessionManager()
	svc := NewChatService(ChatServiceConfig{
		AgentDeps: &runtimeagent.Dependencies{Manager: manager},
	})
	impl, ok := svc.(*chatServiceImpl)
	if !ok {
		t.Fatalf("service type = %T", svc)
	}
	if got := impl.runtimeSessions(); got != manager {
		t.Fatalf("chat manager = %p, dependency manager = %p", got, manager)
	}
}
