package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	feotel "github.com/hollis-labs/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/hollis-labs/conduit/internal/chat"
	"github.com/hollis-labs/conduit/internal/permission"
	"github.com/hollis-labs/conduit/internal/provider"
	"github.com/hollis-labs/conduit/internal/truncate"
)

// toolPlanStatus describes the outcome of pre-checking a tool.
type toolPlanStatus string

const (
	toolPlanReady   toolPlanStatus = "ready"
	toolPlanBlocked toolPlanStatus = "blocked"
	toolPlanDenied  toolPlanStatus = "denied"
	toolPlanMeta    toolPlanStatus = "meta" // request_tools or other meta-tools
)

// toolPlan describes a single tool invocation after pre-checking.
type toolPlan struct {
	tu            provider.ToolUseBlock
	status        toolPlanStatus
	denyReason    string
	concurrent    bool
	originalIndex int
	// resultBlock is set during pre-check for blocked/denied tools.
	resultBlock *provider.ContentBlock
	ref         *chat.ToolCallRef
}

// toolExecResult holds the outcome of executing one tool.
type toolExecResult struct {
	originalIndex int
	resultBlock   provider.ContentBlock
	ref           chat.ToolCallRef
	isError       bool
	rawOutput     string
	duration      time.Duration
}

// toolExecContext bundles the immutable context needed for tool execution.
type toolExecContext struct {
	sessionID      string
	agentID        string
	assistantMsgID string
	agent          interface{ GetID() string } // minimal agent interface for artifacts
}

// preCheckTools evaluates each tool_use block: checks blocked status, permission,
// and concurrency safety. Returns a plan for each tool. Blocked/denied tools get
// their result blocks generated immediately. Permission "ask" tools block inline
// during pre-check (approval UX unchanged).
func (s *chatServiceImpl) preCheckTools(
	ctx context.Context,
	sessionID string,
	toolUseBlocks []provider.ToolUseBlock,
	ls *loopState,
	ch chan chat.StreamEvent,
	selection *ToolSelection,
	tools []provider.ToolDefinition,
) []toolPlan {
	plans := make([]toolPlan, 0, len(toolUseBlocks))

	for i, tu := range toolUseBlocks {
		plan := toolPlan{
			tu:            tu,
			originalIndex: i,
		}

		// Handle request_tools meta-tool.
		if tu.Name == "request_tools" && selection != nil && selection.Progressive {
			plan.status = toolPlanMeta
			plans = append(plans, plan)
			continue
		}

		// Check blocked tools.
		if ls.blockedTools[tu.Name] || ls.isToolExhausted(tu.Name) {
			blockedResult := fmt.Sprintf("BLOCKED: Tool %q has been hard-blocked due to repeated identical results or iteration limit. "+
				"Do NOT call this tool again. Use a different approach or inform the user.", tu.Name)
			log.Printf("chat-service: tool %s SKIPPED (blocked)", tu.Name)
			ch <- chat.StreamEvent{Type: "tool_call", Tool: tu.Name, ToolID: tu.ID}
			ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: blockedResult}
			block := provider.ContentBlock{
				Type: "tool_result", ToolUseID: tu.ID, Content: blockedResult,
			}
			ref := chat.ToolCallRef{ID: tu.ID, Name: tu.Name, Status: "blocked"}
			plan.status = toolPlanBlocked
			plan.resultBlock = &block
			plan.ref = &ref
			plans = append(plans, plan)
			continue
		}

		// Permission check.
		if s.permissions != nil {
			meta := permission.ToolMeta{}
			if toolInfo, ok := s.tools.GetToolMeta(tu.Name); ok {
				meta.IsReadOnly = toolInfo.IsReadOnly
				meta.IsDestructive = toolInfo.IsDestructive
			}
			permResult := s.permissions.Check(ctx, sessionID, tu.Name, tu.Input, meta)
			switch permResult.Decision {
			case permission.DecisionDeny:
				ls.recordPermissionDenial()
				denyMsg := fmt.Sprintf("PERMISSION DENIED: %s — %s", tu.Name, permResult.Reason)
				log.Printf("chat-service: tool %s denied: %s", tu.Name, permResult.Reason)
				ch <- chat.StreamEvent{Type: "tool_call", Tool: tu.Name, ToolID: tu.ID}
				ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: denyMsg}
				block := provider.ContentBlock{
					Type: "tool_result", ToolUseID: tu.ID, Content: denyMsg,
				}
				ref := chat.ToolCallRef{ID: tu.ID, Name: tu.Name, Status: "denied"}
				plan.status = toolPlanDenied
				plan.denyReason = permResult.Reason
				plan.resultBlock = &block
				plan.ref = &ref
				plans = append(plans, plan)
				continue

			case permission.DecisionAsk:
				// Emit approval request and block until user responds.
				req := s.permissions.RequestApproval(sessionID, tu.Name, tu.Input, permResult.Reason)
				approvalData, _ := json.Marshal(map[string]any{
					"request_id": req.ID,
					"tool":       tu.Name,
					"input":      tu.Input,
					"reason":     permResult.Reason,
				})
				ch <- chat.StreamEvent{Type: "approval_request", Data: string(approvalData)}

				resp := s.permissions.WaitForApproval(ctx, req)
				if resp.Decision != permission.DecisionAllow {
					ls.recordPermissionDenial()
					denyMsg := fmt.Sprintf("PERMISSION DENIED: %s — user denied or approval timed out", tu.Name)
					log.Printf("chat-service: tool %s denied by user (scope: %s)", tu.Name, resp.Scope)
					ch <- chat.StreamEvent{Type: "tool_call", Tool: tu.Name, ToolID: tu.ID}
					ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: denyMsg}
					block := provider.ContentBlock{
						Type: "tool_result", ToolUseID: tu.ID, Content: denyMsg,
					}
					ref := chat.ToolCallRef{ID: tu.ID, Name: tu.Name, Status: "denied"}
					plan.status = toolPlanDenied
					plan.denyReason = "user denied"
					plan.resultBlock = &block
					plan.ref = &ref
					plans = append(plans, plan)
					continue
				}
				log.Printf("chat-service: tool %s approved (scope: %s)", tu.Name, resp.Scope)
				ls.continueWith(ContinuePermission, fmt.Sprintf("tool %s approved (scope: %s)", tu.Name, resp.Scope))
			}
		}

		// Tool passed pre-check — determine concurrency safety.
		plan.status = toolPlanReady
		if toolInfo, ok := s.tools.GetToolMeta(tu.Name); ok {
			plan.concurrent = toolInfo.IsConcurrencySafe
		}
		plans = append(plans, plan)
	}

	return plans
}

