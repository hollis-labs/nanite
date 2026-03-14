package contextbroker

import (
	"context"
	"fmt"
	"strings"
)

// MessageFetcher is a function that fetches messages for a session.
// Returns (role, content) pairs. This avoids importing store directly.
type MessageFetcher func(sessionID string, limit int) ([]MessageSummary, error)

// MessageSummary is a minimal message representation for context retrieval.
type MessageSummary struct {
	Role    string
	Content string
}

// SessionSource retrieves context from the current session's message history.
type SessionSource struct {
	Fetcher MessageFetcher
}

// NewSessionSource creates a SessionSource with the given message fetcher.
func NewSessionSource(fetcher MessageFetcher) *SessionSource {
	return &SessionSource{Fetcher: fetcher}
}

func (s *SessionSource) Name() string { return "session" }

func (s *SessionSource) Fetch(ctx context.Context, intent Intent, budget int) ([]ContextItem, error) {
	if s.Fetcher == nil || intent.SessionID == "" {
		return nil, nil
	}

	messages, err := s.Fetcher(intent.SessionID, 50)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}

	if len(messages) == 0 {
		return nil, nil
	}

	// Build a summary of recent conversation, walking from most recent to oldest.
	var sb strings.Builder
	usedTokens := 0

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != "user" && msg.Role != "assistant" {
			continue
		}

		content := msg.Content
		if len(content) > 500 {
			content = content[:500] + "..."
		}

		line := fmt.Sprintf("%s: %s\n", msg.Role, content)
		lineTokens := EstimateTokens(line)

		if usedTokens+lineTokens > budget {
			break
		}

		sb.WriteString(line)
		usedTokens += lineTokens
	}

	if sb.Len() == 0 {
		return nil, nil
	}

	return []ContextItem{{
		Source:        "session",
		Key:           intent.SessionID,
		Content:       sb.String(),
		TokenEstimate: usedTokens,
		Relevance:     0.6,
		Metadata: map[string]string{
			"session_id":    intent.SessionID,
			"message_count": fmt.Sprintf("%d", len(messages)),
		},
	}}, nil
}
