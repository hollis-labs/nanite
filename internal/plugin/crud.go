package plugin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/hollis-labs/go-plugin"
)

// handleCRUDList handles GET /api/plugins/{resourceType}
func (h *Host) handleCRUDList(w http.ResponseWriter, r *http.Request, handler plugin.CRUDHandler) {
	// Parse query parameters as filters
	filters := make(map[string]interface{})
	for key, values := range r.URL.Query() {
		if len(values) > 0 {
			// Try to parse as int, float, or keep as string
			value := values[0]
			if intVal, err := strconv.Atoi(value); err == nil {
				filters[key] = intVal
			} else if floatVal, err := strconv.ParseFloat(value, 64); err == nil {
				filters[key] = floatVal
			} else if boolVal, err := strconv.ParseBool(value); err == nil {
				filters[key] = boolVal
			} else {
				filters[key] = value
			}
		}
	}

	resources, err := handler.List(r.Context(), filters)
	if err != nil {
		h.crudErrorResp(w, err, "Failed to list resources")
		return
	}

	h.jsonResp(w, http.StatusOK, map[string]interface{}{
		"items": resources,
		"count": len(resources),
	})
}

// handleCRUDCreate handles POST /api/plugins/{resourceType}
func (h *Host) handleCRUDCreate(w http.ResponseWriter, r *http.Request, handler plugin.CRUDHandler) {
	var resource interface{}
	if err := json.NewDecoder(r.Body).Decode(&resource); err != nil {
		h.errorResp(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	defer r.Body.Close()

	created, err := handler.Create(r.Context(), resource)
	if err != nil {
		h.crudErrorResp(w, err, "Failed to create resource")
		return
	}

	h.jsonResp(w, http.StatusCreated, created)
}

// handleCRUDRead handles GET /api/plugins/{resourceType}/{id}
func (h *Host) handleCRUDRead(w http.ResponseWriter, r *http.Request, handler plugin.CRUDHandler) {
	id := r.PathValue("id")
	if id == "" {
		h.errorResp(w, http.StatusBadRequest, "Missing resource ID")
		return
	}

	resource, err := handler.Read(r.Context(), id)
	if err != nil {
		h.crudErrorResp(w, err, "Failed to read resource")
		return
	}

	h.jsonResp(w, http.StatusOK, resource)
}

// handleCRUDUpdate handles PUT /api/plugins/{resourceType}/{id}
func (h *Host) handleCRUDUpdate(w http.ResponseWriter, r *http.Request, handler plugin.CRUDHandler) {
	id := r.PathValue("id")
	if id == "" {
		h.errorResp(w, http.StatusBadRequest, "Missing resource ID")
		return
	}

	var resource interface{}
	if err := json.NewDecoder(r.Body).Decode(&resource); err != nil {
		h.errorResp(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	defer r.Body.Close()

	updated, err := handler.Update(r.Context(), id, resource)
	if err != nil {
		h.crudErrorResp(w, err, "Failed to update resource")
		return
	}

	h.jsonResp(w, http.StatusOK, updated)
}

// handleCRUDDelete handles DELETE /api/plugins/{resourceType}/{id}
func (h *Host) handleCRUDDelete(w http.ResponseWriter, r *http.Request, handler plugin.CRUDHandler) {
	id := r.PathValue("id")
	if id == "" {
		h.errorResp(w, http.StatusBadRequest, "Missing resource ID")
		return
	}

	err := handler.Delete(r.Context(), id)
	if err != nil {
		h.crudErrorResp(w, err, "Failed to delete resource")
		return
	}

	h.jsonResp(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// crudErrorResp maps a CRUD handler error to an appropriate HTTP response.
// If the error is a *plugin.PluginError, its Code and Message are used.
// Otherwise falls back to 500.
func (h *Host) crudErrorResp(w http.ResponseWriter, err error, fallbackMsg string) {
	var pe *plugin.PluginError
	if errors.As(err, &pe) {
		h.errorResp(w, pe.Code, pe.Message)
		return
	}
	h.logger.Error("CRUD operation failed", "error", err)
	h.errorResp(w, http.StatusInternalServerError, fallbackMsg)
}

// jsonResp writes a JSON response with the given status code.
func (h *Host) jsonResp(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.logger.Error("failed to encode JSON response", "error", err)
	}
}

// errorResp writes a JSON error response.
func (h *Host) errorResp(w http.ResponseWriter, status int, msg string) {
	h.jsonResp(w, status, map[string]string{"error": msg})
}
