package service

import (
	"context"
	"time"
)

// EventEmitter unifies activity events (Volon GUI), plugin events, and
// presence broadcasts into a single interface. Services call one method
// instead of nil-checking three separate emitters with go-routine wrappers.
//
// All methods are fire-and-forget: failures are logged, never returned.
type EventEmitter interface {
	EmitSessionStart(ctx context.Context, sessionID, agentID, model, mode string)
	EmitSessionEnd(ctx context.Context, sessionID string)
	EmitAgentAssigned(ctx context.Context, sessionID, agentID, mode string)
	EmitResponseComplete(ctx context.Context, sessionID, agentID, model string, inputTokens, outputTokens int)
	EmitToolCall(ctx context.Context, sessionID, toolName string, success bool, resultLen int)
	EmitToolFailed(ctx context.Context, sessionID, toolName string, args any, err string)
	EmitRateLimitHit(ctx context.Context, sessionID, providerName string, retryAfter time.Duration)
	EmitCircuitBreakerTripped(ctx context.Context, sessionID, providerName string)
	EmitContextBudgetExceeded(ctx context.Context, sessionID string, total, ceiling int)
	EmitError(ctx context.Context, sessionID, errorType, detail string)
	EmitMessageReceived(ctx context.Context, sessionID, messageID, contentPreview string, elapsed int64)
	EmitModeChanged(ctx context.Context, sessionID, previousMode, newMode string)
	EmitPreCompact(ctx context.Context, sessionID string, messageCount int, reason string)
	EmitPostCompact(ctx context.Context, sessionID string, tokensSaved int, stagesApplied []string)
}

// PluginEventSink is the subset of plugin.Host used for event emission.
// Matches the existing chat.PluginEventEmitter interface so the concrete
// plugin host satisfies it without changes.
type PluginEventSink interface {
	EmitSessionStart(sessionID, agentID, mode string)
	EmitSessionEnd(sessionID string)
	EmitAgentSwitched(sessionID, previousAgentID, newAgentID string)
	EmitMessageSent(sessionID, messageID, content, role string, tokensUsed int)
	EmitMessageReceived(sessionID, messageID, content string, responseTime int64)
	EmitToolCalled(sessionID, toolName string, args, result any)
	EmitToolFailed(sessionID, toolName string, args any, err string)
	EmitModeChanged(sessionID, previousMode, newMode string)
	EmitPreHook(eventType, sessionID string, data map[string]any) bool
}
