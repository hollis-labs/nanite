package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// --- fakes ---

type executeCall struct {
	AgentID string
	Tool    string
	Input   map[string]any
}

// fakeWorkflowToolService is a scriptable ToolService test double. Every
// Execute call is recorded so tests can assert exactly which tools ran
// (and that no unexpected ones did).
type fakeWorkflowToolService struct {
	summaries []toolclient.ToolSummary
	schemas   map[string]map[string]any

	executeFunc func(ctx context.Context, agentID, toolName string, input map[string]any) (*ToolResult, error)
	calls       []executeCall
}

func (f *fakeWorkflowToolService) SelectForAgent(_ context.Context, _, _, _, _ string, _ int) (*ToolSelection, error) {
	return &ToolSelection{}, nil
}

func (f *fakeWorkflowToolService) Execute(ctx context.Context, agentID, toolName string, input map[string]any) (*ToolResult, error) {
	f.calls = append(f.calls, executeCall{AgentID: agentID, Tool: toolName, Input: input})
	if f.executeFunc != nil {
		return f.executeFunc(ctx, agentID, toolName, input)
	}
	return &ToolResult{Output: "ok:" + toolName}, nil
}

func (f *fakeWorkflowToolService) HandleRequestTools(_ context.Context, _ map[string]any) ([]llmtypes.ToolDefinition, string, error) {
	return nil, "", nil
}

func (f *fakeWorkflowToolService) ListSummaries() []toolclient.ToolSummary {
	return f.summaries
}

func (f *fakeWorkflowToolService) GetToolMeta(string) (ToolMetaInfo, bool) {
	return ToolMetaInfo{}, true
}

func (f *fakeWorkflowToolService) GetToolSchema(toolName string) map[string]any {
	return f.schemas[toolName]
}

var _ ToolService = (*fakeWorkflowToolService)(nil)

// fakeProviderResolver is a scriptable WorkflowProviderResolver.
type fakeProviderResolver struct {
	providers map[string]llmcontracts.Provider
	getCalls  int
}

func (f *fakeProviderResolver) Get(name string) (llmcontracts.Provider, bool) {
	f.getCalls++
	p, ok := f.providers[name]
	return p, ok
}

var _ WorkflowProviderResolver = (*fakeProviderResolver)(nil)

// scriptedProvider replays a queue of StreamChat responses (one per call)
// and records every request it was given, so tests can assert exactly
// which tools were offered to the model.
type scriptedProvider struct {
	responses [][]llmtypes.StreamEvent
	callCount int
	gotReqs   []llmtypes.ChatRequest
}

func (p *scriptedProvider) StreamChat(_ context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	p.gotReqs = append(p.gotReqs, req)
	idx := p.callCount
	p.callCount++
	if idx >= len(p.responses) {
		return nil, fmt.Errorf("scriptedProvider: no response scripted for call %d", idx)
	}
	events := p.responses[idx]
	ch := make(chan llmtypes.StreamEvent, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func (p *scriptedProvider) Complete(context.Context, llmtypes.ChatRequest) (string, error) {
	return "", fmt.Errorf("scriptedProvider: Complete not implemented")
}

func (p *scriptedProvider) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{}
}

var _ llmcontracts.Provider = (*scriptedProvider)(nil)

func toolDefNames(defs []llmtypes.ToolDefinition) []string {
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	return names
}

// --- ExecuteToolStep: never touches model inference ---

func TestExecuteToolStep_CallsToolServiceDirectly_NeverInvokesProvider(t *testing.T) {
	tools := &fakeWorkflowToolService{}
	resolver := &fakeProviderResolver{providers: map[string]llmcontracts.Provider{
		"anthropic": &scriptedProvider{},
	}}
	exec := NewWorkflowStepExecutor(tools, resolver)

	result, err := exec.ExecuteToolStep(context.Background(), agentworkflow.ToolStepRequest{
		AgentID: "agent-1",
		Tool:    "torque_task_get",
		Args:    map[string]any{"id": "T-1"},
	})
	if err != nil {
		t.Fatalf("ExecuteToolStep returned error: %v", err)
	}
	if result.Output != "ok:torque_task_get" || result.IsError {
		t.Fatalf("unexpected result: %+v", result)
	}

	if len(tools.calls) != 1 {
		t.Fatalf("expected exactly 1 Execute call, got %d", len(tools.calls))
	}
	if tools.calls[0].Tool != "torque_task_get" || tools.calls[0].AgentID != "agent-1" {
		t.Fatalf("unexpected Execute call: %+v", tools.calls[0])
	}

	if resolver.getCalls != 0 {
		t.Fatalf("ExecuteToolStep must never resolve a provider — got %d Get calls", resolver.getCalls)
	}
}

func TestExecuteToolStep_PropagatesToolServiceError(t *testing.T) {
	tools := &fakeWorkflowToolService{
		executeFunc: func(context.Context, string, string, map[string]any) (*ToolResult, error) {
			return nil, fmt.Errorf("transport down")
		},
	}
	exec := NewWorkflowStepExecutor(tools, &fakeProviderResolver{})

	_, err := exec.ExecuteToolStep(context.Background(), agentworkflow.ToolStepRequest{
		AgentID: "agent-1",
		Tool:    "some_tool",
	})
	if err == nil || !strings.Contains(err.Error(), "transport down") {
		t.Fatalf("expected wrapped transport error, got %v", err)
	}
}

func TestExecuteToolStep_RequiresToolName(t *testing.T) {
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{})
	if _, err := exec.ExecuteToolStep(context.Background(), agentworkflow.ToolStepRequest{AgentID: "a"}); err == nil {
		t.Fatal("expected error for empty tool name")
	}
}

