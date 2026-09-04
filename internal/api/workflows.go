package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	workflowapi "github.com/hollis-labs/nanite/internal/workflowapi"
	"github.com/hollis-labs/nanite/internal/workflowcompat"
)

const (
	workflowEngineLegacy = "legacy"
	workflowEngineShared = "go-workflow"
	// workflowEngineHadron is the pilot query alias retained for API clients
	// that opted into the durable host before extraction.
	workflowEngineHadron = "hadron"
)

// handleListWorkflowRuns lists recent workflow runs, with optional filters.
//
// GET /api/workflows/runs?status=running&pipeline_id=my-pipe
func (a *API) handleListWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	engine, ok := a.selectedWorkflowAPIEngine(w, r)
	if !ok {
		return
	}
	if engine == workflowEngineShared {
		a.handleListSharedWorkflowRuns(w, r)
		return
	}
	if a.workflowSurface == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "shared workflow engine not initialized")
		return
	}
	runs, err := a.workflowSurface.ListLegacyTerminal(r.Context(), r.URL.Query().Get("pipeline_id"), r.URL.Query().Get("status"))
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to list legacy workflow history")
		return
	}
	if runs == nil {
		runs = []*workflowapi.RunRecord{}
	}
	a.jsonResp(w, http.StatusOK, runs)
}

// handleGetWorkflowRun returns a single workflow run by ID.
//
// GET /api/workflows/runs/{runId}
func (a *API) handleGetWorkflowRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runId")
	engine, ok := a.selectedWorkflowAPIEngine(w, r)
	if !ok {
		return
	}
	if engine == workflowEngineShared {
		a.handleGetSharedWorkflowRun(w, r, runID)
		return
	}
	if a.workflowSurface == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "shared workflow engine not initialized")
		return
	}
	record, err := a.workflowSurface.GetLegacyTerminal(r.Context(), runID)
	if errors.Is(err, workflowcompat.ErrNotFound) {
		a.errorResp(w, http.StatusNotFound, "run not found")
		return
	}
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to load legacy workflow history")
		return
	}

	a.jsonResp(w, http.StatusOK, record)
}

// handleCancelWorkflowRun marks a workflow run as canceled.
//
// POST /api/workflows/runs/{runId}/cancel
func (a *API) handleCancelWorkflowRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runId")
	engine, ok := a.selectedWorkflowAPIEngine(w, r)
	if !ok {
		return
	}
	if engine == workflowEngineShared {
		a.handleCancelSharedWorkflowRun(w, r, runID)
		return
	}
	a.errorResp(w, http.StatusGone, "legacy workflow execution has been retired; terminal history remains queryable")
}

// handleRunWorkflow dispatches source to an explicitly selected workflow
// engine. The retired process-local executor is no longer available.
//
// POST /api/workflows/runs
func (a *API) handleRunWorkflow(w http.ResponseWriter, r *http.Request) {
	engine, ok := a.selectedWorkflowAPIEngine(w, r)
	if !ok {
		return
	}
	if engine == workflowEngineShared {
		a.handleRunSharedWorkflow(w, r)
		return
	}
	a.errorResp(w, http.StatusGone, "legacy workflow execution has been retired; use the default go-workflow engine")
}

// handleWorkflowEvents serves an SSE stream of workflow execution events.
//
// GET /api/workflows/events
func (a *API) handleWorkflowEvents(w http.ResponseWriter, r *http.Request) {
	engine, ok := a.selectedWorkflowAPIEngine(w, r)
	if !ok {
		return
	}
	if engine == workflowEngineShared {
		a.handleSharedWorkflowEvents(w, r)
		return
	}
	a.errorResp(w, http.StatusGone, "legacy workflow event streaming has been retired")
}

func (a *API) selectedWorkflowAPIEngine(w http.ResponseWriter, r *http.Request) (string, bool) {
	engine := strings.TrimSpace(r.URL.Query().Get("engine"))
	switch engine {
	case workflowEngineLegacy:
		return workflowEngineLegacy, true
	case "", workflowEngineShared, workflowEngineHadron:
		return workflowEngineShared, true
	default:
		a.errorResp(w, http.StatusBadRequest, "unsupported workflow engine")
		return "", false
	}
}

func (a *API) handleListSharedWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	if a.workflowSurface == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "shared workflow engine not initialized")
		return
	}
	runs, err := a.workflowSurface.List(r.Context(), r.URL.Query().Get("pipeline_id"), r.URL.Query().Get("status"))
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to list workflow runs")
		return
	}
	if runs == nil {
		runs = []*workflowapi.RunRecord{}
	}
	a.jsonResp(w, http.StatusOK, runs)
}

func (a *API) handleGetSharedWorkflowRun(w http.ResponseWriter, r *http.Request, runID string) {
	if a.workflowSurface == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "shared workflow engine not initialized")
		return
	}
	record, err := a.workflowSurface.Get(r.Context(), runID)
	if errors.Is(err, workflowcompat.ErrNotFound) {
		a.errorResp(w, http.StatusNotFound, "run not found")
		return
	}
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to load workflow run")
		return
	}
	a.jsonResp(w, http.StatusOK, record)
}

func (a *API) handleCancelSharedWorkflowRun(w http.ResponseWriter, r *http.Request, runID string) {
	if a.workflowSurface == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "shared workflow engine not initialized")
		return
	}
	record, err := a.workflowSurface.Cancel(r.Context(), runID)
	if errors.Is(err, workflowcompat.ErrNotFound) {
		a.errorResp(w, http.StatusNotFound, "run not found")
		return
	}
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to cancel workflow run")
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": string(record.Run.Status)})
}

