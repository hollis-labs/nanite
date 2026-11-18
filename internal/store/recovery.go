package store

import (
	"context"
	"database/sql"
	"errors"
)

type InterruptedTurn struct{ ID, UserMessageID string }

type RunningTurn struct{ ConversationID, TurnID string }

func (s *Store) RunningTurns(ctx context.Context) ([]RunningTurn, error) {
	rows, err := s.db.Reader().QueryContext(ctx, "SELECT conversation_id,id FROM turns WHERE status='running' ORDER BY conversation_id,seq")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunningTurn
	for rows.Next() {
		var turn RunningTurn
		if err := rows.Scan(&turn.ConversationID, &turn.TurnID); err != nil {
			return nil, err
		}
		out = append(out, turn)
	}
	return out, rows.Err()
}

func (s *Store) Interrupted(ctx context.Context, conversation string) (*InterruptedTurn, error) {
	var turn InterruptedTurn
	err := s.db.Reader().QueryRowContext(ctx, `SELECT id,user_message_id FROM turns
  WHERE conversation_id=? AND status='interrupted' ORDER BY seq DESC LIMIT 1`, conversation).Scan(&turn.ID, &turn.UserMessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &turn, nil
}

func (s *Store) RunningJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT id,conversation_id,kind,COALESCE(pid,0),process_identity
  FROM jobs WHERE status='running' ORDER BY started_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var job Job
		if err := rows.Scan(&job.ID, &job.ConversationID, &job.Kind, &job.PID, &job.ProcessIdentity); err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}