// --- ExecuteLLMStep: capability restriction ---

func TestExecuteLLMStep_OnlyOffersRequestedToolSurface(t *testing.T) {
	tools := &fakeWorkflowToolService{
		summaries: []toolclient.ToolSummary{
			{Name: "torque_task_get", Description: "fetch a task"},
			{Name: "torque_task_delete", Description: "delete a task"},
		},
		schemas: map[string]map[string]any{
			"torque_task_get":    {"type": "object"},
			"torque_task_delete": {"type": "object"},
		},
	}
	prov := &scriptedProvider{responses: [][]llmtypes.StreamEvent{
		{{Type: llmtypes.EventDelta, Content: "done"}, {Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: "end_turn"}}},
	}}
	resolver := &fakeProviderResolver{providers: map[string]llmcontracts.Provider{"anthropic": prov}}
	exec := NewWorkflowStepExecutor(tools, resolver)

	// The agent profile's default surface would include both tools; this
	// step's request narrows it to exactly one. The agent profile's
	// broader default must never leak through.
	_, err := exec.ExecuteLLMStep(context.Background(), agentworkflow.LLMStepRequest{
		AgentID:  "agent-1",
		Provider: "anthropic",
		Model:    "claude",
		Tools:    []string{"torque_task_get"},
	})
	if err != nil {
		t.Fatalf("ExecuteLLMStep returned error: %v", err)
	}
	if len(prov.gotReqs) != 1 {
		t.Fatalf("expected exactly 1 provider call, got %d", len(prov.gotReqs))
	}
	got := toolDefNames(prov.gotReqs[0].Tools)
	if len(got) != 1 || got[0] != "torque_task_get" {
		t.Fatalf("expected only [torque_task_get] offered to the model, got %v", got)
	}
}

func TestExecuteLLMStep_UnknownToolInSurface_ErrorsBeforeCallingProvider(t *testing.T) {
	tools := &fakeWorkflowToolService{} // no summaries registered
	prov := &scriptedProvider{}
	resolver := &fakeProviderResolver{providers: map[string]llmcontracts.Provider{
		"anthropic": prov,
	}}
	exec := NewWorkflowStepExecutor(tools, resolver)

	_, err := exec.ExecuteLLMStep(context.Background(), agentworkflow.LLMStepRequest{
		AgentID:  "agent-1",
		Provider: "anthropic",
		Tools:    []string{"nonexistent_tool"},
	})
	if err == nil {
		t.Fatal("expected error for unknown tool in capability-restricted surface")
	}
	if prov.callCount != 0 {
		t.Fatalf("the model must never be contacted when the requested tool surface can't be resolved — got %d StreamChat calls", prov.callCount)
	}
}

func TestExecuteLLMStep_ToolCallLoop_ExecutesViaToolServiceAndFeedsResultBack(t *testing.T) {
	tools := &fakeWorkflowToolService{
		summaries: []toolclient.ToolSummary{{Name: "torque_task_get", Description: "fetch a task"}},
		schemas:   map[string]map[string]any{"torque_task_get": {"type": "object"}},
		executeFunc: func(_ context.Context, agentID, toolName string, input map[string]any) (*ToolResult, error) {
			if toolName != "torque_task_get" || input["id"] != "T-1" {
				t.Fatalf("unexpected Execute call: agent=%s tool=%s input=%v", agentID, toolName, input)
			}
			return &ToolResult{Output: `{"id":"T-1","title":"real task"}`}, nil
		},
	}
	prov := &scriptedProvider{responses: [][]llmtypes.StreamEvent{
		{{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "call_1", Name: "torque_task_get", Input: map[string]any{"id": "T-1"}}}},
		{{Type: llmtypes.EventDelta, Content: "Task T-1 is real."}, {Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: "end_turn"}}},
	}}
	resolver := &fakeProviderResolver{providers: map[string]llmcontracts.Provider{"anthropic": prov}}
	exec := NewWorkflowStepExecutor(tools, resolver)

	result, err := exec.ExecuteLLMStep(context.Background(), agentworkflow.LLMStepRequest{
		AgentID:  "agent-1",
		Provider: "anthropic",
		Model:    "claude",
		Tools:    []string{"torque_task_get"},
	})
	if err != nil {
		t.Fatalf("ExecuteLLMStep returned error: %v", err)
	}
	if result.Text != "Task T-1 is real." {
		t.Fatalf("unexpected final text: %q", result.Text)
	}
	if len(tools.calls) != 1 {
		t.Fatalf("expected exactly 1 tool Execute call, got %d", len(tools.calls))
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Tool != "torque_task_get" || result.ToolCalls[0].IsError {
		t.Fatalf("unexpected ToolCalls record: %+v", result.ToolCalls)
	}
	if prov.callCount != 2 {
		t.Fatalf("expected 2 provider turns (tool_use then final), got %d", prov.callCount)
	}
}