// executeToolBatch runs tools from the plan. Concurrent-safe tools run in
// parallel; serial tools run one at a time. Results are stored at their
// original index to preserve ordering.
func (s *chatServiceImpl) executeToolBatch(
	ctx context.Context,
	plans []toolPlan,
	ls *loopState,
	agentID string,
	ch chan chat.StreamEvent,
	sessionID string,
) []toolExecResult {
	results := make([]toolExecResult, len(plans))

	// Separate ready plans into concurrent and serial.
	type indexedPlan struct {
		planIdx int
		plan    toolPlan
	}
	var concurrent, serial []indexedPlan
	for i, p := range plans {
		if p.status != toolPlanReady {
			continue
		}
		ip := indexedPlan{planIdx: i, plan: p}
		if p.concurrent {
			concurrent = append(concurrent, ip)
		} else {
			serial = append(serial, ip)
		}
	}

	// Execute concurrent-safe tools in parallel.
	if len(concurrent) > 0 {
		var wg sync.WaitGroup
		var mu sync.Mutex // protects ch sends ordering (presence events)
		for _, ip := range concurrent {
			wg.Add(1)
			go func(ip indexedPlan) {
				defer wg.Done()
				result := s.executeSingleTool(ctx, ip.plan.tu, agentID, sessionID, ch, &mu)
				results[ip.planIdx] = result
			}(ip)
		}
		wg.Wait()
	}

	// Execute serial tools one at a time.
	for _, ip := range serial {
		result := s.executeSingleTool(ctx, ip.plan.tu, agentID, sessionID, ch, nil)
		results[ip.planIdx] = result
	}

	return results
}

