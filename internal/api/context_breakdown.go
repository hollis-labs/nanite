package api

import (
	"net/http"

	"github.com/hollis-labs/mentat-chat/internal/chat"
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
	SystemPromptTokens int                  `json:"system_prompt_tokens"`
	Messages           []MessageTokenDetail `json:"messages"`
	MessageTokensTotal int                  `json:"message_tokens_total"`
	Tools              []ToolTokenDetail    `json:"tools"`
	ToolTokensTotal    int                  `json:"tool_tokens_total"`
	Total              int                  `json:"total"`
	Ceiling            int                  `json:"ceiling"`
	EstimatedCostUSD   float64              `json:"estimated_cost_usd"`
}

func (a *API) handleGetContextBreakdown(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	// Get messages for the session.
	messages, err := a.Store.ListMessages(sessionID, 200)
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

	// Get tools if tool broker is available.
	toolDetails := make([]ToolTokenDetail, 0)
	toolTokensTotal := 0
	if a.ToolBroker != nil && a.ToolBroker.MCPManager != nil {
		allTools := a.ToolBroker.MCPManager.GetAllTools()
		for _, t := range allTools {
			tokens := chat.EstimateTokens(t.Name + t.Description)
			if tokens < 4 {
				tokens = 4
			}
			toolDetails = append(toolDetails, ToolTokenDetail{
				Name:   t.Name,
				Tokens: tokens,
			})
			toolTokensTotal += tokens
		}
	}

	// Estimate system prompt tokens (rough: we don't have the assembled prompt here,
	// but we can give an estimate based on typical system prompt size).
	// Use a reasonable default estimate.
	systemTokens := 500 // base estimate
	if a.Engine != nil {
		// Try to get a better estimate from the session's agent config.
		session, err := a.Store.GetSession(sessionID)
		if err == nil && session != nil {
			agents, err := a.Store.ListSessionAgents(session.ID)
			if err == nil && len(agents) > 0 {
				agent, err := a.Store.GetAgent(agents[0].AgentID)
				if err == nil && agent != nil {
					systemTokens = chat.EstimateTokens(agent.SystemPrompt)
				}
			}
		}
	}

	ceiling := int(float64(chat.DefaultContextWindow) * chat.HardCeilingPct)
	total := systemTokens + msgTokensTotal + toolTokensTotal

	// Get cost from usage summary.
	costUSD := 0.0
	usage, err := a.Store.GetSessionUsage(sessionID)
	if err == nil && usage != nil {
		costUSD = usage.EstimatedCostUSD
	}

	resp := ContextBreakdownResponse{
		SystemPromptTokens: systemTokens,
		Messages:           msgDetails,
		MessageTokensTotal: msgTokensTotal,
		Tools:              toolDetails,
		ToolTokensTotal:    toolTokensTotal,
		Total:              total,
		Ceiling:            ceiling,
		EstimatedCostUSD:   costUSD,
	}

	a.jsonResp(w, http.StatusOK, resp)
}
