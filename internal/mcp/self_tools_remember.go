package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/hollis-labs/nanite/internal/learnings"
)

// rememberToolDefinition returns the self-tool definition for
// nanite_remember (CW-20260429-0009, D1). Layer 4 of the self-healing
// tool surface — the agent calls this with the lesson_hint from a
// repair_note (or any other in-session insight) so future-self can
// recall it via the lesson-recall slot extension.
//
// The signature mirrors the other layer self-tools (nanite_validate,
// nanite_tool_describe): tight inputs, explicit when-to-use guidance
// in the description, output shape spelled out so the agent can build
// follow-up tool calls without an extra describe round-trip.
func rememberToolDefinition() Tool {
	return Tool{
		Name: "nanite_remember",
		Description: "Persist a one-sentence lesson to durable memory so future sessions surface it on similar tool selection. Layer 4 of the self-healing tool surface (CW-20260429-0009).\n\n" +
			"**When to use:** When you receive a `repair_note` on a tool result — call this with the `lesson_hint` so the same reshape isn't needed next time. Also fine for any high-signal in-session insight worth carrying forward (a project convention you discovered, a session-specific user preference). Cheap, idempotent on the (scope, subject, hint) triple — re-writing the same lesson updates the existing entry rather than creating duplicates.\n\n" +
			"**When NOT to use:** Don't capture conversational chatter, partial guesses, or things you'd be embarrassed to read back to the user. Confidence is stamped at 0.85 — these surface in future agent context, so noise here directly degrades future grounding.\n\n" +
			"**Scopes:**\n" +
			"- `tool_use` (most common): a lesson about how to call a specific tool. `subject` MUST be the tool name (e.g. \"nanite_show_card\"). Surfaced when that tool is considered in a future session.\n" +
			"- `project`: a lesson scoped to a project. `subject` is the project_id.\n" +
			"- `session`: a lesson scoped to a single chat session. `subject` is the session_id.\n\n" +
			"**Output shape:** `{memory_id: string, namespace: string}`. Echo neither back to the user — they're for downstream tool calls or telemetry.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"scope": map[string]any{
					"type":        "string",
					"description": "One of `tool_use`, `project`, `session`. See description for which scope to pick.",
					"enum":        []string{"tool_use", "project", "session"},
				},
				"subject": map[string]any{
					"type":        "string",
					"description": "Subject identifier — tool name for `tool_use`, project_id for `project`, session_id for `session`. Required: a learning without a subject can't be recalled by the layer that consumes it.",
				},
				"hint": map[string]any{
					"type":        "string",
					"description": "One short sentence describing the lesson, phrased for future-you. From a repair_note this is `repair_note.lesson_hint`. Keep it shape-focused (e.g. \"report-card requires {title, metrics}; sections are not allowed\") rather than narrative.",
				},
				"source_event_id": map[string]any{
					"type":        "string",
					"description": "Optional. The event/turn/tool_use_id that triggered the learning. Lands in tags as `source:<id>` so a future review tool can join back to the originating turn.",
				},
				"tags": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional extra tags to merge with the canonical set ([\"learning\", \"self_healed\", \"captured_during_session\", scope, tool:<tool>?, source:<id>?]).",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Optional. Originating chat session ID stamped on the memory revision; the underlying memory store needs a non-empty value, so we fall back to `manual:nanite` when omitted.",
				},
				"user_id": map[string]any{
					"type":        "string",
					"description": "Optional. User namespace root. Defaults to `default` for single-user dogfood.",
				},
			},
			"required": []string{"scope", "subject", "hint"},
		},
	}
}

// rememberCounters holds the per-session telemetry counters required by
// the ticket ("Count nanite_remember calls by scope per session").
// Atomic so the handler can be called from multiple goroutines without
// a lock around the increment.
type rememberCounters struct {
	toolUse atomic.Int64
	project atomic.Int64
	session atomic.Int64
}

// rememberSessionCounters tracks counters per session_id. The map is
// guarded by a sync.Mutex because the handler grows it on first use of
// a new session. Bounded eviction is out of scope — counters are tiny
// and a long-running daemon's working set of sessions stays modest.
type rememberSessionCounters struct {
	mu       sync.Mutex
	bySession map[string]*rememberCounters
}

