package api

import "github.com/hollis-labs/nanite/internal/store"

// SessionView is the wire shape of a session. It is API-owned: its keys and
// null/omitted behavior match what store.Session emitted when handlers
// returned the row directly, so the wire did not change when they stopped.
// halted_at, halted_reason and runtime_state are omitted when unset; the
// sidebar relationship fields are always present and null when unset.
// TestSessionViewJSON pins both.
type SessionView struct {
	ID              string  `json:"id"`
	ShortCode       string  `json:"short_code"`
	Title           string  `json:"title"`
	CustomName      string  `json:"custom_name"`
	ProjectID       string  `json:"project_id"`
	ContextType     string  `json:"context_type"`
	ContextID       string  `json:"context_id"`
	Provider        string  `json:"provider"`
	Model           string  `json:"model"`
	Status          string  `json:"status"`
	IsPinned        bool    `json:"is_pinned"`
	SortOrder       int     `json:"sort_order"`
	MessageCount    int     `json:"message_count"`
	Tags            string  `json:"tags"`
	Metadata        string  `json:"metadata"`
	LastActivity    string  `json:"last_activity"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
	HaltedAt        *string `json:"halted_at,omitempty"`
	HaltedReason    *string `json:"halted_reason,omitempty"`
	RuntimeState    *string `json:"runtime_state,omitempty"`
	ParentSessionID *string `json:"parent_session_id"`
	RootSessionID   *string `json:"root_session_id"`
	Relation        *string `json:"relation"`
	Depth           *int    `json:"depth"`
}

// sessionToView translates a stored session into its wire shape.
func sessionToView(s *store.Session) SessionView {
	return SessionView{
		ID:              s.ID,
		ShortCode:       s.ShortCode,
		Title:           s.Title,
		CustomName:      s.CustomName,
		ProjectID:       s.ProjectID,
		ContextType:     s.ContextType,
		ContextID:       s.ContextID,
		Provider:        s.Provider,
		Model:           s.Model,
		Status:          s.Status,
		IsPinned:        s.IsPinned,
		SortOrder:       s.SortOrder,
		MessageCount:    s.MessageCount,
		Tags:            s.Tags,
		Metadata:        s.Metadata,
		LastActivity:    s.LastActivity,
		CreatedAt:       s.CreatedAt,
		UpdatedAt:       s.UpdatedAt,
		HaltedAt:        s.HaltedAt,
		HaltedReason:    s.HaltedReason,
		RuntimeState:    s.RuntimeState,
		ParentSessionID: s.ParentSessionID,
		RootSessionID:   s.RootSessionID,
		Relation:        s.Relation,
		Depth:           s.Depth,
	}
}

// sessionsToView translates a session list. A nil input stays nil so an
// empty result serializes the way it did before.
func sessionsToView(rows []store.Session) []SessionView {
	if rows == nil {
		return nil
	}
	out := make([]SessionView, 0, len(rows))
	for i := range rows {
		out = append(out, sessionToView(&rows[i]))
	}
	return out
}

// MessageView is the wire shape of a chat message. TestMessageViewJSON pins
// its keys against what store.Message emitted.
type MessageView struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	AgentID     string `json:"agent_id"`
	Role        string `json:"role"`
	Content     string `json:"content"`
	Envelope    string `json:"envelope"`
	Metadata    string `json:"metadata"`
	ParentID    string `json:"parent_id"`
	IsCompacted bool   `json:"is_compacted"`
	CreatedAt   string `json:"created_at"`
}

// messagesToView translates messages into their wire shape. A nil input
// stays nil and an empty one stays [], as the store returned them.
func messagesToView(rows []store.Message) []MessageView {
	if rows == nil {
		return nil
	}
	out := make([]MessageView, 0, len(rows))
	for _, m := range rows {
		out = append(out, MessageView{
			ID:          m.ID,
			SessionID:   m.SessionID,
			AgentID:     m.AgentID,
			Role:        m.Role,
			Content:     m.Content,
			Envelope:    m.Envelope,
			Metadata:    m.Metadata,
			ParentID:    m.ParentID,
			IsCompacted: m.IsCompacted,
			CreatedAt:   m.CreatedAt,
		})
	}
	return out
}

// MessagePageView is the wire shape of a page of messages.
type MessagePageView struct {
	Messages []MessageView `json:"messages"`
	Total    int           `json:"total"`
	HasMore  bool          `json:"has_more"`
}

func messagePageToView(p *store.MessagePage) MessagePageView {
	return MessagePageView{
		Messages: messagesToView(p.Messages),
		Total:    p.Total,
		HasMore:  p.HasMore,
	}
}

// SessionDetailView is the GET /api/sessions/{id} response. Its fields are
// in alphabetical key order so it encodes byte-for-byte like the
// map[string]any it replaced, whose keys encoding/json sorts.
type SessionDetailView struct {
	ActiveMessageID string         `json:"active_message_id"`
	InterruptedTurn map[string]any `json:"interrupted_turn"`
	Messages        []MessageView  `json:"messages"`
	Session         SessionView    `json:"session"`
}