func TestExecuteLLMStep_RejectsToolUseOutsideSurface(t *testing.T) {
	tools := &fakeWorkflowToolService{
		summaries: []toolclient.ToolSummary{{Name: "torque_task_get", Description: "fetch a task"}},
		schemas:   map[string]map[string]any{"torque_task_get": {"type": "object"}},
	}
	// Simulate a misbehaving provider adapter that echoes back a tool_use
	// for a tool never offered (only torque_task_get was in Tools/toolDefs).
	prov := &scriptedProvider{responses: [][]llmtypes.StreamEvent{
		{{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "call_1", Name: "torque_task_delete", Input: map[string]any{"id": "T-1"}}}},
		{{Type: llmtypes.EventDelta, Content: "done"}, {Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: "end_turn"}}},
	}}
	resolver := &fakeProviderResolver{providers: map[string]llmcontracts.Provider{"anthropic": prov}}
	exec := NewWorkflowStepExecutor(tools, resolver)

	result, err := exec.ExecuteLLMStep(context.Background(), agentworkflow.LLMStepRequest{
		AgentID:  "agent-1",
		Provider: "anthropic",
		Tools:    []string{"torque_task_get"},
	})
	if err != nil {
		t.Fatalf("ExecuteLLMStep returned error: %v", err)
	}
	if len(tools.calls) != 0 {
		t.Fatalf("out-of-surface tool must never reach ToolService.Execute — got %d calls: %+v", len(tools.calls), tools.calls)
	}
	if len(result.ToolCalls) != 1 || !result.ToolCalls[0].IsError || result.ToolCalls[0].Tool != "torque_task_delete" {
		t.Fatalf("expected a recorded, errored ToolCallRecord for the rejected call, got %+v", result.ToolCalls)
	}
}

func TestExecuteLLMStep_ExceedsMaxToolIterations(t *testing.T) {
	tools := &fakeWorkflowToolService{
		summaries: []toolclient.ToolSummary{{Name: "loop_tool", Description: "loops forever"}},
		schemas:   map[string]map[string]any{"loop_tool": {"type": "object"}},
	}
	loopEvent := []llmtypes.StreamEvent{{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "x", Name: "loop_tool"}}}
	responses := make([][]llmtypes.StreamEvent, 5)
	for i := range responses {
		responses[i] = loopEvent
	}
	prov := &scriptedProvider{responses: responses}
	resolver := &fakeProviderResolver{providers: map[string]llmcontracts.Provider{"anthropic": prov}}
	exec := NewWorkflowStepExecutor(tools, resolver)

	_, err := exec.ExecuteLLMStep(context.Background(), agentworkflow.LLMStepRequest{
		AgentID:           "agent-1",
		Provider:          "anthropic",
		Tools:             []string{"loop_tool"},
		MaxToolIterations: 3,
	})
	if err == nil || !strings.Contains(err.Error(), "max tool iterations") {
		t.Fatalf("expected max-iterations error, got %v", err)
	}
}

// --- Verify: mode engine ---

func TestVerify_ModeEngine_ToolCalled_Pass(t *testing.T) {
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{})

	result, err := exec.Verify(context.Background(), agentworkflow.VerifyRequest{
		SubjectStepID: "fetch",
		VerifySpec: agentworkflow.VerifySpec{
			Mode:         agentworkflow.VerifyModeEngine,
			EngineCheck:  "tool_called",
			EngineParams: map[string]any{"tool": "torque_task_get"},
		},
		Subject: agentworkflow.VerifySubject{
			ToolCalls: []agentworkflow.ToolCallRecord{{Tool: "torque_task_get", IsError: false}},
		},
	})
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if !result.Passed {
		t.Fatalf("expected Passed=true, reason=%q", result.Reason)
	}
}

