package service

// Wave 5 characterization coverage for generateResponse. These tests enter
// through the production Dispatcher -> chatRunnerAdapter door, use the real
// StreamManager and SQLite store, and pin the current observable turn-loop
// behavior before any decomposition is attempted.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/chat"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/dispatcher"
	pluginpkg "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/nanite/internal/toolclient"
	sdkplugin "github.com/hollis-labs/plugin-sdk"
)

type characterizationSessions struct{ st *store.Store }

func (f *characterizationSessions) Create(context.Context, CreateSessionOpts) (*store.Session, error) {
	panic("characterizationSessions.Create: unexpected call")
}
func (f *characterizationSessions) Get(ctx context.Context, id string) (*store.Session, error) {
	return f.st.GetSession(ctx, id)
}
func (f *characterizationSessions) List(context.Context, bool) ([]store.Session, error) {
	panic("characterizationSessions.List: unexpected call")
}
func (f *characterizationSessions) Update(context.Context, *store.Session) error {
	panic("characterizationSessions.Update: unexpected call")
}
func (f *characterizationSessions) Archive(context.Context, string) error {
	panic("characterizationSessions.Archive: unexpected call")
}
func (f *characterizationSessions) Fork(context.Context, string, ForkOpts) (*store.Session, error) {
	panic("characterizationSessions.Fork: unexpected call")
}
func (f *characterizationSessions) ListMessages(ctx context.Context, id string, limit int) ([]store.Message, error) {
	return f.st.ListMessages(ctx, id, limit)
}
func (f *characterizationSessions) Search(context.Context, string, SearchOpts) ([]store.SearchResult, error) {
	panic("characterizationSessions.Search: unexpected call")
}

type characterizationAgents struct{ agent *store.AgentProfile }

func (f *characterizationAgents) Get(context.Context, string) (*store.AgentProfile, error) {
	// The execution-time grant check intentionally fails open on a stale
	// agent lookup. Agent resolution itself remains real for the turn.
	return nil, errors.New("characterization agent is fixture-local")
}
func (f *characterizationAgents) GetBySlug(context.Context, string) (*store.AgentProfile, error) {
	return f.agent, nil
}
func (f *characterizationAgents) List(context.Context) ([]store.AgentProfile, error) {
	return []store.AgentProfile{*f.agent}, nil
}
func (f *characterizationAgents) Create(context.Context, *store.AgentProfile) error {
	panic("characterizationAgents.Create: unexpected call")
}
func (f *characterizationAgents) Update(context.Context, *store.AgentProfile) error {
	panic("characterizationAgents.Update: unexpected call")
}
func (f *characterizationAgents) Delete(context.Context, string) error {
	panic("characterizationAgents.Delete: unexpected call")
}
func (f *characterizationAgents) ResolveForSession(context.Context, string) (*store.AgentProfile, error) {
	return f.agent, nil
}
func (f *characterizationAgents) ResolveForSessionReadOnly(context.Context, string) (*store.AgentProfile, error) {
	return f.agent, nil
}

type characterizationTools struct {
	definitions []llmtypes.ToolDefinition

	mu       sync.Mutex
	executed []string
}

func (f *characterizationTools) SelectForAgent(context.Context, string, string, string, string, int) (*ToolSelection, error) {
	return &ToolSelection{Tools: append([]llmtypes.ToolDefinition(nil), f.definitions...)}, nil
}
func (f *characterizationTools) Execute(_ context.Context, _ string, name string, input map[string]any) (*ToolResult, error) {
	f.mu.Lock()
	f.executed = append(f.executed, name)
	f.mu.Unlock()
	return &ToolResult{Output: fmt.Sprintf("%s-result:%v", name, input["value"])}, nil
}
func (f *characterizationTools) HandleRequestTools(context.Context, string, map[string]any) ([]llmtypes.ToolDefinition, string, error) {
	panic("characterizationTools.HandleRequestTools: unexpected call")
}
func (f *characterizationTools) ListSummaries() []toolclient.ToolSummary { return nil }
func (f *characterizationTools) GetToolMeta(context.Context, string) (ToolMetaInfo, bool) {
	// Serial execution makes the multi-tool observable ordering deterministic.
	return ToolMetaInfo{IsReadOnly: true, IsConcurrencySafe: false}, true
}
func (f *characterizationTools) GetToolSchema(string) map[string]any { return nil }
func (f *characterizationTools) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.executed...)
}

