package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/hollis-labs/go-agent-broker/broker"
	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/grounding"
	"github.com/hollis-labs/nanite/internal/reflex"
)

// callExecuteTask handles task_execute — the Chat agent's
// dispatch primitive (CW-20260421-0010, B3). Translates the tool args
// into dispatch.ExecuteTaskArgs, invokes the dispatch primitive, and
// returns the resulting envelope JSON as the tool result.
//
// The Chat agent receives the envelope (via the tool result) and
// relays it to the frontend. Raw worker output never enters the Chat
// agent's context window — only the structured envelope does.
//
// E2 integration (CW-20260419-0028): when GroundingRecaller is set and
// NANITE_GROUNDING_ENABLED=true, a memory recall step fires FIRST, before
// the E1 reflex matcher. Memories above the similarity threshold are
// prepended to the message as a "## Relevant memories" block (≤200 tokens).
// Consultation rows are logged; outcome rows are written after the
// follow-up (caller responsibility via grounding.RecordOutcome).
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

	// E2: Pre-strategy memory-grounding recall (CW-20260419-0028).
	// Fires before E1 reflex matching so that memory context can influence
	// the message seen by the dispatch layer. The grounding block is
	// prepended to the dispatch message, not the original user-facing
	// message — the worker/planner sees the enriched prompt.
	//
	// When the gate is off, groundingResult.Enabled == false and no rows
	// are written. When enabled but no hits exceed the threshold, the
	// message is unchanged.
	turnID := strArg(args, "turn_id", "")
	userID := strArg(args, "user_id", "")
	dispatchMessage := message // may be prepended with memories block below
	var groundingConsultationIDs []int64
	if st.GroundingRecaller != nil {
		groundingResult := st.GroundingRecaller.Recall(ctx, grounding.RecallInput{
			UserInput: message,
			SessionID: sessionID,
			UserID:    userID,
			TurnID:    turnID,
		})
		if groundingResult.Enabled {
			// Log all hits (consumed/discarded) before dispatch. Errors are swallowed.
			groundingConsultationIDs = grounding.LogConsultations(st.GroundingLogger, groundingResult, turnID)

			// Prepend the surfaced memories block to the dispatch message.
			if block := grounding.SystemPromptBlock(groundingResult); block != "" {
				dispatchMessage = block + "\n" + message
			}
		}
	}
	// groundingConsultationIDs is available for post-generation outcome
	// write-back via grounding.RecordOutcome; the MCP dispatch layer does
	// not observe the follow-up turn directly, so write-back is the
	// responsibility of the chat generation layer when it records outcomes.
	_ = groundingConsultationIDs

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

	// CW-20260502-0005: agent-broker consultation (no-op scaffold).
	// The broker is upstream of dispatch; the no-op impl reads SessionMode
	// and returns the current-behavior agent profile so wiring it produces
	// no semantic change. Decision.Reason is logged to event_log so future
	// sessions (and the v1 deterministic replacement) can audit routing.
	if st.Broker != nil {
		mode := ""
		var modeLookupErr error
		if st.Store != nil {
			m, err := st.Store.GetSessionMode(sessionID)
			if err != nil {
				// Log the lookup failure so an empty mode in the broker
				// audit trail isn't ambiguous between "no mode set" and
				// "mode lookup failed". PR #113 review feedback.
				modeLookupErr = err
				slog.Warn("mcp: broker session_mode lookup failed",
					"session_id", sessionID, "err", err)
			} else if m != nil {
				mode = m.Slug
			}
		}
		brokerInput := broker.Input{
			// PR #113 review: broker must see the same effective text
			// dispatch will see (post-grounding-injection), otherwise the
			// audit trail and any future non-noop broker logic won't
			// correspond to the actual dispatched prompt.
			UserText:    dispatchMessage,
			SessionMode: mode,
		}
		if reflexHints != nil {
			brokerInput.ReflexMatchID = reflexHints.ReflexID
		}
		decision, derr := st.Broker.Decide(ctx, brokerInput)
		switch {
		case derr != nil:
			// PR #113 review: surface broker failures via slog + event_log
			// so wiring/config regressions are detectable. Non-fatal — the
			// no-op contract preserves current dispatch behavior on broker
			// failure (we just skip recording a decision).
			slog.Warn("mcp: broker decide failed",
				"session_id", sessionID, "err", derr)
			if st.Store != nil {
				meta := fmt.Sprintf(
					`{"error":%q,"session_mode":%q,"reflex_match_id":%q}`,
					derr.Error(), mode, brokerInput.ReflexMatchID,
				)
				st.Store.LogEvent(sessionID, "broker_decision_error", "error", derr.Error(), meta)
			}
		case st.Store != nil:
			modeErrStr := ""
			if modeLookupErr != nil {
				modeErrStr = modeLookupErr.Error()
			}
			meta := fmt.Sprintf(
				`{"agent_profile":%q,"reason":%q,"confidence":%g,"session_mode":%q,"reflex_match_id":%q,"mode_lookup_error":%q}`,
				decision.AgentProfile, decision.Reason, decision.Confidence, mode, brokerInput.ReflexMatchID, modeErrStr,
			)
			st.Store.LogEvent(sessionID, "broker_decision", "info", decision.Reason, meta)
		}
	}

	// H1 trust resolution (CW-20260421-0014): populate WorkspaceID and
	// AgentProfileID from the caller-profile ctx stamped by the service layer
	// in executeToolBatch. When the ctx carries no profile (e.g. direct test
	// invocations), both fields are empty and the subagent gate falls back to
	// TrustNormal (approval required — existing safe default).
	wsID, apID := CallerProfileFromContext(ctx)

	envelope, err := dispatch.ExecuteTask(ctx, st.Dispatch, wrapper, dispatch.ExecuteTaskArgs{
		SessionID:      sessionID,
		ParentAgentID:  strArg(args, "parent_agent_id", ""),
		Message:        dispatchMessage,
		Provider:       strArg(args, "provider", ""),
		TimeoutSeconds: intArg(args, "timeout_seconds", 0),
		ReflexHints:    reflexHints,
		WorkspaceID:    wsID,
		AgentProfileID: apID,
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
