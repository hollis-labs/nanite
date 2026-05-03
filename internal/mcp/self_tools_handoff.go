package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/store"
)

// handoffStashToolDefinition returns the nanite_handoff_stash self-tool —
// the agent-facing primitive for self-authored continuity (Glass-4,
// CW-20260502-0015, SP-20260502-0001).
//
// Mental model for the agent: "compaction is amnesia; with handoff, it's
// sleep". The agent decides what survives by writing a structured payload;
// the harness preserves it across compaction and re-injects it post.
func handoffStashToolDefinition() Tool {
	return Tool{
		Name: "nanite_handoff_stash",
		Description: "Write a self-authored handoff that survives compaction. Use proactively at natural checkpoints in long-running sessions — not only at compaction time.\n\n" +
			"**When to use:** After locking a decision, completing a phase, or whenever you'd want a future-self version of you (post-compaction) to pick up coherently. Aim for one stash per natural checkpoint, not per turn.\n\n" +
			"**Schema:**\n" +
			"- `session_intent` (REQUIRED, ≤200 tokens): one-line summary of what this session is doing\n" +
			"- `next_step_anchor` (REQUIRED, ≤100 tokens): the next concrete action / question, so post-compaction you don't have to re-derive it\n" +
			"- `recent_decisions` (optional, ≤3 items × ≤100 tokens): durable choices, not micro-tactics\n" +
			"- `active_pointers` (optional, ≤5 items): label + purpose + cache_key for heavy artifacts you want to remember the existence of without paying tokens for them now\n\n" +
			"**Total cap:** 1500 tokens across all fields. Validation rejects oversize payloads — keep it lean.\n\n" +
			"**Output:** `{cache_key: \"...\", validated: true}` on success. The cache_key is what post-compaction code uses to retrieve the payload; you can also reference it from `active_pointers` in a future stash.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID this handoff belongs to. Use the current session ID.",
				},
				"session_intent": map[string]any{
					"type":        "string",
					"description": "One-line summary of session intent (≤200 tokens).",
				},
				"next_step_anchor": map[string]any{
					"type":        "string",
					"description": "Next concrete action / question (≤100 tokens).",
				},
				"recent_decisions": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Up to 3 durable decisions, each ≤100 tokens.",
				},
				"active_pointers": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"cache_key": map[string]any{"type": "string"},
							"label":     map[string]any{"type": "string"},
							"purpose":   map[string]any{"type": "string"},
						},
						"required": []string{"label", "purpose"},
					},
					"description": "Up to 5 pointers to harness-cached artifacts (label + purpose + optional cache_key).",
				},
			},
			"required": []string{"session_id", "session_intent", "next_step_anchor"},
		},
	}
}

// handoffPointersExpandToolDefinition returns the nanite_handoff_pointers_expand
// self-tool. Used by the post-compaction agent to retrieve the full payload
// of a stashed handoff via its cache_key (Glass-4, CW-20260502-0015).
func handoffPointersExpandToolDefinition() Tool {
	return Tool{
		Name: "nanite_handoff_pointers_expand",
		Description: "Retrieve the full payload of a previously-stashed handoff by cache_key.\n\n" +
			"**When to use:** When the SlotHandoff content references an `active_pointers` entry and you need the full body the pointer summarized.\n\n" +
			"**Output:** The decoded handoff payload (`{session_intent, next_step_anchor, recent_decisions, active_pointers}`).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID the cache_key belongs to.",
				},
				"cache_key": map[string]any{
					"type":        "string",
					"description": "The cache_key returned by a prior nanite_handoff_stash call.",
				},
			},
			"required": []string{"session_id", "cache_key"},
		},
	}
}

// callHandoffStash dispatches nanite_handoff_stash. Validates the input
// against ctxpkg.ValidateHandoff (caps, required fields, total budget),
// persists it as a Glass-4 envelope in handoff_stashes, and returns
// `{cache_key, validated}` for the agent to record.
func (st *SelfToolsTransport) callHandoffStash(_ context.Context, args map[string]any) (*ToolResult, error) {
	sessionID := strArg(args, "session_id", "")
	if sessionID == "" {
		return errorResult("session_id is required"), nil
	}

	payload := ctxpkg.HandoffPayload{
		SessionIntent:  strArg(args, "session_intent", ""),
		NextStepAnchor: strArg(args, "next_step_anchor", ""),
	}

	if rd, ok := args["recent_decisions"].([]any); ok {
		for _, item := range rd {
			if s, ok := item.(string); ok {
				payload.RecentDecisions = append(payload.RecentDecisions, s)
			}
		}
	}
	if ap, ok := args["active_pointers"].([]any); ok {
		for _, item := range ap {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			payload.ActivePointers = append(payload.ActivePointers, ctxpkg.HandoffPointer{
				CacheKey: strArg(obj, "cache_key", ""),
				Label:    strArg(obj, "label", ""),
				Purpose:  strArg(obj, "purpose", ""),
			})
		}
	}

	// MarshalHandoffEnvelope round-trips through ValidateHandoff — caps and
	// required fields are enforced before any DB write. Sentinel errors
	// (ErrHandoffMissingField / ErrHandoffOversize / ErrHandoffMalformed)
	// flow back as the tool error message verbatim so the agent can
	// self-correct without guessing what failed.
	envelopeBytes, err := ctxpkg.MarshalHandoffEnvelope(payload)
	if err != nil {
		return errorResult(fmt.Sprintf("handoff_stash: %v", err)), nil
	}
	stashID := uuid.New().String()
	if err := st.Store.UpsertHandoffStash(store.HandoffStash{
		ID:        stashID,
		SessionID: sessionID,
		Payload:   string(envelopeBytes),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return errorResult(fmt.Sprintf("handoff_stash: %v", err)), nil
	}

	resp := struct {
		CacheKey  string `json:"cache_key"`
		Validated bool   `json:"validated"`
	}{
		CacheKey:  stashID,
		Validated: true,
	}
	body, err := json.Marshal(resp)
	if err != nil {
		return errorResult(fmt.Sprintf("handoff_stash: marshal: %v", err)), nil
	}
	return textResult(string(body)), nil
}

// callHandoffPointersExpand dispatches nanite_handoff_pointers_expand.
// Returns the full HandoffPayload bytes for the (session_id, cache_key) pair.
func (st *SelfToolsTransport) callHandoffPointersExpand(_ context.Context, args map[string]any) (*ToolResult, error) {
	sessionID := strArg(args, "session_id", "")
	cacheKey := strArg(args, "cache_key", "")
	if sessionID == "" {
		return errorResult("session_id is required"), nil
	}
	if cacheKey == "" {
		return errorResult("cache_key is required"), nil
	}

	row, err := st.Store.GetHandoffStash(sessionID, cacheKey)
	if err != nil {
		return errorResult(fmt.Sprintf("handoff_pointers_expand: %v", err)), nil
	}
	// Try Glass-4 envelope first; fall back to returning the raw payload
	// (covers legacy P7 rows the agent might reference for some reason).
	if payload, perr := ctxpkg.ParseHandoffEnvelope([]byte(row.Payload)); perr == nil {
		body, mErr := json.Marshal(payload)
		if mErr != nil {
			return errorResult(fmt.Sprintf("handoff_pointers_expand: marshal: %v", mErr)), nil
		}
		return textResult(string(body)), nil
	}
	return textResult(row.Payload), nil
}
