package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// --- stubs for ChatService tests ---

type stubSessionService struct {
	sessions map[string]*store.Session
}

func (s *stubSessionService) Create(_ context.Context, _ CreateSessionOpts) (*store.Session, error) {
	return nil, nil
}
func (s *stubSessionService) Get(_ context.Context, id string) (*store.Session, error) {
	if sess, ok := s.sessions[id]; ok {
		return sess, nil
	}
	return nil, fmt.Errorf("session %s not found", id)
}
func (s *stubSessionService) List(_ context.Context, _ bool) ([]store.Session, error) {
	return nil, nil
}
func (s *stubSessionService) Update(_ context.Context, _ *store.Session) error { return nil }
func (s *stubSessionService) Archive(_ context.Context, _ string) error        { return nil }
func (s *stubSessionService) Fork(_ context.Context, _ string, _ ForkOpts) (*store.Session, error) {
	return nil, nil
}
func (s *stubSessionService) ListMessages(_ context.Context, _ string, _ int) ([]store.Message, error) {
	return nil, nil
}
func (s *stubSessionService) Search(_ context.Context, _ string, _ SearchOpts) ([]store.SearchResult, error) {
	return nil, nil
}

type stubAgentService struct {
	agent *store.AgentProfile

	// Call counters so tests can assert which resolution path a caller
	// used — in particular, that resolveMessageWakePolicy uses the
	// non-mutating ResolveForSessionReadOnly rather than ResolveForSession
	// (CW code-review: the mutating variant auto-assigns a session_agents
	// row / emits AgentAssigned as a side effect of what should be a pure
	// policy read).
	resolveForSessionCalls         int
	resolveForSessionReadOnlyCalls int
}

func (s *stubAgentService) Get(_ context.Context, _ string) (*store.AgentProfile, error) {
	return s.agent, nil
}
func (s *stubAgentService) GetBySlug(_ context.Context, _ string) (*store.AgentProfile, error) {
	return s.agent, nil
}
func (s *stubAgentService) List(_ context.Context) ([]store.AgentProfile, error)  { return nil, nil }
func (s *stubAgentService) Create(_ context.Context, _ *store.AgentProfile) error { return nil }
func (s *stubAgentService) Update(_ context.Context, _ *store.AgentProfile) error { return nil }
func (s *stubAgentService) Delete(_ context.Context, _ string) error              { return nil }
func (s *stubAgentService) ResolveForSession(_ context.Context, _ string) (*store.AgentProfile, error) {
	s.resolveForSessionCalls++
	return s.agent, nil
}
func (s *stubAgentService) ResolveForSessionReadOnly(_ context.Context, _ string) (*store.AgentProfile, error) {
	s.resolveForSessionReadOnlyCalls++
	return s.agent, nil
}

type stubToolService struct{}

func (s *stubToolService) SelectForAgent(_ context.Context, _, _, _, _ string, _ int) (*ToolSelection, error) {
	return &ToolSelection{}, nil
}
func (s *stubToolService) Execute(_ context.Context, _, _ string, _ map[string]any) (*ToolResult, error) {
	return &ToolResult{Output: "ok"}, nil
}
func (s *stubToolService) HandleRequestTools(_ context.Context, _ map[string]any) ([]llmtypes.ToolDefinition, string, error) {
	return nil, "No tools", nil
}
func (s *stubToolService) ListSummaries() []toolclient.ToolSummary { return nil }
func (s *stubToolService) GetToolMeta(_ context.Context, toolName string) (ToolMetaInfo, bool) {
	return ToolMetaInfo{}, true
}

type stubContextService struct{}

func (s *stubContextService) PruneAfterTurn(_ context.Context, _ string) error { return nil }

// --- tests ---

func TestChatService_GetStream_Empty(t *testing.T) {
	sm := NewStreamManager()
	svc := NewChatService(ChatServiceConfig{
		Streams:   sm,
		Providers: provider.NewRegistry(),
	})

	_, ok := svc.GetStream("nonexistent")
	if ok {
		t.Error("expected GetStream to return false for nonexistent message")
	}
}

