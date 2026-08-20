package selftools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
)

// fakeStepExecutor records the request it received and returns a
// caller-configured result/error — enough to verify the three MCP tool
// handlers decode arguments correctly and delegate to
// agentworkflow.StepExecutor without adding logic of their own.
type fakeStepExecutor struct {
	llmReq  agentworkflow.LLMStepRequest
	llmRes  agentworkflow.LLMStepResult
	llmErr  error
	toolReq agentworkflow.ToolStepRequest
	toolRes agentworkflow.ToolStepResult
	toolErr error
	verReq  agentworkflow.VerifyRequest
	verRes  agentworkflow.VerifyResult
	verErr  error
}

func (f *fakeStepExecutor) ExecuteLLMStep(_ context.Context, req agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
	f.llmReq = req
	return f.llmRes, f.llmErr
}

func (f *fakeStepExecutor) ExecuteToolStep(_ context.Context, req agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error) {
	f.toolReq = req
	return f.toolRes, f.toolErr
}

func (f *fakeStepExecutor) Verify(_ context.Context, req agentworkflow.VerifyRequest) (agentworkflow.VerifyResult, error) {
	f.verReq = req
	return f.verRes, f.verErr
}

var _ agentworkflow.StepExecutor = (*fakeStepExecutor)(nil)

