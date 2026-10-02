package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/nanite/internal/inspector"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

var ErrPluginQueryDenied = errors.New("plugin query exceeds granted scope")
var ErrPluginQueryNotFound = errors.New("plugin query session not found")

type pluginQueryStore interface {
	ReadPluginQuerySessions(context.Context, []string, bool, int) ([]store.Session, error)
	ReadPluginQueryMetrics(context.Context, string, int) ([]store.ExecutionMetrics, error)
}

// PluginQueryService is a fixed read projection. It has no resolver, SQL text,
// tool dispatcher or mutation callback supplied by the requesting plugin.
type PluginQueryService struct {
	reads     pluginQueryStore
	sessions  SessionService
	usage     *UsageService
	inspector *inspector.Service
}

func NewPluginQueryService(reads pluginQueryStore, sessions SessionService, usage *UsageService, snapshots *inspector.Service) *PluginQueryService {
	return &PluginQueryService{reads: reads, sessions: sessions, usage: usage, inspector: snapshots}
}

func querySessionView(session store.Session) pluginapi.QuerySession {
	return pluginapi.QuerySession{ID: session.ID, ShortCode: session.ShortCode, Title: session.Title, CustomName: session.CustomName,
		ProjectID: session.ProjectID, Provider: session.Provider, Model: session.Model, Status: session.Status,
		CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt}
}

func (service *PluginQueryService) Read(ctx context.Context, scope pluginapi.QueryScope, query pluginapi.QueryRequest) (any, error) {
	if scope.Validate() != nil || !scope.Allows(query.Resource, query.SessionID) {
		return nil, ErrPluginQueryDenied
	}
	if query.Limit < 0 || query.Limit > pluginapi.MaxQueryLimit {
		return nil, fmt.Errorf("invalid plugin query limit")
	}
	limit := query.Limit
	if limit == 0 {
		limit = 50
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var session *store.Session
	if query.SessionID != "" {
		var err error
		session, err = service.sessions.Get(ctx, query.SessionID)
		if errors.Is(err, sql.ErrNoRows) || err == nil && session == nil {
			return nil, ErrPluginQueryNotFound
		}
		if err != nil {
			return nil, err
		}
	}
	switch query.Resource {
	case pluginapi.QuerySessions:
		if session != nil {
			return pluginapi.QuerySessionsData{Sessions: []pluginapi.QuerySession{querySessionView(*session)}}, nil
		}
		rows, err := service.reads.ReadPluginQuerySessions(ctx, scope.SessionIDs, scope.AllSessions, limit+1)
		if err != nil {
			return nil, err
		}
		data := pluginapi.QuerySessionsData{Sessions: make([]pluginapi.QuerySession, 0), More: len(rows) > limit}
		for _, row := range rows[:min(len(rows), limit)] {
			data.Sessions = append(data.Sessions, querySessionView(row))
		}
		return data, nil
	case pluginapi.QueryUsage:
		usage, err := service.usage.SessionUsage(ctx, query.SessionID)
		if err != nil {
			return nil, err
		}
		if usage == nil {
			return pluginapi.QueryUsageData{}, nil
		}
		return pluginapi.QueryUsageData{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
			TotalTokens: usage.TotalTokens, ToolInputTokens: usage.ToolInputTokens, CacheCreationTokens: usage.CacheCreationTokens,
			CacheReadTokens: usage.CacheReadTokens, EstimatedCostUSD: usage.EstimatedCostUSD, MessageCount: usage.MessageCount}, nil
	case pluginapi.QueryExecutionMetrics:
		rows, err := service.reads.ReadPluginQueryMetrics(ctx, query.SessionID, limit+1)
		if err != nil {
			return nil, err
		}
		data := pluginapi.QueryMetricsData{Metrics: make([]pluginapi.QueryMetric, 0), More: len(rows) > limit}
		for _, row := range rows[:min(len(rows), limit)] {
			data.Metrics = append(data.Metrics, pluginapi.QueryMetric{ID: row.ID, SessionID: row.SessionID, MessageID: row.MessageID,
				Provider: row.Provider, Adapter: row.Adapter, Model: row.Model, AgentID: row.AgentID, AgentSlug: row.AgentSlug, Mode: row.Mode,
				DurationMs: row.DurationMs, ContextMessages: row.ContextMessages, ContextTokens: row.ContextTokens,
				InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, CacheCreationTokens: row.CacheCreationTokens,
				CacheReadTokens: row.CacheReadTokens, EstimatedCostUSD: row.EstimatedCostUSD,
				ToolIterations: row.ToolIterations, ToolCalls: row.ToolCalls, IsUtility: row.IsUtility,
				StopReason: row.StopReason, Failed: row.Error != "", ProfileName: row.ProfileName, ProfileDigest: row.ProfileDigest, CreatedAt: row.CreatedAt})
		}
		return data, nil
	case pluginapi.QueryContextSlots:
		data := pluginapi.QuerySlotsData{Slots: []pluginapi.QuerySlot{}}
		if service.inspector == nil {
			return data, nil
		}
		captures := service.inspector.RecentSnapshots(query.SessionID, 1)
		if len(captures) == 0 {
			return data, nil
		}
		capture := captures[0]
		data.Available, data.TurnID, data.StartedAt = true, capture.TurnID, capture.StartedAt.Format(time.RFC3339Nano)
		for _, slot := range capture.Slots {
			view := pluginapi.QuerySlot{Name: slot.Name, Tokens: slot.Tokens, Cached: slot.Cached, CacheKey: slot.CacheKey,
				Sensitive: slot.Sensitive, TrafficLight: slot.TrafficLight}
			if scope.IncludeContent {
				view.Content = slot.Content
			}
			data.Slots = append(data.Slots, view)
		}
		return data, nil
	default:
		return nil, ErrPluginQueryDenied
	}
}