func TestChatService_Shutdown_NilTracker(t *testing.T) {
	svc := NewChatService(ChatServiceConfig{
		Streams:   NewStreamManager(),
		Providers: provider.NewRegistry(),
	})
	// Should not panic.
	svc.Shutdown()
}

// minimalStore embeds stubSettings and provides the other Store methods as no-ops for testing.
type minimalStore struct {
	stubSettings
	stubSessionStore
	stubAgentReaderStore
	stubAgentWriterStore
	stubToolStore
	stubUsageStore
	stubProjectStore
	stubBookmarkStore
	stubArtifactStore
	stubSkillStore
	stubProviderStore
	stubTodoStore
	stubPlanStore
	stubHandoffStashStore
	stubCompactionEventStore
	stubEnvelopeStore
	stubReminderStore
	stubPinnedContentStore
	stubSubagentRunsReader
}

// AgentRuntimeProviderSessionID satisfies the Store interface for the test
// fakes (minimalStore + its embedders). Returns no captured session by default.
func (m *minimalStore) AgentRuntimeProviderSessionID(context.Context, string) (string, error) {
	return "", nil
}

// SetAgentRuntimeProviderSessionID satisfies the Store interface for the test
// fakes. No-op by default; tests that need to assert the clear path can
// override on a per-test stub.
func (m *minimalStore) SetAgentRuntimeProviderSessionID(context.Context, string, string) error {
	return nil
}

// ListEnabledAgentContextResolvers satisfies the Store interface for the
// test fakes (Phase 2 item 02,
// TASKS/phase-2/02-port-forward-dynamic-resolver.md). Returns no
// configured resolvers by default.
func (m *minimalStore) ListEnabledAgentContextResolvers(context.Context, string) ([]store.AgentContextResolver, error) {
	return nil, nil
}

// stubSubagentRunsReader returns no active subagent for any session by
// default. Tests that need to exercise the suppression branch (CW-20260512-0002 d)
// embed a custom reader instead.
type stubSubagentRunsReader struct{}

func (stubSubagentRunsReader) ActiveSubagentRunForParent(context.Context, string) (id, role, child string, ok bool, err error) {
	return "", "", "", false, nil
}

type stubEnvelopeStore struct{}

func (stubEnvelopeStore) CreateEnvelopeInstance(ctx context.Context, inst *store.EnvelopeInstance) error {
	return nil
}
func (stubEnvelopeStore) GetEnvelopeInstance(ctx context.Context, id string) (*store.EnvelopeInstance, error) {
	return nil, fmt.Errorf("not found")
}

type stubReminderStore struct{}

func (stubReminderStore) CreateReminder(context.Context, store.Reminder) error { return nil }
func (stubReminderStore) GetReminder(context.Context, string) (store.Reminder, error) {
	return store.Reminder{}, nil
}
func (stubReminderStore) ListUnfiredReminders(context.Context, string) ([]store.Reminder, error) {
	return nil, nil
}
func (stubReminderStore) MarkReminderFired(context.Context, string) error { return nil }
func (stubReminderStore) UpdateReminderScope(context.Context, string, string, string) error {
	return nil
}
func (stubReminderStore) DeleteReminder(context.Context, string) error { return nil }

type stubPinnedContentStore struct{}

func (stubPinnedContentStore) CreatePinnedContent(context.Context, store.PinnedContent) error {
	return nil
}
func (stubPinnedContentStore) ListPinnedContent(context.Context, string) ([]store.PinnedContent, error) {
	return nil, nil
}
func (stubPinnedContentStore) DeletePinnedContent(context.Context, string) error { return nil }
func (stubPinnedContentStore) UpdatePinScope(context.Context, string, string, string) error {
	return nil
}
func (stubPinnedContentStore) ClearSessionPins(context.Context, string) error { return nil }

type stubHandoffStashStore struct{}

func (stubHandoffStashStore) UpsertHandoffStash(context.Context, store.HandoffStash) error {
	return nil
}
func (stubHandoffStashStore) GetHandoffStash(context.Context, string, string) (store.HandoffStash, error) {
	return store.HandoffStash{}, store.ErrHandoffStashNotFound
}
func (stubHandoffStashStore) GetLatestStashForSession(context.Context, string) (store.HandoffStash, error) {
	return store.HandoffStash{}, store.ErrHandoffStashNotFound
}

