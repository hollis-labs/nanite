package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// A2AMessage represents an agent-to-agent message. Addressing is scoped to
// a (session_id, agent_id) tuple on both ends so that two instances of the
// same agent running in different sessions have distinct inboxes.
type A2AMessage struct {
	ID            string  `json:"id"`
	FromSessionID string  `json:"from_session_id"`
	FromAgentID   string  `json:"from_agent_id"`
	ToSessionID   string  `json:"to_session_id"`
	ToAgentID     string  `json:"to_agent_id"`
	ThreadID      string  `json:"thread_id"`
	ReplyTo       string  `json:"reply_to"`
	Type          string  `json:"type"`
	Subject       string  `json:"subject"`
	Body          string  `json:"body"`
	Metadata      string  `json:"metadata"`
	Priority      int     `json:"priority"`
	Status        string  `json:"status"`
	CreatedAt     string  `json:"created_at"`
	ReadAt        *string `json:"read_at"`
	ResolvedAt    *string `json:"resolved_at"`
}

// a2aSelectColumns is the canonical SELECT clause for loading A2AMessage
// rows. All read queries use this so scanA2AMessages can consume them.
const a2aSelectColumns = `id, from_session_id, from_agent_id, to_session_id, to_agent_id,
	COALESCE(thread_id,''), COALESCE(reply_to,''),
	type, COALESCE(subject,''), body, metadata, priority, status,
	created_at, read_at, resolved_at`

