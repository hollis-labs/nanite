package service

import (
	"context"
	"fmt"
	"strings"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
)

// DefaultMaxToolIterations bounds an ExecuteLLMStep tool-call loop when the
// request doesn't specify one — a misbehaving or looping model can't run
// forever.
const DefaultMaxToolIterations = 10

// reviewerSystemPrompt instructs a mode:agent reviewer step (a nested
// ExecuteLLMStep call) to return a parseable verdict. Kept narrow and
// literal — Verify's job is to interpret PASS/FAIL, not free-form prose.
const reviewerSystemPrompt = "You are an independent reviewer verifying another agent's completed work. " +
	"Inspect the subject step's output (and its recorded tool calls, if any) against the review instructions. " +
	"Respond with a single line starting with exactly \"PASS\" or \"FAIL\", followed by a short reason. " +
	"Do not fabricate — if the output doesn't demonstrate what the instructions require, FAIL it."

// WorkflowProviderResolver resolves a provider name to its chat/completion
// implementation for ExecuteLLMStep. *provider.Registry (the container's
// existing global registry) satisfies this structurally; tests supply a
// fake. Mirrors the Dependencies.ProviderAdapter resolver-function pattern
// from internal/runtime/agent rather than depending on the concrete
// registry type directly.
type WorkflowProviderResolver interface {
	Get(name string) (llmcontracts.Provider, bool)
}

// workflowEngineCheck is a registered deterministic check for
// Verify(mode: engine). Kept as a narrow, growable registry rather than a
// generic framework — design doc: start narrow with what real workflows
// need, expand later.
type workflowEngineCheck func(subject agentworkflow.VerifySubject, params map[string]any) (passed bool, reason string)

var workflowEngineChecks = map[string]workflowEngineCheck{
	"no_error": func(subject agentworkflow.VerifySubject, _ map[string]any) (bool, string) {
		if subject.IsError {
			return false, "subject step returned an error"
		}
		return true, "subject step did not return an error"
	},
	"tool_called": func(subject agentworkflow.VerifySubject, params map[string]any) (bool, string) {
		name, _ := params["tool"].(string)
		if name == "" {
			return false, "tool_called check requires a non-empty \"tool\" param"
		}
		for _, tc := range subject.ToolCalls {
			if tc.Tool == name && !tc.IsError {
				return true, fmt.Sprintf("tool %q was called successfully", name)
			}
		}
		return false, fmt.Sprintf("tool %q was not called successfully", name)
	},
	"output_contains": func(subject agentworkflow.VerifySubject, params map[string]any) (bool, string) {
		substr, _ := params["substring"].(string)
		if substr == "" {
			return false, "output_contains check requires a non-empty \"substring\" param"
		}
		if strings.Contains(subject.Output, substr) {
			return true, fmt.Sprintf("output contains %q", substr)
		}
		return false, fmt.Sprintf("output does not contain %q", substr)
	},
}

// workflowStepExecutor is the real, single implementation of
// agentworkflow.StepExecutor. Every WorkflowEngine — built-in or external —
// calls through this; it is never reimplemented per consumer.
type workflowStepExecutor struct {
	tools     ToolService
	providers WorkflowProviderResolver
}

// NewWorkflowStepExecutor constructs the StepExecutor implementation.
// tools and providers are required — both are checked at call time so a
// wiring bug surfaces as a typed error rather than a nil-pointer panic.
func NewWorkflowStepExecutor(tools ToolService, providers WorkflowProviderResolver) agentworkflow.StepExecutor {
	return &workflowStepExecutor{tools: tools, providers: providers}
}

var _ agentworkflow.StepExecutor = (*workflowStepExecutor)(nil)

// ExecuteToolStep calls the tool broker directly with the specified tool
// and args. This never goes through an LLM — the call happens regardless
// of what any model would have decided, and the literal result (success or
// error) returns to the caller.
func (e *workflowStepExecutor) ExecuteToolStep(ctx context.Context, req agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error) {
	if e.tools == nil {
		return agentworkflow.ToolStepResult{}, fmt.Errorf("workflow: tool step executor has no ToolService configured")
	}
	if req.Tool == "" {
		return agentworkflow.ToolStepResult{}, fmt.Errorf("workflow: tool step requires a tool name")
	}

	result, err := e.tools.Execute(ctx, req.AgentID, req.Tool, req.Args)
	if err != nil {
		return agentworkflow.ToolStepResult{}, fmt.Errorf("workflow: tool step %q: %w", req.Tool, err)
	}
	return agentworkflow.ToolStepResult{Output: result.Output, IsError: result.IsError}, nil
}