type stubCompactionEventStore struct{}

func (stubCompactionEventStore) WriteCompactionEvent(context.Context, store.CompactionEvent) error {
	return nil
}
func (stubCompactionEventStore) GetLatestCompactionEvent(context.Context, string) (*store.CompactionEvent, error) {
	return nil, nil
}
func (stubCompactionEventStore) ListCompactionEventsBySession(context.Context, string, int) ([]store.CompactionEvent, error) {
	return nil, nil
}

// Stubs to satisfy the Store composite interface for tests.
type stubSessionStore struct{}

func (stubSessionStore) GetSession(context.Context, string) (*store.Session, error) {
	return nil, fmt.Errorf("not found")
}
func (stubSessionStore) ListSessions(context.Context, ...bool) ([]store.Session, error) {
	return nil, nil
}
func (stubSessionStore) ListMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (stubSessionStore) ListMessagesPaginated(context.Context, string, int, int) (*store.MessagePage, error) {
	return nil, nil
}
func (stubSessionStore) ListMessagesAroundID(context.Context, string, string, int, int) (*store.MessagePage, error) {
	return nil, nil
}
func (stubSessionStore) GetMessage(context.Context, string) (*store.Message, error) {
	return nil, fmt.Errorf("not found")
}
func (stubSessionStore) SearchMessages(context.Context, string, string, int) ([]store.SearchResult, error) {
	return nil, nil
}
func (stubSessionStore) CreateSession(context.Context, *store.Session) error              { return nil }
func (stubSessionStore) UpdateSession(context.Context, *store.Session) error              { return nil }
func (stubSessionStore) UpdateSessionTags(context.Context, string, string) error          { return nil }
func (stubSessionStore) UpdateSessionMetadata(context.Context, string, string) error      { return nil }
func (stubSessionStore) ArchiveSession(context.Context, string) error                     { return nil }
func (stubSessionStore) NextShortCode(ctx context.Context) (string, error)                { return "", nil }
func (stubSessionStore) CreateMessage(context.Context, *store.Message) error              { return nil }
func (stubSessionStore) UpdateMessageContent(context.Context, string, string, bool) error { return nil }
func (stubSessionStore) ForkSession(context.Context, string, *store.Session, bool) (*store.Session, error) {
	return nil, nil
}
func (stubSessionStore) CopyMessages(context.Context, string, string) error { return nil }

type stubAgentReaderStore struct{}

func (stubAgentReaderStore) GetAgent(context.Context, string) (*store.AgentProfile, error) {
	return nil, fmt.Errorf("not found")
}
func (stubAgentReaderStore) GetAgentBySlug(context.Context, string) (*store.AgentProfile, error) {
	return nil, fmt.Errorf("not found")
}
func (stubAgentReaderStore) ListAgents(ctx context.Context) ([]store.AgentProfile, error) {
	return nil, nil
}
func (stubAgentReaderStore) ListAgentsBySource(context.Context, string) ([]store.AgentProfile, error) {
	return nil, nil
}
func (stubAgentReaderStore) GetSessionPrimaryAgent(context.Context, string) (*store.SessionAgent, error) {
	return nil, fmt.Errorf("not found")
}
func (stubAgentReaderStore) ListSessionAgents(context.Context, string) ([]store.SessionAgent, error) {
	return nil, nil
}
func (stubAgentReaderStore) ListAgentSkills(context.Context, string) ([]store.Skill, error) {
	return nil, nil
}
func (stubAgentReaderStore) ListAgentProjects(context.Context, string) ([]store.Project, error) {
	return nil, nil
}
func (stubAgentReaderStore) ListProjectAgents(context.Context, string) ([]store.AgentProfile, error) {
	return nil, nil
}
func (stubAgentReaderStore) GetRole(context.Context, string) (*store.Role, error) { return nil, nil }

