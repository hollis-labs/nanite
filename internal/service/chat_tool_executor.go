package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	feotel "github.com/hollis-labs/go-otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/hollis-labs/nanite/internal/chat"
	inspectsvc "github.com/hollis-labs/nanite/internal/inspector"
	"github.com/hollis-labs/nanite/internal/loopdetect"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/permission"
	pluginpkg "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/truncate"
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
	tu            llmtypes.ToolUseBlock
	status        toolPlanStatus
	denyReason    string
	concurrent    bool
	originalIndex int
	// resultBlock is set during pre-check for blocked/denied tools.
	resultBlock *llmtypes.ContentBlock
	ref         *chat.ToolCallRef
}

// toolExecResult holds the outcome of executing one tool.
type toolExecResult struct {
	originalIndex int
	resultBlock   llmtypes.ContentBlock
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
	agentID string,
	toolUseBlocks []llmtypes.ToolUseBlock,
	ls *loopState,
	ch chan chat.StreamEvent,
	selection *ToolSelection,
	tools []llmtypes.ToolDefinition,
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
		//
		// Two distinct paths land here. We surface them as distinct messages
		// to the LLM so it gets accurate guidance:
		//
		//   1. blockedTools[tu.Name] = true → the same-result-repeated
		//      detector (detectStuckLoop) tripped after 2 identical results.
		//      This IS a runaway signal; the LLM should pivot, not retry.
		//
		//   2. isToolExhausted(tu.Name) → the high backstop count cap
		//      (defaultPerToolCap, default 150 per CW-20260519-0115) was
		//      reached. With 150 as the floor, this almost always means
		//      the operator asked for a lot of work and the work is real;
		//      the agent should report what it did and offer to continue
		//      in a follow-up turn. The "X of Y, N remaining" shape comes
		//      from the agent's natural-language summary; the harness
		//      surfaces the count + cap so the agent has the data to
		//      build that summary.
		if ls.blockedTools[tu.Name] || ls.isToolExhausted(tu.Name) {
			ls.recordToolCall(tu.Name, false)
			var blockedResult string
			isCountCap := ls.isToolExhausted(tu.Name) && !ls.blockedTools[tu.Name]
			if isCountCap {
				// PR #213 review: renamed local `cap`/`max` so they don't
				// shadow Go's builtins (`cap()` and `max()` / Go 1.21+).
				perTurnCap := ls.limits.defaultPerToolCap
				if toolMax, ok := ls.limits.perToolMax[tu.Name]; ok && toolMax > 0 {
					perTurnCap = toolMax
				}
				blockedResult = fmt.Sprintf(
					"Tool %q hit its per-turn backstop (%d of %d calls used this turn). "+
						"This is the absolute-limit failsafe, not a runaway signal — the loop, error, and same-result detectors did not trip, so the calls you made were intentional. "+
						"Stop calling %q for the rest of this turn. "+
						"Tell the user what you completed (e.g. \"X of Y done, Z remaining\") and offer to finish the rest in a follow-up turn so the per-turn counter resets.",
					tu.Name, ls.toolCallCounts[tu.Name], perTurnCap, tu.Name,
				)
				slog.Warn("chat-service: tool SKIPPED (per-turn backstop)",
					"tool", tu.Name, "calls", ls.toolCallCounts[tu.Name], "cap", perTurnCap)
			} else {
				blockedResult = fmt.Sprintf(
					"Tool %q isn't available for the rest of this turn — it returned the same result repeatedly, so the harness is holding further calls to protect your context budget. "+
						"If you need the data it produced, it's already in the conversation above. If you need something different, try a related tool, change the arguments meaningfully, or summarize what you have for the user.",
					tu.Name,
				)
				slog.Warn("chat-service: tool SKIPPED (stuck-loop detector)", "tool", tu.Name)
			}
			ch <- chat.StreamEvent{Type: "tool_call", Tool: tu.Name, ToolID: tu.ID, Detail: toolCallDetail(tu.Name, tu.Input)}
			ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: blockedResult, IsError: true}
			block := llmtypes.ContentBlock{
				Type: "tool_result", ToolUseID: tu.ID, Content: blockedResult,
			}
			ref := chat.ToolCallRef{ID: tu.ID, Name: tu.Name, Status: "blocked", ErrorReason: blockedResult}
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
			case permission.DecisionAllow:
				// Allow: fall through to tool execution below.
			case permission.DecisionDeny:
				ls.recordToolCall(tu.Name, false)
				denyMsg := fmt.Sprintf("PERMISSION DENIED: %s — %s", tu.Name, permResult.Reason)
				slog.Warn("chat-service: tool denied", "tool", tu.Name, "reason", permResult.Reason)
				ch <- chat.StreamEvent{Type: "tool_call", Tool: tu.Name, ToolID: tu.ID, Detail: toolCallDetail(tu.Name, tu.Input)}
				ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: denyMsg, IsError: true}
				block := llmtypes.ContentBlock{
					Type: "tool_result", ToolUseID: tu.ID, Content: denyMsg,
				}
				ref := chat.ToolCallRef{ID: tu.ID, Name: tu.Name, Status: "denied", ErrorReason: denyMsg}
				plan.status = toolPlanDenied
				plan.denyReason = permResult.Reason
				plan.resultBlock = &block
				plan.ref = &ref
				plans = append(plans, plan)
				continue

			case permission.DecisionAsk:
				// Emit approval request and block until user responds.
				req := s.permissions.RequestApproval(sessionID, tu.Name, tu.Input, permResult.Reason)
				approvalData, _ := json.Marshal(chat.ApprovalRequestPayload{
					RequestID: req.ID,
					Tool:      tu.Name,
					Input:     tu.Input,
					Reason:    permResult.Reason,
				})
				ch <- chat.StreamEvent{Type: "approval_request", Data: string(approvalData)}

				resp := s.permissions.WaitForApproval(ctx, req)
				if resp.Decision != permission.DecisionAllow {
					ls.recordToolCall(tu.Name, false)
					denyReason := "user denied"
					if resp.TimedOut {
						denyReason = "approval timed out"
					}
					denyMsg := fmt.Sprintf("PERMISSION DENIED: %s — %s", tu.Name, denyReason)
					slog.Warn("chat-service: tool denied", "tool", tu.Name, "reason", denyReason, "scope", resp.Scope)
					ch <- chat.StreamEvent{Type: "tool_call", Tool: tu.Name, ToolID: tu.ID, Detail: toolCallDetail(tu.Name, tu.Input)}
					ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: denyMsg, IsError: true}
					// Warn user when consecutive failures approach the stop threshold.
					if ls.consecutiveFailures >= ls.limits.consecutiveFailCap-1 {
						warningJSON, _ := json.Marshal(map[string]any{
							"tool_name":          tu.Name,
							"error":              fmt.Sprintf("Tools failed %d times in a row — agent will pause after one more failure", ls.consecutiveFailures),
							"iteration":          ls.iteration,
							"consecutive_errors": ls.consecutiveFailures,
							"level":              "critical",
						})
						ch <- chat.StreamEvent{Type: "tool_warning", Data: string(warningJSON)}
					}
					block := llmtypes.ContentBlock{
						Type: "tool_result", ToolUseID: tu.ID, Content: denyMsg,
					}
					ref := chat.ToolCallRef{ID: tu.ID, Name: tu.Name, Status: "denied", ErrorReason: denyMsg}
					plan.status = toolPlanDenied
					plan.denyReason = "user denied"
					plan.resultBlock = &block
					plan.ref = &ref
					plans = append(plans, plan)
					continue
				}
				slog.Info("chat-service: tool approved", "tool", tu.Name, "scope", resp.Scope)
				ls.continueWith(ContinuePermission, fmt.Sprintf("tool %s approved (scope: %s)", tu.Name, resp.Scope))

			default:
				// Fail closed on any unknown Decision value (defence against future
				// enum additions that might otherwise silently fall through to tool
				// execution).
				ls.recordToolCall(tu.Name, false)
				denyMsg := fmt.Sprintf("PERMISSION DENIED: %s — unknown permission decision %q", tu.Name, permResult.Decision)
				slog.Warn("chat-service: tool denied, unknown permission decision", "tool", tu.Name, "decision", permResult.Decision)
				ch <- chat.StreamEvent{Type: "tool_call", Tool: tu.Name, ToolID: tu.ID, Detail: toolCallDetail(tu.Name, tu.Input)}
				ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: denyMsg, IsError: true}
				block := llmtypes.ContentBlock{
					Type: "tool_result", ToolUseID: tu.ID, Content: denyMsg,
				}
				ref := chat.ToolCallRef{ID: tu.ID, Name: tu.Name, Status: "denied", ErrorReason: denyMsg}
				plan.status = toolPlanDenied
				plan.denyReason = fmt.Sprintf("unknown permission decision %q", permResult.Decision)
				plan.resultBlock = &block
				plan.ref = &ref
				plans = append(plans, plan)
				continue
			}
		}

		// --- Pre-hook: tool.executing ---
		// Plugins observing "tool.executing" may cancel tool execution.
		// Data shape: {session_id, tool_name, tool_input, tool_id}.
		if s.pluginHost != nil {
			cancelled := s.pluginHost.EmitPreHook("tool.executing", sessionID, map[string]any{
				"tool_name":  tu.Name,
				"tool_input": tu.Input,
				"tool_id":    tu.ID,
			})
			if cancelled {
				ls.recordToolCall(tu.Name, false)
				blockMsg := fmt.Sprintf("Tool %q was refused by a policy plugin for this input. Retrying with the same arguments will be refused again — adjust the arguments, pick a different tool, or explain to the user that this action is gated.", tu.Name)
				slog.Info("chat-service: tool blocked by plugin pre-hook", "tool", tu.Name)
				ch <- chat.StreamEvent{Type: "tool_call", Tool: tu.Name, ToolID: tu.ID, Detail: toolCallDetail(tu.Name, tu.Input)}
				ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: blockMsg, IsError: true}
				block := llmtypes.ContentBlock{
					Type: "tool_result", ToolUseID: tu.ID, Content: blockMsg,
				}
				ref := chat.ToolCallRef{ID: tu.ID, Name: tu.Name, Status: "blocked", ErrorReason: blockMsg}
				plan.status = toolPlanBlocked
				plan.resultBlock = &block
				plan.ref = &ref
				plans = append(plans, plan)
				continue
			}
		}

		// Execution-time rules: re-check agent toolset + permissions.
		if allowed, reason := s.enforceExecutionRulesForTool(ctx, agentID, tu.Name); !allowed {
			ls.recordToolCall(tu.Name, false)
			denyMsg := fmt.Sprintf("EXECUTION_RULES_DENIED: %s — %s", tu.Name, reason)
			slog.Warn("chat-service: tool denied by execution rules", "tool", tu.Name, "reason", reason)
			ch <- chat.StreamEvent{Type: "tool_call", Tool: tu.Name, ToolID: tu.ID, Detail: toolCallDetail(tu.Name, tu.Input)}
			ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: denyMsg, IsError: true}
			block := llmtypes.ContentBlock{
				Type: "tool_result", ToolUseID: tu.ID, Content: denyMsg, IsError: true,
			}
			ref := chat.ToolCallRef{ID: tu.ID, Name: tu.Name, Status: "denied", ErrorReason: denyMsg}
			plan.status = toolPlanDenied
			plan.denyReason = reason
			plan.resultBlock = &block
			plan.ref = &ref
			plans = append(plans, plan)
			continue
		}

		// Arg validation: check tool_use Input against the tool's InputSchema.
		if schema := s.tools.GetToolSchema(tu.Name); len(schema) > 0 {
			if errMsg := s.argValidator.validate(tu.Name, schema, tu.Input); errMsg != "" {
				ls.recordToolCall(tu.Name, false)
				// CW-20260417-0485: arg-validation errors are the canonical
				// trigger for the chat-loop-terminated envelope (see the
				// session evidence in the ticket). Capture the payload so
				// the envelope can surface it to the user.
				ls.recordLastError(tu.Name, errMsg)
				slog.Warn("chat-service: tool arg validation failed", "tool", tu.Name, "err", errMsg)
				ch <- chat.StreamEvent{Type: "tool_call", Tool: tu.Name, ToolID: tu.ID, Detail: toolCallDetail(tu.Name, tu.Input)}
				ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: errMsg, IsError: true}
				block := llmtypes.ContentBlock{
					Type: "tool_result", ToolUseID: tu.ID, Content: errMsg, IsError: true,
				}
				ref := chat.ToolCallRef{ID: tu.ID, Name: tu.Name, Status: "error", ErrorReason: errMsg}
				plan.status = toolPlanBlocked
				plan.resultBlock = &block
				plan.ref = &ref
				plans = append(plans, plan)
				continue
			}
		}

		// Tool passed pre-check — determine concurrency safety.
		plan.status = toolPlanReady
		if toolInfo, ok := s.tools.GetToolMeta(tu.Name); ok {
			plan.concurrent = toolInfo.IsConcurrencySafe
		}
		// Scratchpad tools access loopState directly with no mutex; always serial.
		if isScratchpadTool(tu.Name) {
			plan.concurrent = false
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
	// CW-20260418 (c7 scope_id bug fix): stamp the current chat session
	// onto ctx so downstream tool handlers — notably the MCP self-tools
	// that write todos/plans — can auto-fill scope_id when the agent omits
	// it. The LLM has no way to know its own session_id, so without this
	// the records land in the DB with scope_id="" and the Work drawer
	// (which queries by the real session UUID) never finds them.
	ctx = mcp.WithSessionID(ctx, sessionID)

	// CW-20260430-0009: stamp the trust-agent session-scoped path grants
	// so dev_tools.resolveAllowed can fall back to explicit-mention
	// grants when the static AllowedPaths list rejects. nil-safe.
	if s.pathGrants != nil {
		ctx = permission.WithPathGrants(ctx, sessionID, s.pathGrants)
	}

	// H1 trust gate (CW-20260421-0014): stamp agent_profile_id so
	// MuxTransportAdapter and self_tools_dispatch can derive the caller's
	// trust tier without changing CallTool signatures. agentID IS the
	// agent_profiles.id for the primary agent of this session. Phase 0 item
	// 20 (retire workspaces): this used to also stamp workspace_id — the
	// caller-profile ctx is now agent-only.
	ctx = mcp.WithCallerProfile(ctx, agentID)

	// CW-20260429-0024: stamp the union of (prior iterations' tool_use_ids
	// from ls.toolCallRefs) and (this iteration's plan tool_use_ids) so the
	// card_show sources gate can reject fabricated tool_use_id values.
	// Including the current iteration matters: the agent may call
	// card_show in the same assistant batch as the data tools whose
	// results ground the card, citing those peer tool_use_ids. Without
	// "this iteration" the gate would over-block and force the agent to
	// produce sources after a follow-up turn even when the citation is
	// genuinely concurrent.
	//
	// CW-20260429-0025: also stamp the union of tool *names* called this
	// turn. Originally fed the describe-required gate at callShowCard;
	// the gate was removed in Phase A of the architectural rebalancing,
	// but the per-turn name set is preserved as plumbing for future
	// per-turn observability or trust checks.
	turnIDs := make([]string, 0, len(ls.toolCallRefs)+len(plans))
	turnNames := make([]string, 0, len(ls.toolCallRefs)+len(plans))
	for _, ref := range ls.toolCallRefs {
		if ref.ID != "" {
			turnIDs = append(turnIDs, ref.ID)
		}
		if ref.Name != "" {
			turnNames = append(turnNames, ref.Name)
		}
	}
	for _, p := range plans {
		if p.tu.ID != "" {
			turnIDs = append(turnIDs, p.tu.ID)
		}
		if p.tu.Name != "" {
			turnNames = append(turnNames, p.tu.Name)
		}
	}
	ctx = mcp.WithTurnToolUseIDs(ctx, turnIDs)
	ctx = mcp.WithTurnToolNames(ctx, turnNames)

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
			ipc := ip
			safego.Go(ctx, "service.chat.executeSingleTool.concurrent", func() {
				defer wg.Done()
				result := s.executeSingleTool(ctx, ipc.plan.tu, ls, agentID, sessionID, ch, &mu)
				results[ipc.planIdx] = result
			})
		}
		wg.Wait()
	}

	// Execute serial tools one at a time.
	for _, ip := range serial {
		result := s.executeSingleTool(ctx, ip.plan.tu, ls, agentID, sessionID, ch, nil)
		results[ip.planIdx] = result
	}

	return results
}

