package api

import (
	"context"
	"net/http"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// handleListActions returns all custom actions.
func (a *API) handleListActions(w http.ResponseWriter, r *http.Request) {
	actions, err := a.Services.Store.ListCustomActions()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{
		"actions": actions,
		"count":   len(actions),
	})
}

// handleCreateAction creates a new custom action. If slash_command is set,
// the action is registered as a slash command in the command registry.
func (a *API) handleCreateAction(w http.ResponseWriter, r *http.Request) {
	var req CreateActionRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if req.Name == "" {
		a.errorResp(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Command == "" {
		a.errorResp(w, http.StatusBadRequest, "command is required")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	action := &store.CustomAction{
		Name:         req.Name,
		Description:  req.Description,
		Keybinding:   req.Keybinding,
		Command:      req.Command,
		SlashCommand: req.SlashCommand,
		AutoTriggers: req.AutoTriggers,
		Enabled:      enabled,
	}

	if err := a.Services.Store.CreateCustomAction(action); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Register as slash command if requested.
	if action.SlashCommand != "" && action.Enabled {
		a.RegisterActionCommand(action)
	}

	a.jsonResp(w, http.StatusCreated, action)
}

// handleGetAction returns a single custom action by ID.
func (a *API) handleGetAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	action, err := a.Services.Store.GetCustomAction(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, action)
}

// handleUpdateAction updates a custom action by ID.
func (a *API) handleUpdateAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	existing, err := a.Services.Store.GetCustomAction(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, err.Error())
		return
	}

	var req UpdateActionRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.Description != nil {
		existing.Description = *req.Description
	}
	if req.Keybinding != nil {
		existing.Keybinding = *req.Keybinding
	}
	if req.Command != nil {
		existing.Command = *req.Command
	}
	if req.SlashCommand != nil {
		existing.SlashCommand = *req.SlashCommand
	}
	if req.AutoTriggers != nil {
		existing.AutoTriggers = *req.AutoTriggers
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}

	if err := a.Services.Store.UpdateCustomAction(existing); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Re-register slash command if applicable.
	if existing.SlashCommand != "" && existing.Enabled {
		a.RegisterActionCommand(existing)
	}

	a.jsonResp(w, http.StatusOK, existing)
}

// handleDeleteAction deletes a custom action by ID.
func (a *API) handleDeleteAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Services.Store.DeleteCustomAction(id); err != nil {
		a.errorResp(w, http.StatusNotFound, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleExecuteAction runs a custom action's command in the given session.
func (a *API) handleExecuteAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	action, err := a.Services.Store.GetCustomAction(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, err.Error())
		return
	}
	if !action.Enabled {
		a.errorResp(w, http.StatusBadRequest, "action is disabled")
		return
	}

	var req ExecuteActionRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.SessionID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id is required")
		return
	}

	// Execute the action's command as a message send in the session.
	a.jsonResp(w, http.StatusOK, map[string]any{
		"action":     "inject_message",
		"command":    action.Command,
		"session_id": req.SessionID,
		"action_id":  action.ID,
	})
}

// RegisterActionCommand registers a custom action as a slash command
// in the Engine's command registry.
func (a *API) RegisterActionCommand(action *store.CustomAction) {
	if a.Services.Commands == nil {
		return
	}

	a.Services.Commands.Register(
		chat.SlashCommand{
			Name:        action.SlashCommand,
			Description: action.Description,
			Category:    "action",
			Source:      "custom-action:" + action.ID,
		},
		func(ctx context.Context, sessionID, args string) (*chat.CommandResult, error) {
			return &chat.CommandResult{
				Action:  "inject_message",
				Content: action.Command,
			}, nil
		},
	)
}
