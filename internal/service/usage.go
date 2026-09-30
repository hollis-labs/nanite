package service

import (
	"context"

	"github.com/hollis-labs/nanite/internal/store"
)

// estimatedTokensPerToolCall is the per-call estimate SessionToolTokens uses
// when no execution metrics recorded a session's tool token spend.
const estimatedTokensPerToolCall = 50

// SessionToolTokenReader sums the tool-bearing execution metrics of a
// session.
type SessionToolTokenReader interface {
	GetSessionToolTokenSummary(ctx context.Context, sessionID string) (*store.SessionToolTokenSummary, error)
}

// UsageService is the read side of token accounting: token usage, execution
// metrics and utility calls. The chat service writes the same data through
// the composite Store (RecordUsage, RecordExecutionMetrics); this service
// only reports it.
//
// Chat's /usage command (internal/chat/commands_builtin.go) still reads
// session usage from the store directly. It sits outside the transport
// boundary, and is a candidate consumer of SessionUsage later.
type UsageService struct {
	usage      UsageStore
	toolTokens SessionToolTokenReader // may be nil; SessionToolTokens then estimates
}

func NewUsageService(usage UsageStore, toolTokens SessionToolTokenReader) *UsageService {
	return &UsageService{usage: usage, toolTokens: toolTokens}
}

// SessionUsage returns a session's token usage totals.
func (s *UsageService) SessionUsage(ctx context.Context, sessionID string) (*store.SessionUsageSummary, error) {
	return s.usage.GetSessionUsage(ctx, sessionID)
}

// Summary returns token usage totals across all sessions, broken down by
// model.
func (s *UsageService) Summary(ctx context.Context) (*store.UsageSummary, error) {
	return s.usage.GetUsageSummary(ctx)
}

// SessionExecutionMetrics returns a session's per-turn execution metrics,
// newest first.
func (s *UsageService) SessionExecutionMetrics(ctx context.Context, sessionID string) ([]store.ExecutionMetrics, error) {
	return s.usage.GetSessionExecutionMetrics(ctx, sessionID)
}

// RecentExecutionMetrics returns the most recent execution metrics across
// sessions, newest first.
func (s *UsageService) RecentExecutionMetrics(ctx context.Context, limit int) ([]store.ExecutionMetrics, error) {
	return s.usage.GetRecentExecutionMetrics(ctx, limit)
}

// UtilityCallSummary returns utility calls (auto-title, auto-tags, ...)
// aggregated by provider, model and call type.
func (s *UsageService) UtilityCallSummary(ctx context.Context) ([]store.UtilityCallSummary, error) {
	return s.usage.GetUtilityCallSummary(ctx)
}

// UtilityCallLog returns the most recent utility calls, newest first.
func (s *UsageService) UtilityCallLog(ctx context.Context, limit int) ([]store.ExecutionMetrics, error) {
	return s.usage.GetUtilityCallLog(ctx, limit)
}

// ToolTokenUsage is a session's tool token spend. Estimated reports that
// Tokens is Calls x a fixed per-call estimate rather than a recorded sum.
type ToolTokenUsage struct {
	Calls     int
	Tokens    int
	Estimated bool
}

// SessionToolTokens reports a session's tool token spend. It prefers the
// actual input+output tokens recorded on the session's tool-bearing
// execution metrics. When that read fails or records no tool calls, it
// falls back to the event-log tool-call count at a fixed estimate per call.
// Zero Calls means no tool calls were found either way.
func (s *UsageService) SessionToolTokens(ctx context.Context, sessionID string) ToolTokenUsage {
	if s.toolTokens != nil {
		summary, err := s.toolTokens.GetSessionToolTokenSummary(ctx, sessionID)
		if err == nil && summary.TotalToolCalls > 0 {
			return ToolTokenUsage{
				Calls:  summary.TotalToolCalls,
				Tokens: summary.TotalInputTokens + summary.TotalOutputTokens,
			}
		}
	}
	calls := s.usage.CountSessionToolCalls(ctx, sessionID)
	if calls <= 0 {
		return ToolTokenUsage{}
	}
	return ToolTokenUsage{
		Calls:     calls,
		Tokens:    calls * estimatedTokensPerToolCall,
		Estimated: true,
	}
}
