package selftools

// task_update_report is the worked example built by TASKS/harness-
// reactive-self-tools/07-worked-example-task-update-report.md, proving
// out the harness-reactive self-tool shape docs/engineering/architecture/
// 11-harness-reactive-self-tools.md designs ("Worked example:
// task_update_report(id, msg)"): a self-tool with no independent
// persisted state of its own, whose entire observable effect comes from
// whatever selftool_reactions rows are configured for it. Kept
// illustrative, not bound to a real consumer (Nanite's own todo/plan
// store or otherwise) — the point is proving the mechanism is buildable,
// not shipping a specific integration (see this file's own seed
// function's doc comment, and this task's Work Log, for the full
// reasoning).
//
// Renamed from the design doc's own original draft name
// (nanite_report_task_updated) to task_update_report during the design
// session, to match docs/tool-naming-convention.md's <concept>_<verb>
// convention every other self-tool already follows — not re-litigated
// here, per this task file's own instruction.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/selftools/reactions"
	"github.com/hollis-labs/nanite/internal/store"
)

// taskUpdateReportToolName is task_update_report's own registered name —
// shared between the tool definition, the CallTool dispatch case, and
// the seed function below so all three stay in lock-step by construction
// rather than by three independently-typed string literals.
const taskUpdateReportToolName = "task_update_report"

// taskUpdateReportExampleEndpoint is the illustrative internal_api_call
// reaction target this task seeds — a trivial demo/test fixture
// (internal/api/example_task_updates.go) that accepts the POST and
// returns 200, persisting nothing real. Not a real consumer; see this
// file's own seed function doc comment and this task's Work Log for the
// example-endpoint-vs-httptest call.
const taskUpdateReportExampleEndpoint = "/api/example/task-updates"

// taskUpdateReportToolDefinition declares task_update_report per the
// design doc's Definition section: id/msg both required strings,
// description in the existing When-to-use / When-NOT-to-use /
// Required-context / Output-shape house style every current self-tool
// description uses.
func taskUpdateReportToolDefinition() mcp.Tool {
	return mcp.Tool{
		Name: taskUpdateReportToolName,
		Description: "Declare a task-update fact and let the harness decide how to react — a purely declarative worked example of a harness-reactive self-tool (docs/engineering/architecture/11-harness-reactive-self-tools.md) with NO independent persisted state of its own.\n\n" +
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
	if traceErr := reactions.EmitReactionTrace(ctx, st.Store, "", result); traceErr != nil {
		slog.Warn("task_update_report: emit reaction trace failed", "err", traceErr)
	}

	text := confirmation
	if cardPayload, ok := result.RenderCardPayload(); ok {
		text = EmbedRenderCardMarker(confirmation, string(cardPayload))
	}
	return mcp.TextResult(text), nil
}

// SeedTaskUpdateReportReactions inserts task_update_report's two
// illustrative selftool_reactions rows — one render_card, one
// internal_api_call, both enabled — per docs/engineering/architecture/
// 11-harness-reactive-self-tools.md's worked-example section, exactly:
//
//   - render_card: {envelope_type: "info-card", template: {title: "Task
//     update", body: "{{msg}}"}} — exercises the "engine resolves, caller
//     surfaces" path.
//   - internal_api_call: {endpoint: <apiBaseURL>/api/example/task-updates,
//     method: "POST", body_template: {id: "{{id}}", msg: "{{msg}}"}} —
//     exercises the "engine executes directly" path.
//
// Idempotent across repeat boots: mirrors internal/agent/reflexes/
// seeds.go's SeedBaseReflexes pattern exactly — a row that already
// exists (by tool_name + reaction_kind_id, regardless of its current
// enabled state) is left alone, so an operator's own mutation (disabling
// or reconfiguring a seeded row) survives the next boot instead of being
// silently re-inserted or overwritten. Returns the count of rows
// actually inserted this call (0 on every boot after the first).
//
// Go-side seed function, not a migration-time INSERT — the documented
// choice this task's own Work Log records the reasoning for: the
// internal_api_call reaction's endpoint must be a fully-qualified,
// same-process URL (internal/selftools/reactions/internal_api_call.go's
// own doc comment: "resolving endpoint into a full URL is the seeding
// caller's responsibility"), and that URL depends on apiBaseURL, a
// runtime value (this process's own resolved listen address,
// cmd/nanite/main.go's apiBaseURL) that a migration running at DB-open
// time has no way to know. A Go-side seed run at boot — after apiBaseURL
// is resolved, mirroring reflexes.SeedBaseReflexes's own call site in
// internal/service/container.go — is the only place both facts (the
// seed shape AND the real listen address) are simultaneously available.
func SeedTaskUpdateReportReactions(ctx context.Context, st *store.Store, apiBaseURL string, logger *slog.Logger) (int, error) {
	if logger == nil {
		logger = slog.Default()
	}
	inserted := 0

	renderCardConfig, err := json.Marshal(map[string]any{
		"envelope_type": "info-card",
		"template": map[string]any{
			"title": "Task update",
			"body":  "{{msg}}",
		},
	})
	if err != nil {
		return inserted, fmt.Errorf("seed task_update_report: marshal render_card config: %w", err)
	}

	internalAPICallConfig, err := json.Marshal(map[string]any{
		"endpoint": strings.TrimRight(apiBaseURL, "/") + taskUpdateReportExampleEndpoint,
		"method":   "POST",
		"body_template": map[string]any{
			"id":  "{{id}}",
			"msg": "{{msg}}",
		},
	})
	if err != nil {
		return inserted, fmt.Errorf("seed task_update_report: marshal internal_api_call config: %w", err)
	}

	seeds := []store.SelftoolReaction{
		{
			ToolName:       taskUpdateReportToolName,
			ReactionKindID: reactions.KindRenderCard,
			Config:         string(renderCardConfig),
			Enabled:        true,
		},
		{
			ToolName:       taskUpdateReportToolName,
			ReactionKindID: reactions.KindInternalAPICall,
			Config:         string(internalAPICallConfig),
			Enabled:        true,
		},
	}

	for _, seed := range seeds {
		n, countErr := st.CountSelftoolReactionsByToolAndKind(ctx, seed.ToolName, seed.ReactionKindID)
		if countErr != nil {
			logger.Warn("seed task_update_report: count failed", "kind", seed.ReactionKindID, "err", countErr)
			continue
		}
		if n > 0 {
			continue
		}
		if insertErr := st.InsertSelftoolReaction(ctx, seed); insertErr != nil {
			return inserted, fmt.Errorf("seed task_update_report reaction %s: %w", seed.ReactionKindID, insertErr)
		}
		inserted++
	}

	return inserted, nil
}
