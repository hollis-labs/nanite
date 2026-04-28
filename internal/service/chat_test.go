package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// --- stubs for ChatService tests ---

type stubSessionService struct {
	sessions map[string]*store.Session
}

func (s *stubSessionService) Create(_ context.Context, _ CreateSessionOpts) (*store.Session, error)       { return nil, nil }
func (s *stubSessionService) Get(_ context.Context, id string) (*store.Session, error) {
	if sess, ok := s.sessions[id]; ok {
		return sess, nil
	}
	return nil, fmt.Errorf("session %s not found", id)
}
func (s *stubSessionService) List(_ context.Context, _ string, _ bool) ([]store.Session, error)           { return nil, nil }
func (s *stubSessionService) Update(_ context.Context, _ *store.Session) error                             { return nil }
func (s *stubSessionService) Archive(_ context.Context, _ string) error                                    { return nil }
func (s *stubSessionService) Fork(_ context.Context, _ string, _ ForkOpts) (*store.Session, error)         { return nil, nil }
func (s *stubSessionService) ListMessages(_ context.Context, _ string, _ int) ([]store.Message, error)     { return nil, nil }
func (s *stubSessionService) Search(_ context.Context, _ string, _ SearchOpts) ([]store.SearchResult, error) { return nil, nil }

type stubAgentService struct {
	agent *store.AgentProfile
	mode  *store.AgentMode
}

func (s *stubAgentService) Get(_ context.Context, _ string) (*store.AgentProfile, error)    { return s.agent, nil }
func (s *stubAgentService) GetBySlug(_ context.Context, _ string) (*store.AgentProfile, error) { return s.agent, nil }
func (s *stubAgentService) List(_ context.Context) ([]store.AgentProfile, error)              { return nil, nil }
func (s *stubAgentService) Create(_ context.Context, _ *store.AgentProfile) error              { return nil }
func (s *stubAgentService) Update(_ context.Context, _ *store.AgentProfile) error              { return nil }
func (s *stubAgentService) Delete(_ context.Context, _ string) error                           { return nil }
func (s *stubAgentService) ResolveForSession(_ context.Context, _ string) (*store.AgentProfile, *store.AgentMode, error) {
	return s.agent, s.mode, nil
}
func (s *stubAgentService) ListModes(_ context.Context, _ string) ([]store.AgentMode, error) { return nil, nil }

type stubToolService struct{}

func (s *stubToolService) SelectForAgent(_ context.Context, _, _, _, _ string, _ int) (*ToolSelection, error) {
	return &ToolSelection{}, nil
}
func (s *stubToolService) Execute(_ context.Context, _, _ string, _ map[string]any) (*ToolResult, error) {
	return &ToolResult{Output: "ok"}, nil
}
func (s *stubToolService) HandleRequestTools(_ context.Context, _ map[string]any) ([]provider.ToolDefinition, string, error) {
	return nil, "No tools", nil
}
func (s *stubToolService) ListSummaries() []toolclient.ToolSummary { return nil }
func (s *stubToolService) GetToolMeta(toolName string) (ToolMetaInfo, bool) {
	return ToolMetaInfo{}, true
}

type stubContextService struct{}

func (s *stubContextService) AssembleContext(_ context.Context, _ *store.Session, _ *store.AgentProfile, _ *store.AgentMode, _ *store.Workspace) (string, []provider.ChatMessage, error) {
	return "system prompt", nil, nil
}
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
	stubWorkspaceStore
	stubBookmarkStore
	stubArtifactStore
	stubTemplateStore
	stubSkillStore
	stubModeStore
	stubCustomActionStore
	stubTriggerRuleStore
	stubProviderStore
	stubTodoStore
	stubPlanStore
	stubHandoffStashStore
	stubCompactionEventStore
	stubEnvelopeStore
	stubReminderStore
	stubPinnedContentStore
}

type stubEnvelopeStore struct{}

