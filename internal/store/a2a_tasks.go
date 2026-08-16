package store

import (
	"database/sql"
	"time"

	"github.com/hollis-labs/nanite/internal/a2a"
)

// A2ATask represents a record in the a2a_tasks table. This is bookkeeping
// and protocol translation only — it points at the real execution (a
// workflow_runs row or a durable_agent_instances row), never duplicates
// their state.
type A2ATask struct {
	ID string `json:"id"`

	// Target kind: "workflow" or "instance"
	TargetKind string `json:"target_kind"`
	// Target reference: workflow name or instance msg:// URN
	TargetRef string `json:"target_ref"`

	// Caller-supplied message content
	Message string `json:"message"`

	// DurableAgentInstanceID is set once routing resolves — either newly
	// created via workflow launch, or the existing instance targeted directly
	DurableAgentInstanceID sql.NullString `json:"durable_agent_instance_id"`

	// WorkflowRunID is set when the task is backed by a workflow run
	WorkflowRunID sql.NullString `json:"workflow_run_id"`

	// Derived TaskState — cached, refreshed on read and on push-delivery trigger
	State a2a.TaskState `json:"state"`

	// Result and error fields from the task execution
	Result sql.NullString `json:"result"`
	Error  sql.NullString `json:"error"`

	// Push notification config (JSON-encoded PushNotificationConfig)
	PushNotificationConfig sql.NullString `json:"push_notification_config"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// A2APushDelivery tracks push notification delivery attempts.
type A2APushDelivery struct {
	ID           string    `json:"id"`
	TaskID       string    `json:"task_id"`
	TargetState  string    `json:"target_state"`
	AttemptCount int       `json:"attempt_count"`
	LastError    string    `json:"last_error,omitempty"`
	NextRetry    time.Time `json:"next_retry,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// CreateA2APushDelivery inserts a new push delivery record.
func (s *Store) CreateA2APushDelivery(delivery *A2APushDelivery) error {
	const q = `
		INSERT INTO a2a_push_deliveries (
			id, task_id, target_state, attempt_count,
			last_error, next_retry, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.DB.Exec(q,
		delivery.ID, delivery.TaskID, delivery.TargetState, delivery.AttemptCount,
		delivery.LastError, delivery.NextRetry, delivery.CreatedAt, delivery.UpdatedAt,
	)
	return err
}

// GetPendingPushDeliveries retrieves push deliveries ready for retry.
func (s *Store) GetPendingPushDeliveries(now time.Time) ([]*A2APushDelivery, error) {
	const q = `
		SELECT id, task_id, target_state, attempt_count,
			   last_error, next_retry, created_at, updated_at
		FROM a2a_push_deliveries
		WHERE next_retry <= ?
		ORDER BY next_retry ASC
		LIMIT 100
	`
	rows, err := s.DB.Query(q, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deliveries []*A2APushDelivery
	for rows.Next() {
		var d A2APushDelivery
		if err := rows.Scan(
			&d.ID, &d.TaskID, &d.TargetState, &d.AttemptCount,
			&d.LastError, &d.NextRetry, &d.CreatedAt, &d.UpdatedAt,
		); err != nil {
			return nil, err
		}
		deliveries = append(deliveries, &d)
	}
	return deliveries, rows.Err()
}

// UpdateA2APushDelivery updates an existing push delivery record.
func (s *Store) UpdateA2APushDelivery(delivery *A2APushDelivery) error {
	const q = `
		UPDATE a2a_push_deliveries SET
			attempt_count = ?,
			last_error = ?,
			next_retry = ?,
			updated_at = ?
		WHERE id = ?
	`
	_, err := s.DB.Exec(q,
		delivery.AttemptCount, delivery.LastError, delivery.NextRetry,
		delivery.UpdatedAt, delivery.ID,
	)
	return err
}

// DeleteA2APushDelivery deletes a push delivery record by ID.
func (s *Store) DeleteA2APushDelivery(id string) error {
	const q = `DELETE FROM a2a_push_deliveries WHERE id = ?`
	_, err := s.DB.Exec(q, id)
	return err
}

// CreateA2ATask inserts a new A2A task record.
func (s *Store) CreateA2ATask(task *A2ATask) error {
	now := time.Now()
	task.CreatedAt = now
	task.UpdatedAt = now

	query := `
		INSERT INTO a2a_tasks (
			id, target_kind, target_ref, message,
			durable_agent_instance_id, workflow_run_id,
			state, result, error, push_notification_config,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.DB.Exec(query,
		task.ID,
		task.TargetKind,
		task.TargetRef,
		task.Message,
		task.DurableAgentInstanceID,
		task.WorkflowRunID,
		task.State,
		task.Result,
		task.Error,
		task.PushNotificationConfig,
		task.CreatedAt,
		task.UpdatedAt,
	)
	return err
}

// GetA2ATask retrieves an A2A task by ID.
func (s *Store) GetA2ATask(id string) (*A2ATask, error) {
	query := `
		SELECT
			id, target_kind, target_ref, message,
			durable_agent_instance_id, workflow_run_id,
			state, result, error, push_notification_config,
			created_at, updated_at
		FROM a2a_tasks
		WHERE id = ?
	`
	task := &A2ATask{}
	err := s.DB.QueryRow(query, id).Scan(
		&task.ID,
		&task.TargetKind,
		&task.TargetRef,
		&task.Message,
		&task.DurableAgentInstanceID,
		&task.WorkflowRunID,
		&task.State,
		&task.Result,
		&task.Error,
		&task.PushNotificationConfig,
		&task.CreatedAt,
		&task.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return task, nil
}

// UpdateA2ATask updates an existing A2A task record.
func (s *Store) UpdateA2ATask(task *A2ATask) error {
	task.UpdatedAt = time.Now()

	query := `
		UPDATE a2a_tasks SET
			target_kind = ?,
			target_ref = ?,
			message = ?,
			durable_agent_instance_id = ?,
			workflow_run_id = ?,
			state = ?,
			result = ?,
			error = ?,
			push_notification_config = ?,
			updated_at = ?
		WHERE id = ?
	`
	_, err := s.DB.Exec(query,
		task.TargetKind,
		task.TargetRef,
		task.Message,
		task.DurableAgentInstanceID,
		task.WorkflowRunID,
		task.State,
		task.Result,
		task.Error,
		task.PushNotificationConfig,
		task.UpdatedAt,
		task.ID,
	)
	return err
}
