package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/nanite/internal/usagecost"
	"github.com/hollis-labs/nanite/pkg/models"
	ledger "github.com/hollis-labs/substrate/llm-core/usageledger"
)

// TokenUsage represents a single token usage record.
type TokenUsage struct {
	Provider            string  `json:"provider"`
	ReasoningTokens     int     `json:"reasoning_tokens"`
	CostStatus          string  `json:"cost_status"`
	CostSnapshot        string  `json:"cost_snapshot"`
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
	ReasoningTokens     int     `json:"reasoning_tokens"`
	PartialRows         int     `json:"partial_rows"`
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
	PartialRows      int     `json:"partial_rows"`
	Model            string  `json:"model"`
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
}

// UsageSummary is the global usage summary across all sessions.
type UsageSummary struct {
	PartialRows int          `json:"partial_rows"`
	TotalInput  int          `json:"total_input"`
	TotalOutput int          `json:"total_output"`
	TotalTokens int          `json:"total_tokens"`
	TotalCost   float64      `json:"total_cost"`
	ByModel     []ModelUsage `json:"by_model"`
}

// RecordUsage is the compatibility entry point for callers without provider
// presence metadata. Its snapshot is partial; zeroes cannot be treated as reported.
func (s *Store) RecordUsage(ctx context.Context, sessionID, messageID, model string, inputTokens, outputTokens, toolInputTokens, cacheCreationTokens, cacheReadTokens int) error {
	provider := ""
	if m, ok := models.ByModelID(model); ok {
		provider = m.Provider
	}
	row := usagecost.NewRow(nil, provider, model)
	ptr := func(n int) *int64 {
		if n <= 0 {
			return nil
		}
		v := int64(n)
		return &v
	}
	row.Usage = (usagecost.Report{Input: ptr(inputTokens), Output: ptr(outputTokens), CacheWrite: ptr(cacheCreationTokens), CacheRead: ptr(cacheReadTokens)}).Usage(provider)
	return s.RecordUsageSnapshot(ctx, sessionID, messageID, model, inputTokens, outputTokens, toolInputTokens, cacheCreationTokens, cacheReadTokens, []ledger.Row{row})
}

// RecordUsageSnapshot commits the cost and its per-call component/price evidence
// together. Readers aggregate persisted costs and never consult the live catalog.
func (s *Store) RecordUsageSnapshot(ctx context.Context, sessionID, messageID, model string, inputTokens, outputTokens, toolInputTokens, cacheCreationTokens, cacheReadTokens int, calls []ledger.Row) error {
	for i := range calls {
		calls[i].SessionID = sessionID
		calls[i].MessageID = messageID
	}
	snapshot, cost, err := usagecost.Freeze(calls)
	if err != nil {
		return fmt.Errorf("validate usage snapshot: %w", err)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("marshal usage snapshot: %w", err)
	}
	var reasoning, total int64
	provider := ""
	for _, row := range calls {
		reasoning += row.Usage.ReasoningTokens.Tokens
		total += row.Usage.TotalTokens()
		if provider == "" {
			provider = row.Provider
		}
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO token_usage (session_id,message_id,model,input_tokens,output_tokens,total_tokens,tool_input_tokens,cache_creation_tokens,cache_read_tokens,estimated_cost_usd,provider,reasoning_tokens,cost_status,cost_snapshot)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, sessionID, messageID, model, inputTokens, outputTokens, total, toolInputTokens, cacheCreationTokens, cacheReadTokens, cost, provider, reasoning, snapshot.Status, string(data))
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
		        COALESCE(SUM(estimated_cost_usd),0), COUNT(*), COALESCE(SUM(reasoning_tokens),0), COALESCE(SUM(CASE WHEN cost_status = 'PARTIAL' THEN 1 ELSE 0 END),0)
		 FROM token_usage WHERE session_id = ?`,
		sessionID,
	).Scan(&summary.InputTokens, &summary.OutputTokens, &summary.TotalTokens,
		&summary.ToolInputTokens, &summary.CacheCreationTokens, &summary.CacheReadTokens,
		&summary.EstimatedCostUSD, &summary.MessageCount, &summary.ReasoningTokens, &summary.PartialRows)
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
		        COALESCE(SUM(total_tokens),0), COALESCE(SUM(estimated_cost_usd),0), COALESCE(SUM(CASE WHEN cost_status = 'PARTIAL' THEN 1 ELSE 0 END),0)
		 FROM token_usage`,
	).Scan(&summary.TotalInput, &summary.TotalOutput, &summary.TotalTokens, &summary.TotalCost, &summary.PartialRows)
	if err != nil {
		return nil, fmt.Errorf("get usage summary totals: %w", err)
	}

	// Per-model breakdown.
	rows, err := s.DB.QueryContext(ctx,
		`SELECT model, SUM(input_tokens), SUM(output_tokens), SUM(total_tokens), SUM(estimated_cost_usd), SUM(CASE WHEN cost_status = 'PARTIAL' THEN 1 ELSE 0 END)
		 FROM token_usage GROUP BY model ORDER BY SUM(total_tokens) DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("get usage summary by model: %w", err)
	}
	defer closeRows(rows)

	summary.ByModel = make([]ModelUsage, 0)
	for rows.Next() {
		var mu ModelUsage
		if err := rows.Scan(&mu.Model, &mu.InputTokens, &mu.OutputTokens, &mu.TotalTokens, &mu.EstimatedCostUSD, &mu.PartialRows); err != nil {
			return nil, fmt.Errorf("scan model usage: %w", err)
		}
		summary.ByModel = append(summary.ByModel, mu)
	}
	return &summary, rows.Err()
}
