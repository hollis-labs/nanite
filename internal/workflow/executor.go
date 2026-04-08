package workflow

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Event represents a workflow execution event.
type Event struct {
	Type       string         // e.g. "step.started", "pipeline.completed"
	PipelineID string
	RunID      string
	StepID     string // empty for pipeline events
	Data       map[string]any
	Timestamp  time.Time
}

// EventHandler receives workflow execution events.
type EventHandler func(event Event)

// Executor runs pipelines.
type Executor struct {
	onEvent EventHandler
}

// ExecutorOption configures an Executor.
type ExecutorOption func(*Executor)

// WithEventHandler sets the event callback for the executor.
func WithEventHandler(h EventHandler) ExecutorOption {
	return func(e *Executor) {
		e.onEvent = h
	}
}

// NewExecutor creates a new Executor with the given options.
func NewExecutor(opts ...ExecutorOption) *Executor {
	e := &Executor{}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

func (e *Executor) emit(event Event) {
	if e.onEvent != nil {
		e.onEvent(event)
	}
}

// Run executes a pipeline. Blocks until completion, cancellation, or failure.
func (e *Executor) Run(ctx context.Context, pipeline *Pipeline, input map[string]any) (*RunState, error) {
	if pipeline == nil {
		return nil, fmt.Errorf("executor: nil pipeline")
	}

	runID := uuid.NewString()
	state := &RunState{
		PipelineID: pipeline.ID,
		RunID:      runID,
		Status:     RunRunning,
		StepStates: make(map[string]*StepState),
		StartedAt:  time.Now(),
	}

	// Initialize all step states.
	stepMap := make(map[string]*Step)
	for i := range pipeline.Steps {
		s := &pipeline.Steps[i]
		stepMap[s.ID] = s
		state.StepStates[s.ID] = &StepState{
			StepID: s.ID,
			Status: StepPending,
		}
	}

	// Validate dependencies and build adjacency.
	dependents := make(map[string][]string)   // step -> steps that depend on it
	inDegree := make(map[string]int)          // step -> number of unmet dependencies
	for _, s := range pipeline.Steps {
		inDegree[s.ID] = len(s.DependsOn)
		for _, dep := range s.DependsOn {
			if _, ok := stepMap[dep]; !ok {
				state.Status = RunFailed
				state.Error = fmt.Sprintf("step %q depends on unknown step %q", s.ID, dep)
				state.CompletedAt = time.Now()
				return state, fmt.Errorf("executor: %s", state.Error)
			}
			dependents[dep] = append(dependents[dep], s.ID)
		}
	}

	// Detect cycles using Kahn's algorithm (topological sort).
	sorted, err := topoSort(pipeline.Steps, inDegree, dependents)
	if err != nil {
		state.Status = RunFailed
		state.Error = err.Error()
		state.CompletedAt = time.Now()
		return state, err
	}

	e.emit(Event{
		Type:       "pipeline.started",
		PipelineID: pipeline.ID,
		RunID:      runID,
		Data:       map[string]any{"step_count": len(sorted)},
		Timestamp:  time.Now(),
	})

	// Group steps by topological level for concurrent execution.
	levels := topoLevels(sorted, pipeline.Steps)

	// Build environment: pipeline env + input params.
	env := make(map[string]string)
	for k, v := range pipeline.Env {
		env[k] = v
	}

	// Collect results as steps complete.
	priorResults := make(map[string]*StepOutput)
	var mu sync.Mutex

	defaultTimeout := pipeline.DefaultTimeout
	if defaultTimeout <= 0 {
		defaultTimeout = 30 * time.Second
	}

	pipelineFailed := false

	for _, level := range levels {
		select {
		case <-ctx.Done():
			// Cancel remaining steps.
			for _, lvl := range levels {
				for _, stepID := range lvl {
					ss := state.StepStates[stepID]
					if ss.Status == StepPending {
						ss.Status = StepCancelled
						ss.CompletedAt = time.Now()
					}
				}
			}
			state.Status = RunCancelled
			state.CompletedAt = time.Now()
			e.emit(Event{
				Type:       "pipeline.cancelled",
				PipelineID: pipeline.ID,
				RunID:      runID,
				Timestamp:  time.Now(),
			})
			return state, nil
		default:
		}

		if pipelineFailed {
			// Skip remaining levels.
			for _, stepID := range level {
				ss := state.StepStates[stepID]
				if ss.Status == StepPending {
					ss.Status = StepSkipped
					ss.SkipReason = "pipeline failed"
					ss.CompletedAt = time.Now()
				}
			}
			continue
		}

		var wg sync.WaitGroup
		for _, stepID := range level {
			step := stepMap[stepID]
			ss := state.StepStates[stepID]

			// Check if any required dependency failed.
			skip := false
			for _, depID := range step.DependsOn {
				depState := state.StepStates[depID]
				depStep := stepMap[depID]
				if depState.Status == StepFailed && depStep.Required {
					ss.Status = StepSkipped
					ss.SkipReason = fmt.Sprintf("required dependency %q failed", depID)
					ss.CompletedAt = time.Now()
					e.emit(Event{
						Type:       "step.skipped",
						PipelineID: pipeline.ID,
						RunID:      runID,
						StepID:     stepID,
						Data:       map[string]any{"reason": ss.SkipReason},
						Timestamp:  time.Now(),
					})
					skip = true
					break
				}
				if depState.Status == StepSkipped {
					ss.Status = StepSkipped
					ss.SkipReason = fmt.Sprintf("dependency %q was skipped", depID)
					ss.CompletedAt = time.Now()
					skip = true
					break
				}
			}
			if skip {
				continue
			}

			// Run gate check.
			if step.Gate != nil {
				mu.Lock()
				prCopy := copyResults(priorResults)
				mu.Unlock()
				proceed, reason := step.Gate(ctx, prCopy)
				if !proceed {
					ss.Status = StepSkipped
					ss.SkipReason = reason
					ss.CompletedAt = time.Now()
					e.emit(Event{
						Type:       "step.skipped",
						PipelineID: pipeline.ID,
						RunID:      runID,
						StepID:     stepID,
						Data:       map[string]any{"reason": reason},
						Timestamp:  time.Now(),
					})
					continue
				}
			}

			wg.Add(1)
			go func(s *Step, ss *StepState) {
				defer wg.Done()

				timeout := s.Timeout
				if timeout <= 0 {
					timeout = defaultTimeout
				}
				stepCtx, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()

				ss.Status = StepRunning
				ss.StartedAt = time.Now()
				e.emit(Event{
					Type:       "step.started",
					PipelineID: pipeline.ID,
					RunID:      runID,
					StepID:     s.ID,
					Timestamp:  time.Now(),
				})

				mu.Lock()
				prCopy := copyResults(priorResults)
				mu.Unlock()

				stepInput := StepInput{
					PipelineID:   pipeline.ID,
					StepID:       s.ID,
					Params:       input,
					PriorResults: prCopy,
					Env:          env,
				}

				out, execErr := e.executeWithRetry(stepCtx, s, stepInput)
				ss.CompletedAt = time.Now()

				if execErr != nil {
					ss.Status = StepFailed
					ss.Error = execErr.Error()
					if out != nil {
						ss.Output = out
					}
					e.emit(Event{
						Type:       "step.failed",
						PipelineID: pipeline.ID,
						RunID:      runID,
						StepID:     s.ID,
						Data:       map[string]any{"error": execErr.Error()},
						Timestamp:  time.Now(),
					})
					if s.Required {
						mu.Lock()
						pipelineFailed = true
						mu.Unlock()
					}
				} else {
					ss.Status = StepCompleted
					ss.Output = out
					mu.Lock()
					priorResults[s.ID] = out
					mu.Unlock()
					e.emit(Event{
						Type:       "step.completed",
						PipelineID: pipeline.ID,
						RunID:      runID,
						StepID:     s.ID,
						Timestamp:  time.Now(),
					})
				}
			}(step, ss)
		}
		wg.Wait()
	}

	// Determine final status.
	state.CompletedAt = time.Now()
	if ctx.Err() != nil {
		state.Status = RunCancelled
		e.emit(Event{
			Type:       "pipeline.cancelled",
			PipelineID: pipeline.ID,
			RunID:      runID,
			Timestamp:  time.Now(),
		})
	} else if pipelineFailed {
		state.Status = RunFailed
		// Find the first required step that failed.
		for _, s := range pipeline.Steps {
			ss := state.StepStates[s.ID]
			if ss.Status == StepFailed && s.Required {
				state.Error = fmt.Sprintf("required step %q failed: %s", s.ID, ss.Error)
				break
			}
		}
		e.emit(Event{
			Type:       "pipeline.failed",
			PipelineID: pipeline.ID,
			RunID:      runID,
			Data:       map[string]any{"error": state.Error},
			Timestamp:  time.Now(),
		})
	} else {
		state.Status = RunCompleted
		e.emit(Event{
			Type:       "pipeline.completed",
			PipelineID: pipeline.ID,
			RunID:      runID,
			Timestamp:  time.Now(),
		})
	}

	return state, nil
}

// executeWithRetry runs a step handler with retry logic.
func (e *Executor) executeWithRetry(ctx context.Context, step *Step, input StepInput) (*StepOutput, error) {
	maxAttempts := 1
	var delay time.Duration
	backoff := 1.0

	if step.Retry != nil {
		maxAttempts = step.Retry.MaxAttempts + 1 // +1 for the initial attempt
		delay = step.Retry.Delay
		backoff = step.Retry.Backoff
		if backoff <= 0 {
			backoff = 1.0
		}
	}

	var lastErr error
	var lastOut *StepOutput

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return lastOut, fmt.Errorf("retry cancelled: %w", ctx.Err())
			case <-time.After(delay):
			}
			delay = time.Duration(float64(delay) * backoff)
		}

		out, err := step.Handler.Execute(ctx, input)
		if err == nil {
			return out, nil
		}
		lastErr = err
		lastOut = out
	}

	return lastOut, lastErr
}