type characterizationProviderStep struct {
	events       []llmtypes.StreamEvent
	err          error
	beforeReturn func()
}

type characterizationProvider struct {
	mu       sync.Mutex
	steps    []characterizationProviderStep
	requests []llmtypes.ChatRequest
}

func (p *characterizationProvider) StreamChat(_ context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	idx := len(p.requests) - 1
	if idx >= len(p.steps) {
		return nil, fmt.Errorf("characterization provider: no step %d", idx)
	}
	step := p.steps[idx]
	if step.beforeReturn != nil {
		step.beforeReturn()
	}
	if step.err != nil {
		return nil, step.err
	}
	ch := make(chan llmtypes.StreamEvent, len(step.events))
	for _, event := range step.events {
		ch <- event
	}
	close(ch)
	return ch, nil
}
func (p *characterizationProvider) Complete(context.Context, llmtypes.ChatRequest) (string, error) {
	return "compacted conversation summary", nil
}
func (p *characterizationProvider) CompleteWithUsage(context.Context, llmtypes.ChatRequest) (llmtypes.CompleteResult, error) {
	return llmtypes.CompleteResult{Text: "compacted conversation summary"}, nil
}
func (p *characterizationProvider) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{}
}
func (p *characterizationProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

func (p *characterizationProvider) requestsSnapshot() []llmtypes.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]llmtypes.ChatRequest(nil), p.requests...)
}

var _ llmcontracts.Provider = (*characterizationProvider)(nil)

type characterizationFixture struct {
	svc      *chatServiceImpl
	st       *store.Store
	provider *characterizationProvider
	tools    *characterizationTools
	context  *characterizationContext
	session  string
}

type characterizationContext struct {
	inner  ContextService
	mutate func(*SlotAssemblyResult)

	mu   sync.Mutex
	last *SlotAssemblyResult
}

func (c *characterizationContext) AssembleSlots(ctx context.Context, session *store.Session, agent *store.AgentProfile, tools []llmtypes.ToolDefinition, prefix string, window int, lazyHint string) (*SlotAssemblyResult, error) {
	result, err := c.inner.AssembleSlots(ctx, session, agent, tools, prefix, window, lazyHint)
	if err != nil {
		return nil, err
	}
	if c.mutate != nil {
		c.mutate(result)
	}
	c.mu.Lock()
	c.last = result
	c.mu.Unlock()
	return result, nil
}

func (c *characterizationContext) PruneAfterTurn(ctx context.Context, sessionID string) error {
	return c.inner.PruneAfterTurn(ctx, sessionID)
}

func (c *characterizationContext) mutateLast(fn func(*SlotAssemblyResult)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil {
		panic("characterizationContext: no assembled result")
	}
	fn(c.last)
}