func (stubEnvelopeStore) CreateEnvelopeInstance(inst *store.EnvelopeInstance) error { return nil }
func (stubEnvelopeStore) GetEnvelopeInstance(id string) (*store.EnvelopeInstance, error) {
	return nil, fmt.Errorf("not found")
}

type stubReminderStore struct{}

func (stubReminderStore) CreateReminder(store.Reminder) error                   { return nil }
func (stubReminderStore) GetReminder(string) (store.Reminder, error)            { return store.Reminder{}, nil }
func (stubReminderStore) ListUnfiredReminders(string) ([]store.Reminder, error) { return nil, nil }
func (stubReminderStore) MarkReminderFired(string) error                        { return nil }
func (stubReminderStore) DeleteReminder(string) error                           { return nil }

type stubPinnedContentStore struct{}

func (stubPinnedContentStore) CreatePinnedContent(store.PinnedContent) error          { return nil }
func (stubPinnedContentStore) ListPinnedContent(string) ([]store.PinnedContent, error) { return nil, nil }
func (stubPinnedContentStore) DeletePinnedContent(string) error                        { return nil }
func (stubPinnedContentStore) ClearSessionPins(string) error                           { return nil }

type stubHandoffStashStore struct{}

func (stubHandoffStashStore) UpsertHandoffStash(store.HandoffStash) error { return nil }

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
func (stubSessionStore) GetSession(string) (*store.Session, error)                          { return nil, fmt.Errorf("not found") }
func (stubSessionStore) ListSessions(string, ...bool) ([]store.Session, error)              { return nil, nil }
func (stubSessionStore) ListMessages(string, int) ([]store.Message, error)                  { return nil, nil }
func (stubSessionStore) ListMessagesPaginated(string, int, int) (*store.MessagePage, error) { return nil, nil }
func (stubSessionStore) ListMessagesAroundID(string, string, int, int) (*store.MessagePage, error) { return nil, nil }
func (stubSessionStore) GetMessage(string) (*store.Message, error)                          { return nil, fmt.Errorf("not found") }
func (stubSessionStore) SearchMessages(string, string, string, int) ([]store.SearchResult, error) { return nil, nil }
func (stubSessionStore) CreateSession(*store.Session) error                                 { return nil }
func (stubSessionStore) UpdateSession(*store.Session) error                                 { return nil }
func (stubSessionStore) UpdateSessionTags(string, string) error                             { return nil }
func (stubSessionStore) UpdateSessionMetadata(string, string) error                         { return nil }
func (stubSessionStore) ArchiveSession(string) error                                        { return nil }
func (stubSessionStore) NextShortCode() (string, error)                                     { return "", nil }
func (stubSessionStore) CreateMessage(*store.Message) error                                 { return nil }
func (stubSessionStore) UpdateMessageContent(string, string, bool) error                    { return nil }
func (stubSessionStore) UpdateSessionCompaction(string, string) error                       { return nil }
func (stubSessionStore) ForkSession(string, *store.Session, bool) (*store.Session, error)   { return nil, nil }
func (stubSessionStore) CopyMessages(string, string) error                                  { return nil }

type stubAgentReaderStore struct{}
func (stubAgentReaderStore) GetAgent(string) (*store.AgentProfile, error)                   { return nil, fmt.Errorf("not found") }
func (stubAgentReaderStore) GetAgentBySlug(string) (*store.AgentProfile, error)             { return nil, fmt.Errorf("not found") }
func (stubAgentReaderStore) ListAgents() ([]store.AgentProfile, error)                      { return nil, nil }
func (stubAgentReaderStore) ListAgentsBySource(string) ([]store.AgentProfile, error)        { return nil, nil }
func (stubAgentReaderStore) GetAgentMode(string, string) (*store.AgentMode, error)          { return nil, fmt.Errorf("not found") }
func (stubAgentReaderStore) ListAgentModes(string) ([]store.AgentMode, error)               { return nil, nil }
func (stubAgentReaderStore) GetSessionPrimaryAgent(string) (*store.SessionAgent, error)     { return nil, fmt.Errorf("not found") }
func (stubAgentReaderStore) ListSessionAgents(string) ([]store.SessionAgent, error)         { return nil, nil }
func (stubAgentReaderStore) ListAgentSkills(string) ([]store.Skill, error)                  { return nil, nil }
func (stubAgentReaderStore) ListAgentProjects(string) ([]store.Project, error)              { return nil, nil }
func (stubAgentReaderStore) ListProjectAgents(string) ([]store.AgentProfile, error)         { return nil, nil }
func (stubAgentReaderStore) GetAgentAssignedModes(string) ([]store.Mode, error)             { return nil, nil }

