package store

import (
	"context"
	"fmt"
)

// ExecutionMetrics captures the full context of an LLM call for observability.
type ExecutionMetrics struct {
	ID                  int64   `json:"id"`
	SessionID           string  `json:"session_id"`
	MessageID           string  `json:"message_id"`
	Provider            string  `json:"provider"`
	Adapter             string  `json:"adapter"` // "http", "pty", "sub"
	Model               string  `json:"model"`
	AgentID             string  `json:"agent_id"`
	AgentSlug           string  `json:"agent_slug"`
	Mode                string  `json:"mode"`
	DurationMs          int64   `json:"duration_ms"`
	ContextMessages     int     `json:"context_messages"`
	ContextTokens       int     `json:"context_tokens"`
	InputTokens         int     `json:"input_tokens"`
	OutputTokens        int     `json:"output_tokens"`
	CacheCreationTokens int     `json:"cache_creation_tokens"`
	CacheReadTokens     int     `json:"cache_read_tokens"`
	EstimatedCostUSD    float64 `json:"estimated_cost_usd"`
	ToolIterations      int     `json:"tool_iterations"`
	ToolCalls           int     `json:"tool_calls"`
	IsUtility           bool    `json:"is_utility"`
	StopReason          string  `json:"stop_reason"`
	Error               string  `json:"error"`
	DebugSnapshots      string  `json:"debug_snapshots,omitempty"` // JSON blob of TurnSnapshot[]
	CreatedAt           string  `json:"created_at"`
}

// UtilityCallSummary aggregates utility call metrics by provider+model for comparison.
type UtilityCallSummary struct {
	Provider    string  `json:"provider"`
	Model       string  `json:"model"`
	CallType    string  `json:"call_type"` // "autoTitle", "autoTags", etc.
	CallCount   int     `json:"call_count"`
	AvgDuration int64   `json:"avg_duration_ms"`
	MinDuration int64   `json:"min_duration_ms"`
	MaxDuration int64   `json:"max_duration_ms"`
	ErrorCount  int     `json:"error_count"`
	TotalCost   float64 `json:"total_cost_usd"`
}

const executionMetricsCols = `id, session_id, message_id, provider, adapter, model,
	agent_id, agent_slug, mode, duration_ms,
	context_messages, context_tokens, input_tokens, output_tokens,
	cache_creation_tokens, cache_read_tokens, estimated_cost_usd,
	tool_iterations, tool_calls, is_utility, stop_reason, error,
	COALESCE(debug_snapshots, '') AS debug_snapshots, created_at`

func scanExecutionMetrics(rows interface{ Scan(...any) error }) (ExecutionMetrics, error) {
	var m ExecutionMetrics
	err := rows.Scan(
		&m.ID, &m.SessionID, &m.MessageID, &m.Provider, &m.Adapter, &m.Model,
		&m.AgentID, &m.AgentSlug, &m.Mode, &m.DurationMs,
		&m.ContextMessages, &m.ContextTokens, &m.InputTokens, &m.OutputTokens,
		&m.CacheCreationTokens, &m.CacheReadTokens, &m.EstimatedCostUSD,
		&m.ToolIterations, &m.ToolCalls, &m.IsUtility, &m.StopReason, &m.Error,
		&m.DebugSnapshots, &m.CreatedAt,
	)
	return m, err
}

// RecordExecutionMetrics inserts an execution metrics record.
func (s *Store) RecordExecutionMetrics(ctx context.Context, m *ExecutionMetrics) error {
	m.EstimatedCostUSD = estimateCost(m.Model, m.InputTokens, m.OutputTokens)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO execution_metrics
			(session_id, message_id, provider, adapter, model,
			 agent_id, agent_slug, mode, duration_ms,
			 context_messages, context_tokens, input_tokens, output_tokens,
			 cache_creation_tokens, cache_read_tokens, estimated_cost_usd,
			 tool_iterations, tool_calls, is_utility, stop_reason, error,
			 debug_snapshots)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.SessionID, m.MessageID, m.Provider, m.Adapter, m.Model,
		m.AgentID, m.AgentSlug, m.Mode, m.DurationMs,
		m.ContextMessages, m.ContextTokens, m.InputTokens, m.OutputTokens,
		m.CacheCreationTokens, m.CacheReadTokens, m.EstimatedCostUSD,
		m.ToolIterations, m.ToolCalls, m.IsUtility, m.StopReason, m.Error,
		m.DebugSnapshots,
	)
	if err != nil {
		return fmt.Errorf("record execution metrics: %w", err)
	}
	return nil
}

