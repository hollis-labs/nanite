// TASKS/reflex-taxonomy/04-halt-turn-synchronicity.md — proves both halves
// of "halt must actually halt": (1) no LLM provider call happens for the
// turn that fires a halt_session reflex, and (2) the session row is
// stamped halted_at/halted_reason by that SAME turn, not just discovered
// later by frontend_readiness.go on a subsequent request.
//
// reminder_engine_integration_test.go documents this package's usual
// tradeoff for generateResponse coverage: it's a large function that
// needs a live LLM provider round-trip, so most tests here exercise the
// narrower helper generateResponse delegates to instead of the whole
// function. That tradeoff doesn't fit this task: the fix under test
// lives in generateResponse's OWN control flow (the reflex call site
// aborting before the LLM call), not in a helper it delegates to, so a
// test of a hand-reimplemented version of that control flow would not
// actually prove the fix. This test invokes generateResponse itself, up
// to and past the abort point, wiring every dependency it touches before
// that point to something real (a real *store.Store with real
// migrations, the real reflexes.Engine/Executor/HaltHook pattern
// container.go wires) or to a minimal fake for interfaces this test has
// no other reason to exercise (SessionService, AgentService,
// ToolService) — mirroring the wiring style chat_path_grants_e2e_test.go
// and chat_reflex_dispatch_integration_test.go already use in this
// package. The llmcontracts.Provider fake's call count is the
// load-bearing assertion for half (1).
package service

