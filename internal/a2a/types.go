// Package a2a implements the Agent-to-Agent (A2A) protocol v1.0 types and
// primitives for Nanite. This package follows zero-import discipline:
// no imports of Nanite-internal types, designed for future extraction to
// libs/go-a2a once validated by a second consumer.
//
// A2A Spec: https://a2a-protocol.org (Linux Foundation)
//
// This is a protocol adapter, not a new execution substrate. Every Task
// Nanite serves is fulfilled by exactly one of Nanite's existing execution
// paths: a Workflow run or a durable-agent wake. The a2a_tasks record is
// bookkeeping and protocol translation — external-facing identity, state
// derivation, push notification config — never a third parallel execution
// mechanism.
package a2a

import "time"

// ── Agent Card Types ──────────────────────────────────────────────────────────

// AgentCard represents an A2A-compatible agent card served at
// /.well-known/agent-card.json per the A2A v1.0 spec.
type AgentCard struct {
	Name               string       `json:"name"`
	Description        string       `json:"description"`
	URL                string       `json:"url"`
	Provider           Provider     `json:"provider"`
	Version            string       `json:"version"`
	Capabilities       Capabilities `json:"capabilities"`
	DefaultInputModes  []string     `json:"defaultInputModes"`
	DefaultOutputModes []string     `json:"defaultOutputModes"`
	Skills             []Skill      `json:"skills"`
}

// Provider identifies the organization providing the agent.
type Provider struct {
	Organization string `json:"organization"`
}

// Capabilities describes what the agent supports.
type Capabilities struct {
	Streaming         bool `json:"streaming"`
	PushNotifications bool `json:"pushNotifications"`
}

// Skill represents a single capability (workflow or agent profile) exposed
// by the agent. Derived from WorkflowDefinition registry or durable-agent
// boot profiles.
type Skill struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Tags        []string    `json:"tags"`
	InputSchema InputSchema `json:"inputSchema"`
}

// InputSchema is a JSON Schema object describing skill inputs.
type InputSchema struct {
	Type       string                    `json:"type"`
	Properties map[string]SchemaProperty `json:"properties,omitempty"`
	Required   []string                  `json:"required,omitempty"`
}

// SchemaProperty describes one JSON Schema property.
type SchemaProperty struct {
	Type        string       `json:"type"`
	Description string       `json:"description,omitempty"`
	Pattern     string       `json:"pattern,omitempty"`
	MinLength   *int         `json:"minLength,omitempty"`
	MaxLength   *int         `json:"maxLength,omitempty"`
	Minimum     *float64     `json:"minimum,omitempty"`
	Maximum     *float64     `json:"maximum,omitempty"`
	Enum        []any        `json:"enum,omitempty"`
	Items       *SchemaItems `json:"items,omitempty"`
}

// SchemaItems describes the items constraint for array types.
type SchemaItems struct {
	Type string `json:"type"`
}

// ── Task Types ────────────────────────────────────────────────────────────────

// TaskState represents the current state of a Task per A2A v1.0 spec.
type TaskState string

const (
	// TaskStateSubmitted: Task accepted, routing not yet started.
	TaskStateSubmitted TaskState = "submitted"
	// TaskStateWorking: Target instance is active, or workflow run is running.
	TaskStateWorking TaskState = "working"
	// TaskStateInputRequired: Workflow-backed task, current step is a paused gate.
	TaskStateInputRequired TaskState = "input-required"
	// TaskStateCompleted: Workflow run / wake completed successfully.
	TaskStateCompleted TaskState = "completed"
	// TaskStateFailed: Workflow run / wake ended in error.
	TaskStateFailed TaskState = "failed"
	// TaskStateCanceled: Task canceled before/during execution.
	TaskStateCanceled TaskState = "canceled"
	// TaskStateRejected: Submission fails validation (unknown target, archived).
	TaskStateRejected TaskState = "rejected"
	// TaskStateAuthRequired: Spec-mandated wire vocabulary value; not produced
	// by any code path in this initial implementation. Reserved for future use.
	TaskStateAuthRequired TaskState = "auth-required"
)

// Task represents an A2A Task submission and lifecycle.
type Task struct {
	ID        string    `json:"id"`
	State     TaskState `json:"state"`
	Message   string    `json:"message,omitempty"`
	Result    any       `json:"result,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// TaskSubmitRequest is the JSON-RPC params for submitting a new Task.
type TaskSubmitRequest struct {
	// Target is either a workflow skill ID or an existing durable-agent
	// instance's msg:// address.
	Target string `json:"target"`
	// Message is the task content; becomes WakePayload.Prompt or workflow params.
	Message string `json:"message"`
	// PushNotificationConfig is optional; enables best-effort push on state transitions.
	PushNotificationConfig *PushNotificationConfig `json:"pushNotificationConfig,omitempty"`
}

// TaskSubmitResponse is the JSON-RPC result for task submission.
type TaskSubmitResponse struct {
	TaskID string    `json:"taskId"`
	State  TaskState `json:"state"`
}

// TaskGetRequest is the JSON-RPC params for retrieving Task status.
type TaskGetRequest struct {
	TaskID string `json:"taskId"`
}

// TaskGetResponse is the JSON-RPC result for task retrieval.
type TaskGetResponse struct {
	Task Task `json:"task"`
}

// TaskCancelRequest is the JSON-RPC params for canceling a Task.
type TaskCancelRequest struct {
	TaskID string `json:"taskId"`
}

// TaskCancelResponse is the JSON-RPC result for task cancellation.
type TaskCancelResponse struct {
	TaskID string    `json:"taskId"`
	State  TaskState `json:"state"`
}

// TaskProvideInputRequest is the JSON-RPC params for resolving a paused
// gate on a workflow-backed Task in TaskStateInputRequired (CW-20260814-0017).
type TaskProvideInputRequest struct {
	TaskID string `json:"taskId"`
	Input  string `json:"input"`
}

// TaskProvideInputResponse is the JSON-RPC result for a resolved gate —
// the Task's state after resolution, re-derived from the resumed workflow
// run's real status (never independently maintained).
type TaskProvideInputResponse struct {
	TaskID string    `json:"taskId"`
	State  TaskState `json:"state"`
}

// PushNotificationConfig describes how to deliver state-change notifications.
// Delivery is best-effort — no documented delivery guarantee in the spec.
type PushNotificationConfig struct {
	URL   string `json:"url"`
	Token string `json:"token,omitempty"`
	Auth  string `json:"auth,omitempty"`
}

// TaskStateNotification is the payload sent to PushNotificationConfig.URL on
// task state transitions.
type TaskStateNotification struct {
	TaskID    string    `json:"taskId"`
	State     TaskState `json:"state"`
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message,omitempty"`
}