// SendA2AMessage inserts a new A2A message, populating defaults, and returns
// the freshly-persisted row so callers see authoritative values.
func (s *Store) SendA2AMessage(msg *A2AMessage) (*A2AMessage, error) {
	if msg.ID == "" {
		msg.ID = uuid.New().String()
	}
	if msg.Type == "" {
		msg.Type = "message"
	}
	if msg.Metadata == "" {
		msg.Metadata = "{}"
	}
	if msg.Priority == 0 {
		msg.Priority = 2
	}
	if msg.Status == "" {
		msg.Status = "unread"
	}
	if msg.ThreadID == "" {
		msg.ThreadID = msg.ID // self-thread for top-level messages
	}
	now := time.Now().UTC().Format(time.RFC3339)
	msg.CreatedAt = now

	_, err := s.DB.Exec(
		`INSERT INTO a2a_messages (id, from_session_id, from_agent_id,
		                           to_session_id, to_agent_id,
		                           thread_id, reply_to, type,
		                           subject, body, metadata, priority, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.ID,
		msg.FromSessionID, msg.FromAgentID,
		msg.ToSessionID, msg.ToAgentID,
		nullIfEmpty(msg.ThreadID), nullIfEmpty(msg.ReplyTo),
		msg.Type, nullIfEmpty(msg.Subject), msg.Body,
		msg.Metadata, msg.Priority, msg.Status, now,
	)
	if err != nil {
		return nil, fmt.Errorf("send a2a message: %w", err)
	}

	// Re-fetch to return authoritative row state.
	return s.GetA2AMessage(msg.ID)
}

// GetA2AMessage returns a single message by ID.
func (s *Store) GetA2AMessage(id string) (*A2AMessage, error) {
	row := s.DB.QueryRow(
		`SELECT `+a2aSelectColumns+`
		 FROM a2a_messages WHERE id = ?`, id,
	)
	var m A2AMessage
	if err := row.Scan(
		&m.ID,
		&m.FromSessionID, &m.FromAgentID, &m.ToSessionID, &m.ToAgentID,
		&m.ThreadID, &m.ReplyTo,
		&m.Type, &m.Subject, &m.Body, &m.Metadata, &m.Priority, &m.Status,
		&m.CreatedAt, &m.ReadAt, &m.ResolvedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("a2a message not found: %s", id)
		}
		return nil, fmt.Errorf("get a2a message: %w", err)
	}
	return &m, nil
}

// GetA2AInbox returns messages for (sessionID, agentID), optionally filtered
// by status. Ordered by priority DESC, then created_at ASC so high-priority
// messages float to the top while preserving FIFO within a tier.
func (s *Store) GetA2AInbox(sessionID, agentID, status string) ([]A2AMessage, error) {
	var rows *sql.Rows
	var err error

	if status != "" {
		rows, err = s.DB.Query(
			`SELECT `+a2aSelectColumns+`
			 FROM a2a_messages
			 WHERE to_session_id = ? AND to_agent_id = ? AND status = ?
			 ORDER BY priority DESC, created_at ASC`,
			sessionID, agentID, status,
		)
	} else {
		rows, err = s.DB.Query(
			`SELECT `+a2aSelectColumns+`
			 FROM a2a_messages
			 WHERE to_session_id = ? AND to_agent_id = ?
			 ORDER BY priority DESC, created_at ASC`,
			sessionID, agentID,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("get a2a inbox: %w", err)
	}
	defer rows.Close()

	return scanA2AMessages(rows)
}

// GetA2AThread returns all messages in a thread, ordered chronologically.
// Tiebreak on rowid so same-tick inserts remain deterministic — multiple
// inserts in the same RFC3339 second share created_at.
func (s *Store) GetA2AThread(threadID string) ([]A2AMessage, error) {
	rows, err := s.DB.Query(
		`SELECT `+a2aSelectColumns+`
		 FROM a2a_messages WHERE thread_id = ?
		 ORDER BY created_at ASC, rowid ASC`, threadID,
	)
	if err != nil {
		return nil, fmt.Errorf("get a2a thread: %w", err)
	}
	defer rows.Close()

	return scanA2AMessages(rows)
}

// GetA2ARecent returns up to limit recent messages involving sessionID (as
// sender or receiver), ordered chronologically (oldest first).
func (s *Store) GetA2ARecent(sessionID string, limit int) ([]A2AMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	// Tiebreak on rowid so sub-second bursts remain deterministic — multiple
	// inserts in the same RFC3339 second share created_at.
	rows, err := s.DB.Query(
		`SELECT `+a2aSelectColumns+`
		 FROM a2a_messages
		 WHERE from_session_id = ? OR to_session_id = ?
		 ORDER BY created_at DESC, rowid DESC
		 LIMIT ?`, sessionID, sessionID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get a2a recent: %w", err)
	}
	defer rows.Close()

	msgs, err := scanA2AMessages(rows)
	if err != nil {
		return nil, err
	}
	// Reverse to chronological order (oldest first).
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// AckA2AMessage marks a message as read.
func (s *Store) AckA2AMessage(id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(
		`UPDATE a2a_messages SET status = 'read', read_at = ? WHERE id = ?`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("ack a2a message: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("a2a message not found: %s", id)
	}
	return nil
}

// ResolveA2AMessage marks a message as resolved.
func (s *Store) ResolveA2AMessage(id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(
		`UPDATE a2a_messages SET status = 'resolved', resolved_at = ? WHERE id = ?`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("resolve a2a message: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("a2a message not found: %s", id)
	}
	return nil
}

// A2AUnreadCount returns the number of unread messages addressed to
// (sessionID, agentID).
func (s *Store) A2AUnreadCount(sessionID, agentID string) (int, error) {
	var count int
	err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM a2a_messages
		 WHERE to_session_id = ? AND to_agent_id = ? AND status = 'unread'`,
		sessionID, agentID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("a2a unread count: %w", err)
	}
	return count, nil
}

func scanA2AMessages(rows *sql.Rows) ([]A2AMessage, error) {
	out := make([]A2AMessage, 0)
	for rows.Next() {
		var m A2AMessage
		if err := rows.Scan(
			&m.ID,
			&m.FromSessionID, &m.FromAgentID, &m.ToSessionID, &m.ToAgentID,
			&m.ThreadID, &m.ReplyTo,
			&m.Type, &m.Subject, &m.Body, &m.Metadata, &m.Priority, &m.Status,
			&m.CreatedAt, &m.ReadAt, &m.ResolvedAt,
		); err != nil {
			return nil, fmt.Errorf("scan a2a message: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