import (
	"context"
	"path/filepath"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// haltTestSessions is a minimal SessionService fake. generateResponse only
// calls Get before this task's abort point; every other method panics if
// ever reached so a future change that starts depending on one fails this
// test loudly instead of silently returning a zero value.
type haltTestSessions struct{ st *store.Store }

func (f *haltTestSessions) Create(context.Context, CreateSessionOpts) (*store.Session, error) {
	panic("haltTestSessions: Create not implemented")
}
func (f *haltTestSessions) Get(_ context.Context, id string) (*store.Session, error) {
	return f.st.GetSession(context.Background(), id)
}
func (f *haltTestSessions) List(context.Context, bool) ([]store.Session, error) {
	panic("haltTestSessions: List not implemented")
}
func (f *haltTestSessions) Update(context.Context, *store.Session) error {
	panic("haltTestSessions: Update not implemented")
}
func (f *haltTestSessions) Archive(context.Context, string) error {
	panic("haltTestSessions: Archive not implemented")
}
func (f *haltTestSessions) Fork(context.Context, string, ForkOpts) (*store.Session, error) {
	panic("haltTestSessions: Fork not implemented")
}
func (f *haltTestSessions) ListMessages(context.Context, string, int) ([]store.Message, error) {
	panic("haltTestSessions: ListMessages not implemented")
}
func (f *haltTestSessions) Search(context.Context, string, SearchOpts) ([]store.SearchResult, error) {
	panic("haltTestSessions: Search not implemented")
}

// haltTestAgents is a minimal AgentService fake. generateResponse only
// calls ResolveForSession before this task's abort point.
type haltTestAgents struct{ agent *store.AgentProfile }

func (f *haltTestAgents) Get(context.Context, string) (*store.AgentProfile, error) {
	panic("haltTestAgents: Get not implemented")
}
func (f *haltTestAgents) GetBySlug(context.Context, string) (*store.AgentProfile, error) {
	panic("haltTestAgents: GetBySlug not implemented")
}
func (f *haltTestAgents) List(context.Context) ([]store.AgentProfile, error) {
	panic("haltTestAgents: List not implemented")
}
func (f *haltTestAgents) Create(context.Context, *store.AgentProfile) error {
	panic("haltTestAgents: Create not implemented")
}
func (f *haltTestAgents) Update(context.Context, *store.AgentProfile) error {
	panic("haltTestAgents: Update not implemented")
}
func (f *haltTestAgents) Delete(context.Context, string) error {
	panic("haltTestAgents: Delete not implemented")
}
func (f *haltTestAgents) ResolveForSession(context.Context, string) (*store.AgentProfile, error) {
	return f.agent, nil
}
func (f *haltTestAgents) ResolveForSessionReadOnly(context.Context, string) (*store.AgentProfile, error) {
	return f.agent, nil
}

// haltTestTools is a minimal ToolService fake. generateResponse only calls
// SelectForAgent before this task's abort point; an empty selection keeps
// the pre-abort "no tools" warning path harmless for this test.
type haltTestTools struct{}

func (haltTestTools) SelectForAgent(context.Context, string, string, string, string, int) (*ToolSelection, error) {
	return &ToolSelection{}, nil
}
func (haltTestTools) Execute(context.Context, string, string, map[string]any) (*ToolResult, error) {
	panic("haltTestTools: Execute not implemented")
}
func (haltTestTools) HandleRequestTools(context.Context, map[string]any) ([]llmtypes.ToolDefinition, string, error) {
	panic("haltTestTools: HandleRequestTools not implemented")
}
func (haltTestTools) ListSummaries() []toolclient.ToolSummary { return nil }
func (haltTestTools) GetToolMeta(context.Context, string) (ToolMetaInfo, bool) {
	return ToolMetaInfo{}, false
}
func (haltTestTools) GetToolSchema(string) map[string]any { return nil }

// TestGenerateResponse_HaltSessionReflex_AbortsTurnBeforeLLMCall is the
// task's own Done-means test: seed a real halt_session reflex whose
// trigger is guaranteed to fire on the test turn, invoke generateResponse
// for real, and confirm BOTH halves of the fix — no LLM provider call for
// that turn, AND the session row shows halted_at/halted_reason set.
// Testing only one half would not actually prove the fix (a halt that
// merely stamps the DB without aborting the turn would pass a
// halted-row-only test; a turn that aborts for some unrelated reason
// without ever going through MarkSessionHalted would pass a
// no-LLM-call-only test).
func TestGenerateResponse_HaltSessionReflex_AbortsTurnBeforeLLMCall(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "halt-turn-sync.db")
	st, err := store.New(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	sessionID := "sess-halt-turn-sync-1"
	agentID := "agent-halt-turn-sync-1"

	sess := &store.Session{
		ID: sessionID,
		// Model set so generateResponse's model resolution short-circuits
		// before s.store.ResolveProviderAndModel — irrelevant to this
		// task, not something this test needs to exercise.
		Model: "mock-model",
	}
	if err := st.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	agent := &store.AgentProfile{
		ID:     agentID,
		Name:   "HaltTurnSyncAgent",
		Slug:   "halt-turn-sync-agent",
		Status: "active",
		Class:  "advisor",
		// Non-CLI-shaped (chat.IsCLIProvider — no "pty-"/"sub-" prefix) so
		// resolveProvider resolves it via the registry below rather than
		// routing to the CLI/PTY branch.
		DefaultProvider: "mock-provider",
		Tags:            "[]",
		Tools:           "[]",
	}

	// A real, DB-backed, class-scoped reflex (matches agent.Class="advisor"
	// above via agent_id IS NULL AND class_tag = ?, same as every seeded
	// base reflex in seeds.go). Trigger is mail_unread_count >= 0, which is
	// unconditionally true (MailUnreadCount is a non-negative count) — this
	// deliberately isn't one of the three production halt_session seeds
	// (drift_detector_echo / task_complete_self_terminate / task_timeout);
	// it's an author-controlled trigger chosen so this test doesn't depend
	// on reproducing any of those seeds' own multi-turn window state, only
	// on proving the turn-abort wiring this task adds.
	if _, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:    "advisor",
		Name:        "test_halt_reflex_taxonomy_04",
		TriggerKind: store.ReflexTriggerPredicate,
		TriggerSpec: `{"kind":"mail_unread_count","op":">=","value":0}`,
		ActionKind:  store.ReflexActionHaltSession,
		ActionSpec:  `{"reason":"test halt: reflex-taxonomy/04 coverage"}`,
		Status:      store.ReflexStatusActive,
		CreatedBy:   "operator",
	}); err != nil {
		t.Fatalf("InsertAgentReflex: %v", err)
	}

	reflexEngine := reflexes.NewEngine(st, nil)
	// Mirrors container.go:918-922's real HaltHook wiring exactly (same
	// call, same store method) — this task does not change that wiring;
	// this test proves it stays intact end to end, not just that a
	// test-local reimplementation of it does.
	reflexEngine.Executor.Halt = func(_ context.Context, sid, reason string, _ map[string]interface{}) error {
		return st.MarkSessionHalted(context.Background(), sid, reason)
	}

	mockProv := &mockStreamProvider{
		events: []llmtypes.StreamEvent{
			{Type: "delta", Content: "should never be streamed — the turn must abort before this"},
			{Type: "done"},
		},
	}
	registry := provider.NewRegistry()
	registry.Register("mock-provider", mockProv)

	svc := &chatServiceImpl{
		sessions:     &haltTestSessions{st: st},
		agents:       &haltTestAgents{agent: agent},
		tools:        haltTestTools{},
		streams:      NewStreamManager(),
		context:      NewContextService(ContextServiceConfig{Client: chat.NewContextClient(st)}),
		store:        st,
		providers:    registry,
		reflexEngine: reflexEngine,
	}

	ch := make(chan chat.StreamEvent, 32)
	svc.generateResponse(ctx, sessionID, "msg-halt-turn-sync-1", "trigger the halt reflex", ch)

	var sawErrorEvent bool
	var errMsg string
	for ev := range ch {
		if ev.Type == "error" {
			sawErrorEvent = true
			errMsg = ev.Error
		}
	}

	// --- Half 1: no LLM provider call happened for this turn. ---
	if mockProv.callCount != 0 {
		t.Errorf("mock provider StreamChat called %d times, want 0 — the turn must abort before reaching the LLM call", mockProv.callCount)
	}

	// --- Half 2: the session row is marked halted by THIS turn, not a
	// later request — MarkSessionHalted already ran synchronously inside
	// evaluateAndInjectReflexes, before generateResponse returned above. ---
	halt, err := st.GetSessionHalt(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("GetSessionHalt: %v", err)
	}
	if !halt.IsHalted() {
		t.Fatal("session halt status: IsHalted() = false, want true")
	}
	if halt.HaltedReason == nil || *halt.HaltedReason == "" {
		t.Error("session halt status: HaltedReason is empty, want the reflex's reason")
	}

	if !sawErrorEvent {
		t.Error("expected an error stream event surfacing the halt to the caller, got none")
	}
	if errMsg == "" {
		t.Error("expected a non-empty halt message on the error stream event")
	}
}

