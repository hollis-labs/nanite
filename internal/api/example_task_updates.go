package api

import (
	"log/slog"
	"net/http"
)

// exampleTaskUpdateRequest is the body shape task_update_report's seeded
// internal_api_call reaction sends — {id, msg}, matching
// internal/selftools/self_tools_task_update_report.go's
// SeedTaskUpdateReportReactions body_template exactly
// ({"id": "{{id}}", "msg": "{{msg}}"}).
type exampleTaskUpdateRequest struct {
	ID  string `json:"id"`
	Msg string `json:"msg"`
}

// handleExampleTaskUpdate backs POST /api/example/task-updates — a
// trivial demo/test fixture proving the harness-reactive self-tools
// worked example's internal_api_call reaction executes a real,
// same-process HTTP round-trip (TASKS/harness-reactive-self-tools/
// 07-worked-example-task-update-report.md, "What to do" item 4). It
// accepts the POST, logs the substituted id/msg at debug level so a live
// dogfeed can confirm the values arrived correctly, and returns 200.
// Deliberately illustrative — per this task's own scope, it persists
// nothing real (no store write, no side effect beyond the log line) and
// is not a real Nanite feature or consumer.
//
// Trust boundary: mirrors handleSelfToolCall's own loopback-only gate
// (tools_call.go). The route is exempt from basicAuthMiddleware
// (internal/server/auth.go) on the premise that the internal_api_call
// reaction calling it is a same-host, same-process caller with no
// credentials — that premise only holds if the handler itself enforces
// "same host" rather than accepting any caller that reaches the port.
func (a *API) handleExampleTaskUpdate(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		a.errorResp(w, http.StatusForbidden, "example task-update endpoint is loopback-only")
		return
	}
	var req exampleTaskUpdateRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}
	slog.Debug("example task-update received (demo fixture, not persisted)",
		"id", req.ID, "msg", req.Msg)
	a.jsonResp(w, http.StatusOK, map[string]bool{"received": true})
}
