package engine

import (
	"context"
	"fmt"

	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/permission"
)

// autoApprove is reached only after policy returned Ask, never Deny.
func (x *execution) autoApprove(ctx context.Context, req permission.PermissionRequest) permission.PermissionDecision {
	summary := fmt.Sprintf("[redacted input: %d bytes]", len(req.Input))
	tool := x.engine.scrub.Text(req.Tool)
	x.engine.cfg.Logger.WarnContext(ctx, "nanite: auto-approved tool call", "tool", tool, "conversation", x.info.ID, "input", summary)
	emit := func(meta events.EventMeta) events.Event {
		return events.AutoApproved{EventMeta: meta, Conversation: x.info.ID, CallID: req.ID, Tool: tool, InputSummary: summary}
	}
	x.run.emit(ctx, emit)
	if x.origin != nil {
		x.engine.mu.Lock()
		root := x.engine.active[x.root.ID]
		x.engine.mu.Unlock()
		if root != nil && root != x.run {
			root.emit(ctx, emit)
		}
	}
	return permission.PermissionDecision{Allow: true, Scope: permission.ScopeOnce, Reason: "auto-approved (testing mode)"}
}