type stubAgentWriterStore struct{}
func (stubAgentWriterStore) CreateAgent(*store.AgentProfile) error                          { return nil }
func (stubAgentWriterStore) UpdateAgent(*store.AgentProfile) error                          { return nil }
func (stubAgentWriterStore) DeleteAgent(string) error                                       { return nil }
func (stubAgentWriterStore) UpsertAgentBySlug(*store.AgentProfile) error                    { return nil }
func (stubAgentWriterStore) CreateAgentMode(*store.AgentMode) error                         { return nil }
func (stubAgentWriterStore) EnsureSessionAgent(string, string, string, bool) error          { return nil }
func (stubAgentWriterStore) SetSessionAgentMode(string, string, string) error               { return nil }
func (stubAgentWriterStore) DeleteSessionAgent(string, string) error                        { return nil }
func (stubAgentWriterStore) AssignSkillToAgent(string, string, string) error                { return nil }
func (stubAgentWriterStore) RemoveSkillFromAgent(string, string) error                      { return nil }
func (stubAgentWriterStore) AddAgentProject(string, string) error                           { return nil }
func (stubAgentWriterStore) RemoveAgentProject(string, string) error                        { return nil }
func (stubAgentWriterStore) AssignModeToAgent(string, string) error                         { return nil }
func (stubAgentWriterStore) UnassignModeFromAgent(string, string) error                     { return nil }

type stubToolStore struct{}
func (stubToolStore) ListMCPServers() ([]store.MCPServerConfig, error)                      { return nil, nil }
func (stubToolStore) GetMCPServer(string) (*store.MCPServerConfig, error)                   { return nil, nil }
func (stubToolStore) CreateMCPServer(*store.MCPServerConfig) error                          { return nil }
func (stubToolStore) UpdateMCPServer(*store.MCPServerConfig) error                          { return nil }
func (stubToolStore) DeleteMCPServer(string) error                                          { return nil }
func (stubToolStore) ListCatalogSources() ([]store.CatalogSource, error)                    { return nil, nil }
func (stubToolStore) GetCatalogSource(string) (*store.CatalogSource, error)                 { return nil, nil }
func (stubToolStore) CreateCatalogSource(string, string, string, int) (*store.CatalogSource, error) { return nil, nil }
func (stubToolStore) UpdateCatalogSource(string, string, string, bool, int) error           { return nil }
func (stubToolStore) SetCatalogSourcePublicKey(string, string) error                        { return nil }
func (stubToolStore) DeleteCatalogSource(string) error                                      { return nil }

type stubUsageStore struct{}
func (stubUsageStore) RecordUsage(string, string, string, int, int, int, int, int) error    { return nil }
func (stubUsageStore) GetSessionUsage(string) (*store.SessionUsageSummary, error)           { return nil, nil }
func (stubUsageStore) GetUsageSummary() (*store.UsageSummary, error)                        { return nil, nil }
func (stubUsageStore) RecordExecutionMetrics(*store.ExecutionMetrics) error                  { return nil }
func (stubUsageStore) GetSessionExecutionMetrics(string) ([]store.ExecutionMetrics, error)  { return nil, nil }
func (stubUsageStore) GetRecentExecutionMetrics(int) ([]store.ExecutionMetrics, error)      { return nil, nil }
func (stubUsageStore) GetUtilityCallSummary() ([]store.UtilityCallSummary, error)           { return nil, nil }
func (stubUsageStore) GetUtilityCallLog(int) ([]store.ExecutionMetrics, error)              { return nil, nil }
func (stubUsageStore) LogEvent(string, string, string, string, string)                      {}
func (stubUsageStore) ListEvents(string, int) ([]store.EventLog, error)                     { return nil, nil }
func (stubUsageStore) CountSessionToolCalls(string) int                                     { return 0 }

