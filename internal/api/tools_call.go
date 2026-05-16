package api

import (
	"net/http"

	"github.com/hollis-labs/nanite/internal/mcp"
)

// selfToolCallRequest is the body POST /api/tools/call accepts.
type selfToolCallRequest struct {
	// SessionID scopes the call — stamped onto the dispatch context so
	// session-aware self-tools (todo/plan scope resolution, panel signals,
	// messaging) resolve against the right session. May be empty.
	SessionID string `json:"session_id"`
	// Name is the self-tool to invoke (e.g. "todo_create", "panel_open").
	Name string `json:"name"`
	// Args is the tool's argument map.
	Args map[string]any `json:"args"`
}

// handleSelfToolCall dispatches a self-tool through the fully-wired
// in-process self-tools transport.
//
// This is the live-harness side of the CLI-launch self-tools proxy
// (Option A): a CLI-launched chat agent's `nanite mcp` subprocess runs
// against a bare store with none of the harness services wired (no
// TodoStore, no panel-signal sink, no messaging/subagent/dispatch). When
// the subprocess knows this server's address (planted as NANITE_API_URL
// in the boot dir's .mcp.json), it forwards every self-tool call here so
// dispatch happens in the running process where those dependencies are
// live. dev_* filesystem tools stay subprocess-local and never reach this
// endpoint.
//
// The whole API listens on loopback, so this endpoint inherits the same
// trust boundary as every other route — no separate auth is added.
func (a *API) handleSelfToolCall(w http.ResponseWriter, r *http.Request) {
	if a.selfTools == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "self-tools transport not available")
		return
	}

	var req selfToolCallRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		a.errorResp(w, http.StatusBadRequest, "missing tool name")
		return
	}

	ctx := mcp.WithSessionID(r.Context(), req.SessionID)
	result, err := a.selfTools.CallTool(ctx, req.Name, req.Args)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// A tool-level error (result.IsError) is still a successful dispatch —
	// return 200 and let the caller surface the error content. Only a
	// transport failure above yields a non-200.
	a.jsonResp(w, http.StatusOK, result)
}
