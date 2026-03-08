package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

// ActivityEmitter sends activity events to Volon's GUI server so chat sessions
// appear in the unified activity feed.
type ActivityEmitter struct {
	baseURL    string
	client     *http.Client
	projectID  string // default project_id for events
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

// NewActivityEmitter creates an emitter that posts to the given Volon GUI server URL.
// If url is empty, it falls back to VOLON_GUI_URL env var, then http://localhost:8085.
func NewActivityEmitter(url string) *ActivityEmitter {
	if url == "" {
		url = os.Getenv("VOLON_GUI_URL")
	}
	if url == "" {
		url = "http://localhost:8085"
	}
	return &ActivityEmitter{
		baseURL:   url,
		client:    &http.Client{Timeout: 5 * time.Second},
		projectID: "mentat-chat",
	}
}

// Emit sends an activity event to Volon. It never returns an error — failures
// are logged and silently dropped so chat flow is never blocked.
func (e *ActivityEmitter) Emit(ctx context.Context, ev activityEvent) {
	if ev.ProjectID == "" {
		ev.ProjectID = e.projectID
	}
	if ev.Actor == "" {
		ev.Actor = "mentat-chat"
	}

	body, err := json.Marshal(ev)
	if err != nil {
		log.Printf("activity: marshal error: %v", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/v1/activity/events", bytes.NewReader(body))
	if err != nil {
		log.Printf("activity: request error: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		log.Printf("activity: send error: %v", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("activity: volon returned %d", resp.StatusCode)
	}
}

// EmitSessionStart records that a chat session started generating a response.
func (e *ActivityEmitter) EmitSessionStart(ctx context.Context, sessionID, agentID, model string) {
	e.Emit(ctx, activityEvent{
		EventType:   "chat_session_active",
		EntityType:  "chat_session",
		EntityID:    sessionID,
		EntityTitle: "Chat session started",
		Actor:       agentID,
		Payload:     fmt.Sprintf(`{"model":%q}`, model),
	})
}

// EmitResponseComplete records a completed assistant response with token counts.
func (e *ActivityEmitter) EmitResponseComplete(ctx context.Context, sessionID, agentID, model string, inputTokens, outputTokens int) {
	e.Emit(ctx, activityEvent{
		EventType:   "chat_response_complete",
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
		EventType:   "chat_tool_call",
		EntityType:  "chat_session",
		EntityID:    sessionID,
		EntityTitle: toolName,
		Payload:     fmt.Sprintf(`{"tool":%q,"status":%q,"result_len":%d}`, toolName, status, resultLen),
	})
}

// EmitError records an error during chat processing.
func (e *ActivityEmitter) EmitError(ctx context.Context, sessionID, errorType, detail string) {
	e.Emit(ctx, activityEvent{
		EventType:   "chat_error",
		EntityType:  "chat_session",
		EntityID:    sessionID,
		EntityTitle: errorType,
		Payload:     fmt.Sprintf(`{"error_type":%q,"detail":%q}`, errorType, detail),
	})
}
