package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type InterruptedTurn struct {
	ID, UserMessageID string
	AgentDir          string
	Headers           map[string]string
}

type RunningTurn struct{ ConversationID, TurnID string }

func (s *Store) RunningTurns(ctx context.Context) ([]RunningTurn, error) {
	rows, err := s.db.Reader().QueryContext(ctx, "SELECT conversation_id,id FROM turns WHERE status='running' ORDER BY conversation_id,seq")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // Query errors are returned through rows.Err or Scan.
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
	var headers string
	err := s.db.Reader().QueryRowContext(ctx, `SELECT id,user_message_id,agent_dir,headers_json FROM turns
  WHERE conversation_id=? AND status='interrupted' ORDER BY seq DESC LIMIT 1`, conversation).Scan(&turn.ID, &turn.UserMessageID, &turn.AgentDir, &headers)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(headers), &turn.Headers); err != nil {
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
	defer func() { _ = rows.Close() }() // Query errors are returned through rows.Err or Scan.
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
