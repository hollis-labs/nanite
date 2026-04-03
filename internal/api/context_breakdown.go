package api

import (
	"fmt"
	"net/http"

	"github.com/hollis-labs/conduit/internal/chat"
)

// MessageTokenDetail holds per-message token information.
type MessageTokenDetail struct {
	ID             string `json:"id"`
	Role           string `json:"role"`
	ContentPreview string `json:"content_preview"`
	Tokens         int    `json:"tokens"`
	IsCompacted    bool   `json:"is_compacted"`
}

// ToolTokenDetail holds per-tool token information.
type ToolTokenDetail struct {
	Name   string `json:"name"`
	Tokens int    `json:"tokens"`
}

// ContextBreakdownResponse is the full context breakdown for a session.
type ContextBreakdownResponse struct {
	SystemPromptTokens  int                  `json:"system_prompt_tokens"`
	SystemPromptPreview string               `json:"system_prompt_preview"`
	Messages            []MessageTokenDetail `json:"messages"`
	MessageTokensTotal  int                  `json:"message_tokens_total"`
	Tools               []ToolTokenDetail    `json:"tools"`
	ToolTokensTotal     int                  `json:"tool_tokens_total"`
	ToolsAvailable      int                  `json:"tools_available"`
	Total               int                  `json:"total"`
	Ceiling             int                  `json:"ceiling"`
	EstimatedCostUSD    float64              `json:"estimated_cost_usd"`
}

func (a *API) handleGetContextBreakdown(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	// Get messages for the session.
	messages, err := a.Services.Store.ListMessages(sessionID, 200)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Build per-message breakdown.
	msgDetails := make([]MessageTokenDetail, len(messages))
	msgTokensTotal := 0
	for i, m := range messages {
		tokens := chat.EstimateTokens(m.Content)
		preview := m.Content
		if len(preview) > 120 {
			preview = preview[:120] + "..."
		}
		msgDetails[i] = MessageTokenDetail{
			ID:             m.ID,
			Role:           m.Role,
			ContentPreview: preview,
			Tokens:         tokens,
			IsCompacted:    m.IsCompacted,
		}
		msgTokensTotal += tokens
	}

	// Get the session's agent and its system prompt.
	systemPrompt := ""
	systemTokens := 500 // base estimate
	if a.Services.Store != nil {
		session, err := a.Services.Store.GetSession(sessionID)
		if err == nil && session != nil {
			agents, err := a.Services.Store.ListSessionAgents(session.ID)
			if err == nil && len(agents) > 0 {
				agent, err := a.Services.Store.GetAgent(agents[0].AgentID)
				if err == nil && agent != nil {
					systemPrompt = agent.SystemPrompt
					systemTokens = chat.EstimateTokens(systemPrompt)
				}
			}
		}
	}

	// Count tool calls from the event log (tool results aren't stored as messages).
	toolCallCount := a.Services.Store.CountSessionToolCalls(sessionID)
	toolDetails := make([]ToolTokenDetail, 0)
	toolTokensTotal := 0
	if toolCallCount > 0 {
		// Estimate ~50 tokens per tool call for input/output overhead.
		toolTokensTotal = toolCallCount * 50
		toolDetails = append(toolDetails, ToolTokenDetail{
			Name:   fmt.Sprintf("%d tool calls", toolCallCount),
			Tokens: toolTokensTotal,
		})
	}

	// Total available tools (for display, not context cost).
	toolsAvailable := 0
	if a.Services.ToolClient != nil {
		toolsAvailable = len(a.Services.ToolClient.ListTools())
	}

	// System prompt preview for the inspector.
	systemPreview := systemPrompt
	if len(systemPreview) > 500 {
		systemPreview = systemPreview[:500] + "..."
	}

	ceiling := int(float64(chat.DefaultContextWindow) * chat.HardCeilingPct)
	total := systemTokens + msgTokensTotal + toolTokensTotal

	// Get cost from usage summary.
	costUSD := 0.0
	usage, err := a.Services.Store.GetSessionUsage(sessionID)
	if err == nil && usage != nil {
		costUSD = usage.EstimatedCostUSD
	}

	resp := ContextBreakdownResponse{
		SystemPromptTokens:  systemTokens,
		SystemPromptPreview: systemPreview,
		Messages:            msgDetails,
		MessageTokensTotal:  msgTokensTotal,
		Tools:               toolDetails,
		ToolTokensTotal:     toolTokensTotal,
		ToolsAvailable:      toolsAvailable,
		Total:               total,
		Ceiling:             ceiling,
		EstimatedCostUSD:    costUSD,
	}

	a.jsonResp(w, http.StatusOK, resp)
}