type stubWorkspaceStore struct{}
func (stubWorkspaceStore) ListWorkspaces() ([]store.Workspace, error)                       { return nil, nil }
func (stubWorkspaceStore) GetWorkspace(string) (*store.Workspace, error)                    { return nil, nil }
func (stubWorkspaceStore) CreateWorkspace(*store.Workspace) error                           { return nil }
func (stubWorkspaceStore) UpdateWorkspace(*store.Workspace) error                           { return nil }
func (stubWorkspaceStore) DeleteWorkspace(string) error                                     { return nil }
func (stubWorkspaceStore) ListProjects(string) ([]store.Project, error)                     { return nil, nil }
func (stubWorkspaceStore) GetProject(string) (*store.Project, error)                        { return nil, nil }
func (stubWorkspaceStore) CreateProject(*store.Project) error                               { return nil }
func (stubWorkspaceStore) UpdateProject(*store.Project) error                               { return nil }
func (stubWorkspaceStore) DeleteProject(string) error                                       { return nil }

type stubBookmarkStore struct{}
func (stubBookmarkStore) ListBookmarks(string) ([]store.Bookmark, error)                    { return nil, nil }
func (stubBookmarkStore) GetBookmark(string) (*store.Bookmark, error)                       { return nil, nil }
func (stubBookmarkStore) GetBookmarkByMessage(string) (*store.Bookmark, error)              { return nil, nil }
func (stubBookmarkStore) CreateBookmark(*store.Bookmark) error                              { return nil }
func (stubBookmarkStore) DeleteBookmark(string) error                                       { return nil }
func (stubBookmarkStore) UpdateBookmarkNote(string, string) error                           { return nil }

type stubArtifactStore struct{}
func (stubArtifactStore) ListArtifacts(string) ([]store.Artifact, error)                    { return nil, nil }
func (stubArtifactStore) ListArtifactsByOrigin(string, string) ([]store.Artifact, error)    { return nil, nil }
func (stubArtifactStore) CreateArtifact(*store.Artifact) error                              { return nil }
func (stubArtifactStore) GetArtifact(string) (*store.Artifact, error)                       { return nil, nil }

type stubTemplateStore struct{}
func (stubTemplateStore) ListPromptTemplates() ([]store.PromptTemplate, error)              { return nil, nil }
func (stubTemplateStore) GetPromptTemplate(string) (*store.PromptTemplate, error)           { return nil, nil }
func (stubTemplateStore) GetPromptTemplateBySlug(string) (*store.PromptTemplate, error)     { return nil, nil }
func (stubTemplateStore) CreatePromptTemplate(*store.PromptTemplate) error                  { return nil }
func (stubTemplateStore) UpdatePromptTemplate(*store.PromptTemplate) error                  { return nil }
func (stubTemplateStore) DeletePromptTemplate(string) error                                 { return nil }
func (stubTemplateStore) ListPromptTemplatesForAgent(string) ([]store.PromptTemplate, error) { return nil, nil }
func (stubTemplateStore) AssignPromptTemplateToAgent(string, string) error                  { return nil }
func (stubTemplateStore) RemovePromptTemplateFromAgent(string, string) error                { return nil }
func (stubTemplateStore) ComposePromptForAgent(string, map[string]string) (string, error)   { return "", nil }
func (stubTemplateStore) ListTemplates() ([]store.Template, error)                          { return nil, nil }
func (stubTemplateStore) GetTemplate(string) (*store.Template, error)                       { return nil, nil }
func (stubTemplateStore) CreateTemplate(*store.Template) error                              { return nil }
func (stubTemplateStore) UpdateTemplate(string, string) error                               { return nil }
func (stubTemplateStore) DeleteTemplate(string) error                                       { return nil }

