package workflow

import (
	"context"
	"time"
)

// StepStatus tracks step execution state.
type StepStatus string

const (
	StepPending   StepStatus = "pending"
	StepRunning   StepStatus = "running"
	StepCompleted StepStatus = "completed"
	StepFailed    StepStatus = "failed"
	StepSkipped   StepStatus = "skipped"
	StepCancelled StepStatus = "cancelled"
)

// StepInput is passed to each step handler.
type StepInput struct {
	PipelineID   string
	StepID       string
	Params       map[string]any
	PriorResults map[string]*StepOutput // results from completed prior steps, keyed by step ID
	Env          map[string]string
}

// StepOutput is returned by each step handler.
type StepOutput struct {
	Data     map[string]any // structured output
	Stdout   string         // captured stdout (for shell steps)
	Stderr   string         // captured stderr
	ExitCode int            // for shell steps
}

// GateFunc checks whether a step should proceed.
// Returns (proceed, reason). If !proceed, step is skipped with reason.
type GateFunc func(ctx context.Context, priorResults map[string]*StepOutput) (bool, string)

// RetryPolicy defines retry behavior for a step.
type RetryPolicy struct {
	MaxAttempts int           // max retry attempts (0 = no retry)
	Delay       time.Duration // initial delay between retries
	Backoff     float64       // multiplier per retry (1.0 = fixed, 2.0 = exponential)
}

// StepHandler is the interface all step types implement.
type StepHandler interface {
	Execute(ctx context.Context, input StepInput) (*StepOutput, error)
}

// Step is the atomic unit of a workflow.
type Step struct {
	ID        string
	Name      string
	Handler   StepHandler
	Required  bool          // required (failure aborts pipeline) vs optional (failure continues)
	DependsOn []string      // step IDs that must complete first
	Gate      GateFunc      // optional pre-execution check (nil = always proceed)
	Timeout   time.Duration // per-step timeout (0 = use pipeline default)
	Retry     *RetryPolicy  // nil = no retry
}

// Pipeline is an ordered collection of steps with dependency resolution.
type Pipeline struct {
	ID             string
	Name           string
	Description    string
	Steps          []Step
	DefaultTimeout time.Duration // default per-step timeout (30s)
	Env            map[string]string
}

// RunStatus tracks overall pipeline execution state.
type RunStatus string

const (
	RunPending   RunStatus = "pending"
	RunRunning   RunStatus = "running"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
	RunCancelled RunStatus = "cancelled"
)

// RunState tracks the state of a pipeline execution.
type RunState struct {
	PipelineID  string
	RunID       string
	Status      RunStatus
	StepStates  map[string]*StepState // keyed by step ID
	StartedAt   time.Time
	CompletedAt time.Time
	Error       string
}

// StepState tracks individual step execution state.
type StepState struct {
	StepID      string
	Status      StepStatus
	Output      *StepOutput
	Attempts    int
	StartedAt   time.Time
	CompletedAt time.Time
	Error       string
	SkipReason  string
}