// topoSort performs Kahn's algorithm for topological sorting.
// Returns an error if a cycle is detected.
func topoSort(steps []Step, inDegree map[string]int, dependents map[string][]string) ([]string, error) {
	// Copy inDegree to avoid mutation.
	deg := make(map[string]int, len(inDegree))
	for k, v := range inDegree {
		deg[k] = v
	}

	var queue []string
	for _, s := range steps {
		if deg[s.ID] == 0 {
			queue = append(queue, s.ID)
		}
	}

	var sorted []string
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		sorted = append(sorted, id)
		for _, dep := range dependents[id] {
			deg[dep]--
			if deg[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if len(sorted) != len(steps) {
		return nil, fmt.Errorf("executor: dependency cycle detected")
	}
	return sorted, nil
}

// topoLevels groups steps by their topological level (depth from root).
// Steps at the same level can run concurrently.
func topoLevels(sorted []string, steps []Step) [][]string {
	stepByID := make(map[string]*Step, len(steps))
	for i := range steps {
		stepByID[steps[i].ID] = &steps[i]
	}

	depth := make(map[string]int, len(sorted))
	for _, id := range sorted {
		s := stepByID[id]
		maxDep := -1
		for _, dep := range s.DependsOn {
			if d, ok := depth[dep]; ok && d > maxDep {
				maxDep = d
			}
		}
		depth[id] = maxDep + 1
	}

	// Group by depth.
	maxDepth := 0
	for _, d := range depth {
		if d > maxDepth {
			maxDepth = d
		}
	}

	levels := make([][]string, maxDepth+1)
	// Maintain sorted order within each level.
	for _, id := range sorted {
		d := depth[id]
		levels[d] = append(levels[d], id)
	}
	return levels
}

// copyResults creates a shallow copy of the results map.
func copyResults(m map[string]*StepOutput) map[string]*StepOutput {
	cp := make(map[string]*StepOutput, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}