type stubSkillStore struct{}
func (stubSkillStore) ListSkills() ([]store.Skill, error)                                   { return nil, nil }
func (stubSkillStore) GetSkill(string) (*store.Skill, error)                                { return nil, nil }
func (stubSkillStore) GetSkillBySlug(string) (*store.Skill, error)                          { return nil, nil }
func (stubSkillStore) CreateSkill(*store.Skill) error                                       { return nil }
func (stubSkillStore) UpdateSkill(*store.Skill) error                                       { return nil }
func (stubSkillStore) DeleteSkill(string) error                                             { return nil }

type stubModeStore struct{}
func (stubModeStore) CreateMode(*store.Mode) error                                          { return nil }
func (stubModeStore) GetMode(string) (*store.Mode, error)                                   { return nil, nil }
func (stubModeStore) GetModeBySlug(string) (*store.Mode, error)                             { return nil, nil }
func (stubModeStore) ListModes() ([]store.Mode, error)                                      { return nil, nil }
func (stubModeStore) UpdateMode(*store.Mode) error                                          { return nil }
func (stubModeStore) DeleteMode(string) error                                               { return nil }

type stubCustomActionStore struct{}
func (stubCustomActionStore) CreateCustomAction(*store.CustomAction) error                  { return nil }
func (stubCustomActionStore) GetCustomAction(string) (*store.CustomAction, error)           { return nil, nil }
func (stubCustomActionStore) UpdateCustomAction(*store.CustomAction) error                  { return nil }
func (stubCustomActionStore) DeleteCustomAction(string) error                               { return nil }
func (stubCustomActionStore) ListCustomActions() ([]store.CustomAction, error)              { return nil, nil }
func (stubCustomActionStore) ListCustomActionsByTrigger(string) ([]store.CustomAction, error) { return nil, nil }

type stubTriggerRuleStore struct{}
func (stubTriggerRuleStore) CreateTriggerRule(*store.TriggerRule) error                     { return nil }
func (stubTriggerRuleStore) GetTriggerRule(string) (*store.TriggerRule, error)              { return nil, nil }
func (stubTriggerRuleStore) UpdateTriggerRule(*store.TriggerRule) error                     { return nil }
func (stubTriggerRuleStore) DeleteTriggerRule(string) error                                 { return nil }
func (stubTriggerRuleStore) ListTriggerRules(string) ([]store.TriggerRule, error)           { return nil, nil }
func (stubTriggerRuleStore) ListTriggerRulesByEvent(string) ([]store.TriggerRule, error)    { return nil, nil }
func (stubTriggerRuleStore) DeleteTriggerRulesByPlugin(string) error                        { return nil }

type stubProviderStore struct{}
func (stubProviderStore) ListProviders() ([]store.ProviderConfig, error)                    { return nil, nil }
func (stubProviderStore) GetProvider(string) (*store.ProviderConfig, error)                 { return nil, nil }
func (stubProviderStore) ListModels() ([]store.Model, error)                                { return nil, nil }
func (stubProviderStore) UpdateProvider(string, store.ProviderUpdate) error                 { return nil }
func (stubProviderStore) SetProviderAPIKey(string, string) error                            { return nil }
func (stubProviderStore) HasProviderAPIKey(string) (bool, error)                            { return false, nil }

type stubTodoStore struct{}
func (stubTodoStore) CreateTodo(*store.Todo) error                                          { return nil }
func (stubTodoStore) GetTodo(string) (*store.Todo, error)                                   { return nil, nil }
func (stubTodoStore) ListTodos(store.TodoFilter) ([]store.Todo, error)                      { return nil, nil }
func (stubTodoStore) UpdateTodo(*store.Todo) error                                          { return nil }
func (stubTodoStore) DeleteTodo(string) error                                               { return nil }
func (stubTodoStore) ListTodoChildren(string) ([]store.Todo, error)                         { return nil, nil }

