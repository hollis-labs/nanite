package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/hollis-labs/nanite/internal/brand"
)

// Activity event type constants for the Engine activity feed.
const (
	EventSessionCreated        = "chat_session_created"
	EventSessionEnded          = "chat_session_ended"
	EventSessionActive         = "chat_session_active"
	EventAgentAssigned         = "chat_agent_assigned"
	EventToolExecuted          = "chat_tool_call"
	EventRateLimitHit          = "chat_rate_limit_hit"
	EventCircuitBreakerTripped = "chat_circuit_breaker_tripped"
	EventContextBudgetExceeded = "chat_context_budget_exceeded"
	EventResponseComplete      = "chat_response_complete"
	EventError                 = "chat_error"
)

// ActivityEmitter sends activity events to Engine's activity feed so chat sessions
// appear in the unified activity feed. When the Engine URL is empty (disabled),
// all Emit calls are no-ops.
type ActivityEmitter struct {
	baseURL   string
	client    *http.Client
	projectID string // default project_id for events
	disabled  bool
}

// activityEvent is the JSON payload accepted by POST /v1/activity/events.
type activityEvent struct {
	ProjectID   string `json:"project_id"`
	EventType   string `json:"event_type"`
	EntityType  string `json:"entity_type"`
	EntityID    string `json:"entity_id"`
	EntityTitle string `json:"entity_title,omitempty"`
	Actor       string `json:"actor,omitempty"`
	Payload     string `json:"payload,omitempty"`
}

// NewActivityEmitter creates an emitter that posts to the given Engine activity server URL.
// Resolution order: explicit url arg > ENGINE_ACTIVITY_URL env > disabled.
// If no URL is resolved the emitter is created in disabled mode (all emits are no-ops).
func NewActivityEmitter(url string) *ActivityEmitter {
	if url == "" {
		url = os.Getenv("ENGINE_ACTIVITY_URL")
	}
	disabled := url == ""
	if disabled {
		slog.Info("activity: no ENGINE_ACTIVITY_URL set — emitter disabled")
	}
	return &ActivityEmitter{
		baseURL:   url,
		client:    &http.Client{Timeout: 5 * time.Second},
		projectID: brand.ID,
		disabled:  disabled,
	}
}

