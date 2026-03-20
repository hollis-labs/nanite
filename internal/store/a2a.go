package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// A2AMessage represents an agent-to-agent message.
type A2AMessage struct {
	ID         string  `json:"id"`
	FromAgent  string  `json:"from_agent"`
	ToAgent    string  `json:"to_agent"`
	ThreadID   string  `json:"thread_id"`
	ReplyTo    string  `json:"reply_to"`
	Type       string  `json:"type"`
	Subject    string  `json:"subject"`
	Body       string  `json:"body"`
	Metadata   string  `json:"metadata"`
	Priority   int     `json:"priority"`
	Status     string  `json:"status"`
	CreatedAt  string  `json:"created_at"`
	ReadAt     *string `json:"read_at"`
	ResolvedAt *string `json:"resolved_at"`
}

// SendA2AMessage inserts a new A2A message.
func (s *Store) SendA2AMessage(msg *A2AMessage) error {
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
		`INSERT INTO a2a_messages (id, from_agent, to_agent, thread_id, reply_to, type,
		                           subject, body, metadata, priority, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.FromAgent, msg.ToAgent,
		nullIfEmpty(msg.ThreadID), nullIfEmpty(msg.ReplyTo),
		msg.Type, nullIfEmpty(msg.Subject), msg.Body,
		msg.Metadata, msg.Priority, msg.Status, now,
	)
	if err != nil {
		return fmt.Errorf("send a2a message: %w", err)
	}
	return nil
}

// GetA2AInbox returns messages for an agent, optionally filtered by status.
func (s *Store) GetA2AInbox(agentID string, status string) ([]A2AMessage, error) {
	var rows *sql.Rows
	var err error

	if status != "" {
		rows, err = s.DB.Query(
			`SELECT id, from_agent, to_agent, COALESCE(thread_id,''), COALESCE(reply_to,''),
			        type, COALESCE(subject,''), body, metadata, priority, status,
			        created_at, read_at, resolved_at
			 FROM a2a_messages WHERE to_agent = ? AND status = ?
			 ORDER BY created_at DESC`, agentID, status,
		)
	} else {
		rows, err = s.DB.Query(
			`SELECT id, from_agent, to_agent, COALESCE(thread_id,''), COALESCE(reply_to,''),
			        type, COALESCE(subject,''), body, metadata, priority, status,
			        created_at, read_at, resolved_at
			 FROM a2a_messages WHERE to_agent = ?
			 ORDER BY created_at DESC`, agentID,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("get a2a inbox: %w", err)
	}
	defer rows.Close()

	return scanA2AMessages(rows)
}

// GetA2AThread returns all messages in a thread, ordered chronologically.
func (s *Store) GetA2AThread(threadID string) ([]A2AMessage, error) {
	rows, err := s.DB.Query(
		`SELECT id, from_agent, to_agent, COALESCE(thread_id,''), COALESCE(reply_to,''),
		        type, COALESCE(subject,''), body, metadata, priority, status,
		        created_at, read_at, resolved_at
		 FROM a2a_messages WHERE thread_id = ?
		 ORDER BY created_at ASC`, threadID,
	)
	if err != nil {
		return nil, fmt.Errorf("get a2a thread: %w", err)
	}
	defer rows.Close()

	return scanA2AMessages(rows)
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

// A2AUnreadCount returns the number of unread messages for an agent.
func (s *Store) A2AUnreadCount(agentID string) (int, error) {
	var count int
	err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM a2a_messages WHERE to_agent = ? AND status = 'unread'`,
		agentID,
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
			&m.ID, &m.FromAgent, &m.ToAgent, &m.ThreadID, &m.ReplyTo,
			&m.Type, &m.Subject, &m.Body, &m.Metadata, &m.Priority, &m.Status,
			&m.CreatedAt, &m.ReadAt, &m.ResolvedAt,
		); err != nil {
			return nil, fmt.Errorf("scan a2a message: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
