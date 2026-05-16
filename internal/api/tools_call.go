package api

import (
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/hollis-labs/nanite/internal/mcp"
)

// selfToolCallWriteDeadline extends the per-response write deadline for the
// tool-call route. A forwarded self-tool can be a synchronous dispatch
// (task_execute) that legitimately runs up to the subagent default of 300s,
// well past the server-global 60s WriteTimeout — without this the response
// write fails after the connection has already been timed out.
const selfToolCallWriteDeadline = 10 * time.Minute

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
// Trust boundary: the endpoint runs arbitrary self-tools, so it is
// restricted to loopback callers — the `nanite mcp` subprocess always
// reaches it via http://127.0.0.1. The HTTP server can bind non-loopback
// interfaces, so this in-handler check is the actual boundary. The route is
// also exempt from basicAuthMiddleware: the loopback gate is the trust
// boundary for this internal-only path, so the subprocess needs no
// credentials planted into its boot dir.
func (a *API) handleSelfToolCall(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		a.errorResp(w, http.StatusForbidden, "tool-call endpoint is loopback-only")
		return
	}
	if a.selfTools == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "self-tools transport not available")
		return
	}

	// Extend the write deadline before dispatch — CallTool can block for a
	// long-running tool, and the response is written only after it returns.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(selfToolCallWriteDeadline)); err != nil {
		slog.Warn("tools/call: could not extend write deadline", "err", err)
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

// isLoopbackRequest reports whether the request's TCP peer is a loopback
// address. The HTTP server speaks plain TCP with no proxy in front, so
// r.RemoteAddr is the real peer.
func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
