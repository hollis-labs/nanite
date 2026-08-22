package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Document is a user-uploaded or paste-content document persisted per session.
// J10 (CW-20260426-0008).
type Document struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	Name        string `json:"name"`
	MimeType    string `json:"mime_type"`
	Content     string `json:"content"`
	SizeBytes   int    `json:"size_bytes"`
	Included    bool   `json:"included"`     // include in agent context?
	FullContent bool   `json:"full_content"` // full content vs pointer (summary)
	Summary     string `json:"summary"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// CreateDocument persists a new document.
func (s *Store) CreateDocument(ctx context.Context, d *Document) error {
	if d.ID == "" {
		d.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	d.CreatedAt = now
	d.UpdatedAt = now
	if d.MimeType == "" {
		d.MimeType = "text/plain"
	}
	d.SizeBytes = len(d.Content)

	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO documents
			(id, session_id, name, mime_type, content, size_bytes,
			 included, full_content, summary, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.SessionID, d.Name, d.MimeType, d.Content, d.SizeBytes,
		boolToInt(d.Included), boolToInt(d.FullContent), d.Summary,
		d.CreatedAt, d.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create document: %w", err)
	}
	return nil
}

// GetDocument fetches a single document by ID.
func (s *Store) GetDocument(ctx context.Context, id string) (*Document, error) {
	var d Document
	var included, fullContent int
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, session_id, name, mime_type, content, size_bytes,
		       included, full_content, summary, created_at, updated_at
		FROM documents WHERE id = ?`, id).Scan(
		&d.ID, &d.SessionID, &d.Name, &d.MimeType, &d.Content, &d.SizeBytes,
		&included, &fullContent, &d.Summary, &d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get document %s: %w", id, err)
	}
	d.Included = included != 0
	d.FullContent = fullContent != 0
	return &d, nil
}

// ListDocuments returns all documents for a session, ordered by created_at DESC.
func (s *Store) ListDocuments(ctx context.Context, sessionID string) ([]Document, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, session_id, name, mime_type, content, size_bytes,
		       included, full_content, summary, created_at, updated_at
		FROM documents
		WHERE session_id = ?
		ORDER BY created_at DESC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}
	defer rows.Close()

	var out []Document
	for rows.Next() {
		var d Document
		var included, fullContent int
		if err := rows.Scan(
			&d.ID, &d.SessionID, &d.Name, &d.MimeType, &d.Content, &d.SizeBytes,
			&included, &fullContent, &d.Summary, &d.CreatedAt, &d.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan document: %w", err)
		}
		d.Included = included != 0
		d.FullContent = fullContent != 0
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpdateDocumentToggles updates the include/full-content toggles and summary for a document.
func (s *Store) UpdateDocumentToggles(ctx context.Context, id string, included, fullContent bool, summary string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx, `
		UPDATE documents
		SET included = ?, full_content = ?, summary = ?, updated_at = ?
		WHERE id = ?`,
		boolToInt(included), boolToInt(fullContent), summary, now, id,
	)
	if err != nil {
		return fmt.Errorf("update document toggles %s: %w", id, err)
	}
	return nil
}

// DeleteDocument removes a document by ID.
func (s *Store) DeleteDocument(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM documents WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete document %s: %w", id, err)
	}
	return nil
}

// GetIncludedDocuments returns documents for a session that are set to be
// included in agent context. The returned slice excludes content when the
// document is set to pointer mode (full_content=false) — callers use the
// Summary field instead for pointer-style injection.
func (s *Store) GetIncludedDocuments(ctx context.Context, sessionID string) ([]Document, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, session_id, name, mime_type, content, size_bytes,
		       included, full_content, summary, created_at, updated_at
		FROM documents
		WHERE session_id = ? AND included = 1
		ORDER BY created_at ASC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("get included documents: %w", err)
	}
	defer rows.Close()

	var out []Document
	for rows.Next() {
		var d Document
		var included, fullContent int
		if err := rows.Scan(
			&d.ID, &d.SessionID, &d.Name, &d.MimeType, &d.Content, &d.SizeBytes,
			&included, &fullContent, &d.Summary, &d.CreatedAt, &d.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan document: %w", err)
		}
		d.Included = included != 0
		d.FullContent = fullContent != 0
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetSessionContextPrompt returns the session-scoped user context prompt.
// Returns empty string when not set.
func (s *Store) GetSessionContextPrompt(ctx context.Context, sessionID string) (string, error) {
	var prompt string
	err := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(context_prompt,'') FROM sessions WHERE id = ?`, sessionID,
	).Scan(&prompt)
	if err != nil {
		return "", fmt.Errorf("get session context prompt %s: %w", sessionID, err)
	}
	return prompt, nil
}

// SetSessionContextPrompt persists the session-scoped user context prompt.
func (s *Store) SetSessionContextPrompt(ctx context.Context, sessionID, prompt string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
		`UPDATE sessions SET context_prompt = ?, updated_at = ? WHERE id = ?`,
		prompt, now, sessionID,
	)
	if err != nil {
		return fmt.Errorf("set session context prompt %s: %w", sessionID, err)
	}
	return nil
}

// boolToInt converts bool to SQLite integer (1/0).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