// GetSessionExecutionMetrics returns all execution metrics for a session.
func (s *Store) GetSessionExecutionMetrics(ctx context.Context, sessionID string) ([]ExecutionMetrics, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+executionMetricsCols+` FROM execution_metrics WHERE session_id = ? ORDER BY created_at DESC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("get session execution metrics: %w", err)
	}
	defer closeRows(rows)

	out := make([]ExecutionMetrics, 0)
	for rows.Next() {
		m, err := scanExecutionMetrics(rows)
		if err != nil {
			return nil, fmt.Errorf("scan session execution metrics: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetRecentExecutionMetrics returns the most recent execution metrics across all sessions.
func (s *Store) GetRecentExecutionMetrics(ctx context.Context, limit int) ([]ExecutionMetrics, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+executionMetricsCols+` FROM execution_metrics ORDER BY created_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get recent execution metrics: %w", err)
	}
	defer closeRows(rows)

	out := make([]ExecutionMetrics, 0)
	for rows.Next() {
		m, err := scanExecutionMetrics(rows)
		if err != nil {
			return nil, fmt.Errorf("scan recent execution metrics: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetUtilityCallSummary returns aggregated utility call stats grouped by provider, model, and call type.
func (s *Store) GetUtilityCallSummary(ctx context.Context) ([]UtilityCallSummary, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT provider, model, message_id AS call_type,
		        COUNT(*) AS call_count,
		        CAST(AVG(duration_ms) AS INTEGER) AS avg_duration,
		        MIN(duration_ms) AS min_duration,
		        MAX(duration_ms) AS max_duration,
		        SUM(CASE WHEN error != '' THEN 1 ELSE 0 END) AS error_count,
		        COALESCE(SUM(estimated_cost_usd), 0) AS total_cost
		 FROM execution_metrics
		 WHERE is_utility = TRUE
		 GROUP BY provider, model, message_id
		 ORDER BY call_count DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("get utility call summary: %w", err)
	}
	defer closeRows(rows)

	out := make([]UtilityCallSummary, 0)
	for rows.Next() {
		var ucs UtilityCallSummary
		if err := rows.Scan(
			&ucs.Provider, &ucs.Model, &ucs.CallType,
			&ucs.CallCount, &ucs.AvgDuration, &ucs.MinDuration, &ucs.MaxDuration,
			&ucs.ErrorCount, &ucs.TotalCost,
		); err != nil {
			return nil, fmt.Errorf("scan utility call summary: %w", err)
		}
		out = append(out, ucs)
	}
	return out, rows.Err()
}

// SessionToolTokenSummary holds aggregated tool token data from execution metrics.
type SessionToolTokenSummary struct {
	TotalInputTokens  int // sum of input_tokens across tool-bearing calls
	TotalOutputTokens int // sum of output_tokens across tool-bearing calls
	TotalToolCalls    int // sum of tool_calls
}

// GetSessionToolTokenSummary returns aggregated token data from execution metrics
// for LLM calls that included tool calls, giving actual token costs instead of estimates.
func (s *Store) GetSessionToolTokenSummary(ctx context.Context, sessionID string) (*SessionToolTokenSummary, error) {
	var summary SessionToolTokenSummary
	err := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(tool_calls),0)
		 FROM execution_metrics
		 WHERE session_id = ? AND tool_calls > 0 AND is_utility = FALSE`,
		sessionID,
	).Scan(&summary.TotalInputTokens, &summary.TotalOutputTokens, &summary.TotalToolCalls)
	if err != nil {
		return nil, fmt.Errorf("get session tool token summary: %w", err)
	}
	return &summary, nil
}

// GetUtilityCallLog returns recent individual utility call records.
func (s *Store) GetUtilityCallLog(ctx context.Context, limit int) ([]ExecutionMetrics, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+executionMetricsCols+` FROM execution_metrics WHERE is_utility = TRUE ORDER BY created_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get utility call log: %w", err)
	}
	defer closeRows(rows)

	out := make([]ExecutionMetrics, 0)
	for rows.Next() {
		m, err := scanExecutionMetrics(rows)
		if err != nil {
			return nil, fmt.Errorf("scan utility call log: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
