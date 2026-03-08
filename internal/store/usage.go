package store

import "fmt"

// Model pricing: cost per million tokens (input, output) in USD.
var modelPricing = map[string][2]float64{
	// Anthropic
	"claude-sonnet-4-20250514":    {3.0, 15.0},
	"claude-opus-4-20250514":      {15.0, 75.0},
	"claude-haiku-3-20250307":     {0.25, 1.25},
	"claude-3-5-sonnet-20241022":  {3.0, 15.0},
	"claude-3-5-haiku-20241022":   {1.0, 5.0},
	"claude-3-opus-20240229":      {15.0, 75.0},
	"claude-3-sonnet-20240229":    {3.0, 15.0},
	"claude-3-haiku-20240307":     {0.25, 1.25},
	// OpenAI
	"gpt-4o":      {2.5, 10.0},
	"gpt-4o-mini": {0.15, 0.60},
	"gpt-4-turbo": {10.0, 30.0},
}

// estimateCost returns the estimated cost in USD for the given model and token counts.
func estimateCost(model string, inputTokens, outputTokens int) float64 {
	pricing, ok := modelPricing[model]
	if !ok {
		// Default to Sonnet pricing if model unknown.
		pricing = [2]float64{3.0, 15.0}
	}
	inputCost := float64(inputTokens) * pricing[0] / 1_000_000
	outputCost := float64(outputTokens) * pricing[1] / 1_000_000
	return inputCost + outputCost
}

// TokenUsage represents a single token usage record.
type TokenUsage struct {
	ID               int64   `json:"id"`
	SessionID        string  `json:"session_id"`
	MessageID        string  `json:"message_id"`
	Model            string  `json:"model"`
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
	CreatedAt        string  `json:"created_at"`
}

// SessionUsageSummary is the aggregate usage for a single session.
type SessionUsageSummary struct {
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
	MessageCount     int     `json:"message_count"`
}

// ModelUsage is usage broken down by model.
type ModelUsage struct {
	Model            string  `json:"model"`
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
}

// UsageSummary is the global usage summary across all sessions.
type UsageSummary struct {
	TotalInput  int          `json:"total_input"`
	TotalOutput int          `json:"total_output"`
	TotalTokens int          `json:"total_tokens"`
	TotalCost   float64      `json:"total_cost"`
	ByModel     []ModelUsage `json:"by_model"`
}

// RecordUsage inserts a token usage record for a completed response.
func (s *Store) RecordUsage(sessionID, messageID, model string, inputTokens, outputTokens int) error {
	totalTokens := inputTokens + outputTokens
	cost := estimateCost(model, inputTokens, outputTokens)

	_, err := s.DB.Exec(
		`INSERT INTO token_usage (session_id, message_id, model, input_tokens, output_tokens, total_tokens, estimated_cost_usd)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sessionID, messageID, model, inputTokens, outputTokens, totalTokens, cost,
	)
	if err != nil {
		return fmt.Errorf("record usage: %w", err)
	}
	return nil
}

// GetSessionUsage returns aggregate token usage for a session.
func (s *Store) GetSessionUsage(sessionID string) (*SessionUsageSummary, error) {
	var summary SessionUsageSummary
	err := s.DB.QueryRow(
		`SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		        COALESCE(SUM(total_tokens),0), COALESCE(SUM(estimated_cost_usd),0),
		        COUNT(*)
		 FROM token_usage WHERE session_id = ?`,
		sessionID,
	).Scan(&summary.InputTokens, &summary.OutputTokens, &summary.TotalTokens,
		&summary.EstimatedCostUSD, &summary.MessageCount)
	if err != nil {
		return nil, fmt.Errorf("get session usage %s: %w", sessionID, err)
	}
	return &summary, nil
}

// GetUsageSummary returns global token usage totals and per-model breakdown.
func (s *Store) GetUsageSummary() (*UsageSummary, error) {
	var summary UsageSummary

	// Global totals.
	err := s.DB.QueryRow(
		`SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		        COALESCE(SUM(total_tokens),0), COALESCE(SUM(estimated_cost_usd),0)
		 FROM token_usage`,
	).Scan(&summary.TotalInput, &summary.TotalOutput, &summary.TotalTokens, &summary.TotalCost)
	if err != nil {
		return nil, fmt.Errorf("get usage summary totals: %w", err)
	}

	// Per-model breakdown.
	rows, err := s.DB.Query(
		`SELECT model, SUM(input_tokens), SUM(output_tokens), SUM(total_tokens), SUM(estimated_cost_usd)
		 FROM token_usage GROUP BY model ORDER BY SUM(total_tokens) DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("get usage summary by model: %w", err)
	}
	defer rows.Close()

	summary.ByModel = make([]ModelUsage, 0)
	for rows.Next() {
		var mu ModelUsage
		if err := rows.Scan(&mu.Model, &mu.InputTokens, &mu.OutputTokens, &mu.TotalTokens, &mu.EstimatedCostUSD); err != nil {
			return nil, fmt.Errorf("scan model usage: %w", err)
		}
		summary.ByModel = append(summary.ByModel, mu)
	}
	return &summary, rows.Err()
}
