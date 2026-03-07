package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/hollis-labs/mentat-chat/internal/provider"
	"github.com/hollis-labs/mentat-chat/internal/store"
)

// StreamEvent is the event sent to SSE clients.
type StreamEvent struct {
	Type      string `json:"type"`                 // stream_start, delta, stream_end, error
	Content   string `json:"content,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	Usage     *Usage `json:"usage,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Usage contains token usage for a completed response.
type Usage struct {
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	StopReason   string `json:"stop_reason"`
}

// Engine orchestrates chat sessions, provider calls, and streaming.
type Engine struct {
	Store     *store.Store
	Providers *provider.Registry
	streams   sync.Map // map[string]chan StreamEvent
}

// NewEngine creates a new chat engine.
func NewEngine(s *store.Store, providers *provider.Registry) *Engine {
	return &Engine{
		Store:     s,
		Providers: providers,
	}
}

// HandleMessage processes an incoming user message: persists it, starts async generation, and
// returns the assistant message ID that the client should use to connect to the SSE stream.
func (e *Engine) HandleMessage(sessionID, content string) (string, error) {
	// Persist user message.
	userMsg := &store.Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		Role:      "user",
		Content:   content,
	}
	if err := e.Store.CreateMessage(userMsg); err != nil {
		return "", fmt.Errorf("create user message: %w", err)
	}

	// Create assistant message ID and stream channel.
	assistantMsgID := uuid.New().String()
	ch := make(chan StreamEvent, 128)
	e.streams.Store(assistantMsgID, ch)

	// Start async generation.
	go e.generateResponse(context.Background(), sessionID, assistantMsgID, content, ch)

	return assistantMsgID, nil
}

// GetStream returns the event channel for a given assistant message ID.
func (e *Engine) GetStream(messageID string) (<-chan StreamEvent, bool) {
	val, ok := e.streams.Load(messageID)
	if !ok {
		return nil, false
	}
	return val.(chan StreamEvent), true
}

