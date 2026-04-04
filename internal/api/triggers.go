package api

import (
	"net/http"

	"github.com/hollis-labs/nanite/internal/store"
)

// handleListTriggerRules returns all trigger rules, optionally filtered by plugin_id.
func (a *API) handleListTriggerRules(w http.ResponseWriter, r *http.Request) {
	pluginID := r.URL.Query().Get("plugin_id")
	rules, err := a.Services.Store.ListTriggerRules(pluginID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{
		"rules": rules,
		"count": len(rules),
	})
}

// handleCreateTriggerRule creates a new trigger rule.
func (a *API) handleCreateTriggerRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PluginID        string `json:"plugin_id"`
		EventType       string `json:"event_type"`
		ConnectorName   string `json:"connector_name"`
		PayloadTemplate string `json:"payload_template"`
		FilterExpr      string `json:"filter_expr"`
		Enabled         *bool  `json:"enabled"`
		Description     string `json:"description"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if req.EventType == "" {
		a.errorResp(w, http.StatusBadRequest, "event_type is required")
		return
	}
	if req.ConnectorName == "" {
		a.errorResp(w, http.StatusBadRequest, "connector_name is required")
		return
	}

	// Verify connector exists.
	if a.Services.Plugins != nil {
		if _, ok := a.Services.Plugins.GetConnector(req.ConnectorName); !ok {
			a.errorResp(w, http.StatusBadRequest, "connector not found: "+req.ConnectorName)
			return
		}
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	rule := &store.TriggerRule{
		PluginID:        req.PluginID,
		EventType:       req.EventType,
		ConnectorName:   req.ConnectorName,
		PayloadTemplate: req.PayloadTemplate,
		FilterExpr:      req.FilterExpr,
		Enabled:         enabled,
		Description:     req.Description,
	}

	if rule.PayloadTemplate == "" {
		rule.PayloadTemplate = "{}"
	}

	if err := a.Services.Store.CreateTriggerRule(rule); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, rule)
}

// handleGetTriggerRule returns a single trigger rule by ID.
func (a *API) handleGetTriggerRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rule, err := a.Services.Store.GetTriggerRule(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, rule)
}

// handleUpdateTriggerRule updates a trigger rule by ID.
func (a *API) handleUpdateTriggerRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	existing, err := a.Services.Store.GetTriggerRule(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, err.Error())
		return
	}

	var req struct {
		EventType       *string `json:"event_type"`
		ConnectorName   *string `json:"connector_name"`
		PayloadTemplate *string `json:"payload_template"`
		FilterExpr      *string `json:"filter_expr"`
		Enabled         *bool   `json:"enabled"`
		Description     *string `json:"description"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if req.EventType != nil {
		existing.EventType = *req.EventType
	}
	if req.ConnectorName != nil {
		// Verify connector exists.
		if a.Services.Plugins != nil {
			if _, ok := a.Services.Plugins.GetConnector(*req.ConnectorName); !ok {
				a.errorResp(w, http.StatusBadRequest, "connector not found: "+*req.ConnectorName)
				return
			}
		}
		existing.ConnectorName = *req.ConnectorName
	}
	if req.PayloadTemplate != nil {
		existing.PayloadTemplate = *req.PayloadTemplate
	}
	if req.FilterExpr != nil {
		existing.FilterExpr = *req.FilterExpr
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if req.Description != nil {
		existing.Description = *req.Description
	}

	if err := a.Services.Store.UpdateTriggerRule(existing); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, existing)
}

// handleDeleteTriggerRule deletes a trigger rule by ID.
func (a *API) handleDeleteTriggerRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Services.Store.DeleteTriggerRule(id); err != nil {
		a.errorResp(w, http.StatusNotFound, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "deleted"})
}
