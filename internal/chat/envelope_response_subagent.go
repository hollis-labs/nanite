package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// SubagentApprovalHandler dispatches a typed envelope-response submission to
// subagent.Service.Approve / Reject. Run ID is read from the SERVER-persisted
// envelope payload (env.EnvelopeJSON), NOT from the client response, so a
// buggy or hostile client cannot approve a different run than the one they
// were shown.
type SubagentApprovalHandler struct {
	svc *subagent.Service
}

// NewSubagentApprovalHandler wires the handler with its downstream service.
func NewSubagentApprovalHandler(svc *subagent.Service) *SubagentApprovalHandler {
	return &SubagentApprovalHandler{svc: svc}
}

func (h *SubagentApprovalHandler) HandleResponse(ctx context.Context, env store.EnvelopeInstance, resp ResponseV1) (HandlerResult, error) {
	var p struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal([]byte(env.EnvelopeJSON), &p); err != nil || p.RunID == "" {
		return HandlerResult{}, errors.New("subagent-spawn-approval: missing run_id in envelope payload")
	}

	switch resp.Status {
	case StatusSubmitted:
		if err := h.svc.Approve(ctx, p.RunID); err != nil {
			return HandlerResult{}, fmt.Errorf("approve run %s: %w", p.RunID, err)
		}
		return HandlerResult{
			FollowUp:       fmt.Sprintf("Subagent run %s approved — running.", p.RunID),
			TranscriptData: map[string]any{"run_id": p.RunID, "decision": "approved"},
		}, nil
	case StatusCanceled:
		reason, _ := resp.Data["reason"].(string)
		if err := h.svc.Reject(ctx, p.RunID, reason); err != nil {
			return HandlerResult{}, fmt.Errorf("reject run %s: %w", p.RunID, err)
		}
		return HandlerResult{
			FollowUp:       fmt.Sprintf("Subagent run %s rejected.", p.RunID),
			TranscriptData: map[string]any{"run_id": p.RunID, "decision": "rejected", "reason": reason},
		}, nil
	default:
		return HandlerResult{}, fmt.Errorf("unsupported response status %q", resp.Status)
	}
}
