package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	tiamatotel "github.com/hollis-labs/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/hollis-labs/conduit/internal/store"
)

// DelegationRequest describes a task to delegate to a worker agent session.
type DelegationRequest struct {
	ParentSessionID string `json:"parent_session_id"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	AgentID         string `json:"agent_id,omitempty"`   // worker agent (defaults to parent's agent)
	Mode            string `json:"mode,omitempty"`       // worker mode (defaults to "default")
	Model           string `json:"model,omitempty"`      // LLM model (defaults to parent's model)
	WorkspaceID     string `json:"workspace_id,omitempty"`
}

// DelegationResult holds the outcome of a delegated task.
type DelegationResult struct {
	WorkerSessionID string `json:"worker_session_id"`
	Content         string `json:"content"`
	TokensUsed      int    `json:"tokens_used"`
	Success         bool   `json:"success"`
	Error           string `json:"error,omitempty"`
}

// DelegateTask spawns a worker session, sends the task, waits for completion,
// and returns the result. This is the core delegation loop.
func (e *Engine) DelegateTask(ctx context.Context, req DelegationRequest) (*DelegationResult, error) {
	ctx, span := tiamatotel.StartSpan(ctx, "mentat.delegateTask")
	span.SetAttributes(
		attribute.String("mentat.delegation.parent_session", req.ParentSessionID),
		attribute.String("mentat.delegation.title", req.Title),
	)
	defer span.End()

	// Load parent session for defaults.
	parentSession, err := e.Store.GetSession(req.ParentSessionID)
	if err != nil {
		return nil, fmt.Errorf("load parent session: %w", err)
	}

	// Resolve defaults from parent.
	agentID := req.AgentID
	if agentID == "" {
		sa, err := e.Store.GetSessionPrimaryAgent(req.ParentSessionID)
		if err == nil {
			agentID = sa.AgentID
		} else {
			agentID = "mentat-001"
		}
	}
	mode := req.Mode
	if mode == "" {
		mode = "default"
	}
	model := req.Model
	if model == "" {
		model = parentSession.Model
	}
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	workspaceID := req.WorkspaceID
	if workspaceID == "" {
		workspaceID = parentSession.WorkspaceID
	}

	// Create worker session.
	workerSession := &store.Session{
		ID:          uuid.New().String(),
		Title:       fmt.Sprintf("[Worker] %s", req.Title),
		WorkspaceID: workspaceID,
		ProjectID:   parentSession.ProjectID,
		Model:       model,
		Status:      "active",
		Metadata: fmt.Sprintf(`{"delegation":true,"parent_session_id":%q,"task_title":%q}`,
			req.ParentSessionID, req.Title),
	}
	if err := e.Store.CreateSession(workerSession); err != nil {
		return nil, fmt.Errorf("create worker session: %w", err)
	}

	// Assign agent to worker session.
	if err := e.Store.EnsureSessionAgent(workerSession.ID, agentID, mode, true); err != nil {
		return nil, fmt.Errorf("assign agent to worker: %w", err)
	}

	log.Printf("delegation: created worker session %s (agent=%s, mode=%s) for %q",
		workerSession.ShortCode, agentID, mode, req.Title)

	// Send the task as a user message in the worker session.
	taskContent := fmt.Sprintf("## Delegated Task: %s\n\n%s\n\n"+
		"Complete this task and provide your findings. Be thorough but concise.",
		req.Title, req.Description)

	userMsg := &store.Message{
		ID:        uuid.New().String(),
		SessionID: workerSession.ID,
		Role:      "user",
		Content:   taskContent,
		Metadata:  fmt.Sprintf(`{"source":"delegation","parent_session_id":%q}`, req.ParentSessionID),
	}
	if err := e.Store.CreateMessage(userMsg); err != nil {
		return nil, fmt.Errorf("create delegation message: %w", err)
	}

	// Create assistant message and stream channel for the worker.
	assistantMsgID := uuid.New().String()
	ch := make(chan StreamEvent, 128)
	e.streams.Store(assistantMsgID, ch)

	// Start async generation in worker session.
	go e.generateResponse(ctx, workerSession.ID, assistantMsgID, taskContent, ch)

	// Drain the stream and collect the response.
	result := &DelegationResult{
		WorkerSessionID: workerSession.ID,
		Success:         true,
	}

	var content strings.Builder
	timeout := time.After(5 * time.Minute)

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				// Channel closed — generation complete.
				result.Content = content.String()
				if result.Content == "" {
					result.Success = false
					result.Error = "worker produced no output"
				}
				log.Printf("delegation: worker %s completed — %d chars",
					workerSession.ShortCode, len(result.Content))

				// Archive the worker session after completion.
				_ = e.Store.ArchiveSession(workerSession.ID)
				return result, nil
			}

			switch evt.Type {
			case "delta":
				content.WriteString(evt.Content)
			case "error":
				result.Success = false
				result.Error = evt.Error
				if evt.StructuredError != nil {
					result.Error = evt.StructuredError.Message
				}
			case "stream_end":
				if evt.Usage != nil {
					result.TokensUsed = evt.Usage.InputTokens + evt.Usage.OutputTokens
				}
			}

		case <-timeout:
			result.Content = content.String()
			result.Success = false
			result.Error = "delegation timed out after 5 minutes"
			log.Printf("delegation: worker %s timed out", workerSession.ShortCode)
			return result, nil

		case <-ctx.Done():
			result.Content = content.String()
			result.Success = false
			result.Error = "delegation cancelled"
			return result, nil
		}
	}
}

// DelegateAndAggregate decomposes a complex task, delegates sub-tasks to workers,
// and aggregates results. Returns the combined output.
func (e *Engine) DelegateAndAggregate(ctx context.Context, parentSessionID, userMessage, model string) (*OrchestrationResult, error) {
	if e.Orchestrator == nil || e.Orchestrator.Decomposer == nil {
		return nil, fmt.Errorf("orchestrator not configured")
	}

	// Decompose the task.
	decomposition, err := e.Orchestrator.Decomposer.DecomposeTask(ctx, userMessage, "", model)
	if err != nil {
		return nil, fmt.Errorf("decomposition failed: %w", err)
	}
	if !decomposition.IsComplex || len(decomposition.SubTasks) == 0 {
		return nil, fmt.Errorf("task is not complex enough for delegation")
	}

	log.Printf("delegation: decomposed into %d sub-tasks", len(decomposition.SubTasks))

	// Build orchestration plan (creates Volon tasks if available).
	parentSession, err := e.Store.GetSession(parentSessionID)
	if err != nil {
		return nil, fmt.Errorf("load parent session: %w", err)
	}
	plan, err := e.Orchestrator.BuildPlan(ctx, decomposition, parentSession.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("build plan: %w", err)
	}

	// Execute each sub-task via delegation.
	var results []SubTaskResult
	for i, st := range decomposition.SubTasks {
		log.Printf("delegation: executing sub-task %d/%d: %s", i+1, len(decomposition.SubTasks), st.Title)

		var delegResult *DelegationResult
		delegResult, err = e.DelegateTask(ctx, DelegationRequest{
			ParentSessionID: parentSessionID,
			Title:           st.Title,
			Description:     st.Description,
			Model:           model,
		})
		if err != nil {
			results = append(results, SubTaskResult{
				Title: st.Title,
				Error: err.Error(),
			})
			continue
		}

		result := SubTaskResult{
			Title:  st.Title,
			Output: delegResult.Content,
		}
		if !delegResult.Success {
			result.Error = delegResult.Error
		}
		results = append(results, result)
	}

	// Aggregate results.
	orchResult, err := e.Orchestrator.Aggregate(ctx, plan, results, model)
	if err != nil {
		return nil, fmt.Errorf("aggregation failed: %w", err)
	}

	return orchResult, nil
}

// DelegationStatus returns metadata about a delegation (for UI display).
type DelegationStatus struct {
	ParentSessionID string           `json:"parent_session_id"`
	Workers         []WorkerStatus   `json:"workers"`
	Plan            json.RawMessage  `json:"plan,omitempty"`
}

// WorkerStatus tracks the state of a worker session.
type WorkerStatus struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	Status    string `json:"status"` // running, completed, failed
	Output    string `json:"output,omitempty"`
}
