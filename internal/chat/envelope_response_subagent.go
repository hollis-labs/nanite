package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/agent/subagent"
)

// SubagentApprovalHandler validates the server-persisted approval presentation.
// Continuation refuses until a verified host approval-owner port is adopted;
// a run ID alone does not authorize changing a retained run.
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
	case StatusSubmitted, StatusCanceled:
		// A persisted run_id binds presentation, not current actor ownership. The
		// released approval service does not re-authorize retained runs, and this
		// host has no verified continuation/approval owner port yet.
		return HandlerResult{}, store.ErrVerifiedActorRequired
	default:
		return HandlerResult{}, fmt.Errorf("unsupported response status %q", resp.Status)
	}
}
