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
// By default archived sessions are excluded; set includeArchived to include them.
func (s *Store) ListSessions(workspaceID string, includeArchived ...bool) ([]Session, error) {
	inclArchived := false
	if len(includeArchived) > 0 {
		inclArchived = includeArchived[0]
	}

	query := `SELECT id, short_code, COALESCE(title,''), COALESCE(custom_name,''),
		        COALESCE(workspace_id,''), COALESCE(project_id,''),
		        COALESCE(context_type,''), COALESCE(context_id,''),
		        COALESCE(provider,''), COALESCE(model,''),
		        status, is_pinned, sort_order, message_count,
		        COALESCE(tags,'[]'), COALESCE(metadata,'{}'),
		        last_activity, created_at, updated_at
		 FROM sessions
		 WHERE workspace_id = ?`
	if !inclArchived {
		query += ` AND status != 'archived'`
	}
	query += ` ORDER BY last_activity DESC`

	rows, err := s.DB.Query(query, workspaceID)
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
		`UPDATE sessions SET title = ?, custom_name = ?, is_pinned = ?, model = ?, provider = ?, status = ?, updated_at = ? WHERE id = ?`,
		sess.Title, sess.CustomName, sess.IsPinned, sess.Model, sess.Provider, sess.Status, now, sess.ID,
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

// UpdateSessionMetadata sets the metadata JSON on a session.
func (s *Store) UpdateSessionMetadata(id, metadataJSON string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(
		`UPDATE sessions SET metadata = ?, updated_at = ? WHERE id = ?`,
		metadataJSON, now, id,
	)
	if err != nil {
		return fmt.Errorf("update session metadata %s: %w", id, err)
	}
	return nil
}

// ArchiveSession sets a session's status to "archived" and atomically evicts
// all session_objects rows scoped to that session. The DELETE + UPDATE run
// inside a single transaction so D5 (hard ephemeral: no cross-session lookup
// and no orphan payloads on archived sessions) holds even under mid-operation
// failure.
func (s *Store) ArchiveSession(id string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("archive session %s: begin tx: %w", id, err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(
		`UPDATE sessions SET status = 'archived', updated_at = ? WHERE id = ?`,
		now, id,
	); err != nil {
		return fmt.Errorf("archive session %s: %w", id, err)
	}
	if _, err := tx.Exec(
		`DELETE FROM session_objects WHERE session_id = ?`, id,
	); err != nil {
		return fmt.Errorf("archive session %s: evict session objects: %w", id, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("archive session %s: commit: %w", id, err)
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

// MessagePage holds a page of messages plus pagination metadata.
type MessagePage struct {
	Messages []Message `json:"messages"`
	Total    int       `json:"total"`
	HasMore  bool      `json:"has_more"`
}

// ListMessagesPaginated returns a page of messages for a session with offset-based pagination.
// Messages are returned in chronological order (created_at ASC).
func (s *Store) ListMessagesPaginated(sessionID string, limit, offset int) (*MessagePage, error) {
	var total int
	if err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM messages WHERE session_id = ?`, sessionID,
	).Scan(&total); err != nil {
		return nil, fmt.Errorf("count messages: %w", err)
	}

	rows, err := s.DB.Query(
		`SELECT id, session_id, COALESCE(agent_id,''), role, content,
		        COALESCE(envelope,''), COALESCE(metadata,'{}'),
		        COALESCE(parent_id,''), is_compacted, created_at
		 FROM messages WHERE session_id = ?
		 ORDER BY created_at ASC
		 LIMIT ? OFFSET ?`,
		sessionID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list messages paginated: %w", err)
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

	return &MessagePage{
		Messages: out,
		Total:    total,
		HasMore:  offset+len(out) < total,
	}, nil
}

// ListMessagesAroundID returns a window of messages centered on a specific message ID.
// Returns up to `before` messages before and `after` messages after the target, plus the target itself.
func (s *Store) ListMessagesAroundID(sessionID, messageID string, before, after int) (*MessagePage, error) {
	// Get the target message's created_at for windowing.
	var targetCreatedAt string
	if err := s.DB.QueryRow(
		`SELECT created_at FROM messages WHERE id = ? AND session_id = ?`,
		messageID, sessionID,
	).Scan(&targetCreatedAt); err != nil {
		return nil, fmt.Errorf("get target message: %w", err)
	}

	var total int
	if err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM messages WHERE session_id = ?`, sessionID,
	).Scan(&total); err != nil {
		return nil, fmt.Errorf("count messages: %w", err)
	}

	// Fetch messages: `before` rows before target + target + `after` rows after target.
	rows, err := s.DB.Query(
		`SELECT id, session_id, COALESCE(agent_id,''), role, content,
		        COALESCE(envelope,''), COALESCE(metadata,'{}'),
		        COALESCE(parent_id,''), is_compacted, created_at
		 FROM messages WHERE session_id = ? AND created_at >= (
		     SELECT created_at FROM (
		         SELECT created_at FROM messages
		         WHERE session_id = ? AND created_at <= ?
		         ORDER BY created_at DESC
		         LIMIT ?
		     ) sub ORDER BY created_at ASC LIMIT 1
		 ) AND created_at <= (
		     SELECT created_at FROM (
		         SELECT created_at FROM messages
		         WHERE session_id = ? AND created_at >= ?
		         ORDER BY created_at ASC
		         LIMIT ?
		     ) sub ORDER BY created_at DESC LIMIT 1
		 )
		 ORDER BY created_at ASC`,
		sessionID,
		sessionID, targetCreatedAt, before+1,
		sessionID, targetCreatedAt, after+1,
	)
	if err != nil {
		return nil, fmt.Errorf("list messages around: %w", err)
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

	return &MessagePage{
		Messages: out,
		Total:    total,
		HasMore:  true, // approximate — caller knows the window is partial
	}, nil
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

// ForkSession creates a new session based on a source session, copying agents
// and optionally messages. The entire operation runs inside a single
// transaction — if any step fails, no partial child session is left behind.
func (s *Store) ForkSession(sourceID string, overrides *Session, copyMessages bool) (*Session, error) {
	src, err := s.GetSession(sourceID)
	if err != nil {
		return nil, fmt.Errorf("load source session: %w", err)
	}

	// Build new session from source, applying overrides.
	newSess := &Session{
		WorkspaceID: src.WorkspaceID,
		ProjectID:   src.ProjectID,
		Provider:    src.Provider,
		Model:       src.Model,
		Tags:        src.Tags,
		ContextType: src.ContextType,
		ContextID:   src.ContextID,
	}

	// Apply overrides.
	if overrides != nil {
		if overrides.Provider != "" {
			newSess.Provider = overrides.Provider
		}
		if overrides.Model != "" {
			newSess.Model = overrides.Model
		}
	}

	// Set title based on whether we're forking or cloning.
	sourceTitle := src.CustomName
	if sourceTitle == "" {
		sourceTitle = src.Title
	}
	if sourceTitle != "" {
		if copyMessages {
			newSess.CustomName = "Fork: " + sourceTitle
		} else {
			newSess.CustomName = "Clone: " + sourceTitle
		}
	}

	// Read-side lookups (agents, messages) happen before the tx to keep the
	// write transaction short and avoid read/write interleaving on the same
	// connection.
	agents, err := s.ListSessionAgents(sourceID)
	if err != nil {
		return nil, fmt.Errorf("list source agents: %w", err)
	}

	var msgs []Message
	if copyMessages {
		const maxMessages = 10000
		msgs, err = s.ListMessages(sourceID, maxMessages)
		if err != nil {
			return nil, fmt.Errorf("list source messages: %w", err)
		}
		if len(msgs) >= maxMessages {
			return nil, fmt.Errorf("source session has too many messages (>=%d); fork/clone is not supported for sessions this large", maxMessages)
		}
	}

	// Assign new session ID + short code before opening tx; short_code
	// generation requires its own read and is safely idempotent.
	if newSess.ID == "" {
		newSess.ID = uuid.New().String()
	}
	code, err := s.NextShortCode()
	if err != nil {
		return nil, fmt.Errorf("generate short code: %w", err)
	}
	newSess.ShortCode = code
	if newSess.Status == "" {
		newSess.Status = "active"
	}
	if newSess.Metadata == "" {
		newSess.Metadata = "{}"
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin fork tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)

	if _, err := tx.Exec(
		`INSERT INTO sessions (id, short_code, title, custom_name, workspace_id, project_id,
		                       context_type, context_id, provider, model,
		                       status, is_pinned, sort_order, message_count,
		                       metadata, last_activity, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`,
		newSess.ID, newSess.ShortCode, nullIfEmpty(newSess.Title), nullIfEmpty(newSess.CustomName),
		nullIfEmpty(newSess.WorkspaceID), nullIfEmpty(newSess.ProjectID),
		nullIfEmpty(newSess.ContextType), nullIfEmpty(newSess.ContextID),
		nullIfEmpty(newSess.Provider), nullIfEmpty(newSess.Model),
		newSess.Status, newSess.IsPinned, newSess.SortOrder,
		newSess.Metadata, now, now, now,
	); err != nil {
		return nil, fmt.Errorf("create forked session: %w", err)
	}

	// Copy session agents inside the tx.
	for _, sa := range agents {
		if _, err := tx.Exec(
			`INSERT INTO session_agents (session_id, agent_id, mode, joined_at, is_primary)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(session_id, agent_id) DO UPDATE SET mode = excluded.mode, is_primary = excluded.is_primary`,
			newSess.ID, sa.AgentID, sa.Mode, now, sa.IsPrimary,
		); err != nil {
			return nil, fmt.Errorf("copy agent %s: %w", sa.AgentID, err)
		}
	}

	// Copy messages inside the same tx. Bulk-insert one statement per row,
	// but all under a single tx + single final message_count update.
	if copyMessages && len(msgs) > 0 {
		for _, m := range msgs {
			if _, err := tx.Exec(
				`INSERT INTO messages (id, session_id, agent_id, role, content, envelope, metadata, parent_id, is_compacted, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				uuid.New().String(), newSess.ID, nullIfEmpty(m.AgentID), m.Role, m.Content,
				nullIfEmpty(m.Envelope), m.Metadata, nullIfEmpty(m.ParentID), m.IsCompacted, now,
			); err != nil {
				return nil, fmt.Errorf("copy message: %w", err)
			}
		}
		// One UPDATE to set message_count to the actual copied count, avoiding
		// N separate +1 updates.
		if _, err := tx.Exec(
			`UPDATE sessions SET message_count = ?, last_activity = ?, updated_at = ? WHERE id = ?`,
			len(msgs), now, now, newSess.ID,
		); err != nil {
			return nil, fmt.Errorf("update forked session message_count: %w", err)
		}
		newSess.MessageCount = len(msgs)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit fork tx: %w", err)
	}

	newSess.LastActivity = now
	newSess.CreatedAt = now
	newSess.UpdatedAt = now
	return newSess, nil
}

// CopyMessages copies all messages from one session to another, assigning new IDs.
// Returns an error if the source session exceeds the 10,000 message limit.
// All inserts plus the final message_count update run inside a single
// transaction, so a mid-copy failure leaves the target session unchanged.
func (s *Store) CopyMessages(sourceSessionID, targetSessionID string) error {
	const maxMessages = 10000
	msgs, err := s.ListMessages(sourceSessionID, maxMessages)
	if err != nil {
		return fmt.Errorf("list source messages: %w", err)
	}
	if len(msgs) >= maxMessages {
		return fmt.Errorf("source session has too many messages (>=%d); fork/clone is not supported for sessions this large", maxMessages)
	}
	if len(msgs) == 0 {
		return nil
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("begin copy tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, m := range msgs {
		if _, err := tx.Exec(
			`INSERT INTO messages (id, session_id, agent_id, role, content, envelope, metadata, parent_id, is_compacted, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uuid.New().String(), targetSessionID, nullIfEmpty(m.AgentID), m.Role, m.Content,
			nullIfEmpty(m.Envelope), m.Metadata, nullIfEmpty(m.ParentID), m.IsCompacted, now,
		); err != nil {
			return fmt.Errorf("copy message: %w", err)
		}
	}

	if _, err := tx.Exec(
		`UPDATE sessions SET message_count = message_count + ?, last_activity = ?, updated_at = ? WHERE id = ?`,
		len(msgs), now, now, targetSessionID,
	); err != nil {
		return fmt.Errorf("update target session counts: %w", err)
	}

	return tx.Commit()
}

// SearchResult represents a single search hit with context.
type SearchResult struct {
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id"`
	Role      string `json:"role"`
	Snippet   string `json:"snippet"`
	CreatedAt string `json:"created_at"`
	// Session-level context for display.
	SessionTitle     string `json:"session_title"`
	SessionShortCode string `json:"session_short_code"`
}

// SearchMessages searches message content using LIKE matching.
// Returns matches with surrounding snippet text. Filters by workspace and optionally project.
func (s *Store) SearchMessages(query, workspaceID, projectID string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	likePattern := "%" + query + "%"

	var rows *sql.Rows
	var err error

	if projectID != "" {
		rows, err = s.DB.Query(
			`SELECT m.id, m.session_id, m.role, m.content, m.created_at,
			        COALESCE(s.title,''), s.short_code
			 FROM messages m
			 JOIN sessions s ON m.session_id = s.id
			 WHERE s.workspace_id = ? AND s.project_id = ? AND s.status != 'archived'
			   AND m.content LIKE ?
			 ORDER BY m.created_at DESC
			 LIMIT ?`,
			workspaceID, projectID, likePattern, limit,
		)
	} else {
		rows, err = s.DB.Query(
			`SELECT m.id, m.session_id, m.role, m.content, m.created_at,
			        COALESCE(s.title,''), s.short_code
			 FROM messages m
			 JOIN sessions s ON m.session_id = s.id
			 WHERE s.workspace_id = ? AND s.status != 'archived'
			   AND m.content LIKE ?
			 ORDER BY m.created_at DESC
			 LIMIT ?`,
			workspaceID, likePattern, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}
	defer rows.Close()

	out := make([]SearchResult, 0)
	for rows.Next() {
		var r SearchResult
		var fullContent string
		if err := rows.Scan(&r.MessageID, &r.SessionID, &r.Role, &fullContent, &r.CreatedAt,
			&r.SessionTitle, &r.SessionShortCode); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		r.Snippet = extractSnippet(fullContent, query, 80)
		out = append(out, r)
	}
	return out, rows.Err()
}

// extractSnippet returns a substring of content centered on the first occurrence of query,
// padded by `radius` characters on each side. Adds ellipsis when truncated.
func extractSnippet(content, query string, radius int) string {
	lower := strings.ToLower(content)
	idx := strings.Index(lower, strings.ToLower(query))
	if idx < 0 {
		if len(content) > radius*2 {
			return content[:radius*2] + "..."
		}
		return content
	}

	start := idx - radius
	if start < 0 {
		start = 0
	}
	end := idx + len(query) + radius
	if end > len(content) {
		end = len(content)
	}

	snippet := content[start:end]
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(content) {
		snippet = snippet + "..."
	}
	return snippet
}

// nullIfEmpty returns nil if s is empty, otherwise returns s. Used for nullable TEXT columns.
func nullIfEmpty(val string) interface{} {
	if val == "" {
		return nil
	}
	return val
}