// ExecuteLLMStep runs one capability-restricted agent turn through the
// harness's provider/tool-broker/permission-engine machinery. The tool
// surface offered to the model is exactly req.Tools — there is no fallback
// to a broader default.
func (e *workflowStepExecutor) ExecuteLLMStep(ctx context.Context, req agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
	if e.tools == nil {
		return agentworkflow.LLMStepResult{}, fmt.Errorf("workflow: llm step executor has no ToolService configured")
	}
	if e.providers == nil {
		return agentworkflow.LLMStepResult{}, fmt.Errorf("workflow: llm step executor has no provider resolver configured")
	}
	if req.Provider == "" {
		return agentworkflow.LLMStepResult{}, fmt.Errorf("workflow: llm step requires Provider")
	}
	prov, ok := e.providers.Get(req.Provider)
	if !ok {
		return agentworkflow.LLMStepResult{}, fmt.Errorf("workflow: unknown provider %q", req.Provider)
	}

	toolDefs, err := e.resolveToolDefinitions(req.Tools)
	if err != nil {
		return agentworkflow.LLMStepResult{}, err
	}
	allowed := make(map[string]bool, len(req.Tools))
	for _, name := range req.Tools {
		allowed[name] = true
	}

	maxIter := req.MaxToolIterations
	if maxIter <= 0 {
		maxIter = DefaultMaxToolIterations
	}

	messages := append([]llmtypes.ChatMessage(nil), req.Messages...)
	var toolCalls []agentworkflow.ToolCallRecord
	var lastUsage *llmtypes.Usage

	for iter := 0; ; iter++ {
		if iter >= maxIter {
			return agentworkflow.LLMStepResult{}, fmt.Errorf("workflow: llm step exceeded max tool iterations (%d)", maxIter)
		}

		chatReq := llmtypes.ChatRequest{
			Model:        req.Model,
			SystemPrompt: req.SystemPrompt,
			Messages:     messages,
			Tools:        toolDefs,
		}

		events, err := prov.StreamChat(ctx, chatReq)
		if err != nil {
			return agentworkflow.LLMStepResult{}, fmt.Errorf("workflow: llm step stream error: %w", err)
		}

		var textBuf strings.Builder
		var pendingToolUses []llmtypes.ToolUseBlock
		var stopReason string
		var streamErr error

		for ev := range events {
			switch ev.Type {
			case llmtypes.EventDelta:
				textBuf.WriteString(ev.Content)
			case llmtypes.EventToolUse:
				if ev.ToolUse != nil {
					pendingToolUses = append(pendingToolUses, *ev.ToolUse)
				}
			case llmtypes.EventUsage:
				if ev.Usage != nil {
					lastUsage = ev.Usage
					if ev.Usage.StopReason != "" {
						stopReason = ev.Usage.StopReason
					}
				}
			case llmtypes.EventError:
				streamErr = fmt.Errorf("workflow: llm step provider error: %s", ev.Error)
			}
		}
		if streamErr != nil {
			return agentworkflow.LLMStepResult{}, streamErr
		}

		if len(pendingToolUses) == 0 {
			return agentworkflow.LLMStepResult{
				Text:       textBuf.String(),
				ToolCalls:  toolCalls,
				Usage:      lastUsage,
				StopReason: stopReason,
			}, nil
		}

		assistantBlocks := make([]llmtypes.ContentBlock, 0, len(pendingToolUses)+1)
		if textBuf.Len() > 0 {
			assistantBlocks = append(assistantBlocks, llmtypes.ContentBlock{Type: "text", Text: textBuf.String()})
		}
		for _, tu := range pendingToolUses {
			input := tu.Input
			assistantBlocks = append(assistantBlocks, llmtypes.ContentBlock{Type: "tool_use", ID: tu.ID, Name: tu.Name, Input: &input})
		}
		messages = append(messages, llmtypes.ChatMessage{Role: "assistant", ContentBlocks: assistantBlocks})

		resultBlocks := make([]llmtypes.ContentBlock, 0, len(pendingToolUses))
		for _, tu := range pendingToolUses {
			if !allowed[tu.Name] {
				// Capability-restriction backstop: the model was only ever
				// offered toolDefs (built from req.Tools), so this should be
				// unreachable in practice. If a provider adapter ever
				// echoes back a tool_use outside what was offered, refuse
				// to execute it rather than trusting the model's claim.
				errMsg := fmt.Sprintf("tool %q is outside this step's capability-restricted surface", tu.Name)
				toolCalls = append(toolCalls, agentworkflow.ToolCallRecord{Tool: tu.Name, Input: tu.Input, Output: errMsg, IsError: true})
				resultBlocks = append(resultBlocks, llmtypes.ContentBlock{Type: "tool_result", ToolUseID: tu.ID, Content: errMsg, IsError: true})
				continue
			}

			result, execErr := e.tools.Execute(ctx, req.AgentID, tu.Name, tu.Input)
			var output string
			var isError bool
			if execErr != nil {
				output = execErr.Error()
				isError = true
			} else {
				output = result.Output
				isError = result.IsError
			}
			toolCalls = append(toolCalls, agentworkflow.ToolCallRecord{Tool: tu.Name, Input: tu.Input, Output: output, IsError: isError})
			resultBlocks = append(resultBlocks, llmtypes.ContentBlock{Type: "tool_result", ToolUseID: tu.ID, Content: output, IsError: isError})
		}
		messages = append(messages, llmtypes.ChatMessage{Role: "user", ContentBlocks: resultBlocks})
	}
}

