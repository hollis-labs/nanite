package api

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/hollis-labs/conduit/internal/chat"
	"github.com/hollis-labs/conduit/internal/store"
)

// handleListCommands returns all available slash commands (built-in + plugin).
func (a *API) handleListCommands(w http.ResponseWriter, r *http.Request) {
	cmds := a.Engine.Commands.List()

	// Merge plugin commands if plugin host is available.
	if a.PluginHost != nil {
		for _, pc := range a.PluginHost.GetSlashCommands() {
			cmds = append(cmds, chat.SlashCommand{
				Name:        pc.Name,
				Description: pc.Description,
				Category:    pc.Category,
				Source:      "plugin",
			})
		}
	}

	a.jsonResp(w, http.StatusOK, cmds)
}

// handleExecuteCommand runs a slash command server-side.
// If the command produces a "message" result, it is persisted as a system message
// in the session so the frontend can just refetch messages.
func (a *API) handleExecuteCommand(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"session_id"`
		Name      string `json:"name"`
		Args      string `json:"args"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" {
		a.errorResp(w, http.StatusBadRequest, "name is required")
		return
	}

	// Try built-in commands first.
	result, err := a.Engine.Commands.Execute(r.Context(), req.Name, req.SessionID, req.Args)
	if err != nil {
		// Check plugin commands as fallback.
		if a.PluginHost != nil {
			for _, pc := range a.PluginHost.GetSlashCommands() {
				if pc.Name == req.Name && pc.Handler != nil {
					out, herr := pc.Handler(r.Context(), req.SessionID, req.Args)
					if herr != nil {
						a.errorResp(w, http.StatusInternalServerError, herr.Error())
						return
					}
					// Persist plugin command output as a system message if action=message.
					if action, _ := out["action"].(string); action == "message" {
						if content, _ := out["content"].(string); content != "" && req.SessionID != "" {
							msg := &store.Message{
								ID:        uuid.New().String(),
								SessionID: req.SessionID,
								Role:      "system",
								Content:   content,
							}
							if err := a.Store.CreateMessage(msg); err == nil {
								out["message_id"] = msg.ID
							}
						}
					}
					a.jsonResp(w, http.StatusOK, out)
					return
				}
			}
		}
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
		if err := a.Store.CreateMessage(msg); err == nil {
			result.MessageID = msg.ID
		}
	}

	a.jsonResp(w, http.StatusOK, result)
}