// generateResponse loads context, calls the provider, streams events, and saves the result.
func (e *Engine) generateResponse(ctx context.Context, sessionID, assistantMsgID, userContent string, ch chan StreamEvent) {
	defer func() {
		close(ch)
		e.streams.Delete(assistantMsgID)
	}()

	// Load session.
	session, err := e.Store.GetSession(sessionID)
	if err != nil {
		ch <- StreamEvent{Type: "error", Error: fmt.Sprintf("load session: %v", err)}
		return
	}

	// Look up the primary agent from session_agents.
	var agentID, modeName string
	sa, err := e.Store.GetSessionPrimaryAgent(sessionID)
	if err != nil {
		// No session_agent record — auto-create one with the default agent.
		log.Printf("chat: no primary agent for session %s, auto-assigning mentat-001", sessionID)
		if err := e.Store.EnsureSessionAgent(sessionID, "mentat-001", "default", true); err != nil {
			log.Printf("chat: failed to auto-assign agent: %v", err)
		}
		agentID = "mentat-001"
		modeName = "default"
	} else {
		agentID = sa.AgentID
		modeName = sa.Mode
	}

	agent, err := e.Store.GetAgent(agentID)
	if err != nil {
		// Fallback to slug lookup for backwards compatibility.
		agent, err = e.Store.GetAgentBySlug("mentat")
		if err != nil {
			ch <- StreamEvent{Type: "error", Error: fmt.Sprintf("load agent: %v", err)}
			return
		}
	}

	// Load agent mode.
	mode, err := e.Store.GetAgentMode(agent.ID, modeName)
	if err != nil {
		log.Printf("chat: could not load agent mode %s/%s: %v (using base prompt)", agent.ID, modeName, err)
		mode = &store.AgentMode{}
	}

	// Load workspace for context.
	var workspace *store.Workspace
	if session.WorkspaceID != "" {
		workspace, err = e.Store.GetWorkspace(session.WorkspaceID)
		if err != nil {
			log.Printf("chat: could not load workspace %s: %v", session.WorkspaceID, err)
		}
	}

	// Assemble system prompt.
	systemPrompt := assembleSystemPrompt(agent, mode, workspace)

	// Load recent messages.
	messages, err := e.Store.ListMessages(sessionID, 50)
	if err != nil {
		ch <- StreamEvent{Type: "error", Error: fmt.Sprintf("load messages: %v", err)}
		return
	}

	// Convert to provider messages.
	chatMessages := make([]provider.ChatMessage, len(messages))
	for i, m := range messages {
		role := m.Role
		if role == "system" || role == "tool" {
			role = "user" // Anthropic API only accepts user/assistant
		}
		chatMessages[i] = provider.ChatMessage{Role: role, Content: m.Content}
	}

	// Resolve model: session > agent default > global fallback.
	model := session.Model
	if model == "" && agent.DefaultModel != "" {
		model = agent.DefaultModel
	}
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	// Get provider.
	prov, ok := e.Providers.Get("anthropic")
	if !ok {
		ch <- StreamEvent{Type: "error", Error: "anthropic provider not registered"}
		return
	}

	// Emit stream_start.
	ch <- StreamEvent{Type: "stream_start", MessageID: assistantMsgID, AgentID: agent.ID}

	// Call provider.
	provCh, err := prov.StreamChat(ctx, systemPrompt, chatMessages, model)
	if err != nil {
		ch <- StreamEvent{Type: "error", Error: fmt.Sprintf("start stream: %v", err)}
		return
	}

	// Accumulate content and forward events.
	var fullContent strings.Builder
	var finalUsage *Usage

	for evt := range provCh {
		switch evt.Type {
		case "delta":
			fullContent.WriteString(evt.Content)
			ch <- StreamEvent{Type: "delta", Content: evt.Content}
		case "usage":
			if evt.Usage != nil {
				if finalUsage == nil {
					finalUsage = &Usage{}
				}
				if evt.Usage.InputTokens > 0 {
					finalUsage.InputTokens = evt.Usage.InputTokens
				}
				if evt.Usage.OutputTokens > 0 {
					finalUsage.OutputTokens = evt.Usage.OutputTokens
				}
				if evt.Usage.StopReason != "" {
					finalUsage.StopReason = evt.Usage.StopReason
				}
			}
		case "error":
			ch <- StreamEvent{Type: "error", Error: evt.Error}
			return
		case "done":
			// Will handle below.
		}
	}

	// Parse envelopes from the response content.
	responseContent := fullContent.String()
	envelopes, cleanContent := ParseEnvelopes(responseContent)

	var envelopeJSON string
	if len(envelopes) > 0 {
		if data, err := json.Marshal(envelopes); err == nil {
			envelopeJSON = string(data)
		}
	}

	// Save assistant message to DB.
	assistantMsg := &store.Message{
		ID:        assistantMsgID,
		SessionID: sessionID,
		AgentID:   agent.ID,
		Role:      "assistant",
		Content:   cleanContent,
		Envelope:  envelopeJSON,
	}
	if err := e.Store.CreateMessage(assistantMsg); err != nil {
		log.Printf("chat: failed to save assistant message: %v", err)
		ch <- StreamEvent{Type: "error", Error: "failed to save response"}
		return
	}

	// Emit stream_end.
	ch <- StreamEvent{Type: "stream_end", MessageID: assistantMsgID, Usage: finalUsage}

	// Auto-title: if session has no title, generate one asynchronously.
	if session.Title == "" {
		go e.autoTitle(sessionID, userContent, model)
	}
}

// autoTitle generates a title for a session from the first user message.
func (e *Engine) autoTitle(sessionID, userContent, model string) {
	prov, ok := e.Providers.Get("anthropic")
	if !ok {
		return
	}

	prompt := "Generate a concise 3-5 word title for this conversation. Respond with ONLY the title, no quotes or punctuation."
	msgs := []provider.ChatMessage{
		{Role: "user", Content: fmt.Sprintf("First message: %s", userContent)},
	}

	title, err := prov.Complete(context.Background(), prompt, msgs, model)
	if err != nil {
		log.Printf("chat: auto-title failed: %v", err)
		return
	}

	title = strings.TrimSpace(title)
	if title == "" {
		return
	}

	sess, err := e.Store.GetSession(sessionID)
	if err != nil {
		log.Printf("chat: auto-title get session failed: %v", err)
		return
	}
	sess.Title = title
	if err := e.Store.UpdateSession(sess); err != nil {
		log.Printf("chat: auto-title update failed: %v", err)
	}
}
