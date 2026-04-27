package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/nanite/internal/dispatch"
)

// callExecuteTask handles nanite_execute_task — the Chat agent's
// dispatch primitive (CW-20260421-0010, B3). Translates the tool args
// into dispatch.ExecuteTaskArgs, invokes the dispatch primitive, and
// returns the resulting envelope JSON as the tool result.
//
// The Chat agent receives the envelope (via the tool result) and
// relays it to the frontend. Raw worker output never enters the Chat
// agent's context window — only the structured envelope does.
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

	envelope, err := dispatch.ExecuteTask(ctx, st.Dispatch, wrapper, dispatch.ExecuteTaskArgs{
		SessionID:      sessionID,
		ParentAgentID:  strArg(args, "parent_agent_id", ""),
		Message:        message,
		Provider:       strArg(args, "provider", ""),
		TimeoutSeconds: intArg(args, "timeout_seconds", 0),
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
