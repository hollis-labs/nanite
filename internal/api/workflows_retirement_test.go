package api

import (
	"net/http"
	"testing"
)

func TestWorkflowAPIRetiredLegacyExecutorReturnsMigrationError(t *testing.T) {
	_, mux := newTestAPI(t)
	for _, request := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/workflows/runs?engine=legacy"},
		{method: http.MethodPost, path: "/api/workflows/runs/legacy-run/cancel?engine=legacy"},
		{method: http.MethodPost, path: "/api/workflows/runs/legacy-run/callbacks/step?engine=legacy"},
		{method: http.MethodPost, path: "/api/workflows/runs/legacy-run/approvals/step?engine=legacy"},
		{method: http.MethodGet, path: "/api/workflows/events?engine=legacy"},
	} {
		response := workflowAPIRequest(t, mux, request.method, request.path, "steps: []", nil)
		if response.Code != http.StatusGone {
			t.Fatalf("%s %s status=%d body=%s, want %d", request.method, request.path, response.Code, response.Body.String(), http.StatusGone)
		}
		if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
			t.Fatalf("%s %s content-type=%q, want application/json", request.method, request.path, contentType)
		}
	}
}

func TestWorkflowAPIDefaultUsesSharedEngineAndFailsClosedWhenUnwired(t *testing.T) {
	_, mux := newTestAPI(t)
	response := workflowAPIRequest(t, mux, http.MethodPost, "/api/workflows/runs", "steps: []", nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("default POST status=%d body=%s, want %d", response.Code, response.Body.String(), http.StatusServiceUnavailable)
	}
}