func TestVerify_ModeEngine_ToolCalled_FailsWhenToolMissing(t *testing.T) {
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{})

	result, err := exec.Verify(context.Background(), agentworkflow.VerifyRequest{
		VerifySpec: agentworkflow.VerifySpec{
			Mode:         agentworkflow.VerifyModeEngine,
			EngineCheck:  "tool_called",
			EngineParams: map[string]any{"tool": "torque_task_get"},
		},
		Subject: agentworkflow.VerifySubject{
			// Agent claims success but never actually called the tool —
			// the design doc's motivating failure mode.
			Output:    "I fetched the task.",
			ToolCalls: nil,
		},
	})
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if result.Passed {
		t.Fatal("expected Passed=false when the claimed tool was never called")
	}
}

func TestVerify_ModeEngine_UnknownCheck_Errors(t *testing.T) {
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{})

	_, err := exec.Verify(context.Background(), agentworkflow.VerifyRequest{
		VerifySpec: agentworkflow.VerifySpec{Mode: agentworkflow.VerifyModeEngine, EngineCheck: "does_not_exist"},
	})
	if err == nil {
		t.Fatal("expected error for unknown engine check")
	}
}

// --- Verify: mode agent (nested ExecuteLLMStep) ---

func TestVerify_ModeAgent_NestedLLMStep_ParsesPassVerdict(t *testing.T) {
	tools := &fakeWorkflowToolService{}
	prov := &scriptedProvider{responses: [][]llmtypes.StreamEvent{
		{{Type: llmtypes.EventDelta, Content: "PASS looks correct"}, {Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: "end_turn"}}},
	}}
	resolver := &fakeProviderResolver{providers: map[string]llmcontracts.Provider{"anthropic": prov}}
	exec := NewWorkflowStepExecutor(tools, resolver)

	result, err := exec.Verify(context.Background(), agentworkflow.VerifyRequest{
		SubjectStepID: "fetch",
		VerifySpec: agentworkflow.VerifySpec{
			Mode:             agentworkflow.VerifyModeAgent,
			ReviewerPrompt:   "Check the fetched task looks real.",
			ReviewerProvider: "anthropic",
			ReviewerModel:    "claude",
		},
		Subject: agentworkflow.VerifySubject{Output: `{"id":"T-1"}`},
	})
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if !result.Passed || result.Reason != "looks correct" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.ReviewerResult == nil {
		t.Fatal("expected ReviewerResult to be populated")
	}
	if len(prov.gotReqs) != 1 {
		t.Fatalf("expected exactly 1 nested ExecuteLLMStep call, got %d", len(prov.gotReqs))
	}
	if !strings.Contains(prov.gotReqs[0].Messages[0].Content, "Check the fetched task looks real.") {
		t.Fatalf("reviewer prompt not composed into request: %+v", prov.gotReqs[0].Messages)
	}
}

func TestVerify_ModeAgent_NestedLLMStep_ParsesFailVerdict(t *testing.T) {
	prov := &scriptedProvider{responses: [][]llmtypes.StreamEvent{
		{{Type: llmtypes.EventDelta, Content: "FAIL fabricated, no tool calls"}, {Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: "end_turn"}}},
	}}
	resolver := &fakeProviderResolver{providers: map[string]llmcontracts.Provider{"anthropic": prov}}
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, resolver)

	result, err := exec.Verify(context.Background(), agentworkflow.VerifyRequest{
		VerifySpec: agentworkflow.VerifySpec{Mode: agentworkflow.VerifyModeAgent, ReviewerProvider: "anthropic"},
	})
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if result.Passed {
		t.Fatal("expected Passed=false for a FAIL verdict")
	}
}

func TestVerify_ModeAgent_AmbiguousVerdict_FailsClosed(t *testing.T) {
	prov := &scriptedProvider{responses: [][]llmtypes.StreamEvent{
		{{Type: llmtypes.EventDelta, Content: "I'm not sure, maybe?"}, {Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: "end_turn"}}},
	}}
	resolver := &fakeProviderResolver{providers: map[string]llmcontracts.Provider{"anthropic": prov}}
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, resolver)

	result, err := exec.Verify(context.Background(), agentworkflow.VerifyRequest{
		VerifySpec: agentworkflow.VerifySpec{Mode: agentworkflow.VerifyModeAgent, ReviewerProvider: "anthropic"},
	})
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if result.Passed {
		t.Fatal("an unparseable verdict must fail closed, not pass")
	}
}

func TestVerify_UnknownMode_Errors(t *testing.T) {
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{})
	_, err := exec.Verify(context.Background(), agentworkflow.VerifyRequest{VerifySpec: agentworkflow.VerifySpec{Mode: "bogus"}})
	if err == nil {
		t.Fatal("expected error for unknown verify mode")
	}
}