func newRememberSessionCounters() *rememberSessionCounters {
	return &rememberSessionCounters{bySession: make(map[string]*rememberCounters)}
}

func (c *rememberSessionCounters) bump(sessionID string, scope learnings.Scope) {
	if c == nil {
		return
	}
	if sessionID == "" {
		sessionID = "manual:nanite"
	}
	c.mu.Lock()
	cnt, ok := c.bySession[sessionID]
	if !ok {
		cnt = &rememberCounters{}
		c.bySession[sessionID] = cnt
	}
	c.mu.Unlock()
	switch scope {
	case learnings.ScopeToolUse:
		cnt.toolUse.Add(1)
	case learnings.ScopeProject:
		cnt.project.Add(1)
	case learnings.ScopeSession:
		cnt.session.Add(1)
	}
}

// SnapshotSession returns a snapshot of the counters for sessionID.
// Returns zero values when no entries have been captured for that
// session yet. Exposed so a future inspector or telemetry pipe can
// read counters without poking at unexported state.
func (c *rememberSessionCounters) SnapshotSession(sessionID string) (toolUse, project, session int64) {
	if c == nil {
		return 0, 0, 0
	}
	c.mu.Lock()
	cnt, ok := c.bySession[sessionID]
	c.mu.Unlock()
	if !ok {
		return 0, 0, 0
	}
	return cnt.toolUse.Load(), cnt.project.Load(), cnt.session.Load()
}

// callRemember handles nanite_remember. Translates the JSON args into a
// learnings.CaptureInput, invokes the Recorder, and returns the
// resulting CaptureOutcome as the tool result. Failures surface as
// structured errorResult — never a Go error — so the agent sees a
// uniform shape regardless of what went wrong.
//
// When the LearningRecorder is unwired (production misconfig or tests
// that didn't bother) the call returns a clear errorResult rather than
// silently no-oping; the latter would let bad lessons pile up
// undiagnosed.
func (st *SelfToolsTransport) callRemember(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.LearningRecorder == nil {
		return errorResult("nanite_remember: LearningRecorder is not configured (memory service unavailable)"), nil
	}

	scopeStr := strArg(args, "scope", "")
	scope := learnings.Scope(scopeStr)
	if !scope.IsValid() {
		return errorResult(fmt.Sprintf("nanite_remember: invalid scope %q (want tool_use|project|session)", scopeStr)), nil
	}
	subject := strings.TrimSpace(strArg(args, "subject", ""))
	if subject == "" {
		return errorResult("nanite_remember: subject is required (tool name / project_id / session_id)"), nil
	}
	hint := strings.TrimSpace(strArg(args, "hint", ""))
	if hint == "" {
		return errorResult("nanite_remember: hint is required and must be a non-empty string"), nil
	}

	// Optional fields.
	sourceEventID := strArg(args, "source_event_id", "")
	sessionID := strArg(args, "session_id", "")
	userID := strArg(args, "user_id", "")

	var extraTags []string
	if raw, ok := args["tags"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				extraTags = append(extraTags, s)
			}
		}
	}

	out, err := st.LearningRecorder.Capture(ctx, learnings.CaptureInput{
		Scope:         scope,
		Subject:       subject,
		Hint:          hint,
		SourceEventID: sourceEventID,
		SessionID:     sessionID,
		UserID:        userID,
		Tags:          extraTags,
	})
	if err != nil {
		return errorResult(fmt.Sprintf("nanite_remember: %v", err)), nil
	}

	// Telemetry: count by scope per session. Best-effort — never blocks
	// the response path. Empty session_id falls back to "manual:nanite"
	// (mirrors the memory.Service fallback).
	if st.RememberCounters != nil {
		st.RememberCounters.bump(sessionID, scope)
	}
	slog.Info("learnings: captured",
		"scope", scope,
		"subject", subject,
		"namespace", out.Namespace,
		"memory_id", out.MemoryID,
		"session_id", sessionID,
	)

	body, err := json.Marshal(out)
	if err != nil {
		// Should be impossible — CaptureOutcome is two strings.
		return errorResult(fmt.Sprintf("nanite_remember: marshal result: %v", err)), nil
	}
	return textResult(string(body)), nil
}
