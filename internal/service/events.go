package service

import (
	"context"
	"time"

	messaging "github.com/hollis-labs/go-messaging/mailbox"
	"github.com/hollis-labs/nanite/internal/plugin"
)

// Host runtime event types stored alongside mailbox events in session_events.
// Mailbox mutation event names live in go-messaging/mailbox; provider,
// compaction, and harness lifecycle vocabulary remains owned by Nanite.
const (
	EventPTYTurnStart    = "pty_turn_start"
	EventPTYTurnComplete = "pty_turn_complete"
	EventPTYTurnFailed   = "pty_turn_failed"

	EventContextPreCompact  = "context_pre_compact"
	EventContextPostCompact = "context_post_compact"

	EventHarnessTriggeredTurn = "harness_triggered_turn"
)

// EventEmitter unifies activity events (Engine GUI), plugin events, and
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
	EmitPreCompact(ctx context.Context, sessionID string, messageCount int, reason string)
	EmitPostCompact(ctx context.Context, sessionID string, tokensSaved int, stagesApplied []string)
}

// SessionEventWriter is the narrow interface for writing lifecycle events
// directly into the session_events table. The production implementation is
// the Nanite-owned session-event adapter installed beside mailbox.Service.
// Optional in ChatServiceConfig — nil-safe at all call sites.
type SessionEventWriter interface {
	WriteSessionEvent(ctx context.Context, sessionID, eventType, channel, payloadJSON string)
}

// SubagentResultInbox is the narrow interface chat_generate.go uses to
// pull pending kind=subagent_result agent_messages into turn context at
// turn start (CW-20260512-0019). Satisfied by *messaging.Service.
// Optional in ChatServiceConfig — nil-safe at the call site.
type SubagentResultInbox interface {
	Inbox(ctx context.Context, sessionID, agentID string, filter messaging.InboxFilter, callerSessionID, callerAgentID string) ([]messaging.Message, error)
	Ack(ctx context.Context, sessionID, agentID, msgID string) error
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
	EmitEnvelopeRendered(sessionID, envelopeType string, data interface{})
	EmitProviderError(sessionID, providerName, model, errMsg string)
	EmitProviderFallback(sessionID, fromProvider, toProvider string)
	EmitAgentLoaded(sessionID, agentID, agentName, version string)
	EmitContextAssembled(sessionID string, systemPromptLen, messageCount, toolCount int)
	EmitContextCompacted(sessionID string, tokensSaved int, stagesApplied []string)
	EmitPreHook(eventType, sessionID string, data map[string]any) bool

	// Filter support — synchronous, returns transformed data or error.
	ApplyFilter(name string, data interface{}, ctx plugin.FilterContext) (interface{}, error)
}