// resolveToolDefinitions builds the exact ToolDefinition list for names —
// the capability-restricted surface offered to the model. An unknown name
// is a hard error: silently dropping it would leave the step with fewer
// tools than its author specified, which is a correctness bug, not a
// graceful degradation.
func (e *workflowStepExecutor) resolveToolDefinitions(names []string) ([]llmtypes.ToolDefinition, error) {
	if len(names) == 0 {
		return nil, nil
	}
	descByName := make(map[string]string, len(e.tools.ListSummaries()))
	for _, s := range e.tools.ListSummaries() {
		descByName[s.Name] = s.Description
	}

	defs := make([]llmtypes.ToolDefinition, 0, len(names))
	for _, name := range names {
		desc, ok := descByName[name]
		if !ok {
			return nil, fmt.Errorf("workflow: unknown tool %q in capability-restricted surface", name)
		}
		defs = append(defs, llmtypes.ToolDefinition{
			Name:        name,
			Description: desc,
			InputSchema: e.tools.GetToolSchema(name),
		})
	}
	return defs, nil
}

// Verify checks a prior step's result. mode: engine runs a deterministic,
// registered check. mode: agent constructs and runs a second
// ExecuteLLMStep call with a reviewer-oriented prompt and a narrow tool
// surface — a nested call to the method above, not separate logic
// (design doc: validation that the primitives compose).
func (e *workflowStepExecutor) Verify(ctx context.Context, req agentworkflow.VerifyRequest) (agentworkflow.VerifyResult, error) {
	switch req.Mode {
	case agentworkflow.VerifyModeEngine:
		return e.verifyEngine(req)
	case agentworkflow.VerifyModeAgent:
		return e.verifyAgent(ctx, req)
	default:
		return agentworkflow.VerifyResult{}, fmt.Errorf("workflow: unknown verify mode %q", req.Mode)
	}
}

func (e *workflowStepExecutor) verifyEngine(req agentworkflow.VerifyRequest) (agentworkflow.VerifyResult, error) {
	check, ok := workflowEngineChecks[req.EngineCheck]
	if !ok {
		return agentworkflow.VerifyResult{}, fmt.Errorf("workflow: unknown engine check %q", req.EngineCheck)
	}
	passed, reason := check(req.Subject, req.EngineParams)
	return agentworkflow.VerifyResult{Passed: passed, Reason: reason}, nil
}

func (e *workflowStepExecutor) verifyAgent(ctx context.Context, req agentworkflow.VerifyRequest) (agentworkflow.VerifyResult, error) {
	reviewerReq := agentworkflow.LLMStepRequest{
		WorkflowRunID: req.WorkflowRunID,
		StepID:        req.StepID,
		SessionID:     req.ReviewerSessionID,
		AgentID:       req.ReviewerAgentID,
		Provider:      req.ReviewerProvider,
		Model:         req.ReviewerModel,
		SystemPrompt:  reviewerSystemPrompt,
		Messages: []llmtypes.ChatMessage{{
			Role:    "user",
			Content: composeReviewerPrompt(req),
		}},
		Tools: req.ReviewerTools,
	}

	result, err := e.ExecuteLLMStep(ctx, reviewerReq)
	if err != nil {
		return agentworkflow.VerifyResult{}, fmt.Errorf("workflow: verify(mode=agent) reviewer step failed: %w", err)
	}

	passed, reason := parseReviewerVerdict(result.Text)
	return agentworkflow.VerifyResult{Passed: passed, Reason: reason, ReviewerResult: &result}, nil
}

// composeReviewerPrompt builds the reviewer's user turn from the review
// instructions and the subject step's literal output + tool calls.
func composeReviewerPrompt(req agentworkflow.VerifyRequest) string {
	var b strings.Builder
	if req.ReviewerPrompt != "" {
		b.WriteString(req.ReviewerPrompt)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Subject step: %s (kind=%s, is_error=%t)\n", req.SubjectStepID, req.Subject.StepKind, req.Subject.IsError)
	b.WriteString("Subject output:\n")
	b.WriteString(req.Subject.Output)
	if len(req.Subject.ToolCalls) > 0 {
		b.WriteString("\n\nSubject tool calls:\n")
		for _, tc := range req.Subject.ToolCalls {
			fmt.Fprintf(&b, "- %s (error=%t): %s\n", tc.Tool, tc.IsError, tc.Output)
		}
	}
	return b.String()
}

// parseReviewerVerdict interprets a reviewer step's response. Fails closed:
// an ambiguous or unparseable response is treated as a failed verification
// rather than a pass, since Verify exists precisely to not blindly trust
// what a step (including a reviewer step) says about itself.
func parseReviewerVerdict(text string) (passed bool, reason string) {
	trimmed := strings.TrimSpace(text)
	upper := strings.ToUpper(trimmed)
	switch {
	case strings.HasPrefix(upper, "PASS"):
		return true, strings.TrimSpace(trimmed[len("PASS"):])
	case strings.HasPrefix(upper, "FAIL"):
		return false, strings.TrimSpace(trimmed[len("FAIL"):])
	default:
		return false, fmt.Sprintf("could not parse reviewer verdict: %q", trimmed)
	}
}