// executeSingleTool runs a single tool call and returns the result. If mu is
// non-nil, presence broadcasts are serialized through it.
func (s *chatServiceImpl) executeSingleTool(
	ctx context.Context,
	tu provider.ToolUseBlock,
	agentID string,
	sessionID string,
	ch chan chat.StreamEvent,
	mu *sync.Mutex, // nil for serial execution
) toolExecResult {
	start := time.Now()

	// Broadcast tool pending.
	if mu != nil {
		mu.Lock()
	}
	s.streams.BroadcastPresence(chat.PresenceEvent{
		Type: "tool_pending", SessionID: sessionID, AgentID: agentID,
		ToolName: tu.Name, Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	ch <- chat.StreamEvent{Type: "tool_call", Tool: tu.Name, ToolID: tu.ID, Detail: toolCallDetail(tu.Name, tu.Input)}
	if mu != nil {
		mu.Unlock()
	}

	// Execute tool via ToolService.
	toolCtx, toolSpan := feotel.ToolCallSpan(ctx, tu.Name)
	result, _ := s.tools.Execute(toolCtx, agentID, tu.Name, tu.Input)
	resultText := result.Output
	toolIsError := result.IsError

	if toolIsError {
		toolSpan.RecordError(fmt.Errorf("%s", resultText))
		toolSpan.SetStatus(codes.Error, resultText)
		log.Printf("chat-service: tool %s failed: %s", tu.Name, resultText)
		s.store.LogEvent(sessionID, "tool_error", "error",
			fmt.Sprintf("%s: %s", tu.Name, resultText), "{}")
		if s.events != nil {
			s.events.EmitToolCall(ctx, sessionID, tu.Name, false, 0)
			s.events.EmitToolFailed(ctx, sessionID, tu.Name, tu.Input, resultText)
		}
	} else {
		toolSpan.SetAttributes(attribute.Int("conduit.tool.result_len", len(resultText)))
		s.store.LogEvent(sessionID, "tool_call", "tool",
			tu.Name, fmt.Sprintf(`{"result_len":%d,"agent_id":%q}`, len(resultText), agentID))
		if s.events != nil {
			s.events.EmitToolCall(ctx, sessionID, tu.Name, true, len(resultText))
		}
	}
	toolSpan.End()

	// Broadcast tool resolved.
	if mu != nil {
		mu.Lock()
	}
	s.streams.BroadcastPresence(chat.PresenceEvent{
		Type: "tool_resolved", SessionID: sessionID, AgentID: agentID,
		ToolName: tu.Name, Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	if mu != nil {
		mu.Unlock()
	}

	duration := time.Since(start)

	return toolExecResult{
		resultBlock: provider.ContentBlock{
			Type: "tool_result", ToolUseID: tu.ID, Content: resultText, IsError: toolIsError,
		},
		ref:       chat.ToolCallRef{ID: tu.ID, Name: tu.Name},
		isError:   toolIsError,
		rawOutput: resultText,
		duration:  duration,
	}
}

// postProcessToolResults processes raw execution results: stuck loop detection,
// truncation, envelope capture, warnings, artifact detection. Returns the final
// result blocks and tool call refs in original order.
func (s *chatServiceImpl) postProcessToolResults(
	ctx context.Context,
	plans []toolPlan,
	results []toolExecResult,
	ls *loopState,
	ch chan chat.StreamEvent,
	sessionID string,
	agentID string,
	assistantMsgID string,
) ([]provider.ContentBlock, []chat.ToolCallRef) {
	var resultBlocks []provider.ContentBlock
	var refs []chat.ToolCallRef

	for i, plan := range plans {
		// Blocked/denied tools already have their results.
		if plan.status == toolPlanBlocked || plan.status == toolPlanDenied {
			if plan.resultBlock != nil {
				resultBlocks = append(resultBlocks, *plan.resultBlock)
			}
			if plan.ref != nil {
				refs = append(refs, *plan.ref)
			}
			continue
		}

		// Meta tools (request_tools) are handled separately — skip here.
		if plan.status == toolPlanMeta {
			continue
		}

		// Ready tools — process execution result.
		r := results[i]
		tu := plan.tu

		// Record tool call outcome.
		ls.recordToolCall(tu.Name, !r.isError)

		// Tool warning on errors.
		if r.isError {
			level := "warning"
			if ls.consecutiveFailures >= ls.limits.consecutiveFailCap {
				level = "critical"
			}
			warningError := r.rawOutput
			if len(warningError) > 300 {
				warningError = warningError[:300] + "..."
			}
			warningPayload := chat.ToolWarningPayload{
				ToolName: tu.Name, Error: warningError,
				Iteration: ls.iteration, ConsecutiveErrors: ls.consecutiveFailures, Level: level,
			}
			warningJSON, _ := json.Marshal(warningPayload)
			ch <- chat.StreamEvent{Type: "tool_warning", Data: string(warningJSON)}
		} else {
			// Capture envelope data from successful results.
			ls.pendingEnvelopes = captureEnvelopeData(r.rawOutput, tu.Name, ls.pendingEnvelopes)
		}

		// Detect stuck loops (modifies result text).
		resultText := s.detectStuckLoop(tu.Name, r.rawOutput, ls.lastToolResults, ls.toolRepeatCount, ls.blockedTools)

		// Truncate for LLM context.
		canDelegate := s.orchestrator != nil && s.orchestrator.HasDecomposer()
		tr := truncate.Output(resultText, tu.Name, truncate.WithDelegationHint(canDelegate))

		// Emit tool_result to client.
		summary := resultText
		if len(summary) > 500 {
			summary = summary[:500] + "... (truncated)"
		}
		ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: summary}

		// Auto-detect artifacts.
		if !r.isError {
			s.maybeCreateAutoArtifact(sessionID, assistantMsgID, agentID, tu)
		}

		if tr.Truncated {
			log.Printf("chat-service: tool %s result truncated: %d → %d chars (saved to %s)",
				tu.Name, tr.OriginalLen, len(tr.Content), tr.OutputPath)
			s.store.LogEvent(sessionID, "tool_truncated", "context",
				tu.Name, fmt.Sprintf(`{"original_len":%d,"truncated_len":%d,"output_path":%q}`,
					tr.OriginalLen, len(tr.Content), tr.OutputPath))
		}

		// Build final result block with truncated content.
		resultBlocks = append(resultBlocks, provider.ContentBlock{
			Type: "tool_result", ToolUseID: tu.ID, Content: tr.Content, IsError: r.isError,
		})

		tcStatus := "success"
		if r.isError {
			tcStatus = "error"
		}
		tcRef := chat.ToolCallRef{ID: tu.ID, Name: tu.Name, Status: tcStatus}
		if strings.Contains(r.rawOutput, "<!--ENVELOPE_DATA:") {
			tcRef.HasEnvelope = true
		}
		refs = append(refs, tcRef)
	}

	return resultBlocks, refs
}

// toolCallDetail extracts a short human-readable label from a tool's input.
// For dev_bash this is the command, for file tools the path, etc.
func toolCallDetail(toolName string, input map[string]any) string {
	get := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := input[k]; ok {
				if s, ok := v.(string); ok && s != "" {
					return s
				}
			}
		}
		return ""
	}

	var detail string
	switch {
	case strings.HasSuffix(toolName, "dev_bash"):
		detail = get("command", "cmd")
	case strings.HasSuffix(toolName, "dev_read"):
		detail = get("path", "file_path")
	case strings.HasSuffix(toolName, "dev_write"), strings.HasSuffix(toolName, "dev_edit"):
		detail = get("file_path", "path")
	case strings.HasSuffix(toolName, "dev_grep"):
		detail = get("pattern")
	case strings.HasSuffix(toolName, "dev_glob"):
		detail = get("pattern")
	case strings.HasSuffix(toolName, "web_fetch"):
		detail = get("url")
	case strings.HasSuffix(toolName, "web_search"):
		detail = get("query")
	}

	if detail == "" {
		return ""
	}
	// Truncate long details (e.g., multi-line bash commands).
	if i := strings.IndexByte(detail, '\n'); i > 0 {
		detail = detail[:i] + "…"
	}
	if len(detail) > 120 {
		detail = detail[:117] + "…"
	}
	return detail
}