// ListAgentToolNames and ListAlwaysIncludedKnownTools satisfy the Store
// interface's Phase 5 item 01 additions (TASKS/phase-5/01-build-assignment-
// api.md) for the test fakes. No agent_tools grants / always_included rows
// by default -- tests that need to exercise enforceExecutionRulesViaAgentTools
// construct a real *store.Store instead (see tool_execution_rules_test.go).
func (stubAgentReaderStore) ListAgentToolNames(context.Context, string) ([]string, error) {
	return nil, nil
}
func (stubAgentReaderStore) ListAlwaysIncludedKnownTools(context.Context) ([]store.KnownTool, error) {
	return nil, nil
}

type stubAgentWriterStore struct{}

func (stubAgentWriterStore) CreateAgent(context.Context, *store.AgentProfile) error       { return nil }
func (stubAgentWriterStore) UpdateAgent(context.Context, *store.AgentProfile) error       { return nil }
func (stubAgentWriterStore) DeleteAgent(context.Context, string) error                    { return nil }
func (stubAgentWriterStore) UpsertAgentBySlug(context.Context, *store.AgentProfile) error { return nil }
func (stubAgentWriterStore) EnsureSessionAgent(context.Context, string, string, string, bool) error {
	return nil
}
func (stubAgentWriterStore) SetSessionAgentMode(context.Context, string, string, string) error {
	return nil
}
func (stubAgentWriterStore) DeleteSessionAgent(context.Context, string, string) error { return nil }
func (stubAgentWriterStore) AssignSkillToAgent(context.Context, string, string, string) error {
	return nil
}
func (stubAgentWriterStore) RemoveSkillFromAgent(context.Context, string, string) error { return nil }
func (stubAgentWriterStore) AddAgentProject(context.Context, string, string) error      { return nil }
func (stubAgentWriterStore) RemoveAgentProject(context.Context, string, string) error   { return nil }

type stubToolStore struct{}

func (stubToolStore) ListMCPServers(ctx context.Context) ([]store.MCPServerConfig, error) {
	return nil, nil
}
func (stubToolStore) GetMCPServer(context.Context, string) (*store.MCPServerConfig, error) {
	return nil, nil
}
func (stubToolStore) CreateMCPServer(context.Context, *store.MCPServerConfig) error { return nil }
func (stubToolStore) UpdateMCPServer(context.Context, *store.MCPServerConfig) error { return nil }
func (stubToolStore) DeleteMCPServer(context.Context, string) error                 { return nil }
func (stubToolStore) ListCatalogSources(ctx context.Context) ([]store.CatalogSource, error) {
	return nil, nil
}
func (stubToolStore) GetCatalogSource(context.Context, string) (*store.CatalogSource, error) {
	return nil, nil
}
func (stubToolStore) CreateCatalogSource(context.Context, string, string, string, int) (*store.CatalogSource, error) {
	return nil, nil
}
func (stubToolStore) UpdateCatalogSource(context.Context, string, string, string, bool, int) error {
	return nil
}
func (stubToolStore) SetCatalogSourcePublicKey(context.Context, string, string) error { return nil }
func (stubToolStore) DeleteCatalogSource(context.Context, string) error               { return nil }

type stubUsageStore struct{}

func (stubUsageStore) RecordUsage(context.Context, string, string, string, int, int, int, int, int) error {
	return nil
}
func (stubUsageStore) GetSessionUsage(context.Context, string) (*store.SessionUsageSummary, error) {
	return nil, nil
}
func (stubUsageStore) GetUsageSummary(ctx context.Context) (*store.UsageSummary, error) {
	return nil, nil
}
func (stubUsageStore) RecordExecutionMetrics(context.Context, *store.ExecutionMetrics) error {
	return nil
}
func (stubUsageStore) GetSessionExecutionMetrics(context.Context, string) ([]store.ExecutionMetrics, error) {
	return nil, nil
}
func (stubUsageStore) GetRecentExecutionMetrics(context.Context, int) ([]store.ExecutionMetrics, error) {
	return nil, nil
}
func (stubUsageStore) GetUtilityCallSummary(ctx context.Context) ([]store.UtilityCallSummary, error) {
	return nil, nil
}
func (stubUsageStore) GetUtilityCallLog(context.Context, int) ([]store.ExecutionMetrics, error) {
	return nil, nil
}
func (stubUsageStore) LogEvent(context.Context, string, string, string, string, string) {}
func (stubUsageStore) ListEvents(context.Context, string, int) ([]store.EventLog, error) {
	return nil, nil
}
func (stubUsageStore) CountSessionToolCalls(context.Context, string) int { return 0 }