func newCharacterizationFixture(t *testing.T, steps []characterizationProviderStep, toolNames ...string) *characterizationFixture {
	t.Helper()
	ctx := context.Background()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "characterization.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	if err := st.Seed(ctx); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	sessionID := "session-characterization"
	if err := st.CreateSession(ctx, &store.Session{
		ID:       sessionID,
		Title:    "generateResponse characterization",
		Provider: "characterization",
		Model:    "characterization-model",
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := st.CreateMessage(ctx, &store.Message{
		ID: "user-characterization", SessionID: sessionID, Role: "user", Content: "characterize this turn",
	}); err != nil {
		t.Fatalf("CreateMessage(user): %v", err)
	}

	agent := &store.AgentProfile{
		ID: "agent-characterization", Name: "Characterization Agent", Slug: "characterization-agent",
		Status: "active", Class: "advisor", DefaultProvider: "characterization", Tags: "[]", Tools: "[]",
	}
	definitions := make([]llmtypes.ToolDefinition, 0, len(toolNames))
	for _, name := range toolNames {
		definitions = append(definitions, llmtypes.ToolDefinition{Name: name, Description: name + " fixture tool"})
	}
	toolSvc := &characterizationTools{definitions: definitions}
	prov := &characterizationProvider{steps: steps}
	registry := provider.NewRegistry()
	registry.Register("characterization", prov)
	streams := NewStreamManager()
	contextSvc := &characterizationContext{inner: NewContextService(ContextServiceConfig{Client: chat.NewContextClient(st)})}
	svc := &chatServiceImpl{
		sessions:     &characterizationSessions{st: st},
		agents:       &characterizationAgents{agent: agent},
		tools:        toolSvc,
		streams:      streams,
		context:      contextSvc,
		providers:    registry,
		store:        st,
		argValidator: newArgValidator(),
	}
	svc.dispatcher = dispatcher.New(newChatRunnerAdapter(svc))
	return &characterizationFixture{svc: svc, st: st, provider: prov, tools: toolSvc, context: contextSvc, session: sessionID}
}

func forceCompactableWindow(result *SlotAssemblyResult) {
	result.Window.SetContent(ctxpkg.SlotContext, strings.Repeat("dynamic enrichment ", 400))
	result.Window.SetFlags(ctxpkg.SlotContext, ctxpkg.SlotFlags{EnrichmentActive: true})
	// Keep the persisted conversation small enough for the normal chat hard
	// ceiling while forcing the context compaction pipeline to run. The
	// pipeline refreshes SlotConversation from the real message slice before
	// its first stage, so inflating that slot directly would be erased.
	result.Window.TotalBudget = 1
	result.NeedsCompaction = true
}

func (f *characterizationFixture) run(t *testing.T, messageID string) []chat.StreamEvent {
	t.Helper()
	producer := f.svc.streams.CreateStream(messageID, f.session)
	consumer, ok := f.svc.streams.GetStream(messageID)
	if !ok {
		t.Fatal("GetStream: production stream was not registered")
	}
	if err := f.svc.dispatcher.Run(context.Background(), dispatcher.Request{
		SessionID: f.session, AssistantMsgID: messageID, UserContent: "characterize this turn", CallerType: dispatcher.CallerChat,
	}, producer); err != nil {
		t.Fatalf("Dispatcher.Run: %v", err)
	}
	var events []chat.StreamEvent
	for event := range consumer {
		events = append(events, event)
	}
	return events
}

func doneEvents(content string) []llmtypes.StreamEvent {
	return []llmtypes.StreamEvent{
		{Type: "delta", Content: content},
		{Type: "usage", Usage: &llmtypes.Usage{StopReason: "end_turn", InputTokens: 11, OutputTokens: 3}},
		{Type: "done"},
	}
}

func toolTurnEvents(toolUses ...llmtypes.ToolUseBlock) []llmtypes.StreamEvent {
	events := make([]llmtypes.StreamEvent, 0, len(toolUses)+3)
	events = append(events, llmtypes.StreamEvent{Type: "delta", Content: "I will use the tools. "})
	for i := range toolUses {
		tu := toolUses[i]
		events = append(events, llmtypes.StreamEvent{Type: "tool_use", ToolUse: &tu})
	}
	events = append(events,
		llmtypes.StreamEvent{Type: "usage", Usage: &llmtypes.Usage{StopReason: "tool_use"}},
		llmtypes.StreamEvent{Type: "done"},
	)
	return events
}

func eventTypes(events []chat.StreamEvent) []string {
	types := make([]string, len(events))
	for i, event := range events {
		types[i] = event.Type
	}
	return types
}

func toolEventSequence(events []chat.StreamEvent) []string {
	var got []string
	for _, event := range events {
		if event.Type == "tool_call" || event.Type == "tool_result" {
			got = append(got, event.Type+":"+event.Tool)
		}
	}
	return got
}

func findEvent(events []chat.StreamEvent, eventType string) *chat.StreamEvent {
	for i := range events {
		if events[i].Type == eventType {
			return &events[i]
		}
	}
	return nil
}

func TestGenerateResponseCharacterization_PlainNoToolTurn(t *testing.T) {
	f := newCharacterizationFixture(t, []characterizationProviderStep{{events: doneEvents("plain answer")}})
	events := f.run(t, "assistant-plain")

	if got := f.provider.callCount(); got != 1 {
		t.Fatalf("provider calls = %d, want 1", got)
	}
	if !reflect.DeepEqual(eventTypes(events), []string{"tool_warning", "stream_start", "delta", "stream_end"}) {
		t.Fatalf("event sequence = %v", eventTypes(events))
	}
	if events[2].Content != "plain answer" || events[2].Phase != chat.PhaseFinal {
		t.Fatalf("final delta = %+v", events[2])
	}
}

func TestGenerateResponseCharacterization_PersistenceFailureSuppressesStreamEnd(t *testing.T) {
	f := newCharacterizationFixture(t, nil)
	f.provider.steps = []characterizationProviderStep{{
		events: doneEvents("cannot persist"),
		beforeReturn: func() {
			if err := f.st.Close(context.Background()); err != nil {
				t.Fatalf("close store before final persistence: %v", err)
			}
		},
	}}
	events := f.run(t, "assistant-persistence-failure")

	if findEvent(events, "error") == nil {
		t.Fatalf("events = %v, want persistence error", eventTypes(events))
	}
	if findEvent(events, "stream_end") != nil {
		t.Fatalf("events = %v, stream_end must follow successful persistence", eventTypes(events))
	}
}

func TestGenerateResponseCharacterization_DisabledAgentTerminatesBeforeStreamStart(t *testing.T) {
	f := newCharacterizationFixture(t, []characterizationProviderStep{{events: doneEvents("must not run")}})
	f.svc.agents.(*characterizationAgents).agent.Status = "disabled"
	events := f.run(t, "assistant-disabled-agent")

	if got := f.provider.callCount(); got != 0 {
		t.Fatalf("provider calls = %d, want 0", got)
	}
	if got := eventTypes(events); !reflect.DeepEqual(got, []string{"error"}) {
		t.Fatalf("event sequence = %v, want early error only", got)
	}
}

func TestGenerateResponseCharacterization_SingleToolTurn(t *testing.T) {
	tu := llmtypes.ToolUseBlock{ID: "tool-single", Name: "echo", Input: map[string]any{"value": "one"}}
	toolEvents := toolTurnEvents(tu)
	toolEvents[len(toolEvents)-2].Usage.InputTokens = 5
	toolEvents[len(toolEvents)-2].Usage.OutputTokens = 2
	f := newCharacterizationFixture(t, []characterizationProviderStep{
		{events: toolEvents},
		{events: doneEvents("single tool complete")},
	}, "echo")
	events := f.run(t, "assistant-single")

	if got := f.tools.calls(); !reflect.DeepEqual(got, []string{"echo"}) {
		t.Fatalf("executed tools = %v", got)
	}
	if got := toolEventSequence(events); !reflect.DeepEqual(got, []string{"tool_call:echo", "tool_result:echo"}) {
		t.Fatalf("tool event sequence = %v", got)
	}
	if f.provider.callCount() != 2 || findEvent(events, "stream_end") == nil {
		t.Fatalf("provider calls/events = %d/%v", f.provider.callCount(), eventTypes(events))
	}
	var narration, final bool
	for _, event := range events {
		if event.Type == "delta" && event.Content == "I will use the tools. " && event.Phase == chat.PhaseNarration {
			narration = true
		}
		if event.Type == "delta" && event.Content == "single tool complete" && event.Phase == chat.PhaseFinal {
			final = true
		}
	}
	if !narration || !final {
		t.Fatalf("delta phases missing: narration=%v final=%v events=%+v", narration, final, events)
	}
	end := findEvent(events, "stream_end")
	if end.Usage == nil || end.Usage.InputTokens != 16 || end.Usage.OutputTokens != 5 || end.Usage.StopReason != "end_turn" {
		t.Fatalf("aggregated usage = %+v, want input=16 output=5 stop=end_turn", end.Usage)
	}
}

func TestGenerateResponseCharacterization_MultiToolTurn(t *testing.T) {
	alpha := llmtypes.ToolUseBlock{ID: "tool-alpha", Name: "alpha", Input: map[string]any{"value": "a"}}
	beta := llmtypes.ToolUseBlock{ID: "tool-beta", Name: "beta", Input: map[string]any{"value": "b"}}
	f := newCharacterizationFixture(t, []characterizationProviderStep{
		{events: toolTurnEvents(alpha, beta)},
		{events: doneEvents("multi tool complete")},
	}, "alpha", "beta")
	events := f.run(t, "assistant-multi")

	if got := f.tools.calls(); !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Fatalf("serial tool execution order = %v", got)
	}
	wantEvents := []string{"tool_call:alpha", "tool_call:beta", "tool_result:alpha", "tool_result:beta"}
	if got := toolEventSequence(events); !reflect.DeepEqual(got, wantEvents) {
		t.Fatalf("tool event sequence = %v, want %v", got, wantEvents)
	}
}

func TestGenerateResponseCharacterization_ProviderErrorMidStream(t *testing.T) {
	// This intentionally omits EventDone after EventError. The chat loop must
	// terminate on the error event rather than waiting for a done sentinel.
	f := newCharacterizationFixture(t, []characterizationProviderStep{{events: []llmtypes.StreamEvent{
		{Type: "delta", Content: "partial-but-buffered"},
		{Type: "error", Error: "upstream disconnected"},
	}}})
	events := f.run(t, "assistant-provider-error")

	if findEvent(events, "error") == nil {
		t.Fatalf("events = %v, want structured error", eventTypes(events))
	}
	if findEvent(events, "stream_end") != nil {
		t.Fatalf("events = %v, did not expect stream_end after provider EventError", eventTypes(events))
	}
	for _, event := range events {
		if event.Type == "delta" && event.Content == "partial-but-buffered" {
			t.Fatal("mid-stream partial delta was unexpectedly flushed before provider error")
		}
	}
	msgs, err := f.st.ListMessages(context.Background(), f.session, 20)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var persisted *store.Message
	for i := range msgs {
		if msgs[i].ID == "assistant-provider-error" {
			persisted = &msgs[i]
			break
		}
	}
	if persisted == nil {
		t.Fatalf("assistant-provider-error row missing: %+v", msgs)
	}
	if persisted.Role != "assistant" {
		t.Fatalf("partial row role = %q, want assistant", persisted.Role)
	}
	if !strings.Contains(persisted.Content, "partial-but-buffered") {
		t.Fatalf("partial row content = %q, want buffered partial", persisted.Content)
	}
	if !strings.Contains(persisted.Metadata, `"had_error":true`) {
		t.Fatalf("partial assistant metadata = %q, want had_error", persisted.Metadata)
	}
}

func TestGenerateResponseCharacterization_ProviderErrorBeforeStreamKeepsToolsDetailKey(t *testing.T) {
	f := newCharacterizationFixture(t, []characterizationProviderStep{{err: errors.New("upstream unavailable")}})
	events := f.run(t, "assistant-provider-error-before-stream")

	errEvent := findEvent(events, "error")
	if errEvent == nil || errEvent.StructuredError == nil {
		t.Fatalf("events = %v, want structured provider error", eventTypes(events))
	}
	if got, ok := errEvent.StructuredError.Details["tools"]; !ok || got != 0 {
		t.Fatalf("provider error details = %#v, want tools:0", errEvent.StructuredError.Details)
	}
	if _, leaked := errEvent.StructuredError.Details["run.tools"]; leaked {
		t.Fatalf("provider error details leaked state field name: %#v", errEvent.StructuredError.Details)
	}
}

func TestGenerateResponseCharacterization_RefusedOverflowRecoveryTerminates(t *testing.T) {
	f := newCharacterizationFixture(t, []characterizationProviderStep{{err: ctxpkg.ErrContextOverflow}})
	events := f.run(t, "assistant-overflow-refused")

	if f.provider.callCount() != 1 {
		t.Fatalf("provider calls = %d, want no retry after refused recovery", f.provider.callCount())
	}
	errEvent := findEvent(events, "error")
	if errEvent == nil || errEvent.StructuredError == nil {
		t.Fatalf("events = %v, want structured recovery error", eventTypes(events))
	}
	if got := errEvent.StructuredError.Details["recovery"]; got != "refused" {
		t.Fatalf("recovery detail = %#v, want refused", got)
	}
	if findEvent(events, "stream_end") != nil {
		t.Fatalf("refused recovery unexpectedly emitted stream_end: %v", eventTypes(events))
	}
}

func TestGenerateResponseCharacterization_ContextOverflowCompactionTrigger(t *testing.T) {
	f := newCharacterizationFixture(t, nil)
	if err := f.st.UpdateUserSettings(context.Background(), &store.UserSettings{
		ContextOverflowRecovery: true,
		SummarizerProvider:      "characterization",
		SummarizerModel:         "characterization-model",
	}); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}
	f.provider.steps = []characterizationProviderStep{
		{err: ctxpkg.ErrContextOverflow, beforeReturn: func() {
			f.context.mutateLast(forceCompactableWindow)
		}},
		{events: doneEvents("recovered after compaction")},
	}
	events := f.run(t, "assistant-overflow")

	if f.provider.callCount() != 2 {
		t.Fatalf("provider calls = %d, want overflow plus retry", f.provider.callCount())
	}
	if findEvent(events, "slot_changed") == nil || findEvent(events, "stream_end") == nil {
		t.Fatalf("overflow recovery events = %v, want slot_changed and stream_end", eventTypes(events))
	}
	if findEvent(events, "error") != nil {
		t.Fatalf("overflow recovery surfaced error: %v", eventTypes(events))
	}
	msgs, err := f.st.ListMessages(context.Background(), f.session, 20)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var saved bool
	for _, msg := range msgs {
		saved = saved || (msg.ID == "assistant-overflow" && msg.Role == "assistant" && strings.Contains(msg.Content, "recovered after compaction"))
	}
	if !saved {
		t.Fatalf("recovered assistant row missing: %+v", msgs)
	}
}

