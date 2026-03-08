package store

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Session represents a chat session.
type Session struct {
	ID           string `json:"id"`
	ShortCode    string `json:"short_code"`
	Title        string `json:"title"`
	CustomName   string `json:"custom_name"`
	WorkspaceID  string `json:"workspace_id"`
	ProjectID    string `json:"project_id"`
	ContextType  string `json:"context_type"`
	ContextID    string `json:"context_id"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	Status       string `json:"status"` // active, paused, archived
	IsPinned     bool   `json:"is_pinned"`
	SortOrder    int    `json:"sort_order"`
	MessageCount int    `json:"message_count"`
	Tags         string `json:"tags"` // JSON array of strings, e.g. '["go","refactor"]'
	Metadata     string `json:"metadata"`
	LastActivity string `json:"last_activity"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// Message represents a chat message.
type Message struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	AgentID     string `json:"agent_id"`
	Role        string `json:"role"` // user, assistant, system, tool
	Content     string `json:"content"`
	Envelope    string `json:"envelope"`
	Metadata    string `json:"metadata"`
	ParentID    string `json:"parent_id"`
	IsCompacted bool   `json:"is_compacted"`
	CreatedAt   string `json:"created_at"`
}

// ListSessions returns sessions filtered by workspace, ordered by last_activity DESC.
func (s *Store) ListSessions(workspaceID string) ([]Session, error) {
	rows, err := s.DB.Query(
		`SELECT id, short_code, COALESCE(title,''), COALESCE(custom_name,''),
		        COALESCE(workspace_id,''), COALESCE(project_id,''),
		        COALESCE(context_type,''), COALESCE(context_id,''),
		        COALESCE(provider,''), COALESCE(model,''),
		        status, is_pinned, sort_order, message_count,
		        COALESCE(tags,'[]'), COALESCE(metadata,'{}'),
		        last_activity, created_at, updated_at
		 FROM sessions
		 WHERE workspace_id = ?
		 ORDER BY last_activity DESC`,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	out := make([]Session, 0)
	for rows.Next() {
		var sess Session
		if err := rows.Scan(
			&sess.ID, &sess.ShortCode, &sess.Title, &sess.CustomName,
			&sess.WorkspaceID, &sess.ProjectID,
			&sess.ContextType, &sess.ContextID,
			&sess.Provider, &sess.Model,
			&sess.Status, &sess.IsPinned, &sess.SortOrder, &sess.MessageCount,
			&sess.Tags, &sess.Metadata, &sess.LastActivity, &sess.CreatedAt, &sess.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// GetSession returns a single session by ID.
func (s *Store) GetSession(id string) (*Session, error) {
	var sess Session
	err := s.DB.QueryRow(
		`SELECT id, short_code, COALESCE(title,''), COALESCE(custom_name,''),
		        COALESCE(workspace_id,''), COALESCE(project_id,''),
		        COALESCE(context_type,''), COALESCE(context_id,''),
		        COALESCE(provider,''), COALESCE(model,''),
		        status, is_pinned, sort_order, message_count,
		        COALESCE(tags,'[]'), COALESCE(metadata,'{}'),
		        last_activity, created_at, updated_at
		 FROM sessions WHERE id = ?`, id,
	).Scan(
		&sess.ID, &sess.ShortCode, &sess.Title, &sess.CustomName,
		&sess.WorkspaceID, &sess.ProjectID,
		&sess.ContextType, &sess.ContextID,
		&sess.Provider, &sess.Model,
		&sess.Status, &sess.IsPinned, &sess.SortOrder, &sess.MessageCount,
		&sess.Tags, &sess.Metadata, &sess.LastActivity, &sess.CreatedAt, &sess.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get session %s: %w", id, err)
	}
	return &sess, nil
}

// CreateSession inserts a new session, auto-generating ID and short_code.
func (s *Store) CreateSession(sess *Session) error {
	if sess.ID == "" {
		sess.ID = uuid.New().String()
	}

	code, err := s.NextShortCode()
	if err != nil {
		return fmt.Errorf("generate short code: %w", err)
	}
	sess.ShortCode = code

	now := time.Now().UTC().Format(time.RFC3339)
	if sess.Status == "" {
		sess.Status = "active"
	}
	if sess.Metadata == "" {
		sess.Metadata = "{}"
	}

	_, err = s.DB.Exec(
		`INSERT INTO sessions (id, short_code, title, custom_name, workspace_id, project_id,
		                       context_type, context_id, provider, model,
		                       status, is_pinned, sort_order, message_count,
		                       metadata, last_activity, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`,
		sess.ID, sess.ShortCode, nullIfEmpty(sess.Title), nullIfEmpty(sess.CustomName),
		nullIfEmpty(sess.WorkspaceID), nullIfEmpty(sess.ProjectID),
		nullIfEmpty(sess.ContextType), nullIfEmpty(sess.ContextID),
		nullIfEmpty(sess.Provider), nullIfEmpty(sess.Model),
		sess.Status, sess.IsPinned, sess.SortOrder,
		sess.Metadata, now, now, now,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	sess.LastActivity = now
	sess.CreatedAt = now
	sess.UpdatedAt = now
	return nil
}

// UpdateSession updates mutable session fields.
func (s *Store) UpdateSession(sess *Session) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(
		`UPDATE sessions SET title = ?, custom_name = ?, is_pinned = ?, model = ?, provider = ?, updated_at = ? WHERE id = ?`,
		sess.Title, sess.CustomName, sess.IsPinned, sess.Model, sess.Provider, now, sess.ID,
	)
	if err != nil {
		return fmt.Errorf("update session: %w", err)
	}
	sess.UpdatedAt = now
	return nil
}

// UpdateSessionTags sets the tags JSON array on a session.
func (s *Store) UpdateSessionTags(id, tagsJSON string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(
		`UPDATE sessions SET tags = ?, updated_at = ? WHERE id = ?`,
		tagsJSON, now, id,
	)
	if err != nil {
		return fmt.Errorf("update session tags %s: %w", id, err)
	}
	return nil
}

// ArchiveSession sets a session's status to "archived".
func (s *Store) ArchiveSession(id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(
		`UPDATE sessions SET status = 'archived', updated_at = ? WHERE id = ?`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("archive session %s: %w", id, err)
	}
	return nil
}

// NextShortCode returns the next available short code (c1, c2, ...).
func (s *Store) NextShortCode() (string, error) {
	var raw sql.NullString
	err := s.DB.QueryRow(
		`SELECT short_code FROM sessions ORDER BY CAST(SUBSTR(short_code, 2) AS INTEGER) DESC LIMIT 1`,
	).Scan(&raw)
	if err == sql.ErrNoRows || !raw.Valid {
		return "c1", nil
	}
	if err != nil {
		return "", fmt.Errorf("query short_code: %w", err)
	}

	numStr := strings.TrimPrefix(raw.String, "c")
	num, err := strconv.Atoi(numStr)
	if err != nil {
		return "", fmt.Errorf("parse short_code %q: %w", raw.String, err)
	}
	return fmt.Sprintf("c%d", num+1), nil
}

// ListMessages returns the latest N messages for a session, ordered by created_at ASC.
func (s *Store) ListMessages(sessionID string, limit int) ([]Message, error) {
	rows, err := s.DB.Query(
		`SELECT id, session_id, COALESCE(agent_id,''), role, content,
		        COALESCE(envelope,''), COALESCE(metadata,'{}'),
		        COALESCE(parent_id,''), is_compacted, created_at
		 FROM messages WHERE session_id = ?
		 ORDER BY created_at DESC LIMIT ?`,
		sessionID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	out := make([]Message, 0)
	for rows.Next() {
		var m Message
		if err := rows.Scan(
			&m.ID, &m.SessionID, &m.AgentID, &m.Role, &m.Content,
			&m.Envelope, &m.Metadata, &m.ParentID, &m.IsCompacted, &m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Reverse to get ASC order.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// GetMessage returns a single message by ID.
func (s *Store) GetMessage(id string) (*Message, error) {
	var m Message
	err := s.DB.QueryRow(
		`SELECT id, session_id, COALESCE(agent_id,''), role, content,
		        COALESCE(envelope,''), COALESCE(metadata,'{}'),
		        COALESCE(parent_id,''), is_compacted, created_at
		 FROM messages WHERE id = ?`, id,
	).Scan(
		&m.ID, &m.SessionID, &m.AgentID, &m.Role, &m.Content,
		&m.Envelope, &m.Metadata, &m.ParentID, &m.IsCompacted, &m.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get message %s: %w", id, err)
	}
	return &m, nil
}

// CreateMessage inserts a new message and updates the session's message_count and last_activity.
func (s *Store) CreateMessage(msg *Message) error {
	if msg.ID == "" {
		msg.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if msg.Metadata == "" {
		msg.Metadata = "{}"
	}
	msg.CreatedAt = now

	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(
		`INSERT INTO messages (id, session_id, agent_id, role, content, envelope, metadata, parent_id, is_compacted, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.SessionID, nullIfEmpty(msg.AgentID), msg.Role, msg.Content,
		nullIfEmpty(msg.Envelope), msg.Metadata, nullIfEmpty(msg.ParentID), msg.IsCompacted, now,
	)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}

	_, err = tx.Exec(
		`UPDATE sessions SET message_count = message_count + 1, last_activity = ?, updated_at = ? WHERE id = ?`,
		now, now, msg.SessionID,
	)
	if err != nil {
		return fmt.Errorf("update session counts: %w", err)
	}

	return tx.Commit()
}

// UpdateMessageContent updates a message's content and compaction flag.
func (s *Store) UpdateMessageContent(id, content string, isCompacted bool) error {
	_, err := s.DB.Exec(
		`UPDATE messages SET content = ?, is_compacted = ? WHERE id = ?`,
		content, isCompacted, id,
	)
	if err != nil {
		return fmt.Errorf("update message content %s: %w", id, err)
	}
	return nil
}

// UpdateSessionCompaction saves a compaction summary on a session.
func (s *Store) UpdateSessionCompaction(id, summary string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(
		`UPDATE sessions SET compaction_summary = ?, compacted_at = ?, updated_at = ? WHERE id = ?`,
		summary, now, now, id,
	)
	if err != nil {
		return fmt.Errorf("update session compaction %s: %w", id, err)
	}
	return nil
}

// nullIfEmpty returns nil if s is empty, otherwise returns s. Used for nullable TEXT columns.
func nullIfEmpty(val string) interface{} {
	if val == "" {
		return nil
	}
	return val
}
