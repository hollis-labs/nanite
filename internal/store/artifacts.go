package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Artifact origin types.
const (
	ArtifactOriginAuto     = "auto"     // auto-detected from tool calls that write files
	ArtifactOriginPlaced   = "placed"   // deliberately placed by tools/plugins for the user
	ArtifactOriginUploaded = "uploaded" // user-uploaded for agent context
)

// Artifact represents a file artifact attached to a session.
type Artifact struct {
	ID               string `json:"id"`
	SessionID        string `json:"session_id"`
	MessageID        string `json:"message_id"`
	Name             string `json:"name"`
	MimeType         string `json:"mime_type"`
	SizeBytes        int64  `json:"size_bytes"`
	StoragePath      string `json:"storage_path"`
	Metadata         string `json:"metadata"`
	Origin           string `json:"origin"` // "auto", "placed", "uploaded"
	SourceToolCallID string `json:"source_tool_call_id,omitempty"`
	SourceAgentID    string `json:"source_agent_id,omitempty"`
	SourcePluginID   string `json:"source_plugin_id,omitempty"`
	CreatedAt        string `json:"created_at"`
}

// ListArtifacts returns all artifacts for a session.
func (s *Store) ListArtifacts(ctx context.Context, sessionID string) ([]Artifact, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, session_id, COALESCE(message_id,''), name, mime_type,
		        COALESCE(size_bytes,0), storage_path, COALESCE(metadata,'{}'),
		        COALESCE(origin,'uploaded'), COALESCE(source_tool_call_id,''),
		        COALESCE(source_agent_id,''), COALESCE(source_plugin_id,''), created_at
		 FROM artifacts WHERE session_id = ?
		 ORDER BY created_at DESC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	defer closeRows(rows)

	out := make([]Artifact, 0)
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.ID, &a.SessionID, &a.MessageID, &a.Name, &a.MimeType,
			&a.SizeBytes, &a.StoragePath, &a.Metadata,
			&a.Origin, &a.SourceToolCallID, &a.SourceAgentID, &a.SourcePluginID,
			&a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListArtifactsByOrigin returns artifacts for a session filtered by origin type.
func (s *Store) ListArtifactsByOrigin(ctx context.Context, sessionID, origin string) ([]Artifact, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, session_id, COALESCE(message_id,''), name, mime_type,
		        COALESCE(size_bytes,0), storage_path, COALESCE(metadata,'{}'),
		        COALESCE(origin,'uploaded'), COALESCE(source_tool_call_id,''),
		        COALESCE(source_agent_id,''), COALESCE(source_plugin_id,''), created_at
		 FROM artifacts WHERE session_id = ? AND origin = ?
		 ORDER BY created_at DESC`,
		sessionID, origin,
	)
	if err != nil {
		return nil, fmt.Errorf("list artifacts by origin: %w", err)
	}
	defer closeRows(rows)

	out := make([]Artifact, 0)
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.ID, &a.SessionID, &a.MessageID, &a.Name, &a.MimeType,
			&a.SizeBytes, &a.StoragePath, &a.Metadata,
			&a.Origin, &a.SourceToolCallID, &a.SourceAgentID, &a.SourcePluginID,
			&a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListArtifactsByProject returns artifacts whose owning session belongs to the
// given project. F4 (CW-20260429-0004): right-rail Artifacts panel uses this
// to render a "This Project" inherited-artifacts section so users opening a
// new session in a project still see prior artifacts.
//
// When excludeSessionID is non-empty, artifacts owned by that session are
// excluded from the result (the FE renders the active session in its own
// "This Session" section already, so excluding here avoids double-counting).
func (s *Store) ListArtifactsByProject(ctx context.Context, projectID, excludeSessionID string) ([]Artifact, error) {
	if projectID == "" {
		return []Artifact{}, nil
	}

	query := `SELECT a.id, a.session_id, COALESCE(a.message_id,''), a.name, a.mime_type,
		        COALESCE(a.size_bytes,0), a.storage_path, COALESCE(a.metadata,'{}'),
		        COALESCE(a.origin,'uploaded'), COALESCE(a.source_tool_call_id,''),
		        COALESCE(a.source_agent_id,''), COALESCE(a.source_plugin_id,''), a.created_at
		 FROM artifacts a
		 INNER JOIN sessions s ON s.id = a.session_id
		 WHERE s.project_id = ?`
	args := []any{projectID}
	if excludeSessionID != "" {
		query += ` AND a.session_id != ?`
		args = append(args, excludeSessionID)
	}
	query += ` ORDER BY a.created_at DESC`

	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list artifacts by project: %w", err)
	}
	defer closeRows(rows)

	out := make([]Artifact, 0)
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.ID, &a.SessionID, &a.MessageID, &a.Name, &a.MimeType,
			&a.SizeBytes, &a.StoragePath, &a.Metadata,
			&a.Origin, &a.SourceToolCallID, &a.SourceAgentID, &a.SourcePluginID,
			&a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CreateArtifact inserts a new artifact record.
func (s *Store) CreateArtifact(ctx context.Context, a *Artifact) error {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	if a.Metadata == "" {
		a.Metadata = "{}"
	}
	if a.Origin == "" {
		a.Origin = ArtifactOriginUploaded
	}
	now := time.Now().UTC().Format(time.RFC3339)
	a.CreatedAt = now

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO artifacts (id, session_id, message_id, name, mime_type, size_bytes,
		        storage_path, metadata, origin, source_tool_call_id, source_agent_id,
		        source_plugin_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.SessionID, nullIfEmpty(a.MessageID), a.Name, a.MimeType,
		a.SizeBytes, a.StoragePath, a.Metadata, a.Origin,
		nullIfEmpty(a.SourceToolCallID), nullIfEmpty(a.SourceAgentID),
		nullIfEmpty(a.SourcePluginID), now,
	)
	if err != nil {
		return fmt.Errorf("create artifact: %w", err)
	}
	return nil
}

// GetArtifact returns a single artifact by ID.
func (s *Store) GetArtifact(ctx context.Context, id string) (*Artifact, error) {
	var a Artifact
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, session_id, COALESCE(message_id,''), name, mime_type,
		        COALESCE(size_bytes,0), storage_path, COALESCE(metadata,'{}'),
		        COALESCE(origin,'uploaded'), COALESCE(source_tool_call_id,''),
		        COALESCE(source_agent_id,''), COALESCE(source_plugin_id,''), created_at
		 FROM artifacts WHERE id = ?`, id,
	).Scan(&a.ID, &a.SessionID, &a.MessageID, &a.Name, &a.MimeType,
		&a.SizeBytes, &a.StoragePath, &a.Metadata,
		&a.Origin, &a.SourceToolCallID, &a.SourceAgentID, &a.SourcePluginID,
		&a.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get artifact %s: %w", id, err)
	}
	return &a, nil
}