func (a *API) handleRunSharedWorkflow(w http.ResponseWriter, r *http.Request) {
	if a.workflowSurface == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "shared workflow engine not initialized")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	locator := strings.TrimSpace(r.URL.Query().Get("locator"))
	if locator == "" {
		locator = "nanite-api.workflow.yaml"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	record, err := a.workflowSurface.RunSource(ctx, locator, body)
	if errors.Is(err, workflowcompat.ErrInvalidSource) {
		a.errorResp(w, http.StatusBadRequest, fmt.Sprintf("invalid workflow: %v", err))
		return
	}
	if errors.Is(err, workflowcompat.ErrCutoverNotReady) {
		a.errorResp(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "workflow run failed")
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{
		"run_id": record.Run.RunID, "pipeline_id": record.Run.PipelineID,
		"status": string(record.Run.Status),
	})
}

type workflowExternalResponseRequest struct {
	ResumeToken string `json:"resume_token"`
	Payload     any    `json:"payload"`
}

func (a *API) handleResumeWorkflowCallback(w http.ResponseWriter, r *http.Request) {
	a.handleResumeWorkflowExternal(w, r, false)
}

func (a *API) handleResumeWorkflowApproval(w http.ResponseWriter, r *http.Request) {
	a.handleResumeWorkflowExternal(w, r, true)
}

// handleResumeWorkflowExternal accepts only a principal supplied by the
// injected server authentication boundary. The body cannot claim responder
// identity, and Idempotency-Key is mandatory so a network retry cannot apply
// a callback or approval twice.
func (a *API) handleResumeWorkflowExternal(w http.ResponseWriter, r *http.Request, approval bool) {
	engine, ok := a.selectedWorkflowAPIEngine(w, r)
	if !ok {
		return
	}
	if engine == workflowEngineLegacy {
		a.errorResp(w, http.StatusGone, "legacy workflow resume has been retired")
		return
	}
	if a.workflowSurface == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "shared workflow engine not initialized")
		return
	}
	if a.workflowResponderAuthenticator == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "workflow responder authentication is not configured")
		return
	}
	principal, authenticated := a.workflowResponderAuthenticator(r)
	principal = strings.TrimSpace(principal)
	if !authenticated || principal == "" {
		a.errorResp(w, http.StatusUnauthorized, "workflow responder authentication required")
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		a.errorResp(w, http.StatusBadRequest, "Idempotency-Key header is required")
		return
	}
	var body workflowExternalResponseRequest
	if err := a.decode(r, &body); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid workflow response body")
		return
	}
	response := workflowcompat.ExternalResponse{
		RunID: r.PathValue("runId"), StepID: r.PathValue("stepId"),
		ResumeToken: body.ResumeToken, Payload: body.Payload,
		Principal: principal, IdempotencyKey: idempotencyKey, ReceivedAt: time.Now().UTC(),
	}
	var (
		record *workflowapi.RunRecord
		err    error
	)
	if approval {
		record, err = a.workflowSurface.ResumeApproval(r.Context(), response)
	} else {
		record, err = a.workflowSurface.ResumeCallback(r.Context(), response)
	}
	if a.writeWorkflowExternalError(w, err) {
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{
		"run_id": record.Run.RunID, "pipeline_id": record.Run.PipelineID,
		"status": string(record.Run.Status),
	})
}

func (a *API) writeWorkflowExternalError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, workflowcompat.ErrUnauthorized):
		a.errorResp(w, http.StatusForbidden, "workflow response is not authorized")
	case errors.Is(err, workflowcompat.ErrNotFound):
		a.errorResp(w, http.StatusNotFound, "workflow wait not found")
	case errors.Is(err, workflowcompat.ErrConflict):
		a.errorResp(w, http.StatusConflict, "workflow wait is already resolved")
	default:
		a.errorResp(w, http.StatusInternalServerError, "failed to resume workflow")
	}
	return true
}

func (a *API) handleSharedWorkflowEvents(w http.ResponseWriter, r *http.Request) {
	if a.workflowSurface == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "shared workflow engine not initialized")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.errorResp(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	runID := strings.TrimSpace(r.URL.Query().Get("run_id"))
	cursorText, explicitCursor := r.URL.Query()["after"]
	if !explicitCursor {
		if value := strings.TrimSpace(r.Header.Get("Last-Event-ID")); value != "" {
			cursorText = []string{value}
			explicitCursor = true
		}
	}
	var cursor int64
	var err error
	if explicitCursor {
		value := ""
		if len(cursorText) != 0 {
			value = strings.TrimSpace(cursorText[0])
		}
		cursor, err = workflowcompat.ParseEventCursor(value)
	} else {
		cursor, err = a.workflowSurface.CurrentEventCursor(r.Context(), runID)
	}
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	clearSSEWriteDeadline(w)
	flusher.Flush()

	poll := time.NewTicker(50 * time.Millisecond)
	keepalive := time.NewTicker(15 * time.Second)
	defer poll.Stop()
	defer keepalive.Stop()

	writePending := func() bool {
		events, next, listErr := a.workflowSurface.EventsAfter(r.Context(), runID, cursor, 128)
		if listErr != nil {
			return false
		}
		cursor = next
		for _, item := range events {
			data, marshalErr := json.Marshal(item.Event)
			if marshalErr != nil {
				continue
			}
			if _, writeErr := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", strconv.FormatInt(item.Cursor, 10), item.Event.Type, data); writeErr != nil {
				return false
			}
			flusher.Flush()
		}
		return true
	}
	if explicitCursor && !writePending() {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
			if !writePending() {
				return
			}
		case <-keepalive.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
