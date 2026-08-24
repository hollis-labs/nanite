package chat

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/nanite/internal/elicitation"
	"github.com/hollis-labs/nanite/internal/store"
)

// ElicitationResponseHandler routes an elicitation-prompt envelope response
// back to the elicitation.Service so the blocked tool call can continue.
//
// The elicitation_id is read from the SERVER-persisted envelope payload
// (env.EnvelopeJSON), not from the client response, so a buggy or hostile
// client cannot respond on behalf of a different pending request.
//
// Wired in service/container.go alongside the subagent-spawn-approval handler.
// CW-20260420-0018.
type ElicitationResponseHandler struct {
	svc ElicitationResponder
}

// ElicitationResponder is the narrow interface ElicitationResponseHandler uses.
// *elicitation.Service satisfies this structurally.
type ElicitationResponder interface {
	Respond(id string, resp elicitation.Response) error
}

// NewElicitationResponseHandler wires the handler with its downstream service.
func NewElicitationResponseHandler(svc ElicitationResponder) *ElicitationResponseHandler {
	return &ElicitationResponseHandler{svc: svc}
}

func (h *ElicitationResponseHandler) HandleResponse(_ context.Context, env store.EnvelopeInstance, resp ResponseV1) (HandlerResult, error) {
	// Extract the canonical elicitation_id from the server-persisted envelope JSON.
	var payload struct {
		ElicitationID string `json:"elicitation_id"`
	}
	if err := json.Unmarshal([]byte(env.EnvelopeJSON), &payload); err != nil || payload.ElicitationID == "" {
		return HandlerResult{}, fmt.Errorf("elicitation-prompt: missing elicitation_id in envelope payload")
	}

	// Map ResponseV1 status to an elicitation.Action.
	var action elicitation.Action
	switch resp.Status {
	case StatusSubmitted:
		// Read the action from resp.Data["action"] (as submitted by the UI).
		actionStr, _ := resp.Data["action"].(string)
		switch elicitation.Action(actionStr) {
		case elicitation.ActionAccept, elicitation.ActionDecline:
			action = elicitation.Action(actionStr)
		default:
			// Default to accept for StatusSubmitted if the UI omits the action field
			// (backward-compat with basic submitted responses).
			action = elicitation.ActionAccept
		}
	case StatusCancelled:
		action = elicitation.ActionCancel
	default:
		action = elicitation.ActionCancel
	}

	content, _ := resp.Data["content"].(string)
	elicitResp := elicitation.Response{
		Action:  action,
		Content: content,
	}

	if err := h.svc.Respond(payload.ElicitationID, elicitResp); err != nil {
		// Already resolved (timeout race) — not a fatal error; log and continue.
		return HandlerResult{ //nolint:nilerr // A late response is represented as a silent handler result, not a pipeline failure.
			Silent:   true,
			FollowUp: fmt.Sprintf("elicitation %s already resolved (timeout race)", payload.ElicitationID),
		}, nil
	}

	return HandlerResult{
		FollowUp: fmt.Sprintf("Elicitation %s responded: action=%s", payload.ElicitationID, action),
		TranscriptData: map[string]any{
			"elicitation_id": payload.ElicitationID,
			"action":         string(action),
		},
	}, nil
}
