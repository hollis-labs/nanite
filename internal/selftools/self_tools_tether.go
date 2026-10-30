package selftools

import (
	"context"
	"fmt"

	"github.com/hollis-labs/nanite/internal/mcp"
)

// TetherSender sends Tether mail as the enrolled actor attached to a session.
type TetherSender interface {
	SendAsSessionActor(ctx context.Context, sessionID, to, subject, body, inReplyTo, threadID, idemKey string) (string, error)
}

// callTetherSend handles tether_send. The sender is derived from the session in
// the MCP context, never from an argument.
func (st *SelfToolsTransport) callTetherSend(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.TetherSend == nil {
		return mcp.ErrorResult("tether_send: Tether is not configured on this server"), nil
	}
	sid := mcp.SessionIDFromContext(ctx)
	id, err := st.TetherSend.SendAsSessionActor(ctx, sid,
		strArg(args, "to", ""), strArg(args, "subject", ""), strArg(args, "body", ""),
		strArg(args, "in_reply_to", ""), strArg(args, "thread_id", ""), strArg(args, "idempotency_key", ""))
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	return mcp.TextResult(fmt.Sprintf("sent: %s", id)), nil
}
