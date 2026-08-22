package api

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/store"
)

// handleListCommands returns all available slash commands (built-in + plugin).
// All commands live in a single unified registry (Engine.Commands).
func (a *API) handleListCommands(w http.ResponseWriter, r *http.Request) {
	if a.Services.Commands == nil {
		a.jsonResp(w, http.StatusOK, []any{})
		return
	}
	cmds := a.Services.Commands.List()
	a.jsonResp(w, http.StatusOK, cmds)
}

// handleExecuteCommand runs a slash command server-side.
// If the command produces a "message" result, it is persisted as a system message
// in the session so the frontend can just refetch messages.
func (a *API) handleExecuteCommand(w http.ResponseWriter, r *http.Request) {
	var req ExecuteCommandRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" {
		a.errorResp(w, http.StatusBadRequest, "name is required")
		return
	}

	if a.Services.Commands == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "command system not initialized")
		return
	}

	// Execute from the unified registry (built-in + plugin commands).
	result, err := a.Services.Commands.Execute(r.Context(), req.Name, req.SessionID, req.Args)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, err.Error())
		return
	}

	// If the command produced a message and we have a session, persist it.
	if result.Action == "message" && result.Content != "" && req.SessionID != "" {
		msg := &store.Message{
			ID:        uuid.New().String(),
			SessionID: req.SessionID,
			Role:      "system",
			Content:   result.Content,
		}
		if err := a.Services.Store.CreateMessage(r.Context(), msg); err == nil {
			result.MessageID = msg.ID
		}
	}

	a.jsonResp(w, http.StatusOK, result)
}
