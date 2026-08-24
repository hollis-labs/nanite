package store

import (
	"context"
	"fmt"

	"github.com/hollis-labs/nanite/pkg/models"
)

// estimateCost returns the estimated cost in USD for the given model and
// token counts. Pricing is sourced from pkg/models (single source of truth);
// historical model IDs are represented as IsLegacy rows there so usage
// records for archived sessions remain numerically accurate.
func estimateCost(model string, inputTokens, outputTokens int) float64 {
	inputPerM, outputPerM := models.Pricing(model)
	inputCost := float64(inputTokens) * inputPerM / 1_000_000
	outputCost := float64(outputTokens) * outputPerM / 1_000_000
	return inputCost + outputCost
}

// TokenUsage represents a single token usage record.
type TokenUsage struct {
	ID                  int64   `json:"id"`
	SessionID           string  `json:"session_id"`
	MessageID           string  `json:"message_id"`
	Model               string  `json:"model"`
	InputTokens         int     `json:"input_tokens"`
	OutputTokens        int     `json:"output_tokens"`
	TotalTokens         int     `json:"total_tokens"`
	ToolInputTokens     int     `json:"tool_input_tokens"`
	CacheCreationTokens int     `json:"cache_creation_tokens"`
	CacheReadTokens     int     `json:"cache_read_tokens"`
	EstimatedCostUSD    float64 `json:"estimated_cost_usd"`
	CreatedAt           string  `json:"created_at"`
}

// SessionUsageSummary is the aggregate usage for a single session.
type SessionUsageSummary struct {
	InputTokens         int     `json:"input_tokens"`
	OutputTokens        int     `json:"output_tokens"`
	TotalTokens         int     `json:"total_tokens"`
	ToolInputTokens     int     `json:"tool_input_tokens"`
	CacheCreationTokens int     `json:"cache_creation_tokens"`
	CacheReadTokens     int     `json:"cache_read_tokens"`
	EstimatedCostUSD    float64 `json:"estimated_cost_usd"`
	MessageCount        int     `json:"message_count"`
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
func (s *Store) RecordUsage(ctx context.Context, sessionID, messageID, model string, inputTokens, outputTokens, toolInputTokens, cacheCreationTokens, cacheReadTokens int) error {
	totalTokens := inputTokens + outputTokens
	cost := estimateCost(model, inputTokens, outputTokens)

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO token_usage (session_id, message_id, model, input_tokens, output_tokens, total_tokens, tool_input_tokens, cache_creation_tokens, cache_read_tokens, estimated_cost_usd)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, messageID, model, inputTokens, outputTokens, totalTokens, toolInputTokens, cacheCreationTokens, cacheReadTokens, cost,
	)
	if err != nil {
		return fmt.Errorf("record usage: %w", err)
	}
	return nil
}

// GetSessionUsage returns aggregate token usage for a session.
func (s *Store) GetSessionUsage(ctx context.Context, sessionID string) (*SessionUsageSummary, error) {
	var summary SessionUsageSummary
	err := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		        COALESCE(SUM(total_tokens),0), COALESCE(SUM(tool_input_tokens),0),
		        COALESCE(SUM(cache_creation_tokens),0), COALESCE(SUM(cache_read_tokens),0),
		        COALESCE(SUM(estimated_cost_usd),0), COUNT(*)
		 FROM token_usage WHERE session_id = ?`,
		sessionID,
	).Scan(&summary.InputTokens, &summary.OutputTokens, &summary.TotalTokens,
		&summary.ToolInputTokens, &summary.CacheCreationTokens, &summary.CacheReadTokens,
		&summary.EstimatedCostUSD, &summary.MessageCount)
	if err != nil {
		return nil, fmt.Errorf("get session usage %s: %w", sessionID, err)
	}
	return &summary, nil
}

// GetUsageSummary returns global token usage totals and per-model breakdown.
func (s *Store) GetUsageSummary(ctx context.Context) (*UsageSummary, error) {
	var summary UsageSummary

	// Global totals.
	err := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		        COALESCE(SUM(total_tokens),0), COALESCE(SUM(estimated_cost_usd),0)
		 FROM token_usage`,
	).Scan(&summary.TotalInput, &summary.TotalOutput, &summary.TotalTokens, &summary.TotalCost)
	if err != nil {
		return nil, fmt.Errorf("get usage summary totals: %w", err)
	}

	// Per-model breakdown.
	rows, err := s.DB.QueryContext(ctx,
		`SELECT model, SUM(input_tokens), SUM(output_tokens), SUM(total_tokens), SUM(estimated_cost_usd)
		 FROM token_usage GROUP BY model ORDER BY SUM(total_tokens) DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("get usage summary by model: %w", err)
	}
	defer closeRows(rows)

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