// executeSingleTool runs a single tool call and returns the result. If mu is
// non-nil, presence broadcasts are serialized through it.
func (s *chatServiceImpl) executeSingleTool(
	ctx context.Context,
	tu llmtypes.ToolUseBlock,
	ls *loopState,
	agentID string,
	sessionID string,
	ch chan chat.StreamEvent,
	mu *sync.Mutex, // nil for serial execution
) toolExecResult {
	start := time.Now()

	// Handle result-cache meta-tools locally (no MCP routing).
	if tu.Name == "fetch_tool_result" || tu.Name == "search_tool_result" {
		return s.handleResultCacheMetaTool(tu, sessionID, ch, mu, start)
	}

	// Handle P4 scratchpad tools locally (pure loopState access — no MCP routing).
	if isScratchpadTool(tu.Name) {
		return handleScratchpadTool(tu, ls, ch, mu, start)
	}

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

	// Trust-agent notify-pause middleware (CW-20260430-0009 Q5). For
	// dev_* tools that touch the filesystem or shell, emit a brief
	// inline placeholder UI ("Reading `<path>`… (cancel?)") and pause
	// for ~1.5s. If the user cancels (ctx.Done() fires) during the
	// window we return a clean errorResult rather than running the
	// tool. Read-only discovery tools (dev_grep, dev_glob) are exempted
	// inside shouldNotifyPause to avoid the rule-following-defendant
	// pattern from docs/architecture/agent-context-architecture.md.
	if cancelled := emitNotifyPause(ctx, tu, ch, mu, 0); cancelled {
		cancelMsg := fmt.Sprintf("Tool %q cancelled by user during notify-pause window.", tu.Name)
		ls.recordToolCall(tu.Name, false)
		ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: cancelMsg, IsError: true}
		return toolExecResult{
			resultBlock: llmtypes.ContentBlock{
				Type: "tool_result", ToolUseID: tu.ID, Content: cancelMsg, IsError: true,
			},
			ref:       chat.ToolCallRef{ID: tu.ID, Name: tu.Name, Status: "cancelled", ErrorReason: cancelMsg},
			isError:   true,
			rawOutput: cancelMsg,
			duration:  time.Since(start),
		}
	}

	// Execute tool via ToolService.
	toolCtx, toolSpan := feotel.ToolCallSpan(ctx, tu.Name)
	result, _ := s.tools.Execute(toolCtx, agentID, tu.Name, tu.Input)
	resultText := result.Output
	toolIsError := result.IsError

	// Filter: tool_result — summarize, redact secrets, reformat.
	if s.pluginHost != nil && !toolIsError {
		fctx := pluginpkg.FilterContext{
			SessionID: sessionID,
			AgentID:   agentID,
			Metadata:  map[string]interface{}{"tool_name": tu.Name},
		}
		if filtered, err := s.pluginHost.ApplyFilter(pluginpkg.FilterToolResult, resultText, fctx); err != nil {
			slog.Warn("chat-service: tool_result filter error", "tool", tu.Name, "err", err)
		} else if fs, ok := filtered.(string); ok {
			resultText = fs
		}
	}

	if toolIsError {
		resultText = chat.SanitizeToolError(resultText)
		toolSpan.RecordError(fmt.Errorf("%s", resultText))
		toolSpan.SetStatus(codes.Error, resultText)
		slog.Warn("chat-service: tool failed", "tool", tu.Name, "content", resultText)
		s.store.LogEvent(sessionID, "tool_error", "error",
			fmt.Sprintf("%s: %s", tu.Name, resultText), "{}")
		if s.events != nil {
			s.events.EmitToolCall(ctx, sessionID, tu.Name, false, 0)
			s.events.EmitToolFailed(ctx, sessionID, tu.Name, tu.Input, resultText)
		}
	} else {
		toolSpan.SetAttributes(attribute.Int("nanite.tool.result_len", len(resultText)))
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
	// Notify UI when agent mutates todos/plans so the Work tab can refresh.
	if isWorkTool(tu.Name) && !toolIsError {
		s.streams.BroadcastPresence(chat.PresenceEvent{
			Type: "work_changed", SessionID: sessionID,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}
	if mu != nil {
		mu.Unlock()
	}

	duration := time.Since(start)

	// I1 (CW-20260426-0004): record tool call to inspector (additive, non-blocking).
	if s.inspector != nil && ls != nil && ls.inspectorTurnID != "" {
		argsJSON, _ := json.Marshal(tu.Input)
		s.inspector.RecordToolCall(sessionID, ls.inspectorTurnID, inspectsvc.ToolCallRecord{
			ToolID:     tu.ID,
			Name:       tu.Name,
			Arguments:  string(argsJSON),
			Result:     resultText,
			IsError:    toolIsError,
			LatencyMs:  duration.Milliseconds(),
			CacheState: "n/a",
		})
	}

	// I2 (CW-20260420-0029): fingerprint-based loop detection.
	// Non-blocking: Record acquires its own mutex and returns immediately.
	if s.loopDetector != nil && ls != nil {
		argsJSON, _ := json.Marshal(tu.Input)
		turnID := ""
		if ls.inspectorTurnID != "" {
			turnID = ls.inspectorTurnID
		}
		sig := loopdetect.Signal{
			SessionID: sessionID,
			TurnID:    turnID,
			ToolName:  tu.Name,
			Args:      json.RawMessage(argsJSON),
			Timestamp: time.Now(),
		}
		if det, detected := s.loopDetector.Record(sig); detected {
			slog.Warn("loop_detected",
				"session_id", sessionID,
				"tool_name", det.ToolName,
				"fingerprint", string(det.Fingerprint),
				"count", det.Count,
				"window_size", det.WindowSize,
				"turn_id", det.DetectedAtTurn,
				"reason", det.Reason,
			)
			if s.inspector != nil && det.DetectedAtTurn != "" {
				s.inspector.RecordLoopStatus(sessionID, det.DetectedAtTurn, &inspectsvc.LoopRecord{
					Detected: true,
					Reason:   det.Reason,
				})
			}
		}
	}

	return toolExecResult{
		resultBlock: llmtypes.ContentBlock{
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
//
// modelID is the active LLM model's wire-level identifier (e.g.
// "claude-sonnet-4-20250514"). Threaded so the truncation step can size the
// per-call MaxChars budget against the model's context window via
// truncate.OutputForModel — see CW-20260430-0008. Empty modelID is valid; it
// falls through to the static MaxChars floor (matches Output's behavior).
func (s *chatServiceImpl) postProcessToolResults(
	ctx context.Context,
	plans []toolPlan,
	results []toolExecResult,
	ls *loopState,
	ch chan chat.StreamEvent,
	sessionID string,
	agentID string,
	assistantMsgID string,
	modelID string,
) ([]llmtypes.ContentBlock, []chat.ToolCallRef) {
	var resultBlocks []llmtypes.ContentBlock
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
			// CW-20260417-0485: remember the last tool error so a later
			// chat-loop-terminated envelope (runaway breaker) can carry the
			// triggering payload for the FE to render.
			ls.recordLastError(tu.Name, r.rawOutput)
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
			ls.pendingEnvelopes = captureEnvelopeData(r.rawOutput, ls.pendingEnvelopes)
		}

		// Detect stuck loops (modifies result text).
		// Scratchpad tools are exempt: scratchpad_read legitimately returns
		// the same value on repeated reads (the scratchpad contents haven't changed),
		// and blocking it would deny the agent its own working memory.
		var resultText string
		if isScratchpadTool(tu.Name) {
			resultText = r.rawOutput
		} else {
			resultText = s.detectStuckLoop(tu.Name, r.rawOutput, ls.lastToolResults, ls.toolRepeatCount, ls.blockedTools)
		}

		// Cache-and-pointer: route through ResultCache before truncation.
		// If the result exceeds the soft threshold, the cache returns a
		// truncated view with a pointer footer. Skip truncate.Output in
		// that case — the cache already sized the LLM-visible view and
		// truncate.Output's 4K cap would drop the pointer footer.
		//
		// Meta-tools (discovery + cache-navigation) are exempt: their
		// output is what the agent reads to *decide* its next action, and
		// caching them produces a pointer-to-pointer dance that wastes
		// turns. tool_describe in particular went over the 2 KiB
		// soft cap in c114 (6813 bytes), forcing the agent through
		// fetch/search and burning all 10 turns before it could emit a
		// card.
		wasCached := false
		if s.resultCache != nil && !r.isError && !isScratchpadTool(tu.Name) && !isCacheExemptTool(tu.Name) {
			visible, cached, err := s.resultCache.StoreResult(sessionID, tu.ID, tu.Name, resultText)
			if err != nil {
				slog.Warn("chat-service: result cache store error", "tool", tu.Name, "err", err)
			} else if cached {
				resultText = visible
				wasCached = true
			}
		}

		// Truncate for LLM context (handles results not caught by the cache).
		// Skip when the cache already produced the LLM-visible view.
		//
		// CW-20260501-0006: error results are exempt from truncation. Errors
		// are short and load-bearing — the agent's recovery decision depends
		// on reading the actual reason ("memory service not configured",
		// "query is required", etc.). Truncating them swaps the verbatim
		// reason for a misleading "delegate to a research agent" hint, which
		// caused c121's "I don't have access to a memory recall tool"
		// hallucination. The cache layer already exempts errors (see the
		// !r.isError gate above); this matches that contract for the
		// truncate path.
		var tr truncate.Result
		if wasCached || isScratchpadTool(tu.Name) || isCacheExemptTool(tu.Name) || r.isError {
			// Scratchpad results are bounded by the 64 KiB turn cap enforced in
			// loopState.scratchpadWrite — no caching or disk truncation needed.
			// Errors pass through verbatim (load-bearing for agent recovery).
			tr = truncate.Result{Content: resultText}
		} else {
			canDelegate := s.orchestrator != nil && s.orchestrator.HasDecomposer()
			// CW-20260430-0008 pilot: route through OutputForModel so the
			// MaxChars cap scales with the model's context window. modelID
			// may be empty (pre-resolution paths or stub callers); the
			// helper falls through to the static MaxChars floor in that
			// case — behavior identical to truncate.Output.
			tr = truncate.OutputForModel(resultText, tu.Name, modelID, truncate.WithDelegationHint(canDelegate))
		}

		// Emit tool_result to client.
		summary := resultText
		if len(summary) > 500 {
			summary = summary[:500] + "... (truncated)"
		}
		// IsError mirrors the underlying ToolCallRef.Status — load-bearing
		// for the subagent fabrication-suspected detector (CW-20260512-0095).
		// Consumers like drainCapture count tool_result events split by
		// IsError to detect "tools attempted but none succeeded".
		ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: summary, IsError: r.isError}

		// Auto-detect artifacts.
		if !r.isError {
			s.maybeCreateAutoArtifact(sessionID, assistantMsgID, agentID, tu)
		}

		if tr.Truncated {
			slog.Info("chat-service: tool result truncated",
				"tool", tu.Name, "original_len", tr.OriginalLen,
				"truncated_len", len(tr.Content), "path", tr.OutputPath)
			s.store.LogEvent(sessionID, "tool_truncated", "context",
				tu.Name, fmt.Sprintf(`{"original_len":%d,"truncated_len":%d,"output_path":%q}`,
					tr.OriginalLen, len(tr.Content), tr.OutputPath))
		}

		// Build final result block with truncated content.
		resultBlocks = append(resultBlocks, llmtypes.ContentBlock{
			Type: "tool_result", ToolUseID: tu.ID, Content: tr.Content, IsError: r.isError,
		})
		if shouldDirectReturnSubagentLiteral(plans, tu.Name, tr.Content, r.isError) {
			ls.directReturn = tr.Content
		}

		tcStatus := "success"
		if r.isError {
			tcStatus = "error"
		}
		tcRef := chat.ToolCallRef{ID: tu.ID, Name: tu.Name, Status: tcStatus}
		if r.isError {
			// CW-20260501-0013: capture the verbatim error reason so the
			// failure-footer enrichment can inline it next to the tool name.
			// tr.Content is the LLM-visible content of the tool_result block;
			// for errors it is the same as r.rawOutput (errors are exempt
			// from cache + truncation per CW-20260501-0006).
			tcRef.ErrorReason = tr.Content
		}
		if strings.Contains(r.rawOutput, "<!--ENVELOPE_DATA:") {
			tcRef.HasEnvelope = true
		}
		refs = append(refs, tcRef)
	}

	return resultBlocks, refs
}

func shouldDirectReturnSubagentLiteral(plans []toolPlan, toolName, content string, isError bool) bool {
	if isError || len(plans) != 1 || toolName != "subagent_spawn" {
		return false
	}
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") && strings.Count(content, "```") >= 2 {
		return true
	}
	if strings.HasPrefix(content, "1. ") {
		return true
	}
	return strings.HasPrefix(content, "First ") || strings.HasPrefix(content, "Requested content from ")
}

// toolCallDetail extracts a short human-readable label from a tool's input.
// For dev_bash this is the command, for file tools the path, etc.
// isWorkTool returns true for agent tools that mutate todos or plans.
func isWorkTool(name string) bool {
	switch name {
	case "todo_create", "todo_update", "plan_create", "plan_update":
		return true
	}
	return false
}

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

// handleResultCacheMetaTool handles fetch_tool_result and search_tool_result
// meta-tool calls locally without MCP routing.
func (s *chatServiceImpl) handleResultCacheMetaTool(
	tu llmtypes.ToolUseBlock,
	sessionID string,
	ch chan chat.StreamEvent,
	mu *sync.Mutex,
	start time.Time,
) toolExecResult {
	// Broadcast tool pending.
	if mu != nil {
		mu.Lock()
	}
	ch <- chat.StreamEvent{Type: "tool_call", Tool: tu.Name, ToolID: tu.ID, Detail: toolCallDetail(tu.Name, tu.Input)}
	if mu != nil {
		mu.Unlock()
	}

	var resultText string
	var isError bool

	if s.resultCache == nil {
		resultText = "Error: result cache not available"
		isError = true
	} else if tu.Name == "fetch_tool_result" {
		resultText, isError = s.handleFetchToolResult(sessionID, tu.Input)
	} else {
		resultText, isError = s.handleSearchToolResult(sessionID, tu.Input)
	}

	duration := time.Since(start)
	summary := resultText
	if len(summary) > 500 {
		summary = summary[:500] + "... (truncated)"
	}
	ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: summary, IsError: isError}

	return toolExecResult{
		resultBlock: llmtypes.ContentBlock{
			Type: "tool_result", ToolUseID: tu.ID, Content: resultText, IsError: isError,
		},
		ref:       chat.ToolCallRef{ID: tu.ID, Name: tu.Name},
		isError:   isError,
		rawOutput: resultText,
		duration:  duration,
	}
}

func (s *chatServiceImpl) handleFetchToolResult(sessionID string, input map[string]any) (string, bool) {
	id, _ := input["id"].(string)
	if id == "" {
		return "Error: 'id' is required", true
	}
	offset := 0
	if v, ok := input["offset"].(float64); ok {
		offset = int(v)
	}
	length := 65536
	if v, ok := input["length"].(float64); ok && v > 0 {
		length = int(v)
	}

	slice, totalSize, err := s.resultCache.Fetch(sessionID, id, offset, length)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	header := fmt.Sprintf("[Cached result %s — showing bytes %d-%d of %d total]\n\n",
		id, offset, offset+len(slice), totalSize)
	return header + slice, false
}

func (s *chatServiceImpl) handleSearchToolResult(sessionID string, input map[string]any) (string, bool) {
	id, _ := input["id"].(string)
	pattern, _ := input["pattern"].(string)
	if id == "" || pattern == "" {
		return "Error: 'id' and 'pattern' are required", true
	}
	maxMatches := 20
	if v, ok := input["max_matches"].(float64); ok && v > 0 {
		maxMatches = int(v)
	}

	matches, err := s.resultCache.Search(sessionID, id, pattern, maxMatches)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	if len(matches) == 0 {
		return fmt.Sprintf("No matches found for pattern %q in cached result %s", pattern, id), false
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d match(es) for %q in cached result %s:\n\n", len(matches), pattern, id))
	for i, m := range matches {
		sb.WriteString(fmt.Sprintf("--- Match %d (line %d) ---\n%s\n\n", i+1, m.LineStart, m.Context))
	}
	return sb.String(), false
}
