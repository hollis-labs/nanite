package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/mentat-chat/internal/mcp"
	"github.com/hollis-labs/mentat-chat/internal/provider"
	"github.com/hollis-labs/mentat-chat/internal/store"
	"github.com/hollis-labs/mentat-chat/internal/truncate"
)

// maxToolIterations prevents infinite tool-use loops.
const maxToolIterations = 10


// StreamEvent is the event sent to SSE clients.
type StreamEvent struct {
	Type      string `json:"type"`                 // stream_start, delta, stream_end, error, tool_call, tool_result
	Content   string `json:"content,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	Usage     *Usage `json:"usage,omitempty"`
	Error     string `json:"error,omitempty"`
	Tool      string `json:"tool,omitempty"`      // tool name for tool_call/tool_result
	ToolID    string `json:"tool_id,omitempty"`    // tool_use_id
	Summary   string `json:"summary,omitempty"`    // tool result summary
}

// Usage contains token usage for a completed response.
type Usage struct {
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	StopReason   string `json:"stop_reason"`
}

// Engine orchestrates chat sessions, provider calls, and streaming.
type Engine struct {
	Store      *store.Store
	Providers  *provider.Registry
	Broker     *ContextBroker
	MCPManager *mcp.Manager
	streams    sync.Map // map[string]chan StreamEvent
}

// NewEngine creates a new chat engine.
func NewEngine(s *store.Store, providers *provider.Registry) *Engine {
	return &Engine{
		Store:     s,
		Providers: providers,
		Broker:    NewContextBroker(s),
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

	// Assemble context via broker.
	systemPrompt, chatMessages, err := e.Broker.AssembleContext(session, agent, mode, workspace)
	if err != nil {
		ch <- StreamEvent{Type: "error", Error: fmt.Sprintf("assemble context: %v", err)}
		return
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

	// Get available tools from MCP manager.
	// TODO: detect intent from user message and call GetToolsForIntent
	var tools []provider.ToolDefinition
	if e.MCPManager != nil && e.MCPManager.HasTools() {
		tools = e.MCPManager.GetTools()
	}

	// Tool-use loop: call the provider, handle tool calls, repeat.
	var fullContent strings.Builder
	var finalUsage *Usage

	for iteration := 0; iteration < maxToolIterations; iteration++ {
		// Call provider with or without tools.
		var provCh <-chan provider.StreamEvent
		if len(tools) > 0 {
			// Estimate context size for debugging.
			var contextChars int
			for _, m := range chatMessages {
				contextChars += len(m.Content)
				for _, b := range m.ContentBlocks {
					contextChars += len(b.Text) + len(b.Content)
				}
			}
			contextChars += len(systemPrompt)
			log.Printf("chat: tool-use iteration %d — %d tools, %d messages, ~%d context chars (~%d tokens)", iteration, len(tools), len(chatMessages), contextChars, contextChars/4)
			provCh, err = prov.StreamChatWithTools(ctx, systemPrompt, chatMessages, model, tools)
		} else {
			provCh, err = prov.StreamChat(ctx, systemPrompt, chatMessages, model)
		}
		if err != nil {
			log.Printf("chat: provider stream error on iteration %d: %v", iteration, err)
			e.Store.LogEvent(sessionID, "provider_error", "error",
				fmt.Sprintf("iteration %d: %v", iteration, err),
				fmt.Sprintf(`{"model":%q,"tools":%d,"messages":%d}`, model, len(tools), len(chatMessages)))
			ch <- StreamEvent{Type: "error", Error: fmt.Sprintf("start stream: %v", err)}
			return
		}

		// Accumulate content and tool_use blocks from this turn.
		var turnContent strings.Builder
		var toolUseBlocks []provider.ToolUseBlock
		var stopReason string

		for evt := range provCh {
			switch evt.Type {
			case "delta":
				turnContent.WriteString(evt.Content)
				fullContent.WriteString(evt.Content)
				ch <- StreamEvent{Type: "delta", Content: evt.Content}
			case "tool_use":
				if evt.ToolUse != nil {
					toolUseBlocks = append(toolUseBlocks, *evt.ToolUse)
				}
			case "usage":
				if evt.Usage != nil {
					if finalUsage == nil {
						finalUsage = &Usage{}
					}
					if evt.Usage.InputTokens > 0 {
						finalUsage.InputTokens += evt.Usage.InputTokens
					}
					if evt.Usage.OutputTokens > 0 {
						finalUsage.OutputTokens += evt.Usage.OutputTokens
					}
					if evt.Usage.StopReason != "" {
						stopReason = evt.Usage.StopReason
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

		// If no tool use, we are done.
		if stopReason != "tool_use" || len(toolUseBlocks) == 0 {
			break
		}

		// Build the assistant message with content blocks (text + tool_use).
		var assistantBlocks []provider.ContentBlock
		if text := turnContent.String(); text != "" {
			assistantBlocks = append(assistantBlocks, provider.ContentBlock{
				Type: "text",
				Text: text,
			})
		}
		for _, tu := range toolUseBlocks {
			input := tu.Input
			if input == nil {
				input = map[string]any{}
			}
			assistantBlocks = append(assistantBlocks, provider.ContentBlock{
				Type:  "tool_use",
				ID:    tu.ID,
				Name:  tu.Name,
				Input: &input,
			})
		}
		chatMessages = append(chatMessages, provider.ChatMessage{
			Role:          "assistant",
			ContentBlocks: assistantBlocks,
		})

		// Execute each tool and build tool_result blocks.
		var resultBlocks []provider.ContentBlock
		for _, tu := range toolUseBlocks {
			// Emit tool_call event to the client.
			ch <- StreamEvent{
				Type:   "tool_call",
				Tool:   tu.Name,
				ToolID: tu.ID,
			}

			var resultText string
			if e.MCPManager != nil {
				result, execErr := e.MCPManager.ExecuteTool(ctx, tu.Name, tu.Input)
				if execErr != nil {
					resultText = fmt.Sprintf("Error: %v", execErr)
					log.Printf("chat: tool %s failed: %v", tu.Name, execErr)
					e.Store.LogEvent(sessionID, "tool_error", "error",
						fmt.Sprintf("%s: %v", tu.Name, execErr), "{}")
				} else {
					resultText = result
					e.Store.LogEvent(sessionID, "tool_call", "tool",
						tu.Name, fmt.Sprintf(`{"result_len":%d}`, len(result)))
				}
			} else {
				resultText = "Error: no MCP manager configured"
			}

			// Truncate for the LLM context; save full output to disk if large.
			tr := truncate.Output(resultText, tu.Name)

			// Emit tool_result event to the client (use full result for UI summary).
			summary := resultText
			if len(summary) > 500 {
				summary = summary[:500] + "... (truncated)"
			}
			ch <- StreamEvent{
				Type:    "tool_result",
				Tool:    tu.Name,
				ToolID:  tu.ID,
				Summary: summary,
			}

			if tr.Truncated {
				log.Printf("chat: tool %s result truncated: %d → %d chars (saved to %s)",
					tu.Name, tr.OriginalLen, len(tr.Content), tr.OutputPath)
				e.Store.LogEvent(sessionID, "tool_truncated", "context",
					tu.Name, fmt.Sprintf(`{"original_len":%d,"truncated_len":%d,"output_path":%q}`,
						tr.OriginalLen, len(tr.Content), tr.OutputPath))
			}

			resultBlocks = append(resultBlocks, provider.ContentBlock{
				Type:      "tool_result",
				ToolUseID: tu.ID,
				Content:   tr.Content,
			})
		}

		// Append tool results as a user message.
		chatMessages = append(chatMessages, provider.ChatMessage{
			Role:          "user",
			ContentBlocks: resultBlocks,
		})

		// Brief pause between iterations to avoid rate limit spikes.
		if iteration > 0 {
			time.Sleep(1 * time.Second)
		}

		// Loop back for the next provider call.
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

	// Prune old tool messages after saving.
	if err := e.Broker.PruneAfterTurn(sessionID); err != nil {
		log.Printf("chat: prune after turn failed: %v", err)
	}

	// Record token usage.
	if finalUsage != nil && (finalUsage.InputTokens > 0 || finalUsage.OutputTokens > 0) {
		if err := e.Store.RecordUsage(sessionID, assistantMsgID, model, finalUsage.InputTokens, finalUsage.OutputTokens); err != nil {
			log.Printf("chat: failed to record token usage: %v", err)
		}
	}

	// Emit stream_end.
	ch <- StreamEvent{Type: "stream_end", MessageID: assistantMsgID, Usage: finalUsage}

	// Auto-title: if session has no title, generate one asynchronously.
	if session.Title == "" {
		go e.autoTitle(sessionID, userContent, model)
	}
}

// SendAgentMessage allows one agent session to send a message to another session.
// The message is stored with the sending agent's ID and processed as if from a user
// but with agent attribution.
func (e *Engine) SendAgentMessage(fromSessionID, toSessionID, content string) (string, error) {
	// Look up the sending agent.
	var fromAgentID string
	sa, err := e.Store.GetSessionPrimaryAgent(fromSessionID)
	if err != nil {
		fromAgentID = "unknown"
	} else {
		fromAgentID = sa.AgentID
	}

	// Create the message in the target session with agent attribution.
	msg := &store.Message{
		ID:        uuid.New().String(),
		SessionID: toSessionID,
		AgentID:   fromAgentID,
		Role:      "user",
		Content:   content,
		Metadata:  fmt.Sprintf(`{"source":"agent","from_session":"%s","from_agent":"%s"}`, fromSessionID, fromAgentID),
	}
	if err := e.Store.CreateMessage(msg); err != nil {
		return "", fmt.Errorf("create agent message: %w", err)
	}

	// Start async generation in the target session.
	assistantMsgID := uuid.New().String()
	ch := make(chan StreamEvent, 128)
	e.streams.Store(assistantMsgID, ch)
	go e.generateResponse(context.Background(), toSessionID, assistantMsgID, content, ch)

	return assistantMsgID, nil
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