func TestGenerateResponseCharacterization_MidStreamOverflowRetriesIteration(t *testing.T) {
	f := newCharacterizationFixture(t, nil)
	if err := f.st.UpdateUserSettings(context.Background(), &store.UserSettings{
		ContextOverflowRecovery: true,
		SummarizerProvider:      "characterization",
		SummarizerModel:         "characterization-model",
	}); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}
	f.provider.steps = []characterizationProviderStep{
		{events: []llmtypes.StreamEvent{{Type: "error", Error: "prompt is too long"}}, beforeReturn: func() {
			f.context.mutateLast(forceCompactableWindow)
		}},
		{events: doneEvents("recovered after mid-stream compaction")},
	}
	events := f.run(t, "assistant-midstream-overflow")

	if f.provider.callCount() != 2 {
		t.Fatalf("provider calls = %d, want overflow plus same-iteration retry", f.provider.callCount())
	}
	if findEvent(events, "slot_changed") == nil || findEvent(events, "stream_end") == nil {
		t.Fatalf("mid-stream recovery events = %v, want slot_changed and stream_end", eventTypes(events))
	}
	if findEvent(events, "error") != nil {
		t.Fatalf("mid-stream recovery surfaced error: %v", eventTypes(events))
	}
}

func TestGenerateResponseCharacterization_PreLoopBudgetCompaction(t *testing.T) {
	f := newCharacterizationFixture(t, []characterizationProviderStep{{events: doneEvents("pre-loop compacted")}})
	if err := f.st.UpdateUserSettings(context.Background(), &store.UserSettings{
		ContextOverflowRecovery: true,
		SummarizerProvider:      "characterization",
		SummarizerModel:         "characterization-model",
	}); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}
	f.context.mutate = forceCompactableWindow
	events := f.run(t, "assistant-preloop-compaction")
	if f.provider.callCount() != 1 || findEvent(events, "slot_changed") == nil || findEvent(events, "stream_end") == nil {
		t.Fatalf("pre-loop compaction calls/events = %d/%v", f.provider.callCount(), eventTypes(events))
	}
	var streamStartAt, slotChangedAt = -1, -1
	for i, event := range events {
		if event.Type == "stream_start" {
			streamStartAt = i
		}
		if event.Type == "slot_changed" {
			slotChangedAt = i
		}
	}
	if streamStartAt < 0 || slotChangedAt < 0 || streamStartAt >= slotChangedAt {
		t.Fatalf("initialization ordering = %v, want stream_start before pre-loop compaction slot_changed", eventTypes(events))
	}
}

