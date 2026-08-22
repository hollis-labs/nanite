package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	feotel "github.com/hollis-labs/go-otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/dispatcher"
	"github.com/hollis-labs/nanite/internal/lifecycle"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/task"
	"github.com/hollis-labs/nanite/internal/worker"
)

// fullWorkerSpawner is the narrow worker-manager surface orchestration needs.
// Keeping the dependency narrow also makes panic-before-result regressions
// directly injectable without constructing a complete worker manager.
type fullWorkerSpawner interface {
	SpawnFull(context.Context, worker.SpawnRequest) (*worker.Result, error)
}

func delegateSubTasks(ctx context.Context, owner *lifecycle.Manager, workers fullWorkerSpawner, subTasks []chat.SubTask, parentSessionID, model string) ([]chat.SubTaskResult, error) {
	type indexedResult struct {
		idx    int
		result chat.SubTaskResult
	}
	ch := make(chan indexedResult, len(subTasks))
	results := make([]chat.SubTaskResult, len(subTasks))

	for i, st := range subTasks {
		idx := i
		sub := st
		execute := func(ownerCtx context.Context) (ir indexedResult) {
			ir.idx = idx
			ir.result.Title = sub.Title
			// Always publish exactly one result, including a synthetic error
			// when SpawnFull (or closure bookkeeping) panics before returning.
			defer func() {
				if recovered := recover(); recovered != nil {
					slog.Error("delegation: worker panicked before publishing result",
						"idx", idx+1, "title", sub.Title, "panic", recovered)
					ir.result = chat.SubTaskResult{
						Title: sub.Title,
						Error: fmt.Sprintf("worker panicked: %v", recovered),
					}
				}
			}()
			workerCtx := ctx
			if ownerCtx != nil {
				var cancel context.CancelFunc
				workerCtx, cancel = context.WithCancel(ctx)
				stopOwnerCancel := context.AfterFunc(ownerCtx, cancel)
				defer func() {
					stopOwnerCancel()
					cancel()
				}()
			}
			slog.Info("delegation: spawning worker", "idx", idx+1, "total", len(subTasks), "title", sub.Title)
			wr, err := workers.SpawnFull(workerCtx, worker.SpawnRequest{
				ParentSessionID: parentSessionID,
				Title:           sub.Title,
				Description:     sub.Description,
				Model:           model,
			})
			if err != nil {
				ir.result.Error = err.Error()
			} else {
				ir.result.Output = wr.Content
				if !wr.Success {
					ir.result.Error = wr.Error
				}
			}
			return ir
		}

		if owner == nil {
			// Bare chatServiceImpl values are common in focused unit tests. Match
			// goTracked's ownership rule: execute inline rather than creating an
			// unowned manager or goroutine. Check cancellation before every
			// invocation and again after it returns. An inline worker cannot be
			// preempted if it ignores ctx, but cancellation still prevents every
			// later task from starting and deterministically wins over success.
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			ir := execute(nil)
			results[ir.idx] = ir.result
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			continue
		}
		owner.Go("delegation.delegateAndAggregate.spawnWorker", func(ownerCtx context.Context) {
			ch <- execute(ownerCtx)
		})
	}

	if owner == nil {
		return results, nil
	}
	var ownerDone <-chan struct{}
	if owner != nil {
		ownerDone = owner.Context().Done()
	}
	for range subTasks {
		select {
		case ir := <-ch:
			results[ir.idx] = ir.result
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ownerDone:
			return nil, context.Canceled
		}
	}
	return results, nil
}

