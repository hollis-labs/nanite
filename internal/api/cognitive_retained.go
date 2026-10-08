package api

import (
	"errors"
	"net/http"

	"github.com/hollis-labs/nanite/internal/service"
)

func (a *API) allowRetainedChatOperation(w http.ResponseWriter, r *http.Request, viewID string) bool {
	if a.Services == nil || a.Services.CognitiveViews == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "native view policy unavailable")
		return false
	}
	if err := a.Services.CognitiveViews.RequireLegacyTarget(r.Context(), viewID); err != nil {
		if errors.Is(err, service.ErrDefinedViewOperation) {
			a.errorResp(w, http.StatusUnprocessableEntity, "defined views require native turn operations")
		} else {
			a.errorResp(w, http.StatusInternalServerError, "view policy lookup failed")
		}
		return false
	}
	return true
}
