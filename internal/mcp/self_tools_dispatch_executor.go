package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/envelope"
)

// dispatchExecutorToolDefinition returns the dispatch_executor self-tool —
// the chat-agent-facing destination for the executor-handoff capability bullet
// B5 added to the chat prompt (CW-20260429-0036, B2 closing piece for sprint
// SP-20260429-0001 Phase B).
//
// v1 supports intent="render_envelope" only; routes to the B3 in-process
// envelope_render executor (internal/executor/envelope_render). Future
// LLM-driven executor profiles register additional intents per
// docs/architecture/executor-handoff.md §6 Phase 3 — the chat-facing surface
// of dispatch_executor stays the same.
//
// Tool description budget per B1 §"Tool description vs prompt": ≤250 tokens,
// verb-led, generic phrasing, no embedded HomeDir.
func dispatchExecutorToolDefinition() Tool {
	return Tool{
		Name: "dispatch_executor",
		Description: "Hand off a multi-step structured intent to a specialized executor. Use when you need to render a typed envelope card and you have the data ready — the executor validates, repairs known shape mistakes, and returns the envelope plus a summary, so you don't run the describe/validate/emit loop yourself.\n\n" +
			"**When to use:** Producing a v1 passive-renderable envelope (report-card, info-card, list-card, metric-card, progress-card, table-card, timeline-card, diff-card, document-viewer, giphy-modal, artifact-mini). The executor owns the multi-step recovery loop.\n\n" +
			"**When NOT to use:** Single conversational replies, decision-flow envelopes (approval-card, question-form, etc.), or one-off read tools — those stay chat-direct.\n\n" +
			"**Required inputs:** intent (\"render_envelope\" today), target_envelope_type, user_request (verbatim), and either data (the resolved payload — fast path for the in-process pilot) or context_handles (forward-compat for future LLM-driven executors).\n\n" +
			"**Grounded types:** report-card and document-viewer also require sources (each with tool_use_id or tool_name) — the executor refuses to render grounded cards without them. For demo / test / sketch requests, set synthetic_allowed=true and the executor will disclose the synthesis in its summary.\n\n" +
			"**Output shape:** {envelope, summary} on success; {failure: {code, message, partial_envelope?}, summary} on failure. Failure codes: invalid_intent, missing_context, unrecoverable, budget_exhausted, trust_denied. Surface failures to the user; do NOT auto-retry (one exception: missing_context after the user supplies the missing piece).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"intent": map[string]any{
					"type":        "string",
					"description": "Executor intent. v1 vocabulary: \"render_envelope\". Unknown intents return invalid_intent.",
				},
				"target_envelope_type": map[string]any{
					"type": "string",
					"description": "For intent=\"render_envelope\": one of " +
						strings.Join(envelope.PassiveRenderableTypes, " | ") + ".",
				},
				"user_request": map[string]any{
					"type":        "string",
					"description": "The user's actual ask, passed verbatim to the executor for grounding. Do NOT paraphrase.",
				},
				"data": map[string]any{
					"type":                 "object",
					"additionalProperties": true,
					"description":          "The resolved envelope payload. Required by the in-process pilot for intent=\"render_envelope\" — schema-validated against the target_envelope_type. Future LLM-driven executors may resolve from context_handles instead.",
				},
				"sources": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"tool_use_id": map[string]any{"type": "string", "description": "ID of the tool call whose result grounds this citation."},
							"tool_name":   map[string]any{"type": "string", "description": "Name of the tool whose result grounds this citation."},
							"note":        map[string]any{"type": "string", "description": "Optional one-line note about what this source contributes."},
						},
					},
					"description": "Grounding citations. Required for grounded types (report-card, document-viewer); each entry must include tool_use_id or tool_name.",
				},
				"context_handles": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"source": map[string]any{"type": "string", "description": "One of \"scratchpad\", \"memory\", \"message_window\"."},
							"key":    map[string]any{"type": "string", "description": "Source-specific identifier."},
						},
						"required": []string{"source", "key"},
					},
					"description": "Forward-compat: handles the executor uses to fetch additional context. The in-process pilot does not resolve them — pass through unchanged for future LLM-driven executors.",
				},
				"synthetic_allowed": map[string]any{
					"type":        "boolean",
					"description": "Set true when the user's request is recognizably synthetic (\"demo\", \"test\", \"sketch\", \"example\") so the executor may synthesize realistic placeholder content with a disclosure in the response summary. Default false — the executor must ground in real data or fail with missing_context.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Dispatching session ID. Used for path-grant lineage (B1 §4). Optional today — the in-process pilot inherits the calling context naturally; populated for forward-compat with future agent-spawning executors.",
				},
			},
			"required": []string{"intent", "user_request"},
		},
	}
}