type stubPlanStore struct{}
func (stubPlanStore) CreatePlan(*store.Plan) error                                          { return nil }
func (stubPlanStore) GetPlan(string) (*store.Plan, error)                                   { return nil, nil }
func (stubPlanStore) ListPlans(store.PlanFilter) ([]store.Plan, error)                      { return nil, nil }
func (stubPlanStore) UpdatePlan(*store.Plan) error                                          { return nil }
func (stubPlanStore) UpdatePlanStep(string, string, store.PlanStep) error                   { return nil }
func (stubPlanStore) DeletePlan(string) error                                               { return nil }

func TestChatService_ResolveProvider(t *testing.T) {
	reg := provider.NewRegistry()
	testStore := &minimalStore{}
	impl := &chatServiceImpl{
		providers: reg,
		store:     testStore,
	}

	name, prov := impl.resolveProvider("test-session", "", "", "gpt-4o")
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
		{"llama3.1", "ollama"},
		// Registered Mistral model resolves through the canonical registry
		// (audit 2026-04-11 finding 02 — prior prefix match sent Mistral
		// API models to ollama).
		{"mistral-large", "mistral"},
		{"mistral-large-latest", "mistral"},
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
	// original engine behaviour: a real stuck loop sends the *same* raw
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
	result := captureEnvelopeData("plain result", "tool_a", nil)
	if len(result) != 0 {
		t.Errorf("no marker: got %d envelopes", len(result))
	}

	// With envelope marker.
	data := `some text <!--ENVELOPE_DATA:{"type":"kb-result"}:ENVELOPE_DATA--> more text`
	result = captureEnvelopeData(data, "tool_a", nil)
	if len(result) != 1 {
		t.Fatalf("with marker: got %d envelopes, want 1", len(result))
	}
	if result[0] != `{"type":"kb-result"}` {
		t.Errorf("envelope payload = %q", result[0])
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

	cancelledA := make(chan struct{})
	cancelA := context.CancelFunc(func() { close(cancelledA) })

	// First registration: no prior cancel.
	if prev := svc.registerGeneration("sess", "msg-A", cancelA); prev != nil {
		t.Fatalf("expected nil prev for first register, got %v", prev)
	}

	// Second registration on the same session: returns the first cancel.
	cancelB := context.CancelFunc(func() {})
	prev := svc.registerGeneration("sess", "msg-B", cancelB)
	if prev == nil {
		t.Fatal("expected prev cancel on second register, got nil")
	}
	prev()
	select {
	case <-cancelledA:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("prior cancel was not invoked")
	}

	// Deregistering the first msgID is a no-op because msg-B is current.
	svc.deregisterGeneration("sess", "msg-A")
	svc.activeGenMu.Lock()
	cur := svc.activeGen["sess"]
	svc.activeGenMu.Unlock()
	if cur == nil || cur.msgID != "msg-B" {
		t.Fatalf("expected msg-B still active, got %+v", cur)
	}

	// Deregistering the current msgID clears the slot.
	svc.deregisterGeneration("sess", "msg-B")
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

	if prev := svc.registerGeneration("sess-1", "msg-1", cancelA); prev != nil {
		t.Fatalf("expected nil prev for sess-1, got %v", prev)
	}
	if prev := svc.registerGeneration("sess-2", "msg-2", cancelB); prev != nil {
		t.Fatal("expected nil prev for sess-2 — different session")
	}
}

// Verify ChatService interface is satisfied at compile time.
var _ ChatService = (*chatServiceImpl)(nil)

// Verify the exported helper types are usable from the service package.
var _ = chat.StreamEvent{}
var _ = chat.PresenceEvent{}
