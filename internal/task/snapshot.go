package task

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// SQLiteSnapshot implements SnapshotStore using a *sql.DB.
type SQLiteSnapshot struct {
	DB *sql.DB
}

func (s *SQLiteSnapshot) UpsertTask(t *Task) error {
	meta, _ := json.Marshal(t.Metadata)
	var completedAt *string
	if t.CompletedAt != nil {
		v := t.CompletedAt.Format(time.RFC3339)
		completedAt = &v
	}

	_, err := s.DB.Exec(`
		INSERT INTO tasks (id, parent_id, session_id, worker_session_id, title,
			description, status, assignee_agent_id, result, error,
			tokens_used, metadata, created_at, updated_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			parent_id = excluded.parent_id,
			session_id = excluded.session_id,
			worker_session_id = excluded.worker_session_id,
			title = excluded.title,
			description = excluded.description,
			status = excluded.status,
			assignee_agent_id = excluded.assignee_agent_id,
			result = excluded.result,
			error = excluded.error,
			tokens_used = excluded.tokens_used,
			metadata = excluded.metadata,
			updated_at = excluded.updated_at,
			completed_at = excluded.completed_at`,
		t.ID, t.ParentID, t.SessionID, t.WorkerSessionID, t.Title,
		t.Description, string(t.Status), t.AssigneeAgentID, t.Result, t.Error,
		t.TokensUsed, string(meta), t.CreatedAt.Format(time.RFC3339),
		t.UpdatedAt.Format(time.RFC3339), completedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert task: %w", err)
	}
	return nil
}

func (s *SQLiteSnapshot) ListTasks(filter TaskFilter) ([]*Task, error) {
	query := `SELECT id, COALESCE(parent_id,''), session_id,
		COALESCE(worker_session_id,''), title, COALESCE(description,''),
		status, COALESCE(assignee_agent_id,''), COALESCE(result,''),
		COALESCE(error,''), tokens_used, COALESCE(metadata,'{}'),
		created_at, updated_at, completed_at
		FROM tasks WHERE 1=1`
	var args []any

	if filter.SessionID != "" {
		query += " AND session_id = ?"
		args = append(args, filter.SessionID)
	}
	if filter.ParentID != "" {
		query += " AND parent_id = ?"
		args = append(args, filter.ParentID)
	}
	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, string(filter.Status))
	}

	query += " ORDER BY created_at DESC"

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer func() {
		_ = rows.Close() // Query and iteration errors are surfaced separately; deferred close is cleanup only.
	}()

	var tasks []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func scanTask(rows *sql.Rows) (*Task, error) {
	var t Task
	var status, metaJSON, createdAt, updatedAt string
	var completedAt sql.NullString

	err := rows.Scan(&t.ID, &t.ParentID, &t.SessionID,
		&t.WorkerSessionID, &t.Title, &t.Description,
		&status, &t.AssigneeAgentID, &t.Result,
		&t.Error, &t.TokensUsed, &metaJSON,
		&createdAt, &updatedAt, &completedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan task: %w", err)
	}

	t.Status = Status(status)
	if err := json.Unmarshal([]byte(metaJSON), &t.Metadata); err != nil {
		slog.Warn("task snapshot: failed to parse stored value", "task_id", t.ID, "field", "metadata", "err", err)
	}
	if t.Metadata == nil {
		t.Metadata = make(map[string]string)
	}
	var parseErr error
	t.CreatedAt, parseErr = time.Parse(time.RFC3339, createdAt)
	if parseErr != nil {
		slog.Warn("task snapshot: failed to parse stored value", "task_id", t.ID, "field", "created_at", "err", parseErr)
	}
	t.UpdatedAt, parseErr = time.Parse(time.RFC3339, updatedAt)
	if parseErr != nil {
		slog.Warn("task snapshot: failed to parse stored value", "task_id", t.ID, "field", "updated_at", "err", parseErr)
	}
	if completedAt.Valid {
		ct, err := time.Parse(time.RFC3339, completedAt.String)
		if err != nil {
			slog.Warn("task snapshot: failed to parse stored value", "task_id", t.ID, "field", "completed_at", "err", err)
		}
		t.CompletedAt = &ct
	}
	return &t, nil
}
