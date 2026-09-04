// Package workflowapi contains the stable product projection used by the
// Agent Workflows HTTP and SSE endpoints. It deliberately owns no workflow
// loading, scheduling, or execution semantics.
package workflowapi

import "time"

// StepStatus is the status vocabulary exposed by the Agent Workflows API.
type StepStatus string

const (
	StepPending   StepStatus = "pending"
	StepRunning   StepStatus = "running"
	StepCompleted StepStatus = "completed"
	StepFailed    StepStatus = "failed"
	StepSkipped   StepStatus = "skipped"
	StepCanceled  StepStatus = "canceled"
)

// StepOutput is the step output shape exposed by the Agent Workflows API.
type StepOutput struct {
	Data     map[string]any `json:"data,omitempty"`
	Stdout   string         `json:"stdout,omitempty"`
	Stderr   string         `json:"stderr,omitempty"`
	ExitCode int            `json:"exit_code"`
}

// RunStatus is the run status vocabulary exposed by the Agent Workflows API.
type RunStatus string

const (
	RunPending   RunStatus = "pending"
	RunRunning   RunStatus = "running"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
	RunCanceled  RunStatus = "canceled"
)

// RunState is the run state shape exposed by the Agent Workflows API.
type RunState struct {
	PipelineID  string                `json:"pipeline_id"`
	RunID       string                `json:"run_id"`
	Status      RunStatus             `json:"status"`
	StepStates  map[string]*StepState `json:"step_states"`
	StartedAt   time.Time             `json:"started_at"`
	CompletedAt time.Time             `json:"completed_at"`
	Error       string                `json:"error"`
}

// StepState is the step state shape exposed by the Agent Workflows API.
type StepState struct {
	StepID      string      `json:"step_id"`
	Status      StepStatus  `json:"status"`
	Output      *StepOutput `json:"output,omitempty"`
	Attempts    int         `json:"attempts"`
	StartedAt   time.Time   `json:"started_at"`
	CompletedAt time.Time   `json:"completed_at"`
	Error       string      `json:"error"`
	SkipReason  string      `json:"skip_reason"`
}

// Event is the event shape emitted by the Agent Workflows SSE endpoint.
type Event struct {
	Type       string         `json:"type"`
	PipelineID string         `json:"pipeline_id"`
	RunID      string         `json:"run_id"`
	StepID     string         `json:"step_id,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
	Timestamp  time.Time      `json:"timestamp"`
}

// PipelineInfo is workflow definition metadata exposed by the API.
type PipelineInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	StepCount   int    `json:"step_count"`
}

// RunRecord groups an API run projection with its definition and events.
type RunRecord struct {
	Pipeline PipelineInfo `json:"pipeline"`
	Run      *RunState    `json:"run"`
	Events   []Event      `json:"events"`
}