type stubProjectStore struct{}

func (stubProjectStore) ListProjects(ctx context.Context) ([]store.Project, error)  { return nil, nil }
func (stubProjectStore) GetProject(context.Context, string) (*store.Project, error) { return nil, nil }
func (stubProjectStore) CreateProject(context.Context, *store.Project) error        { return nil }
func (stubProjectStore) UpdateProject(context.Context, *store.Project) error        { return nil }
func (stubProjectStore) DeleteProject(context.Context, string) error                { return nil }

type stubBookmarkStore struct{}

func (stubBookmarkStore) ListBookmarks(context.Context, string) ([]store.Bookmark, error) {
	return nil, nil
}
func (stubBookmarkStore) GetBookmark(context.Context, string) (*store.Bookmark, error) {
	return nil, nil
}
func (stubBookmarkStore) GetBookmarkByMessage(context.Context, string) (*store.Bookmark, error) {
	return nil, nil
}
func (stubBookmarkStore) CreateBookmark(context.Context, *store.Bookmark) error    { return nil }
func (stubBookmarkStore) DeleteBookmark(context.Context, string) error             { return nil }
func (stubBookmarkStore) UpdateBookmarkNote(context.Context, string, string) error { return nil }

type stubArtifactStore struct{}

func (stubArtifactStore) ListArtifacts(context.Context, string) ([]store.Artifact, error) {
	return nil, nil
}
func (stubArtifactStore) ListArtifactsByOrigin(context.Context, string, string) ([]store.Artifact, error) {
	return nil, nil
}
func (stubArtifactStore) ListArtifactsByProject(context.Context, string, string) ([]store.Artifact, error) {
	return nil, nil
}
func (stubArtifactStore) CreateArtifact(context.Context, *store.Artifact) error { return nil }
func (stubArtifactStore) GetArtifact(context.Context, string) (*store.Artifact, error) {
	return nil, nil
}

type stubSkillStore struct{}

func (stubSkillStore) ListSkills(ctx context.Context) ([]store.Skill, error)        { return nil, nil }
func (stubSkillStore) GetSkill(context.Context, string) (*store.Skill, error)       { return nil, nil }
func (stubSkillStore) GetSkillBySlug(context.Context, string) (*store.Skill, error) { return nil, nil }
func (stubSkillStore) CreateSkill(context.Context, *store.Skill) error              { return nil }
func (stubSkillStore) UpdateSkill(context.Context, *store.Skill) error              { return nil }
func (stubSkillStore) DeleteSkill(context.Context, string) error                    { return nil }

type stubProviderStore struct{}

func (stubProviderStore) ListProviders(ctx context.Context) ([]store.ProviderConfig, error) {
	return nil, nil
}
func (stubProviderStore) GetProvider(context.Context, string) (*store.ProviderConfig, error) {
	return nil, nil
}
func (stubProviderStore) ListModels(ctx context.Context) ([]store.Model, error) { return nil, nil }
func (stubProviderStore) UpdateProvider(context.Context, string, store.ProviderUpdate) error {
	return nil
}
func (stubProviderStore) DefaultModelForProvider(context.Context, string) (string, error) {
	return "", nil
}
func (stubProviderStore) ResolveProviderAndModel(ctx context.Context, explicitProvider, explicitModel string) (string, string, error) {
	return explicitProvider, explicitModel, nil
}

type stubTodoStore struct{}

