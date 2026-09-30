package api

import (
	"fmt"
	"net/http"

	"github.com/hollis-labs/nanite/internal/chat"
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
	messages, err := a.Services.Sessions.ListMessages(r.Context(), sessionID, 200)
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
	if a.Services.Sessions != nil {
		session, getErr := a.Services.Sessions.Get(r.Context(), sessionID)
		if getErr == nil && session != nil {
			agents, listErr := a.Services.AgentMembership.ListSessionAgents(r.Context(), session.ID)
			if listErr == nil && len(agents) > 0 {
				agent, agentErr := a.Services.Agents.Get(r.Context(), agents[0].AgentID)
				if agentErr == nil && agent != nil {
					systemPrompt = agent.SystemPrompt
					systemTokens = chat.EstimateTokens(systemPrompt)
				}
			}
		}
	}

	// Tool token cost: actual when execution metrics recorded it, else an
	// event-log estimate — see UsageService.SessionToolTokens.
	toolDetails := make([]ToolTokenDetail, 0)
	toolTokens := a.Services.Usage.SessionToolTokens(r.Context(), sessionID)
	toolTokensTotal := toolTokens.Tokens
	if toolTokens.Calls > 0 {
		kind := "actual"
		if toolTokens.Estimated {
			kind = "estimated"
		}
		toolDetails = append(toolDetails, ToolTokenDetail{
			Name:   fmt.Sprintf("%d tool calls (%s)", toolTokens.Calls, kind),
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
	usage, err := a.Services.Usage.SessionUsage(r.Context(), sessionID)
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
