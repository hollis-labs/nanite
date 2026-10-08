package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type CognitiveViewRecord struct {
	SessionViewID     string
	DefinitionRefJSON string
	ChatConfigJSON    string
}

func (s *Store) GetCognitiveView(ctx context.Context, viewID string) (CognitiveViewRecord, error) {
	var record CognitiveViewRecord
	err := s.DB.QueryRowContext(ctx, `SELECT session_view_id,definition_ref_json,chat_config_json FROM cognitive_views WHERE session_view_id=?`, viewID).Scan(&record.SessionViewID, &record.DefinitionRefJSON, &record.ChatConfigJSON)
	return record, err
}

func (s *Store) CreateDefinedCognitiveSession(ctx context.Context, view *Session, policyProfileID string, record CognitiveViewRecord) error {
	return s.createCognitiveSession(ctx, view, policyProfileID, &record)
}

// CreateCognitiveTurn commits admission and its user message together. Queue
// validation belongs to the caller's serialized admission boundary.
func (s *Store) CreateCognitiveTurn(ctx context.Context, user *Message, turnID, snapshotJSON string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `INSERT INTO messages(id,session_id,role,content,metadata,created_at) VALUES(?,?,'user',?,'{}',?)`, user.ID, user.SessionID, user.Content, now); err != nil {
		return fmt.Errorf("create cognitive input: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO cognitive_turns(turn_id,session_view_id,snapshot_json) VALUES(?,?,?)`, turnID, user.SessionID, snapshotJSON); err != nil {
		return fmt.Errorf("create cognitive turn: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE sessions SET last_activity=?,updated_at=? WHERE id=?`, now, now, user.SessionID); err != nil {
		return err
	}
	if err = tx.Commit(); err == nil {
		user.CreatedAt = now
	}
	return err
}

func (s *Store) SaveCognitiveTurnSnapshot(ctx context.Context, viewID, turnID, snapshotJSON string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE cognitive_turns SET snapshot_json=? WHERE turn_id=? AND session_view_id=?`, snapshotJSON, turnID, viewID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) GetCognitiveTurnSnapshot(ctx context.Context, viewID, turnID string) (string, error) {
	var data string
	err := s.DB.QueryRowContext(ctx, `SELECT snapshot_json FROM cognitive_turns WHERE session_view_id=? AND turn_id=?`, viewID, turnID).Scan(&data)
	return data, err
}

func (s *Store) LatestCognitiveTurn(ctx context.Context, viewID string) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT turn_id FROM cognitive_turns WHERE session_view_id=? ORDER BY rowid DESC LIMIT 1`, viewID).Scan(&id)
	return id, err
}
