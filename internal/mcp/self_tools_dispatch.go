package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/reflex"
)

// callExecuteTask handles nanite_execute_task — the Chat agent's
// dispatch primitive (CW-20260421-0010, B3). Translates the tool args
// into dispatch.ExecuteTaskArgs, invokes the dispatch primitive, and
// returns the resulting envelope JSON as the tool result.
//
// The Chat agent receives the envelope (via the tool result) and
// relays it to the frontend. Raw worker output never enters the Chat
// agent's context window — only the structured envelope does.
//
// E1 integration (CW-20260419-0027): before calling dispatch.ExecuteTask
// this function runs the reflex matcher over the message. A matched reflex
// injects ReflexHints into ExecuteTaskArgs so the dispatch layer uses the
// reflex's ScopeTier/ExecutionPattern/AgentSlug hints instead of the raw
// classifier output. On a miss the dispatch path is unchanged.
func (st *SelfToolsTransport) callExecuteTask(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.Dispatch == nil {
		return errorResult("dispatch is not configured (subagent service unavailable)"), nil
	}
	wrapper := st.DispatchWrapper
	if wrapper == nil {
		// Default to the package-provided wrapper so production wiring
		// gets a working envelope path even if DispatchWrapper was not
		// explicitly set.
		wrapper = dispatch.DefaultEnvelopeWrapper{}
	}

	sessionID := strArg(args, "session_id", "")
	message := strArg(args, "message", "")
	if sessionID == "" {
		return errorResult("session_id is required"), nil
	}
	if message == "" {
		return errorResult("message is required"), nil
	}

	// E1: Run reflex matcher before dispatch classification.
	// Classify the message to get M1 priors, then let the reflex matcher
	// override them when a phrase-match fires.
	var reflexHints *dispatch.ReflexHints
	if st.ReflexSet != nil {
		m1Tier, m1Pattern := classify.Classify(classify.IntentSignals{
			Message:         message,
			MessageTokenEst: len(message) / 4,
		})
		if match, ok := reflex.Match(message, m1Tier, m1Pattern, st.ReflexSet); ok {
			reflexHints = &dispatch.ReflexHints{
				HintTier:    match.HintTier,
				HintPattern: match.HintPattern,
				AgentSlug:   match.Reflex.ResolvesTo.Profile,
				Mode:        modeFromDispatchVia(match.Reflex.SideEffects.DispatchVia),
				ReflexID:    match.Reflex.ID,
			}
			// Log the match; errors are swallowed (never block dispatch).
			if st.ReflexLogger != nil {
				excerpt := message
				if len(excerpt) > 200 {
					excerpt = excerpt[:200]
				}
				_ = st.ReflexLogger.LogReflexMatch(reflex.ReflexMatchEntry{
					SessionID:           sessionID,
					TurnID:              strArg(args, "turn_id", ""),
					ReflexID:            match.Reflex.ID,
					Priority:            match.Reflex.Priority,
					Source:              "reflex",
					MatchedInputExcerpt: excerpt,
					HintTier:            match.HintTier.String(),
					HintPattern:         match.HintPattern.String(),
					ProfileSlug:         match.Reflex.ResolvesTo.Profile,
					Mode:                match.Reflex.SideEffects.ModeSignal,
				})
			}
		}
	}

	envelope, err := dispatch.ExecuteTask(ctx, st.Dispatch, wrapper, dispatch.ExecuteTaskArgs{
		SessionID:      sessionID,
		ParentAgentID:  strArg(args, "parent_agent_id", ""),
		Message:        message,
		Provider:       strArg(args, "provider", ""),
		TimeoutSeconds: intArg(args, "timeout_seconds", 0),
		ReflexHints:    reflexHints,
	})
	if err != nil {
		return errorResult(fmt.Sprintf("dispatch: %v", err)), nil
	}

	// Marshal the envelope back to JSON for the tool result. The Chat
	// agent's harness prompt instructs it to relay the structured
	// payload — this is the conduit through which the worker's result
	// reaches the frontend, with no raw text leakage.
	envJSON, err := json.Marshal(envelope)
	if err != nil {
		return errorResult(fmt.Sprintf("dispatch: marshal envelope: %v", err)), nil
	}
	return textResult(string(envJSON)), nil
}

// modeFromDispatchVia maps a reflex DispatchVia hint to the mode string
// dispatch.ExecuteTask understands. Defined here (not in the reflex package) to
// avoid duplicate logic — this is the MCP layer's translation of the hint.
func modeFromDispatchVia(via string) string {
	switch via {
	case "executeBackground":
		return "async"
	case "executeTask":
		return "sync"
	default:
		return ""
	}
}
