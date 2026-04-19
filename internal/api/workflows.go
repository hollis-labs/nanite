package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/hollis-labs/nanite/internal/workflow"
)

// handleListWorkflowRuns lists recent workflow runs, with optional filters.
//
// GET /api/workflows/runs?status=running&pipeline_id=my-pipe
func (a *API) handleListWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	if a.Services.RunStore == nil {
		a.jsonResp(w, http.StatusOK, []*workflow.RunRecord{})
		return
	}

	pipelineID := r.URL.Query().Get("pipeline_id")
	status := r.URL.Query().Get("status")

	runs := a.Services.RunStore.List(pipelineID, status)
	if runs == nil {
		runs = []*workflow.RunRecord{}
	}
	a.jsonResp(w, http.StatusOK, runs)
}

// handleGetWorkflowRun returns a single workflow run by ID.
//
// GET /api/workflows/runs/{runId}
func (a *API) handleGetWorkflowRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runId")

	if a.Services.RunStore == nil {
		a.errorResp(w, http.StatusNotFound, "run not found")
		return
	}

	record, ok := a.Services.RunStore.Get(runID)
	if !ok {
		a.errorResp(w, http.StatusNotFound, "run not found")
		return
	}

	a.jsonResp(w, http.StatusOK, record)
}

// handleCancelWorkflowRun marks a workflow run as cancelled.
//
// POST /api/workflows/runs/{runId}/cancel
func (a *API) handleCancelWorkflowRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runId")

	if a.Services.RunStore == nil {
		a.errorResp(w, http.StatusNotFound, "run not found")
		return
	}

	record, ok := a.Services.RunStore.Get(runID)
	if !ok {
		a.errorResp(w, http.StatusNotFound, "run not found")
		return
	}

	record.Run.Status = workflow.RunCancelled
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// handleRunWorkflow accepts a YAML workflow definition and executes it.
// The run is stored in the RunStore and events are broadcast via SSE.
//
// POST /api/workflows/runs
func (a *API) handleRunWorkflow(w http.ResponseWriter, r *http.Request) {
	if a.Services.RunStore == nil || a.Services.WorkflowBroadcaster == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "workflow system not initialized")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	pipeline, err := workflow.Load(body)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, fmt.Sprintf("invalid workflow: %v", err))
		return
	}
	if pipeline.ID == "" {
		pipeline.ID = pipeline.Name
	}

	info := workflow.PipelineInfo{
		ID:          pipeline.ID,
		Name:        pipeline.Name,
		Description: pipeline.Description,
		StepCount:   len(pipeline.Steps),
	}

	broadcaster := a.Services.WorkflowBroadcaster
	store := a.Services.RunStore

	// Collect events in a local slice; we'll attach them to the record after the run.
	var events []workflow.Event
	executor := workflow.NewExecutor(workflow.WithEventHandler(func(event workflow.Event) {
		events = append(events, event)
		broadcaster.Broadcast(event)
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	runState, runErr := executor.Run(ctx, pipeline, nil)
	if runState == nil {
		a.errorResp(w, http.StatusInternalServerError, fmt.Sprintf("workflow run failed: %v", runErr))
		return
	}

	store.Add(info, runState)
	// Attach collected events to the stored record.
	for _, ev := range events {
		store.AppendEvent(runState.RunID, ev)
	}

	a.jsonResp(w, http.StatusOK, map[string]any{
		"run_id":      runState.RunID,
		"pipeline_id": pipeline.ID,
		"status":      string(runState.Status),
	})
}

// handleWorkflowEvents serves an SSE stream of workflow execution events.
//
// GET /api/workflows/events
func (a *API) handleWorkflowEvents(w http.ResponseWriter, r *http.Request) {
	if a.Services.WorkflowBroadcaster == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "workflow system not initialized")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		a.errorResp(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	ch := a.Services.WorkflowBroadcaster.Subscribe()
	defer a.Services.WorkflowBroadcaster.Unsubscribe(ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	clearSSEWriteDeadline(w)
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event, open := <-ch:
			if !open {
				return
			}

			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
			flusher.Flush()
		}
	}
}
