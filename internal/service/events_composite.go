package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/lifecycle"
	"github.com/hollis-labs/nanite/internal/messaging"
	"github.com/hollis-labs/nanite/internal/safego"
)

// CompositeEmitter fans out events to ActivityEmitter (Engine GUI),
// PluginEventSink (plugin hooks), and SessionEventWriter (session_events table).
// All sinks are nil-safe — a nil sub-emitter is silently skipped. All emissions
// run in goroutines so the caller never blocks.
type CompositeEmitter struct {
	activity      *chat.ActivityEmitter
	plugin        PluginEventSink
	sessionWriter SessionEventWriter
	lifecycle     *lifecycle.Manager
}

// NewCompositeEmitter creates a composite emitter. Any argument may be nil.
func NewCompositeEmitter(activity *chat.ActivityEmitter, plugin PluginEventSink, lifecycleManager *lifecycle.Manager) *CompositeEmitter {
	return &CompositeEmitter{activity: activity, plugin: plugin, lifecycle: lifecycleManager}
}

// emitPlugin schedules a plugin-contract callback on the chat lifecycle. The
// composition root always supplies the manager; focused unit-test emitters
// without one execute inline rather than creating an unowned fallback spawn.
func (c *CompositeEmitter) emitPlugin(label string, fn func()) {
	if c.lifecycle == nil {
		fn()
		return
	}
	c.lifecycle.Go("events.plugin."+label, func(context.Context) { fn() })
}

// WithSessionWriter attaches a SessionEventWriter so compaction events are
// persisted to session_events. Called once at container build time.
func (c *CompositeEmitter) WithSessionWriter(w SessionEventWriter) *CompositeEmitter {
	c.sessionWriter = w
	return c
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
		c.emitPlugin("session-start", func() {
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
		c.emitPlugin("session-end", func() {
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
		c.emitPlugin("message-sent", func() {
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
			c.emitPlugin("tool-called", func() {
				c.plugin.EmitToolCalled(sessionID, toolName, nil, nil)
			})
		}
	}
}

func (c *CompositeEmitter) EmitToolFailed(ctx context.Context, sessionID, toolName string, args any, err string) {
	if c.plugin != nil {
		c.emitPlugin("tool-failed", func() {
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
		c.emitPlugin("message-received", func() {
			c.plugin.EmitMessageReceived(sessionID, messageID, contentPreview, elapsed)
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
		c.emitPlugin("pre-compact", func() {
			c.plugin.EmitPreHook("context.pre_compact", sessionID, map[string]any{
				"message_count": messageCount,
				"reason":        reason,
			})
		})
	}
	// Persist a context_pre_compact row so P8 part C (CW-20260420-0027) has
	// a queryable seam. channel = trigger_kind; payload carries message_count
	// and trigger_kind so the row is self-contained for consumers.
	// Written synchronously (not via safego.Go) so the row lands before the
	// caller proceeds to EmitPostCompact — ordering is required for P8 consumers
	// that query pre/post compact rows in sequence.
	if c.sessionWriter != nil {
		payload := fmt.Sprintf(`{"message_count":%d,"trigger_kind":%q}`, messageCount, reason)
		c.sessionWriter.WriteSessionEvent(ctx, sessionID,
			messaging.EventContextPreCompact, reason, payload)
	}
}

func (c *CompositeEmitter) EmitPostCompact(ctx context.Context, sessionID string, tokensSaved int, stagesApplied []string) {
	// Post-compact is informational — no pre-hook cancellation.
	if c.plugin != nil {
		c.emitPlugin("context-compacted", func() {
			c.plugin.EmitContextCompacted(sessionID, tokensSaved, stagesApplied)
		})
	}
	// Persist a context_post_compact row for P8 part C (CW-20260420-0027).
	// stages_applied is a JSON array; tokens_saved and the channel are
	// included so a single row query is sufficient for P8 consumption.
	// Written synchronously (not via safego.Go) to preserve pre→post ordering
	// guarantee for P8 consumers (mirrors EmitPreCompact's synchronous write).
	if c.sessionWriter != nil {
		stagesJSON := `[]`
		if len(stagesApplied) > 0 {
			quoted := make([]string, len(stagesApplied))
			for i, s := range stagesApplied {
				quoted[i] = fmt.Sprintf("%q", s)
			}
			stagesJSON = "[" + strings.Join(quoted, ",") + "]"
		}
		payload := fmt.Sprintf(`{"tokens_saved":%d,"stages_applied":%s}`,
			tokensSaved, stagesJSON)
		c.sessionWriter.WriteSessionEvent(ctx, sessionID,
			messaging.EventContextPostCompact, "compaction", payload)
	}
}