// Emit sends an activity event to Engine. It never returns an error — failures
// are logged and silently dropped so chat flow is never blocked.
func (e *ActivityEmitter) Emit(ctx context.Context, ev activityEvent) {
	if e.disabled {
		return
	}
	if ev.ProjectID == "" {
		ev.ProjectID = e.projectID
	}
	if ev.Actor == "" {
		ev.Actor = brand.ID
	}

	body, err := json.Marshal(ev)
	if err != nil {
		slog.Warn("activity: marshal error", "err", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/v1/activity/events", bytes.NewReader(body))
	if err != nil {
		slog.Warn("activity: request error", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		slog.Warn("activity: send error (engine unreachable)", "err", err)
		return
	}
	_ = resp.Body.Close() // The activity probe has consumed the response; close is best-effort cleanup.
	if resp.StatusCode >= 300 {
		slog.Warn("activity: engine non-2xx", "status", resp.StatusCode)
	}
}

// EmitSessionCreated records that a new chat session was created.
func (e *ActivityEmitter) EmitSessionCreated(ctx context.Context, sessionID string) {
	e.Emit(ctx, activityEvent{
		EventType:   EventSessionCreated,
		EntityType:  "chat_session",
		EntityID:    sessionID,
		EntityTitle: "Session created",
	})
}

// EmitSessionEnded records that a chat session was archived/ended.
func (e *ActivityEmitter) EmitSessionEnded(ctx context.Context, sessionID string) {
	e.Emit(ctx, activityEvent{
		EventType:   EventSessionEnded,
		EntityType:  "chat_session",
		EntityID:    sessionID,
		EntityTitle: "Session ended",
	})
}

// EmitSessionStart records that a chat session started generating a response.
func (e *ActivityEmitter) EmitSessionStart(ctx context.Context, sessionID, agentID, model string) {
	e.Emit(ctx, activityEvent{
		EventType:   EventSessionActive,
		EntityType:  "chat_session",
		EntityID:    sessionID,
		EntityTitle: "Chat session started",
		Actor:       agentID,
		Payload:     fmt.Sprintf(`{"model":%q}`, model),
	})
}

// EmitAgentAssigned records that an agent was assigned to a session.
func (e *ActivityEmitter) EmitAgentAssigned(ctx context.Context, sessionID, agentID, mode string) {
	e.Emit(ctx, activityEvent{
		EventType:   EventAgentAssigned,
		EntityType:  "chat_session",
		EntityID:    sessionID,
		EntityTitle: "Agent assigned: " + agentID,
		Actor:       agentID,
		Payload:     fmt.Sprintf(`{"agent_id":%q,"mode":%q}`, agentID, mode),
	})
}

// EmitResponseComplete records a completed assistant response with token counts.
func (e *ActivityEmitter) EmitResponseComplete(ctx context.Context, sessionID, agentID, model string, inputTokens, outputTokens int) {
	e.Emit(ctx, activityEvent{
		EventType:   EventResponseComplete,
		EntityType:  "chat_session",
		EntityID:    sessionID,
		EntityTitle: "Response complete",
		Actor:       agentID,
		Payload:     fmt.Sprintf(`{"model":%q,"input_tokens":%d,"output_tokens":%d}`, model, inputTokens, outputTokens),
	})
}

// EmitToolCall records a tool invocation.
func (e *ActivityEmitter) EmitToolCall(ctx context.Context, sessionID, toolName string, success bool, resultLen int) {
	status := "success"
	if !success {
		status = "error"
	}
	e.Emit(ctx, activityEvent{
		EventType:   EventToolExecuted,
		EntityType:  "chat_session",
		EntityID:    sessionID,
		EntityTitle: toolName,
		Payload:     fmt.Sprintf(`{"tool":%q,"status":%q,"result_len":%d}`, toolName, status, resultLen),
	})
}

// EmitRateLimitHit records a rate limit error from a provider.
func (e *ActivityEmitter) EmitRateLimitHit(ctx context.Context, sessionID, providerName string, retryAfter time.Duration) {
	e.Emit(ctx, activityEvent{
		EventType:   EventRateLimitHit,
		EntityType:  "chat_session",
		EntityID:    sessionID,
		EntityTitle: "Rate limit hit",
		Payload:     fmt.Sprintf(`{"provider":%q,"retry_after_seconds":%.1f}`, providerName, retryAfter.Seconds()),
	})
}

// EmitCircuitBreakerTripped records that the circuit breaker opened for a provider.
func (e *ActivityEmitter) EmitCircuitBreakerTripped(ctx context.Context, sessionID, providerName string) {
	e.Emit(ctx, activityEvent{
		EventType:   EventCircuitBreakerTripped,
		EntityType:  "chat_session",
		EntityID:    sessionID,
		EntityTitle: "Circuit breaker tripped",
		Payload:     fmt.Sprintf(`{"provider":%q}`, providerName),
	})
}

// EmitContextBudgetExceeded records that the token budget ceiling was exceeded.
func (e *ActivityEmitter) EmitContextBudgetExceeded(ctx context.Context, sessionID string, total, ceiling int) {
	e.Emit(ctx, activityEvent{
		EventType:   EventContextBudgetExceeded,
		EntityType:  "chat_session",
		EntityID:    sessionID,
		EntityTitle: "Context budget exceeded",
		Payload:     fmt.Sprintf(`{"total_tokens":%d,"ceiling_tokens":%d}`, total, ceiling),
	})
}

// EmitPreCompact records that a compaction cycle is about to run.
func (e *ActivityEmitter) EmitPreCompact(ctx context.Context, sessionID string, messageCount int, reason string) {
	e.Emit(ctx, activityEvent{
		EventType:   EventPreCompact,
		EntityType:  "chat_session",
		EntityID:    sessionID,
		EntityTitle: "Context compaction starting",
		Payload:     fmt.Sprintf(`{"message_count":%d,"reason":%q}`, messageCount, reason),
	})
}

// EventPreCompact is the activity event type for pre-compaction.
const EventPreCompact = "chat_pre_compact"

// EmitError records an error during chat processing.
func (e *ActivityEmitter) EmitError(ctx context.Context, sessionID, errorType, detail string) {
	e.Emit(ctx, activityEvent{
		EventType:   EventError,
		EntityType:  "chat_session",
		EntityID:    sessionID,
		EntityTitle: errorType,
		Payload:     fmt.Sprintf(`{"error_type":%q,"detail":%q}`, errorType, detail),
	})
}
