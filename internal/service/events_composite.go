package service

import (
	"context"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/safego"
)

// CompositeEmitter fans out events to ActivityEmitter (Engine GUI) and
// PluginEventSink (plugin hooks). Both sinks are nil-safe — a nil sub-emitter
// is silently skipped. All emissions run in goroutines so the caller never blocks.
type CompositeEmitter struct {
	activity *chat.ActivityEmitter
	plugin   PluginEventSink
}

// NewCompositeEmitter creates a composite emitter. Either argument may be nil.
func NewCompositeEmitter(activity *chat.ActivityEmitter, plugin PluginEventSink) *CompositeEmitter {
	return &CompositeEmitter{activity: activity, plugin: plugin}
}

// Compile-time verification.
var _ EventEmitter = (*CompositeEmitter)(nil)

func (c *CompositeEmitter) EmitSessionStart(ctx context.Context, sessionID, agentID, model, mode string) {
	if c.activity != nil {
		safego.Go(ctx, "service.events.activity.session-start", func() {
			c.activity.EmitSessionStart(ctx, sessionID, agentID, model)
		})
	}
	if c.plugin != nil {
		safego.Go(ctx, "service.events.plugin.session-start", func() {
			c.plugin.EmitSessionStart(sessionID, agentID, mode)
		})
	}
}

func (c *CompositeEmitter) EmitSessionEnd(ctx context.Context, sessionID string) {
	if c.activity != nil {
		safego.Go(ctx, "service.events.activity.session-ended", func() {
			c.activity.EmitSessionEnded(ctx, sessionID)
		})
	}
	if c.plugin != nil {
		safego.Go(ctx, "service.events.plugin.session-end", func() {
			c.plugin.EmitSessionEnd(sessionID)
		})
	}
}

func (c *CompositeEmitter) EmitAgentAssigned(ctx context.Context, sessionID, agentID, mode string) {
	if c.activity != nil {
		safego.Go(ctx, "service.events.activity.agent-assigned", func() {
			c.activity.EmitAgentAssigned(ctx, sessionID, agentID, mode)
		})
	}
}

func (c *CompositeEmitter) EmitResponseComplete(ctx context.Context, sessionID, agentID, model string, inputTokens, outputTokens int) {
	if c.activity != nil {
		safego.Go(ctx, "service.events.activity.response-complete", func() {
			c.activity.EmitResponseComplete(ctx, sessionID, agentID, model, inputTokens, outputTokens)
		})
	}
	if c.plugin != nil {
		safego.Go(ctx, "service.events.plugin.message-sent", func() {
			c.plugin.EmitMessageSent(sessionID, "", "", "assistant", inputTokens+outputTokens)
		})
	}
}

func (c *CompositeEmitter) EmitToolCall(ctx context.Context, sessionID, toolName string, success bool, resultLen int) {
	if c.activity != nil {
		safego.Go(ctx, "service.events.activity.tool-call", func() {
			c.activity.EmitToolCall(ctx, sessionID, toolName, success, resultLen)
		})
	}
	if c.plugin != nil {
		if success {
			safego.Go(ctx, "service.events.plugin.tool-called", func() {
				c.plugin.EmitToolCalled(sessionID, toolName, nil, nil)
			})
		}
	}
}

func (c *CompositeEmitter) EmitToolFailed(ctx context.Context, sessionID, toolName string, args any, err string) {
	if c.plugin != nil {
		safego.Go(ctx, "service.events.plugin.tool-failed", func() {
			c.plugin.EmitToolFailed(sessionID, toolName, args, err)
		})
	}
}

func (c *CompositeEmitter) EmitRateLimitHit(ctx context.Context, sessionID, providerName string, retryAfter time.Duration) {
	if c.activity != nil {
		safego.Go(ctx, "service.events.activity.rate-limit-hit", func() {
			c.activity.EmitRateLimitHit(ctx, sessionID, providerName, retryAfter)
		})
	}
}

func (c *CompositeEmitter) EmitCircuitBreakerTripped(ctx context.Context, sessionID, providerName string) {
	if c.activity != nil {
		safego.Go(ctx, "service.events.activity.circuit-breaker-tripped", func() {
			c.activity.EmitCircuitBreakerTripped(ctx, sessionID, providerName)
		})
	}
}

func (c *CompositeEmitter) EmitContextBudgetExceeded(ctx context.Context, sessionID string, total, ceiling int) {
	if c.activity != nil {
		safego.Go(ctx, "service.events.activity.context-budget-exceeded", func() {
			c.activity.EmitContextBudgetExceeded(ctx, sessionID, total, ceiling)
		})
	}
}

func (c *CompositeEmitter) EmitError(ctx context.Context, sessionID, errorType, detail string) {
	if c.activity != nil {
		safego.Go(ctx, "service.events.activity.error", func() {
			c.activity.EmitError(ctx, sessionID, errorType, detail)
		})
	}
}

func (c *CompositeEmitter) EmitMessageReceived(ctx context.Context, sessionID, messageID, contentPreview string, elapsed int64) {
	if c.plugin != nil {
		safego.Go(ctx, "service.events.plugin.message-received", func() {
			c.plugin.EmitMessageReceived(sessionID, messageID, contentPreview, elapsed)
		})
	}
}

func (c *CompositeEmitter) EmitModeChanged(ctx context.Context, sessionID, previousMode, newMode string) {
	if c.activity != nil {
		safego.Go(ctx, "service.events.activity.mode-changed", func() {
			c.activity.EmitModeChanged(ctx, sessionID, previousMode, newMode)
		})
	}
	if c.plugin != nil {
		safego.Go(ctx, "service.events.plugin.mode-changed", func() {
			c.plugin.EmitModeChanged(sessionID, previousMode, newMode)
		})
	}
}

func (c *CompositeEmitter) EmitPreCompact(ctx context.Context, sessionID string, messageCount int, reason string) {
	if c.activity != nil {
		safego.Go(ctx, "service.events.activity.pre-compact", func() {
			c.activity.EmitPreCompact(ctx, sessionID, messageCount, reason)
		})
	}
	// Plugin pre-compact hook: plugins can extract ADR, memories, etc.
	// before the raw content is replaced.
	if c.plugin != nil {
		safego.Go(ctx, "service.events.plugin.pre-compact", func() {
			c.plugin.EmitPreHook("context.pre_compact", sessionID, map[string]any{
				"message_count": messageCount,
				"reason":        reason,
			})
		})
	}
}

func (c *CompositeEmitter) EmitPostCompact(ctx context.Context, sessionID string, tokensSaved int, stagesApplied []string) {
	// Post-compact is informational — no pre-hook cancellation.
	if c.plugin != nil {
		safego.Go(ctx, "service.events.plugin.context-compacted", func() {
			c.plugin.EmitContextCompacted(sessionID, tokensSaved, stagesApplied)
		})
	}
}
