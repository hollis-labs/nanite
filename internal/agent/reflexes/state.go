package reflexes

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/nanite/internal/store"
)

// StateCollector reads recent session/agent signals from the agridd
// store and assembles a State the evaluator can run against.
type StateCollector struct {
	Store *store.Store
	// Window is the number of recent assistant messages to collect.
	// Defaults to 5 (covers the largest predicate window seeded —
	// idle_drift_advisor uses N=5).
	Window int
}

// Collect builds a fresh State snapshot.
func (sc *StateCollector) Collect(ctx context.Context, sessionID, agentID, agentClass string) (State, error) {
	window := sc.Window
	if window <= 0 {
		window = 5
	}
	if sc.Store == nil {
		return State{}, fmt.Errorf("StateCollector: Store is nil")
	}

	msgs, err := sc.Store.LastNAssistantMessages(ctx, sessionID, window)
	if err != nil {
		return State{}, fmt.Errorf("collect last messages: %w", err)
	}
	usages, err := sc.Store.LastNTokenUsage(ctx, sessionID, window)
	if err != nil {
		return State{}, fmt.Errorf("collect token usage: %w", err)
	}
	// Build a usage lookup keyed by message_id. Multiple usage rows
	// for the same message_id (rare) take the first (most recent).
	usageByMessage := make(map[string]store.TokenUsage, len(usages))
	for _, u := range usages {
		if _, ok := usageByMessage[u.MessageID]; !ok {
			usageByMessage[u.MessageID] = u
		}
	}

	signals := make([]MessageSignal, 0, len(msgs))
	for _, m := range msgs {
		ms := MessageSignal{
			MessageID: m.ID,
			Role:      m.Role,
			Content:   unwrapEnvelopeText(m.Content),
			CreatedAt: m.CreatedAt,
		}
		ms.ToolNames, ms.EnvelopeTypes = structuredRefs(m.Content)
		if u, ok := usageByMessage[m.ID]; ok {
			ms.InputTokens = u.InputTokens
			ms.OutputTokens = u.OutputTokens
			ms.CacheRead = u.CacheReadTokens
		}
		if n, err := sc.Store.ToolCallsForMessage(ctx, m.ID); err == nil {
			ms.ToolCalls = n
		}
		signals = append(signals, ms)
	}
	userMessages, err := sc.recentMessagesByRole(ctx, sessionID, "user", window)
	if err != nil {
		return State{}, fmt.Errorf("collect user messages: %w", err)
	}

	// PrefixTokens: take latest input_tokens as an approximation.
	prefix := 0
	if len(usages) > 0 {
		prefix = usages[0].InputTokens
	}
	mailUnread, _ := sc.mailUnreadCount(ctx, sessionID, agentID)
	events, _ := sc.recentEvents(ctx, sessionID, 50)
	tickN, _ := sc.sessionMessageCount(ctx, sessionID)

	return State{
		SessionID:       sessionID,
		AgentID:         agentID,
		AgentClass:      agentClass,
		Messages:        signals,
		UserMessages:    userMessages,
		PrefixTokens:    prefix,
		MailUnreadCount: mailUnread,
		Events:          events,
		TickN:           tickN,
	}, nil
}

func (sc *StateCollector) mailUnreadCount(ctx context.Context, sessionID, agentID string) (int, error) {
	if sessionID == "" || agentID == "" {
		return 0, nil
	}
	var count int
	err := sc.Store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_messages
		  WHERE to_session_id = ? AND to_agent_id = ? AND status = 'unread'`,
		sessionID, agentID,
	).Scan(&count)
	return count, err
}

func (sc *StateCollector) recentMessagesByRole(ctx context.Context, sessionID, role string, limit int) ([]MessageSignal, error) {
	if sessionID == "" || role == "" || limit <= 0 {
		return nil, nil
	}
	rows, err := sc.Store.DB.QueryContext(ctx,
		`SELECT id, role, content, created_at
		   FROM messages
		  WHERE session_id = ? AND role = ?
		  ORDER BY created_at DESC, id DESC
		  LIMIT ?`,
		sessionID, role, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MessageSignal, 0, limit)
	for rows.Next() {
		var m MessageSignal
		if err := rows.Scan(&m.MessageID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Content = unwrapEnvelopeText(m.Content)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (sc *StateCollector) recentEvents(ctx context.Context, sessionID string, limit int) ([]EventSignal, error) {
	if sessionID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := sc.Store.DB.QueryContext(ctx,
		`SELECT event_type, category, created_at
		   FROM event_log
		  WHERE session_id = ?
		  ORDER BY created_at DESC, id DESC
		  LIMIT ?`,
		sessionID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]EventSignal, 0, limit)
	for rows.Next() {
		var e EventSignal
		if err := rows.Scan(&e.EventType, &e.Category, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (sc *StateCollector) sessionMessageCount(ctx context.Context, sessionID string) (int, error) {
	if sessionID == "" {
		return 0, nil
	}
	var n int
	err := sc.Store.DB.QueryRowContext(ctx,
		`SELECT message_count FROM sessions WHERE id = ?`,
		sessionID,
	).Scan(&n)
	return n, err
}

// unwrapEnvelopeText is a thin mirror of api.unwrapEnvelopeText —
// duplicated here to avoid an api → reflexes import cycle. Pulls
// human-visible text out of {"v":1,"text":"..."} envelope bodies.
func unwrapEnvelopeText(body string) string {
	if body == "" || body[0] != '{' {
		return body
	}
	var env struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil || env.Text == "" {
		return body
	}
	return env.Text
}

func structuredRefs(body string) ([]string, []string) {
	if body == "" || body[0] != '{' {
		return nil, nil
	}
	var env struct {
		ToolCalls []struct {
			Name string `json:"name"`
		} `json:"tool_calls"`
		Envelopes []struct {
			Type string `json:"type"`
		} `json:"envelopes"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return nil, nil
	}
	toolNames := make([]string, 0, len(env.ToolCalls))
	for _, tc := range env.ToolCalls {
		if tc.Name != "" {
			toolNames = append(toolNames, tc.Name)
		}
	}
	envelopeTypes := make([]string, 0, len(env.Envelopes))
	for _, e := range env.Envelopes {
		if e.Type != "" {
			envelopeTypes = append(envelopeTypes, e.Type)
		}
	}
	return toolNames, envelopeTypes
}