func (stubTodoStore) CreateTodo(context.Context, *store.Todo) error        { return nil }
func (stubTodoStore) GetTodo(context.Context, string) (*store.Todo, error) { return nil, nil }
func (stubTodoStore) ListTodos(context.Context, store.TodoFilter) ([]store.Todo, error) {
	return nil, nil
}
func (stubTodoStore) UpdateTodo(context.Context, *store.Todo) error { return nil }
func (stubTodoStore) UpdateTodoScope(context.Context, string, string, string, string) error {
	return nil
}
func (stubTodoStore) DeleteTodo(context.Context, string) error                       { return nil }
func (stubTodoStore) ListTodoChildren(context.Context, string) ([]store.Todo, error) { return nil, nil }

type stubPlanStore struct{}

func (stubPlanStore) CreatePlan(context.Context, *store.Plan) error        { return nil }
func (stubPlanStore) GetPlan(context.Context, string) (*store.Plan, error) { return nil, nil }
func (stubPlanStore) ListPlans(context.Context, store.PlanFilter) ([]store.Plan, error) {
	return nil, nil
}
func (stubPlanStore) UpdatePlan(context.Context, *store.Plan) error { return nil }
func (stubPlanStore) UpdatePlanStep(context.Context, string, string, store.PlanStep) error {
	return nil
}
func (stubPlanStore) DeletePlan(context.Context, string) error { return nil }

func TestChatService_ResolveProvider(t *testing.T) {
	reg := provider.NewRegistry()
	testStore := &minimalStore{}
	impl := &chatServiceImpl{
		providers: reg,
		store:     testStore,
	}

	name, prov := impl.resolveProvider("test-session", "", "", "gpt-4o", "api")
	// No provider registered, so prov should be nil and name should be inferred.
	if prov != nil {
		t.Error("expected nil provider when none registered")
	}
	if name != "openai" {
		t.Errorf("expected inferred provider 'openai', got %q", name)
	}
}

func TestInferProvider(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{"claude-cli", "pty"},
		{"codex-cli", "pty-codex"},
		{"gemini-cli", "pty-gemini"},
		{"gpt-4o", "openai"},
		{"o1-preview", "openai"},
		{"o3-mini", "openai"},
		// Step 6.5 (SP-20260508-0001) removed the Mistral and Ollama API
		// adapters; their registry rows are gone. The dangling "ollama"
		// routing branch itself was removed 2026-08-18 (TASKS/phase-0/
		// 05-remove-ollama-routing.md). Bare "llama3.1" / "mistral-large"
		// inputs now fall straight through to the DefaultProvider terminal,
		// which is dead-letter behavior. Coverage retained for the
		// surviving live cases only.
		{"claude-sonnet-4-20250514", "anthropic"},
		{"unknown-model", "anthropic"},
	}

	for _, tt := range tests {
		got := chat.InferProvider(tt.model)
		if got != tt.want {
			t.Errorf("chat.InferProvider(%q) = %q, want %q", tt.model, got, tt.want)
		}
	}
}