type characterizationCancelHook struct{ called bool }

func (h *characterizationCancelHook) Handle(context.Context, sdkplugin.Event) error {
	h.called = true
	return sdkplugin.ErrCancelled
}
func (h *characterizationCancelHook) EventTypes() []string {
	return []string{pluginpkg.EventMessageSending}
}
func (h *characterizationCancelHook) PluginID() string { return "characterization-cancel" }

func TestGenerateResponseCharacterization_PluginInitiatedCancel(t *testing.T) {
	f := newCharacterizationFixture(t, []characterizationProviderStep{{events: doneEvents("must not run")}})
	host := pluginpkg.NewHost(http.NewServeMux(), pluginpkg.NewLogger("characterization"))
	t.Cleanup(func() { _ = host.Shutdown() })
	hook := &characterizationCancelHook{}
	if err := host.RegisterEventHook([]string{pluginpkg.EventMessageSending}, hook); err != nil {
		t.Fatalf("RegisterEventHook: %v", err)
	}
	f.svc.pluginHost = host
	events := f.run(t, "assistant-plugin-cancel")

	if !hook.called {
		t.Fatal("message.sending hook was not invoked")
	}
	if got := f.provider.callCount(); got != 0 {
		t.Fatalf("provider calls = %d, want 0 after plugin cancellation", got)
	}
	var blocked bool
	for _, event := range events {
		blocked = blocked || (event.Type == "delta" && event.Content == "Message blocked by plugin policy.")
	}
	if !blocked || findEvent(events, "stream_end") == nil {
		t.Fatalf("plugin-cancel events = %v", eventTypes(events))
	}
}