// callDispatchExecutor handles dispatch_executor — the chat-agent-facing
// destination for the executor-handoff (CW-20260429-0036, B2 closing piece).
// Translates the tool args into a dispatch.ExecutorRequest, dispatches via
// dispatch.DispatchExecutor (which handles unknown-intent routing for any
// intent the wired executor doesn't recognize), and returns the typed
// ExecutorResponse as JSON.
//
// Failure-handling contract per B1 §5:
//   - Validation misses on the tool args themselves return errorResult with
//     IsError=true so the chat agent's normal recoverable-error path picks
//     them up (not a typed ExecutorFailure — those describe executor-side
//     failures, not malformed dispatch calls).
//   - Executor-side failures (invalid_intent, missing_context,
//     unrecoverable, budget_exhausted, trust_denied) ride in the response
//     JSON as resp.Failure; the tool result itself is non-error so the chat
//     agent narrates the failure to the user without auto-retrying.
//   - Harness/wiring problems (nil Executor, marshal failure) return
//     errorResult so a misconfiguration is visible at the call site.
func (st *SelfToolsTransport) callDispatchExecutor(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.Executor == nil {
		return errorResult("dispatch_executor: executor is not configured (envelope_render not wired)"), nil
	}

	intent := strings.TrimSpace(strArg(args, "intent", ""))
	userRequest := strings.TrimSpace(strArg(args, "user_request", ""))
	if intent == "" {
		return errorResult("dispatch_executor: intent is required"), nil
	}
	if userRequest == "" {
		return errorResult("dispatch_executor: user_request is required (pass the user's verbatim ask)"), nil
	}

	syntheticAllowed, _ := args["synthetic_allowed"].(bool)
	req := dispatch.ExecutorRequest{
		Intent:             intent,
		TargetEnvelopeType: strings.TrimSpace(strArg(args, "target_envelope_type", "")),
		UserRequest:        userRequest,
		SessionID:          strings.TrimSpace(strArg(args, "session_id", "")),
		SyntheticAllowed:   syntheticAllowed,
	}

	if data, ok := args["data"].(map[string]any); ok {
		req.Data = data
	}
	if sources := parseExecutorSources(args["sources"]); len(sources) > 0 {
		req.Sources = sources
	}
	if handles := parseContextHandles(args["context_handles"]); len(handles) > 0 {
		req.ContextHandles = handles
	}

	resp, err := dispatch.DispatchExecutor(ctx, st.Executor, req)
	if err != nil {
		// dispatch.DispatchExecutor reserves errors for harness-level
		// problems (nil Executor — already guarded above — plus whatever
		// the wrapped Executor.Execute returns, which is reserved for
		// wiring failures per its own contract). Surface as errorResult so
		// the chat agent sees a recoverable error rather than a silently
		// dropped dispatch.
		return errorResult(fmt.Sprintf("dispatch_executor: %v", err)), nil
	}
	if resp == nil {
		// Defensive — dispatch.DispatchExecutor's documented contract is
		// non-nil response in the common case. A nil here is a wiring
		// regression worth surfacing rather than papering over.
		return errorResult("dispatch_executor: nil response from executor (wiring regression)"), nil
	}

	body, err := json.Marshal(resp)
	if err != nil {
		return errorResult(fmt.Sprintf("dispatch_executor: marshal response: %v", err)), nil
	}
	return textResult(string(body)), nil
}

// parseExecutorSources converts the raw `sources` arg (an array of objects)
// into typed []dispatch.ExecutorSource. Per-entry validation lives in the
// executor itself (envelope_render rejects entries missing both
// tool_use_id and tool_name), so this helper keeps the conversion lossless
// and lets the executor produce the structured failure message.
func parseExecutorSources(raw any) []dispatch.ExecutorSource {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	out := make([]dispatch.ExecutorSource, 0, len(list))
	for _, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, dispatch.ExecutorSource{
			ToolUseID: strArg(obj, "tool_use_id", ""),
			ToolName:  strArg(obj, "tool_name", ""),
			Note:      strArg(obj, "note", ""),
		})
	}
	return out
}

// parseContextHandles converts the raw `context_handles` arg into typed
// []dispatch.ContextHandle. Both fields (source, key) are required by the
// schema; entries missing either are dropped silently — the executor sees
// only well-formed handles and the dispatching agent gets the schema-level
// validation feedback for malformed entries via the model layer's pre-call
// validation.
func parseContextHandles(raw any) []dispatch.ContextHandle {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	out := make([]dispatch.ContextHandle, 0, len(list))
	for _, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		source := strings.TrimSpace(strArg(obj, "source", ""))
		key := strings.TrimSpace(strArg(obj, "key", ""))
		if source == "" || key == "" {
			continue
		}
		out = append(out, dispatch.ContextHandle{Source: source, Key: key})
	}
	return out
}
