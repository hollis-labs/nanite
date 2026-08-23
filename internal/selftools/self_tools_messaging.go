package selftools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/messaging"
)

// MessagingTools owns the internal messaging and session-handoff self-tool
// policy. The outer SelfToolsTransport remains the sole MCP catalog and name
// dispatcher; this collaborator owns the cohesive service calls, timeout, and
// directive-elicitation gate behind those routes.
type MessagingTools struct {
	Service     *messaging.Service
	Elicitation mcp.ElicitationService
}

// NewMessagingTools constructs a nil-safe messaging collaborator. A nil
// service preserves catalog availability while returning the established
// unwired-service result from every messaging route.
func NewMessagingTools(service *messaging.Service, elicitation mcp.ElicitationService) *MessagingTools {
	return &MessagingTools{Service: service, Elicitation: elicitation}
}

// messageCallTimeout bounds every unary messaging tool call so a wedged store
// or slow subscriber cannot hang the MCP handler forever.
const messageCallTimeout = 30 * time.Second

func (mt *MessagingTools) callMessageSend(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if mt == nil || mt.Service == nil {
		return mcp.ErrorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()

	kind := strArg(args, "kind", "")
	body := strArg(args, "body", "")
	msgType := strArg(args, "type", "")

	// Directive messages broadcast instructions with elevated blast radius.
	// Require explicit user confirmation when elicitation is wired.
	if msgType == "directive" && mt.Elicitation != nil {
		fromSessionID := strArg(args, "from_session_id", "")
		fromAgentID := strArg(args, "from_agent_id", "")
		elicitResp, err := mcp.ElicitUserInput(ctx, mt.Elicitation, fromSessionID, fromAgentID, "",
			mcp.ElicitationCreateParams{
				Message: fmt.Sprintf("Send directive to %s? Body: %q", strArg(args, "to_agent_id", ""), body),
				RequestedSchema: &mcp.ElicitationRequestedSchema{
					Type:        "boolean",
					Title:       "Confirm directive send",
					Description: "Directive messages instruct recipient agents to take action. Confirm to proceed.",
				},
			})
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("elicitation: %v", err)), nil
		}
		if elicitResp.Action != "accept" {
			return mcp.TextResult(fmt.Sprintf("directive send aborted by user (action=%s)", elicitResp.Action)), nil
		}
	}

	msg := messaging.SendInput{
		FromSessionID: strArg(args, "from_session_id", ""),
		FromAgentID:   strArg(args, "from_agent_id", ""),
		ToSessionID:   strArg(args, "to_session_id", ""),
		ToAgentID:     strArg(args, "to_agent_id", ""),
		Channel:       strArg(args, "channel", ""),
		Kind:          kind,
		PayloadJSON:   strArg(args, "payload_json", ""),
		Subject:       strArg(args, "subject", ""),
		Body:          body,
		Type:          msgType,
		ReplyTo:       strArg(args, "reply_to", ""),
		RegisterAs:    strArg(args, "register_as", ""),
	}
	out, err := mt.Service.SendMessage(ctx, msg)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("message send: %v", err)), nil
	}
	return mcp.TextResult(fmt.Sprintf("sent: %s", out.ID)), nil
}

func (mt *MessagingTools) callMessageInbox(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if mt == nil || mt.Service == nil {
		return mcp.ErrorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	sessionID := strArg(args, "session_id", "")
	agentID := strArg(args, "agent_id", "")
	inbox, err := mt.Service.Inbox(ctx, sessionID, agentID, messaging.InboxFilter{
		Status:  strArg(args, "status", ""),
		Channel: strArg(args, "channel", ""),
		Kind:    strArg(args, "kind", ""),
	}, sessionID, agentID)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("message inbox: %v", err)), nil
	}
	if inbox == nil {
		inbox = []messaging.Message{}
	}
	data, err := json.Marshal(inbox)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("messaging inbox marshal: %v", err)), nil
	}
	return mcp.TextResult(string(data)), nil
}

func (mt *MessagingTools) callMessageThread(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if mt == nil || mt.Service == nil {
		return mcp.ErrorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	messages, err := mt.Service.Thread(ctx, strArg(args, "thread_id", ""), strArg(args, "session_id", ""), strArg(args, "agent_id", ""))
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("message thread: %v", err)), nil
	}
	if messages == nil {
		messages = []messaging.Message{}
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("messaging thread marshal: %v", err)), nil
	}
	return mcp.TextResult(string(data)), nil
}

func (mt *MessagingTools) callMessageAck(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if mt == nil || mt.Service == nil {
		return mcp.ErrorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	if err := mt.Service.Ack(ctx, strArg(args, "session_id", ""), strArg(args, "agent_id", ""), strArg(args, "message_id", "")); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("message ack: %v", err)), nil
	}
	return mcp.TextResult("acked"), nil
}

func (mt *MessagingTools) callMessageResolve(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if mt == nil || mt.Service == nil {
		return mcp.ErrorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	if err := mt.Service.Resolve(ctx, strArg(args, "session_id", ""), strArg(args, "agent_id", ""), strArg(args, "message_id", "")); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("message resolve: %v", err)), nil
	}
	return mcp.TextResult("resolved"), nil
}

func (mt *MessagingTools) callMessageCatchUp(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if mt == nil || mt.Service == nil {
		return mcp.ErrorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	messages, err := mt.Service.RecentForSession(ctx, strArg(args, "session_id", ""), mcp.IntArg(args, "limit", 20))
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("message catch_up: %v", err)), nil
	}
	if messages == nil {
		messages = []messaging.Message{}
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("messaging catch_up marshal: %v", err)), nil
	}
	return mcp.TextResult(string(data)), nil
}

func (mt *MessagingTools) callHandoffRequest(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if mt == nil || mt.Service == nil {
		return mcp.ErrorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	sessionID := mcp.SessionIDFromContext(ctx)
	if sessionID == "" {
		sessionID = strArg(args, "session_id", "")
	}
	id, err := mt.Service.RequestHandoff(ctx, sessionID, strArg(args, "from_agent_id", ""), strArg(args, "to_agent_id", ""), strArg(args, "requested_by", ""))
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("handoff request: %v", err)), nil
	}
	return mcp.TextResult("handoff requested: " + id), nil
}

func (mt *MessagingTools) callHandoffApprove(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if mt == nil || mt.Service == nil {
		return mcp.ErrorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	if err := mt.Service.ApproveHandoff(ctx, strArg(args, "handoff_id", "")); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("handoff approve: %v", err)), nil
	}
	return mcp.TextResult("approved"), nil
}

func (mt *MessagingTools) callHandoffReject(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if mt == nil || mt.Service == nil {
		return mcp.ErrorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	if err := mt.Service.RejectHandoff(ctx, strArg(args, "handoff_id", ""), strArg(args, "reason", "")); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("handoff reject: %v", err)), nil
	}
	return mcp.TextResult("rejected"), nil
}