type characterizationToolCancelHook struct {
	called bool
	event  sdkplugin.Event
}

func (h *characterizationToolCancelHook) Handle(_ context.Context, event sdkplugin.Event) error {
	h.called = true
	h.event = event
	return sdkplugin.ErrCancelled
}
func (h *characterizationToolCancelHook) EventTypes() []string {
	return []string{pluginpkg.EventToolExecuting}
}
func (h *characterizationToolCancelHook) PluginID() string {
	return "characterization-tool-cancel"
}

func TestGenerateResponseCharacterization_PluginToolExecutingCancel(t *testing.T) {
	tu := llmtypes.ToolUseBlock{ID: "tool-blocked", Name: "echo", Input: map[string]any{"value": "blocked"}}
	f := newCharacterizationFixture(t, []characterizationProviderStep{
		{events: toolTurnEvents(tu)},
		{events: doneEvents("continued after blocked tool")},
	}, "echo")
	host := pluginpkg.NewHost(http.NewServeMux(), pluginpkg.NewLogger("characterization"))
	t.Cleanup(func() { _ = host.Shutdown() })
	hook := &characterizationToolCancelHook{}
	if err := host.RegisterEventHook([]string{pluginpkg.EventToolExecuting}, hook); err != nil {
		t.Fatalf("RegisterEventHook: %v", err)
	}
	f.svc.pluginHost = host
	events := f.run(t, "assistant-tool-plugin-cancel")

	if !hook.called {
		t.Fatal("tool.executing hook was not invoked")
	}
	if got := hook.event.Data["tool_name"]; got != "echo" {
		t.Fatalf("tool.executing tool_name = %v, want echo", got)
	}
	if got := hook.event.Data["tool_id"]; got != "tool-blocked" {
		t.Fatalf("tool.executing tool_id = %v, want tool-blocked", got)
	}
	if got := f.tools.calls(); len(got) != 0 {
		t.Fatalf("ToolService.Execute calls = %v, want none", got)
	}
	if got := toolEventSequence(events); !reflect.DeepEqual(got, []string{"tool_call:echo", "tool_result:echo"}) {
		t.Fatalf("blocked tool event sequence = %v", got)
	}
	var blockedCall *chat.StreamEvent
	var blockedResult *chat.StreamEvent
	for i := range events {
		if events[i].Type == "tool_call" && events[i].ToolID == "tool-blocked" {
			blockedCall = &events[i]
		}
		if events[i].Type == "tool_result" && events[i].ToolID == "tool-blocked" {
			blockedResult = &events[i]
		}
	}
	if blockedCall == nil || blockedCall.Tool != "echo" {
		t.Fatalf("blocked tool call = %+v", blockedCall)
	}
	if blockedResult == nil || !blockedResult.IsError || !strings.Contains(blockedResult.Summary, "refused by a policy plugin") {
		t.Fatalf("blocked tool result = %+v", blockedResult)
	}
	if findEvent(events, "stream_end") == nil || findEvent(events, "error") != nil {
		t.Fatalf("tool-cancel terminal events = %v", eventTypes(events))
	}

	requests := f.provider.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("provider requests = %d, want tool turn plus continuation", len(requests))
	}
	var continuationBlock *llmtypes.ContentBlock
	var continuationRole string
	for _, msg := range requests[1].Messages {
		for i := range msg.ContentBlocks {
			block := &msg.ContentBlocks[i]
			if block.Type == "tool_result" && block.ToolUseID == "tool-blocked" {
				continuationBlock = block
				continuationRole = msg.Role
				break
			}
		}
	}
	if continuationBlock == nil || continuationRole != "user" || !strings.Contains(continuationBlock.Content, "refused by a policy plugin") {
		t.Fatalf("continuation blocked tool result role/block = %q/%+v", continuationRole, continuationBlock)
	}

	msgs, err := f.st.ListMessages(context.Background(), f.session, 20)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var persisted bool
	for _, msg := range msgs {
		persisted = persisted || (msg.ID == "assistant-tool-plugin-cancel" && msg.Role == "assistant" && strings.Contains(msg.Content, "continued after blocked tool"))
	}
	if !persisted {
		t.Fatalf("completed assistant row missing: %+v", msgs)
	}
}