// DelegateTask implements ChatService. It spawns a worker session, sends the
// task, waits for completion, and returns the result.
func (s *chatServiceImpl) DelegateTask(ctx context.Context, req chat.DelegationRequest) (*chat.DelegationResult, error) {
	ctx, span := feotel.StartSpan(ctx, "nanite.delegateTask")
	span.SetAttributes(
		attribute.String("nanite.delegation.parent_session", req.ParentSessionID),
		attribute.String("nanite.delegation.title", req.Title),
	)
	defer span.End()

	// Load parent session for defaults.
	parentSession, err := s.sessions.Get(ctx, req.ParentSessionID)
	if err != nil {
		return nil, fmt.Errorf("load parent session: %w", err)
	}

	// Resolve defaults from parent. TASKS/adhoc/01-eliminate-file-based-
	// agent-runtime.md: the error branch below used to hardcode the literal
	// placeholder string "file-default" (written straight into
	// session_agents.agent_id, which has no FK, so a bad value here was
	// never caught at write time) — resolve the real "default" agent row
	// instead. Unlike the other two call sites this task touched
	// (internal/api/sessions.go, internal/service/session.go, both
	// explicitly best-effort — "log but don't fail"), this call site's
	// existing EnsureSessionAgent write below is NOT best-effort: it already
	// returns a hard error on failure. A worker session with no resolvable
	// agent at all can't actually do anything useful, so if even the
	// "default" agent row can't be found, fail the delegation outright
	// (mirroring agent.go's ResolveForSession hard-error behavior) rather
	// than proceed to create an orphaned, agent-less worker session.
	agentID := req.AgentID
	if agentID == "" {
		if agent, resolveErr := s.agents.ResolveForSession(ctx, req.ParentSessionID); resolveErr == nil {
			agentID = agent.ID
		} else if defaultAgent, defaultErr := s.agents.GetBySlug(ctx, "default"); defaultErr == nil && defaultAgent != nil {
			agentID = defaultAgent.ID
		}
	}
	if agentID == "" {
		return nil, fmt.Errorf("delegation: no agent could be resolved for worker session (parent session %s)", req.ParentSessionID)
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
		// CW-20260526-0003: resolver walks user_settings →
		// providers.default_model. Worker sessions inherit the system
		// default if neither the request nor the parent set one.
		if _, rm, err := s.store.ResolveProviderAndModel(ctx, "", ""); err == nil {
			model = rm
		} else {
			return nil, fmt.Errorf("delegation: %w", err)
		}
	}
	// Create worker session.
	workerSession := &store.Session{
		ID:        uuid.New().String(),
		Title:     fmt.Sprintf("[Worker] %s", req.Title),
		ProjectID: parentSession.ProjectID,
		Model:     model,
		Status:    "active",
		Metadata: fmt.Sprintf(`{"delegation":true,"parent_session_id":%q,"task_title":%q}`,
			req.ParentSessionID, req.Title),
	}
	if err := s.store.CreateSession(ctx, workerSession); err != nil {
		return nil, fmt.Errorf("create worker session: %w", err)
	}

	// Assign agent to worker session.
	if err := s.store.EnsureSessionAgent(ctx, workerSession.ID, agentID, mode, true); err != nil {
		return nil, fmt.Errorf("assign agent to worker: %w", err)
	}

	slog.Info("delegation: created worker session",
		"short_code", workerSession.ShortCode, "agent", agentID, "mode", mode, "title", req.Title)

	// Track task lifecycle if task service is available.
	var trackedTask *task.Task
	if s.tasks != nil {
		trackedTask = &task.Task{
			SessionID:       req.ParentSessionID,
			WorkerSessionID: workerSession.ID,
			Title:           req.Title,
			Description:     req.Description,
			AssigneeAgentID: agentID,
		}
		if err := s.tasks.Create(ctx, trackedTask); err != nil {
			slog.Warn("delegation: failed to create task", "err", err)
			trackedTask = nil
		} else {
			_ = s.tasks.Transition(ctx, trackedTask.ID, task.StatusInProgress)
		}
	}

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
	if err := s.store.CreateMessage(ctx, userMsg); err != nil {
		return nil, fmt.Errorf("create delegation message: %w", err)
	}

	// Create assistant message and stream channel for the worker.
	assistantMsgID := uuid.New().String()
	ch := s.streams.CreateStream(assistantMsgID, workerSession.ID)

	// Phase 4 task 04 (docs/engineering/architecture/04-harness.md,
	// "Run-another-agent surfaces, unified"): build the shared,
	// surface-agnostic AgentRunRequest before deriving the narrower
	// dispatcher.Request Dispatcher.Run actually consumes. Delegation's
	// 5-minute synchronous timeout now has a single source of truth
	// (runReq.Timeout) instead of being a literal re-hardcoded at the
	// select loop below.
	runReq := dispatcher.AgentRunRequest{
		CallerType:      dispatcher.CallerSubagent,
		Completion:      dispatcher.CompletionSyncDrain,
		TargetSessionID: workerSession.ID,
		Prompt:          taskContent,
		Timeout:         5 * time.Minute,
	}

	// Start async generation in worker session.
	// CW-20260512-0121 (SP-20260512-0011): delegation spawns a child
	// worker session and runs one assistant turn against it — the
	// agent-dispatch flavor of subagent. Route through the single
	// dispatcher door with CallerSubagent so the request_build slog
	// reports caller=subagent and the structural slot shape lines up
	// with the other subagent path (ChatRunner). Dispatcher rejection
	// (programmer-only path) is fatal for the delegation — close ch
	// so the drain loop below terminates rather than blocking until
	// the 5-minute timeout.
	s.goTracked("delegation.delegateTask.dispatch", func(ownerCtx context.Context) {
		dispatchCtx, cancel := context.WithCancel(ctx)
		stopOwnerCancel := context.AfterFunc(ownerCtx, cancel)
		defer func() {
			stopOwnerCancel()
			cancel()
		}()
		if err := s.dispatcher.Run(dispatchCtx, dispatcher.Request{
			SessionID:      runReq.TargetSessionID,
			AssistantMsgID: assistantMsgID,
			UserContent:    runReq.Prompt,
			CallerType:     runReq.CallerType,
		}, ch); err != nil {
			slog.Error("delegation: dispatcher.Run rejected",
				"worker_session_id", workerSession.ID,
				"assistant_msg_id", assistantMsgID,
				"err", err,
			)
			close(ch)
		}
	})

	// Drain the stream and collect the response.
	result := &chat.DelegationResult{
		WorkerSessionID: workerSession.ID,
		Success:         true,
	}
	if trackedTask != nil {
		result.TaskID = trackedTask.ID
	}

	var content strings.Builder
	timeout := time.After(runReq.Timeout)

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

				// Phase 4 task 04: derive (never drive) the shared
				// AgentRunResult from the already-finalized result
				// fields above — this is purely an additional,
				// normalized reporting view (dispatcher.LogOutcome),
				// not a second source of truth for result's fields.
				outcome := dispatcher.AgentRunResult{
					CallerType:      runReq.CallerType,
					Completion:      runReq.Completion,
					TargetSessionID: workerSession.ID,
					Content:         result.Content,
					TokensUsed:      result.TokensUsed,
					Status:          dispatcher.RunStatusCompleted,
				}
				if !result.Success {
					outcome.Status = dispatcher.RunStatusFailed
					outcome.Err = errors.New(result.Error)
				}
				dispatcher.LogOutcome(outcome)

				slog.Info("delegation: worker completed",
					"short_code", workerSession.ShortCode, "chars", len(result.Content))

				// Update task tracking.
				if trackedTask != nil && s.tasks != nil {
					if result.Success {
						trackedTask.Result = result.Content
						trackedTask.TokensUsed = result.TokensUsed
						_ = s.tasks.Update(ctx, trackedTask)
						_ = s.tasks.Transition(ctx, trackedTask.ID, task.StatusCompleted)
					} else {
						trackedTask.Error = result.Error
						_ = s.tasks.Update(ctx, trackedTask)
						_ = s.tasks.Transition(ctx, trackedTask.ID, task.StatusFailed)
					}
				}

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
			dispatcher.LogOutcome(dispatcher.AgentRunResult{
				CallerType:      runReq.CallerType,
				Completion:      runReq.Completion,
				TargetSessionID: workerSession.ID,
				Content:         result.Content,
				Status:          dispatcher.RunStatusTimedOut,
				Err:             errors.New(result.Error),
			})
			slog.Warn("delegation: worker timed out", "short_code", workerSession.ShortCode)
			if trackedTask != nil && s.tasks != nil {
				trackedTask.Error = result.Error
				_ = s.tasks.Update(ctx, trackedTask)
				_ = s.tasks.Transition(ctx, trackedTask.ID, task.StatusFailed)
			}
			return result, nil

		case <-ctx.Done():
			result.Content = content.String()
			result.Success = false
			result.Error = "delegation cancelled"
			dispatcher.LogOutcome(dispatcher.AgentRunResult{
				CallerType:      runReq.CallerType,
				Completion:      runReq.Completion,
				TargetSessionID: workerSession.ID,
				Content:         result.Content,
				Status:          dispatcher.RunStatusCancelled,
				Err:             errors.New(result.Error),
			})
			if trackedTask != nil && s.tasks != nil {
				_ = s.tasks.Cancel(ctx, trackedTask.ID)
			}
			return result, nil

		case <-s.trackedDone():
			result.Content = content.String()
			result.Success = false
			result.Error = "delegation stopped during shutdown"
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

	slog.Info("delegation: decomposed into sub-tasks", "count", len(decomposition.SubTasks))

	// Create parent task for orchestration tracking.
	var parentTask *task.Task
	if s.tasks != nil {
		parentTask = &task.Task{
			SessionID:   parentSessionID,
			Title:       "Orchestration: " + userMessage[:min(60, len(userMessage))],
			Description: userMessage,
		}
		if err := s.tasks.Create(ctx, parentTask); err != nil {
			slog.Warn("delegation: failed to create parent task", "err", err)
			parentTask = nil
		} else {
			_ = s.tasks.Transition(ctx, parentTask.ID, task.StatusInProgress)
		}
	}

	// Build orchestration plan.
	parentSession, err := s.sessions.Get(ctx, parentSessionID)
	if err != nil {
		return nil, fmt.Errorf("load parent session: %w", err)
	}
	plan, err := s.orchestrator.BuildPlan(ctx, decomposition, parentSession.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("build plan: %w", err)
	}

	// Execute sub-tasks. Use worker manager for concurrent execution when
	// available, fall back to sequential delegation otherwise.
	var results []chat.SubTaskResult
	if s.workers != nil {
		results, err = delegateSubTasks(ctx, s.lifecycle, s.workers, decomposition.SubTasks, parentSessionID, model)
		if err != nil {
			return nil, err
		}
	} else {
		// Sequential fallback via direct delegation.
		for i, st := range decomposition.SubTasks {
			slog.Info("delegation: executing sub-task", "idx", i+1, "total", len(decomposition.SubTasks), "title", st.Title)

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
	}

	// Aggregate results.
	orchResult, err := s.orchestrator.Aggregate(ctx, plan, results, model)
	if err != nil {
		if parentTask != nil && s.tasks != nil {
			parentTask.Error = err.Error()
			_ = s.tasks.Update(ctx, parentTask)
			_ = s.tasks.Transition(ctx, parentTask.ID, task.StatusFailed)
		}
		return nil, fmt.Errorf("aggregation failed: %w", err)
	}

	// Mark parent task as completed.
	if parentTask != nil && s.tasks != nil {
		parentTask.Result = orchResult.FinalOutput
		_ = s.tasks.Update(ctx, parentTask)
		_ = s.tasks.Transition(ctx, parentTask.ID, task.StatusCompleted)
	}

	return orchResult, nil
}
