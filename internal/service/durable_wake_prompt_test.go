package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestDurableAgentStartDeliversWakePromptAsUserTurn covers CW-20260814-0013:
// DurableAgentWakePayload.Prompt must reach the launched session as a real
// user turn (via the runtime controller's SendMessage), not just sit in
// session metadata.
func TestDurableAgentStartDeliversWakePromptAsUserTurn(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Wake Prompt Agent", Slug: "wake-prompt-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	runtime := &fakeDurableRuntimeController{}
	svc := NewDurableAgentServiceWithRuntime(st, runtime)
	inst := &store.DurableAgentInstance{
		Name:             "Wake Prompt Instance",
		Slug:             "wake-prompt-instance",
		ProfileID:        profile.ID,
		LifecycleClass:   store.DurableAgentClassAdvisor,
		Provider:         "anthropic",
		Model:            "model-a",
		RuntimeKind:      "api",
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
	}
	if err := svc.Create(context.Background(), inst); err != nil {
		t.Fatalf("Create: %v", err)
	}

	result, err := svc.Start(context.Background(), inst.ID, DurableAgentStartRequest{
		WakePayload: DurableAgentWakePayload{Reason: DurableAgentWakeExternalMessage, Prompt: "hello from an inbound wake"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(runtime.sent) != 1 {
		t.Fatalf("sent = %+v, want exactly one delivered message", runtime.sent)
	}
	if runtime.sent[0].sessionID != result.Session.ID || runtime.sent[0].content != "hello from an inbound wake" {
		t.Fatalf("sent[0] = %+v, want session=%s content=%q", runtime.sent[0], result.Session.ID, "hello from an inbound wake")
	}
}

// TestDurableAgentStartWithEmptyPromptDoesNotDeliverMessage preserves
// today's manual-wake-with-no-prompt behavior: no Prompt means no message
// delivery, so a plain lifecycle start/wake stays unaffected.
func TestDurableAgentStartWithEmptyPromptDoesNotDeliverMessage(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "No Prompt Agent", Slug: "no-prompt-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	runtime := &fakeDurableRuntimeController{}
	svc := NewDurableAgentServiceWithRuntime(st, runtime)
	inst := &store.DurableAgentInstance{
		Name:             "No Prompt Instance",
		Slug:             "no-prompt-instance",
		ProfileID:        profile.ID,
		LifecycleClass:   store.DurableAgentClassAdvisor,
		Provider:         "anthropic",
		Model:            "model-a",
		RuntimeKind:      "api",
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
	}
	if err := svc.Create(context.Background(), inst); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.Start(context.Background(), inst.ID, DurableAgentStartRequest{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(runtime.sent) != 0 {
		t.Fatalf("sent = %+v, want no delivered messages for an empty prompt", runtime.sent)
	}
}

// TestDurableAgentResumeDeliversWakePromptAsUserTurn covers the resume path:
// a wake against an already-attached (possibly non-empty) session must also
// deliver a non-empty Prompt as a real turn on the reattached session — it
// is not necessarily the session's first message overall.
func TestDurableAgentResumeDeliversWakePromptAsUserTurn(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Resume Prompt Agent", Slug: "resume-prompt-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	runtime := &fakeDurableRuntimeController{}
	svc := NewDurableAgentServiceWithRuntime(st, runtime)
	inst := &store.DurableAgentInstance{
		Name:             "Resume Prompt Instance",
		Slug:             "resume-prompt-instance",
		ProfileID:        profile.ID,
		LifecycleClass:   store.DurableAgentClassAdvisor,
		Provider:         "anthropic",
		Model:            "model-a",
		RuntimeKind:      "api",
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
	}
	if err := svc.Create(context.Background(), inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	started, err := svc.Start(context.Background(), inst.ID, DurableAgentStartRequest{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	runtime.sent = nil // only interested in what Resume delivers

	resumed, err := svc.Resume(context.Background(), inst.ID, DurableAgentStartRequest{
		WakePayload: DurableAgentWakePayload{Reason: DurableAgentWakeExternalMessage, Prompt: "resume with new context"},
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumed.Session.ID != started.Session.ID {
		t.Fatalf("resume should reuse the started session: got %s want %s", resumed.Session.ID, started.Session.ID)
	}
	if len(runtime.sent) != 1 || runtime.sent[0].sessionID != started.Session.ID || runtime.sent[0].content != "resume with new context" {
		t.Fatalf("sent = %+v, want one message on session %s", runtime.sent, started.Session.ID)
	}
}

// fakeWakePromptChatService is a minimal ChatService stand-in exercising
// only what wake-prompt delivery needs: HandleMessage must persist a real
// user message via the store, mirroring chatServiceImpl.HandleMessage's
// first step, without booting an actual runtime/provider.
type fakeWakePromptChatService struct {
	store *store.Store
}

func (f *fakeWakePromptChatService) HandleMessage(_ context.Context, sessionID, content string) (string, error) {
	msg := &store.Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		Role:      "user",
		Content:   content,
	}
	if err := f.store.CreateMessage(context.Background(), msg); err != nil {
		return "", err
	}
	return msg.ID, nil
}

func (f *fakeWakePromptChatService) RetryLastMessage(context.Context, string) (string, error) {
	return "", nil
}

func (f *fakeWakePromptChatService) SendAgentMessage(context.Context, string, string, string) (string, error) {
	return "", nil
}

func (f *fakeWakePromptChatService) DelegateTask(context.Context, chat.DelegationRequest) (*chat.DelegationResult, error) {
	return nil, nil
}

func (f *fakeWakePromptChatService) DelegateAndAggregate(context.Context, string, string, string) (*chat.OrchestrationResult, error) {
	return nil, nil
}

func (f *fakeWakePromptChatService) GetStream(string) (<-chan chat.StreamEvent, bool) {
	return nil, false
}

func (f *fakeWakePromptChatService) CancelActiveGeneration(string) bool { return false }

func (f *fakeWakePromptChatService) RebootSessionAgent(context.Context, string) (RebootResult, error) {
	return RebootResult{}, nil
}

func (f *fakeWakePromptChatService) RecoverSession(context.Context, string) (RebootResult, error) {
	return RebootResult{}, nil
}

func (f *fakeWakePromptChatService) Shutdown() error { return nil }

// TestDurableAgentWakeEndToEndPersistsPromptAsSessionMessage exercises the
// full real path (DurableAgentWakeService -> durableAgentService.Start ->
// chatDurableAgentRuntimeController -> ChatService.HandleMessage) and
// asserts the resulting session's message history contains the wake
// Prompt as a real user turn — not session metadata, not a template
// placeholder.
func TestDurableAgentWakeEndToEndPersistsPromptAsSessionMessage(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "E2E Wake Agent", Slug: "e2e-wake-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	runtime := NewChatDurableAgentRuntimeController(&fakeWakePromptChatService{store: st})
	durableSvc := NewDurableAgentServiceWithRuntime(st, runtime)
	wakeSvc := NewDurableAgentWakeService(st, durableSvc)

	// Seed an attached session, matching how a real A2A-targeted instance
	// would already have a primary session attached before it can be woken.
	seedSession := &store.Session{Provider: "anthropic", Model: "model-a"}
	if err := st.CreateSession(context.Background(), seedSession); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:             "E2E Wake Instance",
		Slug:             "e2e-wake-instance",
		ProfileID:        profile.ID,
		LifecycleClass:   store.DurableAgentClassAdvisor,
		Provider:         "anthropic",
		Model:            "model-a",
		RuntimeKind:      "api",
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
		CurrentSessionID: seedSession.ID,
	}
	if err := durableSvc.Create(context.Background(), inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := st.AttachDurableAgentInstanceSession(context.Background(), inst.ID, seedSession.ID, store.DurableAgentSessionRelationPrimary); err != nil {
		t.Fatalf("AttachDurableAgentInstanceSession: %v", err)
	}

	result, err := wakeSvc.Wake(context.Background(), inst.ID, DurableAgentWakeRequest{
		WakePayload: DurableAgentWakePayload{Reason: DurableAgentWakeExternalMessage, Prompt: "an inbound A2A-style task message"},
	})
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if result.Skipped || result.LaunchResult == nil || result.LaunchResult.Session == nil {
		t.Fatalf("wake result = %+v", result)
	}

	messages, err := st.ListMessages(context.Background(), result.LaunchResult.Session.ID, 10)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var found bool
	for _, msg := range messages {
		if msg.Role == "user" && msg.Content == "an inbound A2A-style task message" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("session messages = %+v, want a user turn carrying the wake prompt", messages)
	}
}