// Targeted helper coverage complements the production-door overflow scenario:
// it pins the successful forced-compaction branch independently of provider
// retry orchestration, including slot_changed emission and rebuilt slot views.
func TestRecoverFromContextOverflow_RateBudgetForcedCompactionSucceeds(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "forced-compaction.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	if err := st.Seed(ctx); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if err := st.CreateSession(ctx, &store.Session{ID: "forced-compaction", Title: "forced"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := st.UpdateUserSettings(ctx, &store.UserSettings{
		ContextOverflowRecovery: true,
		SummarizerProvider:      "characterization",
		SummarizerModel:         "characterization-model",
	}); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}
	prov := &characterizationProvider{}
	registry := provider.NewRegistry()
	registry.Register("characterization", prov)
	svc := &chatServiceImpl{store: st, providers: registry}
	window := ctxpkg.NewContextWindow(200000, ctxpkg.DefaultEstimator{})
	window.SetContent(ctxpkg.SlotContext, strings.Repeat("enrichment ", 500))
	window.SetFlags(ctxpkg.SlotContext, ctxpkg.SlotFlags{EnrichmentActive: true})
	result := &SlotAssemblyResult{Window: window}
	result.Blocks = window.Assemble()
	ch := make(chan chat.StreamEvent, 16)

	_, _, ok := svc.recoverFromContextOverflow(ctx, "forced-compaction", result,
		&store.AgentProfile{ID: "agent"}, []llmtypes.ChatMessage{{Role: "user", Content: "continue"}}, nil,
		ch, llmcontracts.ErrRequestExceedsRateBudget.Error(), compactTriggerRateBudget)
	if !ok {
		t.Fatal("forced rate-budget compaction returned ok=false")
	}
	close(ch)
	var sawSlotChanged bool
	for event := range ch {
		sawSlotChanged = sawSlotChanged || event.Type == "slot_changed"
	}
	if !sawSlotChanged {
		t.Fatal("successful compaction did not emit slot_changed")
	}
	if slot := result.Window.Slot(ctxpkg.SlotContext); slot != nil && slot.Content != "" {
		t.Fatalf("compactable context enrichment survived forced compaction: %d bytes", len(slot.Content))
	}
}
