package store

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Artifact represents a file artifact attached to a session.
type Artifact struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	MessageID   string `json:"message_id"`
	Name        string `json:"name"`
	MimeType    string `json:"mime_type"`
	SizeBytes   int64  `json:"size_bytes"`
	StoragePath string `json:"storage_path"`
	Metadata    string `json:"metadata"`
	CreatedAt   string `json:"created_at"`
}

// ListArtifacts returns all artifacts for a session.
func (s *Store) ListArtifacts(sessionID string) ([]Artifact, error) {
	rows, err := s.DB.Query(
		`SELECT id, session_id, COALESCE(message_id,''), name, mime_type,
		        COALESCE(size_bytes,0), storage_path, COALESCE(metadata,'{}'), created_at
		 FROM artifacts WHERE session_id = ?
		 ORDER BY created_at DESC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	defer rows.Close()

	out := make([]Artifact, 0)
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.ID, &a.SessionID, &a.MessageID, &a.Name, &a.MimeType,
			&a.SizeBytes, &a.StoragePath, &a.Metadata, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CreateArtifact inserts a new artifact record.
func (s *Store) CreateArtifact(a *Artifact) error {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	if a.Metadata == "" {
		a.Metadata = "{}"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	a.CreatedAt = now

	_, err := s.DB.Exec(
		`INSERT INTO artifacts (id, session_id, message_id, name, mime_type, size_bytes, storage_path, metadata, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.SessionID, nullIfEmpty(a.MessageID), a.Name, a.MimeType,
		a.SizeBytes, a.StoragePath, a.Metadata, now,
	)
	if err != nil {
		return fmt.Errorf("create artifact: %w", err)
	}
	return nil
}

// GetArtifact returns a single artifact by ID.
func (s *Store) GetArtifact(id string) (*Artifact, error) {
	var a Artifact
	err := s.DB.QueryRow(
		`SELECT id, session_id, COALESCE(message_id,''), name, mime_type,
		        COALESCE(size_bytes,0), storage_path, COALESCE(metadata,'{}'), created_at
		 FROM artifacts WHERE id = ?`, id,
	).Scan(&a.ID, &a.SessionID, &a.MessageID, &a.Name, &a.MimeType,
		&a.SizeBytes, &a.StoragePath, &a.Metadata, &a.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get artifact %s: %w", id, err)
	}
	return &a, nil
}
