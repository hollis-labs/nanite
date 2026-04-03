package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	feotel "github.com/hollis-labs/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/hollis-labs/conduit/internal/chat"
	"github.com/hollis-labs/conduit/internal/store"
)

// DelegateTask implements ChatService. It spawns a worker session, sends the
// task, waits for completion, and returns the result.
func (s *chatServiceImpl) DelegateTask(ctx context.Context, req chat.DelegationRequest) (*chat.DelegationResult, error) {
	ctx, span := feotel.StartSpan(ctx, "conduit.delegateTask")
	span.SetAttributes(
		attribute.String("conduit.delegation.parent_session", req.ParentSessionID),
		attribute.String("conduit.delegation.title", req.Title),
	)
	defer span.End()

	// Load parent session for defaults.
	parentSession, err := s.sessions.Get(ctx, req.ParentSessionID)
	if err != nil {
		return nil, fmt.Errorf("load parent session: %w", err)
	}

	// Resolve defaults from parent.
	agentID := req.AgentID
	if agentID == "" {
		if agent, _, resolveErr := s.agents.ResolveForSession(ctx, req.ParentSessionID); resolveErr == nil {
			agentID = agent.ID
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
	if err := s.store.CreateSession(workerSession); err != nil {
		return nil, fmt.Errorf("create worker session: %w", err)
	}

	// Assign agent to worker session.
	if err := s.store.EnsureSessionAgent(workerSession.ID, agentID, mode, true); err != nil {
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
	if err := s.store.CreateMessage(userMsg); err != nil {
		return nil, fmt.Errorf("create delegation message: %w", err)
	}

	// Create assistant message and stream channel for the worker.
	assistantMsgID := uuid.New().String()
	ch := s.streams.CreateStream(assistantMsgID, workerSession.ID)

	// Start async generation in worker session.
	go s.generateResponse(ctx, workerSession.ID, assistantMsgID, taskContent, ch)

	// Drain the stream and collect the response.
	result := &chat.DelegationResult{
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
				_ = s.sessions.Archive(ctx, workerSession.ID)
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

// DelegateAndAggregate implements ChatService. It decomposes a complex task,
// delegates sub-tasks to workers, and aggregates results.
func (s *chatServiceImpl) DelegateAndAggregate(ctx context.Context, parentSessionID, userMessage, model string) (*chat.OrchestrationResult, error) {
	if s.orchestrator == nil || s.orchestrator.Decomposer == nil {
		return nil, fmt.Errorf("orchestrator not configured")
	}

	// Decompose the task.
	decomposition, err := s.orchestrator.Decomposer.DecomposeTask(ctx, userMessage, "", model)
	if err != nil {
		return nil, fmt.Errorf("decomposition failed: %w", err)
	}
	if !decomposition.IsComplex || len(decomposition.SubTasks) == 0 {
		return nil, fmt.Errorf("task is not complex enough for delegation")
	}

	log.Printf("delegation: decomposed into %d sub-tasks", len(decomposition.SubTasks))

	// Build orchestration plan.
	parentSession, err := s.sessions.Get(ctx, parentSessionID)
	if err != nil {
		return nil, fmt.Errorf("load parent session: %w", err)
	}
	plan, err := s.orchestrator.BuildPlan(ctx, decomposition, parentSession.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("build plan: %w", err)
	}

	// Execute each sub-task via delegation.
	var results []chat.SubTaskResult
	for i, st := range decomposition.SubTasks {
		log.Printf("delegation: executing sub-task %d/%d: %s", i+1, len(decomposition.SubTasks), st.Title)

		delegResult, delegErr := s.DelegateTask(ctx, chat.DelegationRequest{
			ParentSessionID: parentSessionID,
			Title:           st.Title,
			Description:     st.Description,
			Model:           model,
		})
		if delegErr != nil {
			results = append(results, chat.SubTaskResult{
				Title: st.Title,
				Error: delegErr.Error(),
			})
			continue
		}

		result := chat.SubTaskResult{
			Title:  st.Title,
			Output: delegResult.Content,
		}
		if !delegResult.Success {
			result.Error = delegResult.Error
		}
		results = append(results, result)
	}

	// Aggregate results.
	orchResult, err := s.orchestrator.Aggregate(ctx, plan, results, model)
	if err != nil {
		return nil, fmt.Errorf("aggregation failed: %w", err)
	}

	return orchResult, nil
}