func TestIsCLIProvider(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"pty", true},
		{"pty-claude", true},
		{"pty-codex", true},
		{"sub-aider", true},
		{"anthropic", false},
		{"openai", false},
	}
	for _, tt := range tests {
		if got := chat.IsCLIProvider(tt.name); got != tt.want {
			t.Errorf("chat.IsCLIProvider(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestDetectStuckLoop(t *testing.T) {
	impl := &chatServiceImpl{}
	last := map[string]string{}
	counts := map[string]int{}
	blocked := map[string]bool{}

	// First call — no repeat, result stored as-is.
	result := impl.detectStuckLoop("tool_a", "result1", last, counts, blocked)
	if result != "result1" {
		t.Errorf("first call: got %q, want 'result1'", result)
	}

	// Second identical call — note appended (count=1).
	result = impl.detectStuckLoop("tool_a", "result1", last, counts, blocked)
	if !contains(result, "same result") {
		t.Error("second call: expected repeat warning in result")
	}

	// Note: lastResults now contains the modified (note-appended) text,
	// so passing "result1" again won't match. The stuck loop detector
	// correctly resets since the stored result differs. This matches the
	// original engine behavior: a real stuck loop sends the *same* raw
	// tool output each time, which would be the unmodified result.
	//
	// To test the block path, we need consecutive matching results stored
	// in lastResults. Set them up manually:
	last["tool_b"] = "same"
	counts["tool_b"] = 1
	// Third call with same result — blocks (count reaches 2).
	result = impl.detectStuckLoop("tool_b", "same", last, counts, blocked)
	if !blocked["tool_b"] {
		t.Error("expected tool_b to be blocked after 3 identical results")
	}
	if !contains(result, "holding") {
		t.Error("expected block message in result text")
	}
}

func TestCaptureEnvelopeData(t *testing.T) {
	// No envelope marker.
	result := captureEnvelopeData("plain result", nil)
	if len(result) != 0 {
		t.Errorf("no marker: got %d envelopes", len(result))
	}

	// With envelope marker.
	data := `some text <!--ENVELOPE_DATA:{"type":"metric-card"}:ENVELOPE_DATA--> more text`
	result = captureEnvelopeData(data, nil)
	if len(result) != 1 {
		t.Fatalf("with marker: got %d envelopes, want 1", len(result))
	}
	if result[0] != `{"type":"metric-card"}` {
		t.Errorf("envelope payload = %q", result[0])
	}
}

// TestCaptureEnvelopeData_UsesSharedExtractor is
// TASKS/harness-reactive-self-tools/06-collapse-envelope-marker-consumers.md's
// regression proof for this call site: captureEnvelopeData no longer
// hand-scans for the marker itself — it delegates to
// chat.ExtractEnvelopeMarker (internal/chat/envelope_marker.go), the same
// shared function internal/api/tools_call.go's extractEnvelopeMarker and
// internal/mcpserver/handlers.go's convertEnvelopeMarkers now delegate to.
// This exercises captureEnvelopeData's own real call-site behavior
// (appending to a pending slice, ignoring a malformed marker, and
// accumulating across repeated calls the way chat_tool_executor.go's tool
// loop actually calls it) rather than testing the shared function in
// isolation.
func TestCaptureEnvelopeData_UsesSharedExtractor(t *testing.T) {
	var pending []string

	// A malformed marker (no closing delimiter) must not be captured —
	// this is the shared function's malformed-marker behavior surfacing
	// through this call site.
	pending = captureEnvelopeData(`<!--ENVELOPE_DATA:{"truncated":true} no closing delimiter`, pending)
	if len(pending) != 0 {
		t.Fatalf("malformed marker: got %d envelopes, want 0", len(pending))
	}

	// Simulates chat_tool_executor.go's real call pattern: each tool
	// result's captured envelope (if any) is appended onto the same
	// pendingEnvelopes slice across the turn's sequence of tool calls.
	pending = captureEnvelopeData(`no marker in this result`, pending)
	pending = captureEnvelopeData(`<!--ENVELOPE_DATA:{"type":"info-card","data":{"title":"first"}}:ENVELOPE_DATA-->`, pending)
	pending = captureEnvelopeData(`<!--ENVELOPE_DATA:{"type":"info-card","data":{"title":"second"}}:ENVELOPE_DATA-->`, pending)

	if len(pending) != 2 {
		t.Fatalf("got %d accumulated envelopes, want 2: %#v", len(pending), pending)
	}
	if pending[0] != `{"type":"info-card","data":{"title":"first"}}` {
		t.Errorf("pending[0] = %q", pending[0])
	}
	if pending[1] != `{"type":"info-card","data":{"title":"second"}}` {
		t.Errorf("pending[1] = %q", pending[1])
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestChatService_RegisterGeneration_Takeover verifies that registering a
// new generation for a session returns the prior cancel — the takeover
// primitive that prevents concurrent duplicate loops on retry.
// CW-20260418-0043.
func TestChatService_RegisterGeneration_Takeover(t *testing.T) {
	svc := &chatServiceImpl{activeGen: make(map[string]*inFlightGen)}

	canceledA := make(chan struct{})
	cancelA := context.CancelFunc(func() { close(canceledA) })

	// First registration: no prior cancel.
	prev, first := svc.registerGeneration("sess", "msg-A", cancelA)
	if prev != nil {
		t.Fatalf("expected nil prev for first register, got %v", prev)
	}

	// Second registration on the same session: returns the first cancel.
	cancelB := context.CancelFunc(func() {})
	prev, second := svc.registerGeneration("sess", "msg-B", cancelB)
	if prev == nil {
		t.Fatal("expected prev cancel on second register, got nil")
	}
	prev.cancel()
	select {
	case <-canceledA:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("prior cancel was not invoked")
	}

	// Deregistering the first msgID is a no-op because msg-B is current.
	svc.deregisterGeneration("sess", first)
	svc.activeGenMu.Lock()
	cur := svc.activeGen["sess"]
	svc.activeGenMu.Unlock()
	if cur == nil || cur.msgID != "msg-B" {
		t.Fatalf("expected msg-B still active, got %+v", cur)
	}

	// Deregistering the current msgID clears the slot.
	svc.deregisterGeneration("sess", second)
	svc.activeGenMu.Lock()
	if _, ok := svc.activeGen["sess"]; ok {
		t.Fatal("expected slot cleared after deregister of active msg")
	}
	svc.activeGenMu.Unlock()
}

// TestChatService_RegisterGeneration_ScopedPerSession verifies two sessions
// do not interfere with each other's takeover registry.
func TestChatService_RegisterGeneration_ScopedPerSession(t *testing.T) {
	svc := &chatServiceImpl{activeGen: make(map[string]*inFlightGen)}
	cancelA := context.CancelFunc(func() {})
	cancelB := context.CancelFunc(func() {})

	if prev, _ := svc.registerGeneration("sess-1", "msg-1", cancelA); prev != nil {
		t.Fatalf("expected nil prev for sess-1, got %v", prev)
	}
	if prev, _ := svc.registerGeneration("sess-2", "msg-2", cancelB); prev != nil {
		t.Fatal("expected nil prev for sess-2 — different session")
	}
}

// TestChatService_CancelActiveGeneration_DispatchesCancel verifies that
// CancelActiveGeneration invokes the registered cancel for the session
// and returns true. CW-20260512-0006: this is the user-stop backstop
// now that the 5-minute parent wall-clock deadline has been removed.
func TestChatService_CancelActiveGeneration_DispatchesCancel(t *testing.T) {
	svc := &chatServiceImpl{activeGen: make(map[string]*inFlightGen)}

	canceled := make(chan struct{})
	cancel := context.CancelFunc(func() { close(canceled) })

	_, _ = svc.registerGeneration("sess-active", "msg-1", cancel)

	if ok := svc.CancelActiveGeneration("sess-active"); !ok {
		t.Fatal("CancelActiveGeneration returned false for an active generation")
	}
	select {
	case <-canceled:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("registered cancel was not invoked by CancelActiveGeneration")
	}

	// The slot is NOT cleared here; deregisterGeneration handles that when
	// the canceled goroutine returns. This preserves the takeover
	// semantics — a stale slot for a draining goroutine is fine because
	// registerGeneration only consults the slot to compute `prev`.
	svc.activeGenMu.Lock()
	cur, ok := svc.activeGen["sess-active"]
	svc.activeGenMu.Unlock()
	if !ok || cur == nil || cur.msgID != "msg-1" {
		t.Fatalf("expected slot retained for the canceled gen, got %+v ok=%v", cur, ok)
	}
}

// TestChatService_CancelActiveGeneration_NoActive verifies that the call
// is idempotent when no generation is registered — returns false so the
// API layer can surface a 404 without treating it as a failure.
func TestChatService_CancelActiveGeneration_NoActive(t *testing.T) {
	svc := &chatServiceImpl{activeGen: make(map[string]*inFlightGen)}
	if ok := svc.CancelActiveGeneration("sess-no-active"); ok {
		t.Fatal("CancelActiveGeneration returned true for a session with no active gen")
	}
}

// Verify ChatService interface is satisfied at compile time.
var _ ChatService = (*chatServiceImpl)(nil)

// Verify the exported helper types are usable from the service package.
var _ = chat.StreamEvent{}
var _ = chat.PresenceEvent{}