func TestCallWorkflowExecuteLLMStep_Unwired(t *testing.T) {
	st := newSelfTools(t)
	res, err := st.CallTool(context.Background(), "workflow_execute_llm_step", map[string]any{
		"provider": "anthropic",
		"model":    "claude-sonnet-5",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatalf("CallTool returned err: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true when WorkflowExecutor is unwired, got %+v", res)
	}
}

func TestCallWorkflowExecuteLLMStep_DecodesAndDelegates(t *testing.T) {
	st := newSelfTools(t)
	fake := &fakeStepExecutor{
		llmRes: agentworkflow.LLMStepResult{Text: "hello back", StopReason: "end_turn"},
	}
	st.WorkflowExecutor = fake

	res, err := st.CallTool(context.Background(), "workflow_execute_llm_step", map[string]any{
		"workflow_run_id": "wfr-1",
		"step_id":         "s1",
		"provider":        "anthropic",
		"model":           "claude-sonnet-5",
		"system_prompt":   "be terse",
		"messages":        []any{map[string]any{"role": "user", "content": "hi"}},
		"tools":           []any{"torque_task_get"},
	})
	if err != nil {
		t.Fatalf("CallTool returned err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}

	if fake.llmReq.Provider != "anthropic" || fake.llmReq.Model != "claude-sonnet-5" {
		t.Fatalf("provider/model not decoded correctly: %+v", fake.llmReq)
	}
	if fake.llmReq.WorkflowRunID != "wfr-1" || fake.llmReq.StepID != "s1" {
		t.Fatalf("workflow_run_id/step_id not decoded correctly: %+v", fake.llmReq)
	}
	if fake.llmReq.SystemPrompt != "be terse" {
		t.Fatalf("system_prompt not decoded correctly: %+v", fake.llmReq)
	}
	if len(fake.llmReq.Messages) != 1 || fake.llmReq.Messages[0].Role != "user" || fake.llmReq.Messages[0].Content != "hi" {
		t.Fatalf("messages not decoded correctly: %+v", fake.llmReq.Messages)
	}
	if len(fake.llmReq.Tools) != 1 || fake.llmReq.Tools[0] != "torque_task_get" {
		t.Fatalf("tools (capability surface) not decoded correctly: %+v", fake.llmReq.Tools)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if out["text"] != "hello back" {
		t.Fatalf("expected literal executor result to pass through unmodified, got %v", out)
	}
}

func TestCallWorkflowExecuteLLMStep_MissingProvider(t *testing.T) {
	st := newSelfTools(t)
	st.WorkflowExecutor = &fakeStepExecutor{}

	res, err := st.CallTool(context.Background(), "workflow_execute_llm_step", map[string]any{
		"model":    "claude-sonnet-5",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatalf("CallTool returned err: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true when provider is missing, got %+v", res)
	}
}

func TestCallWorkflowExecuteToolStep_NeverTouchesLLM(t *testing.T) {
	st := newSelfTools(t)
	fake := &fakeStepExecutor{
		toolRes: agentworkflow.ToolStepResult{Output: "ok", IsError: false},
	}
	st.WorkflowExecutor = fake

	res, err := st.CallTool(context.Background(), "workflow_execute_tool_step", map[string]any{
		"workflow_run_id": "wfr-1",
		"step_id":         "s2",
		"tool":            "torque_task_get",
		"args":            map[string]any{"id": "CW-20260813-0011"},
	})
	if err != nil {
		t.Fatalf("CallTool returned err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}

	if fake.toolReq.Tool != "torque_task_get" {
		t.Fatalf("tool name not decoded correctly: %+v", fake.toolReq)
	}
	if fake.toolReq.Args["id"] != "CW-20260813-0011" {
		t.Fatalf("args not decoded correctly: %+v", fake.toolReq.Args)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if out["output"] != "ok" {
		t.Fatalf("expected literal executor result to pass through unmodified, got %v", out)
	}
}

func TestCallWorkflowExecuteToolStep_MissingTool(t *testing.T) {
	st := newSelfTools(t)
	st.WorkflowExecutor = &fakeStepExecutor{}

	res, err := st.CallTool(context.Background(), "workflow_execute_tool_step", map[string]any{})
	if err != nil {
		t.Fatalf("CallTool returned err: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true when tool is missing, got %+v", res)
	}
}

func TestCallWorkflowVerifyStep_EngineMode(t *testing.T) {
	st := newSelfTools(t)
	fake := &fakeStepExecutor{
		verRes: agentworkflow.VerifyResult{Passed: true, Reason: "subject step did not return an error"},
	}
	st.WorkflowExecutor = fake

	res, err := st.CallTool(context.Background(), "workflow_verify_step", map[string]any{
		"subject_step_id": "s1",
		"mode":            "engine",
		"engine_check":    "no_error",
		"subject": map[string]any{
			"step_kind": "tool",
			"output":    "some output",
			"is_error":  false,
		},
	})
	if err != nil {
		t.Fatalf("CallTool returned err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}

	if fake.verReq.Mode != agentworkflow.VerifyModeEngine || fake.verReq.EngineCheck != "no_error" {
		t.Fatalf("mode/engine_check not decoded correctly: %+v", fake.verReq)
	}
	if fake.verReq.Subject.StepKind != agentworkflow.StepKindTool || fake.verReq.Subject.Output != "some output" {
		t.Fatalf("subject not decoded correctly: %+v", fake.verReq.Subject)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if out["passed"] != true {
		t.Fatalf("expected literal executor result to pass through unmodified, got %v", out)
	}
}

func TestCallWorkflowVerifyStep_AgentModeNestsLLMStep(t *testing.T) {
	st := newSelfTools(t)
	fake := &fakeStepExecutor{
		verRes: agentworkflow.VerifyResult{Passed: false, Reason: "did not demonstrate the requirement"},
	}
	st.WorkflowExecutor = fake

	res, err := st.CallTool(context.Background(), "workflow_verify_step", map[string]any{
		"mode":                "agent",
		"reviewer_prompt":     "Check the summary is accurate.",
		"reviewer_provider":   "anthropic",
		"reviewer_model":      "claude-sonnet-5",
		"reviewer_tools":      []any{},
		"reviewer_agent_id":   "reviewer-1",
		"reviewer_session_id": "sess-1",
		"subject": map[string]any{
			"step_kind": "llm",
			"output":    "a summary",
			"is_error":  false,
			"tool_calls": []any{
				map[string]any{"tool": "x", "input": map[string]any{}, "output": "y", "is_error": true},
			},
		},
	})
	if err != nil {
		t.Fatalf("CallTool returned err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}

	if fake.verReq.Mode != agentworkflow.VerifyModeAgent {
		t.Fatalf("mode not decoded correctly: %+v", fake.verReq)
	}
	if fake.verReq.ReviewerPrompt != "Check the summary is accurate." || fake.verReq.ReviewerProvider != "anthropic" {
		t.Fatalf("reviewer_* fields not decoded correctly: %+v", fake.verReq)
	}
	if len(fake.verReq.Subject.ToolCalls) != 1 || !fake.verReq.Subject.ToolCalls[0].IsError {
		t.Fatalf("subject.tool_calls not decoded correctly (is_error must round-trip): %+v", fake.verReq.Subject.ToolCalls)
	}
}

func TestCallWorkflowVerifyStep_MissingMode(t *testing.T) {
	st := newSelfTools(t)
	st.WorkflowExecutor = &fakeStepExecutor{}

	res, err := st.CallTool(context.Background(), "workflow_verify_step", map[string]any{
		"subject": map[string]any{"output": "x", "is_error": false},
	})
	if err != nil {
		t.Fatalf("CallTool returned err: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true when mode is missing, got %+v", res)
	}
}

// TestCallWorkflowVerifyStep_MissingSubject verifies a wholly absent
// subject is rejected before ever reaching the executor — a missing
// subject must not silently decode to a zero-value VerifySubject
// (StepKind:"", Output:"", IsError:false), which would let mode=engine
// checks like no_error incorrectly pass.
func TestCallWorkflowVerifyStep_MissingSubject(t *testing.T) {
	st := newSelfTools(t)
	fake := &fakeStepExecutor{}
	st.WorkflowExecutor = fake

	res, err := st.CallTool(context.Background(), "workflow_verify_step", map[string]any{
		"mode":         "engine",
		"engine_check": "no_error",
	})
	if err != nil {
		t.Fatalf("CallTool returned err: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true when subject is missing, got %+v", res)
	}
	if fake.verReq.Mode != "" {
		t.Fatalf("expected the executor to never be called, but it was: %+v", fake.verReq)
	}
}

// TestCallWorkflowVerifyStep_MissingSubjectIsError verifies that an
// omitted subject.is_error is rejected (fail closed) rather than
// silently decoding to Go's bool zero value (false), which would let
// mode=engine/engine_check=no_error incorrectly pass a subject whose
// error status the caller never actually reported.
func TestCallWorkflowVerifyStep_MissingSubjectIsError(t *testing.T) {
	st := newSelfTools(t)
	fake := &fakeStepExecutor{
		verRes: agentworkflow.VerifyResult{Passed: true, Reason: "should never be reached"},
	}
	st.WorkflowExecutor = fake

	res, err := st.CallTool(context.Background(), "workflow_verify_step", map[string]any{
		"mode":         "engine",
		"engine_check": "no_error",
		"subject": map[string]any{
			"step_kind": "tool",
			"output":    "some output",
			// is_error deliberately omitted
		},
	})
	if err != nil {
		t.Fatalf("CallTool returned err: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true when subject.is_error is omitted, got %+v", res)
	}
	if fake.verReq.Mode != "" {
		t.Fatalf("expected the executor to never be called, but it was: %+v", fake.verReq)
	}
}
