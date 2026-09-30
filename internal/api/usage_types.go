package api

import "github.com/hollis-labs/nanite/internal/store"

// The usage and execution-metrics wire types are API-owned: their keys and
// omitempty behavior match what the store rows emitted when handlers
// returned them directly. The UI calls .length/.reduce on every list without
// a null guard, so the translators keep an empty list as [] (and nil as
// nil, which the store never returns). usage_types_test.go pins all of it.

// SessionUsageView is a session's token usage totals.
type SessionUsageView struct {
	InputTokens         int     `json:"input_tokens"`
	OutputTokens        int     `json:"output_tokens"`
	TotalTokens         int     `json:"total_tokens"`
	ToolInputTokens     int     `json:"tool_input_tokens"`
	CacheCreationTokens int     `json:"cache_creation_tokens"`
	CacheReadTokens     int     `json:"cache_read_tokens"`
	EstimatedCostUSD    float64 `json:"estimated_cost_usd"`
	MessageCount        int     `json:"message_count"`
}

func sessionUsageToView(u *store.SessionUsageSummary) SessionUsageView {
	return SessionUsageView{
		InputTokens:         u.InputTokens,
		OutputTokens:        u.OutputTokens,
		TotalTokens:         u.TotalTokens,
		ToolInputTokens:     u.ToolInputTokens,
		CacheCreationTokens: u.CacheCreationTokens,
		CacheReadTokens:     u.CacheReadTokens,
		EstimatedCostUSD:    u.EstimatedCostUSD,
		MessageCount:        u.MessageCount,
	}
}

// sessionUsageToViewPtr is sessionUsageToView for an optional response
// field: nil stays nil.
func sessionUsageToViewPtr(u *store.SessionUsageSummary) *SessionUsageView {
	if u == nil {
		return nil
	}
	v := sessionUsageToView(u)
	return &v
}

// ModelUsageView is one model's share of the usage summary.
type ModelUsageView struct {
	Model            string  `json:"model"`
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
}

// UsageSummaryView is token usage across all sessions.
type UsageSummaryView struct {
	TotalInput  int              `json:"total_input"`
	TotalOutput int              `json:"total_output"`
	TotalTokens int              `json:"total_tokens"`
	TotalCost   float64          `json:"total_cost"`
	ByModel     []ModelUsageView `json:"by_model"`
}

func usageSummaryToView(u *store.UsageSummary) UsageSummaryView {
	out := UsageSummaryView{
		TotalInput:  u.TotalInput,
		TotalOutput: u.TotalOutput,
		TotalTokens: u.TotalTokens,
		TotalCost:   u.TotalCost,
	}
	if u.ByModel != nil {
		out.ByModel = make([]ModelUsageView, 0, len(u.ByModel))
		for _, m := range u.ByModel {
			out.ByModel = append(out.ByModel, ModelUsageView{
				Model:            m.Model,
				InputTokens:      m.InputTokens,
				OutputTokens:     m.OutputTokens,
				TotalTokens:      m.TotalTokens,
				EstimatedCostUSD: m.EstimatedCostUSD,
			})
		}
	}
	return out
}

// ExecutionMetricsView is one turn's (or utility call's) execution record.
// debug_snapshots and the harness-profile keys are omitted when empty.
type ExecutionMetricsView struct {
	ID                  int64   `json:"id"`
	SessionID           string  `json:"session_id"`
	MessageID           string  `json:"message_id"`
	Provider            string  `json:"provider"`
	Adapter             string  `json:"adapter"`
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
	DebugSnapshots      string  `json:"debug_snapshots,omitempty"`
	ProfileName         string  `json:"profile_name,omitempty"`
	ProfileDigest       string  `json:"profile_digest,omitempty"`
	EffectiveLimitsJSON string  `json:"effective_limits_json,omitempty"`
	CreatedAt           string  `json:"created_at"`
}

func executionMetricsToView(rows []store.ExecutionMetrics) []ExecutionMetricsView {
	if rows == nil {
		return nil
	}
	out := make([]ExecutionMetricsView, 0, len(rows))
	for _, m := range rows {
		out = append(out, ExecutionMetricsView{
			ID:                  m.ID,
			SessionID:           m.SessionID,
			MessageID:           m.MessageID,
			Provider:            m.Provider,
			Adapter:             m.Adapter,
			Model:               m.Model,
			AgentID:             m.AgentID,
			AgentSlug:           m.AgentSlug,
			Mode:                m.Mode,
			DurationMs:          m.DurationMs,
			ContextMessages:     m.ContextMessages,
			ContextTokens:       m.ContextTokens,
			InputTokens:         m.InputTokens,
			OutputTokens:        m.OutputTokens,
			CacheCreationTokens: m.CacheCreationTokens,
			CacheReadTokens:     m.CacheReadTokens,
			EstimatedCostUSD:    m.EstimatedCostUSD,
			ToolIterations:      m.ToolIterations,
			ToolCalls:           m.ToolCalls,
			IsUtility:           m.IsUtility,
			StopReason:          m.StopReason,
			Error:               m.Error,
			DebugSnapshots:      m.DebugSnapshots,
			ProfileName:         m.ProfileName,
			ProfileDigest:       m.ProfileDigest,
			EffectiveLimitsJSON: m.EffectiveLimitsJSON,
			CreatedAt:           m.CreatedAt,
		})
	}
	return out
}

// UtilityCallSummaryView aggregates utility calls by provider, model and
// call type.
type UtilityCallSummaryView struct {
	Provider    string  `json:"provider"`
	Model       string  `json:"model"`
	CallType    string  `json:"call_type"`
	CallCount   int     `json:"call_count"`
	AvgDuration int64   `json:"avg_duration_ms"`
	MinDuration int64   `json:"min_duration_ms"`
	MaxDuration int64   `json:"max_duration_ms"`
	ErrorCount  int     `json:"error_count"`
	TotalCost   float64 `json:"total_cost_usd"`
}

func utilityCallSummaryToView(rows []store.UtilityCallSummary) []UtilityCallSummaryView {
	if rows == nil {
		return nil
	}
	out := make([]UtilityCallSummaryView, 0, len(rows))
	for _, u := range rows {
		out = append(out, UtilityCallSummaryView{
			Provider:    u.Provider,
			Model:       u.Model,
			CallType:    u.CallType,
			CallCount:   u.CallCount,
			AvgDuration: u.AvgDuration,
			MinDuration: u.MinDuration,
			MaxDuration: u.MaxDuration,
			ErrorCount:  u.ErrorCount,
			TotalCost:   u.TotalCost,
		})
	}
	return out
}
