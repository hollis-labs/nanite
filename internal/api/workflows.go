package api

import (
	"encoding/json"
	"fmt"
	"net/http"

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
