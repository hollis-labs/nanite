package selftools

// task_update_report reports an opaque update to operator-configured reactions.
// It records no independent task state and installs no default reactions.

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/selftools/reactions"
)

// taskUpdateReportToolName is shared by the definition and dispatch handler.
const taskUpdateReportToolName = "task_update_report"

// taskUpdateReportToolDefinition declares task_update_report per the
// design doc's Definition section: id/msg both required strings,
// description in the existing When-to-use / When-NOT-to-use /
// Required-context / Output-shape house style every current self-tool
// description uses.
func taskUpdateReportToolDefinition() mcp.Tool {
	return mcp.Tool{
		Name: taskUpdateReportToolName,
		Description: "Declare a task-update fact and let the harness decide how to react through operator-configured reactions. This tool has no independent persisted state.\n\n" +
			"**When to use:** When you want to report that some task's status or progress changed and let pre-configured, operator-defined reactions (a rendered card, an internal API call, both, or neither) decide what surfaces — not when you need to control exactly what happens next yourself.\n\n" +
			"**When NOT to use:** This does NOT create, update, or query Nanite's own todo/plan store — use todo_update or plan_update for that. Do not rely on this for durable state: task_update_report persists nothing on its own; every observable effect comes entirely from however this tool's reactions happen to be configured (which may be none at all).\n\n" +
			"**Required context:** `id` (an opaque identifier — you don't need to know what it maps to; it is threaded through verbatim to any configured reaction, never interpreted by the harness) and `msg` (a short, human-readable update).\n\n" +
			"**Output shape:** A short text confirmation. When a render_card reaction is configured and resolves, an <!--ENVELOPE_DATA:...--> marker is appended so the update also surfaces as a card.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Opaque task identifier. Threaded through to reaction configs verbatim — the harness never interprets or validates it against any store.",
				},
				"msg": map[string]any{
					"type":        "string",
					"description": "Human-readable update text.",
				},
			},
			"required": []string{"id", "msg"},
		},
	}
}

// callTaskUpdateReport is task_update_report's handler — deliberately
// thin per the design doc's "Handler's own work is deliberately thin"
// section: validate id/msg, call reactions.Fire, embed the render_card
// marker if that reaction resolved, call EmitReactionTrace for full
// telemetry coverage, return a short confirmation. Every side effect
// worth having happens through the reaction layer, not here.
func (st *SelfToolsTransport) callTaskUpdateReport(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	id := strArg(args, "id", "")
	msg := strArg(args, "msg", "")
	if id == "" || msg == "" {
		return mcp.ErrorResult("id and msg are required"), nil
	}

	confirmation := fmt.Sprintf("Reported task update for %q: %s", id, msg)

	if st.Reactions == nil {
		// Reaction engine not wired (e.g. a bare SelfToolsTransport built
		// directly in a test, without main.go's post-construction
		// wiring) — the call is still valid; it simply has no
		// declarative side effects available to fire. Nil-safe, matching
		// every other optional field on this struct.
		return mcp.TextResult(confirmation), nil
	}

	payload := map[string]any{"id": id, "msg": msg}
	result, fireErr := st.Reactions.Fire(ctx, taskUpdateReportToolName, payload)
	if fireErr != nil {
		// Fire only returns a non-nil error when it can't even enumerate
		// the tool's configured reactions (a store error) — never for an
		// individual reaction's own outcome, which is always recorded
		// per-entry in result.Reactions instead. Surface it as a clear
		// tool error rather than silently dropping it.
		return mcp.ErrorResult(fmt.Sprintf("task_update_report: fire reactions: %v", fireErr)), nil
	}

	// Full telemetry coverage (TASKS/harness-reactive-self-tools/
	// 05-selftool-reaction-telemetry.md) — one event_log row per
	// attempted reaction, success or not, regardless of how many (zero,
	// one, or both) actually fired. toolCallID is passed empty: no
	// per-call tool_use_id is threaded into ctx at the self-tool-handler
	// layer today (mcp.tool_ctx.go only stamps the turn-level aggregate
	// via WithTurnToolUseIDs, not a single current-call ID) — a
	// deliberate, documented gap left for future work, the same posture
	// 05's own Work Log already took for EmitReactionTrace's session_id
	// (see this task's own Work Log for the full reasoning).
	if traceErr := reactions.EmitReactionTrace(ctx, st.Writes.Events, "", result); traceErr != nil {
		slog.Warn("task_update_report: emit reaction trace failed", "err", traceErr)
	}

	text := confirmation
	if cardPayload, ok := result.RenderCardPayload(); ok {
		text = EmbedRenderCardMarker(confirmation, string(cardPayload))
	}
	return mcp.TextResult(text), nil
}
