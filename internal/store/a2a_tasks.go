package store

import (
	"context"
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
func (s *Store) CreateA2APushDelivery(ctx context.Context, delivery *A2APushDelivery) error {
	// Fabric/A2A adoption is held; old target references are historical only.
	return ErrVerifiedActorRequired
}

// GetPendingPushDeliveries retrieves push deliveries ready for retry.
func (s *Store) GetPendingPushDeliveries(ctx context.Context, now time.Time) ([]*A2APushDelivery, error) {
	// Fabric/A2A adoption is held; old target references are historical only.
	return nil, ErrVerifiedActorRequired
}

// UpdateA2APushDelivery updates an existing push delivery record.
func (s *Store) UpdateA2APushDelivery(ctx context.Context, delivery *A2APushDelivery) error {
	// Fabric/A2A adoption is held; old target references are historical only.
	return ErrVerifiedActorRequired
}

// DeleteA2APushDelivery deletes a push delivery record by ID.
func (s *Store) DeleteA2APushDelivery(ctx context.Context, id string) error {
	// Fabric/A2A adoption is held; old target references are historical only.
	return ErrVerifiedActorRequired
}

// CreateA2ATask inserts a new A2A task record.
func (s *Store) CreateA2ATask(ctx context.Context, task *A2ATask) error {
	// Fabric/A2A adoption is held; old target references are historical only.
	return ErrVerifiedActorRequired
}

// GetA2ATask retrieves an A2A task by ID.
func (s *Store) GetA2ATask(ctx context.Context, id string) (*A2ATask, error) {
	// Fabric/A2A adoption is held; old target references are historical only.
	return nil, ErrVerifiedActorRequired
}

// UpdateA2ATask updates an existing A2A task record.
func (s *Store) UpdateA2ATask(ctx context.Context, task *A2ATask) error {
	// Fabric/A2A adoption is held; old target references are historical only.
	return ErrVerifiedActorRequired
}