// TestGenerateResponse_NonHaltReflex_DoesNotAbortTurn is the control case:
// a fired reflex whose action_kind is NOT halt_session (inject_reminder)
// must not trip the new abort path. Guards against a check that's too
// broad (e.g. aborting on ANY fired reflex action instead of specifically
// ActionKind == store.ReflexActionHaltSession).
func TestGenerateResponse_NonHaltReflex_DoesNotAbortTurn(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "halt-turn-sync-control.db")
	st, err := store.New(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	sessionID := "sess-halt-turn-sync-control-1"
	agentID := "agent-halt-turn-sync-control-1"

	sess := &store.Session{ID: sessionID, Model: "mock-model"}
	if err := st.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	agent := &store.AgentProfile{
		ID:              agentID,
		Name:            "HaltTurnSyncControlAgent",
		Slug:            "halt-turn-sync-control-agent",
		Status:          "active",
		Class:           "advisor",
		DefaultProvider: "mock-provider",
		Tags:            "[]",
		Tools:           "[]",
	}

	if _, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:    "advisor",
		Name:        "test_inject_reminder_control",
		TriggerKind: store.ReflexTriggerPredicate,
		TriggerSpec: `{"kind":"mail_unread_count","op":">=","value":0}`,
		ActionKind:  store.ReflexActionInjectReminder,
		ActionSpec:  `{"body":"control reflex — should not abort the turn"}`,
		Status:      store.ReflexStatusActive,
		CreatedBy:   "operator",
	}); err != nil {
		t.Fatalf("InsertAgentReflex: %v", err)
	}

	reflexEngine := reflexes.NewEngine(st, nil)
	reflexEngine.Executor.Halt = func(_ context.Context, sid, reason string, _ map[string]interface{}) error {
		return st.MarkSessionHalted(context.Background(), sid, reason)
	}

	mockProv := &mockStreamProvider{
		events: []llmtypes.StreamEvent{
			{Type: "delta", Content: "answer"},
			{Type: "done"},
		},
	}
	registry := provider.NewRegistry()
	registry.Register("mock-provider", mockProv)

	svc := &chatServiceImpl{
		sessions:     &haltTestSessions{st: st},
		agents:       &haltTestAgents{agent: agent},
		tools:        haltTestTools{},
		streams:      NewStreamManager(),
		context:      NewContextService(ContextServiceConfig{Client: chat.NewContextClient(st)}),
		store:        st,
		providers:    registry,
		reflexEngine: reflexEngine,
	}

	ch := make(chan chat.StreamEvent, 32)
	svc.generateResponse(ctx, sessionID, "msg-halt-turn-sync-control-1", "no halt here", ch)

	for range ch {
		// Drain — this test only cares about the provider call count and
		// halt status below, not the specific event sequence.
	}

	if mockProv.callCount != 1 {
		t.Errorf("mock provider StreamChat called %d times, want 1 — a non-halt reflex must not abort the turn", mockProv.callCount)
	}

	halt, err := st.GetSessionHalt(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("GetSessionHalt: %v", err)
	}
	if halt.IsHalted() {
		t.Error("session halt status: IsHalted() = true, want false — no halt_session reflex fired")
	}
}
