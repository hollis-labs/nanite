package api

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/hollis-labs/nanite/internal/service"
)

func (a *API) handleClearSession(w http.ResponseWriter, r *http.Request) {
	var opts *service.ClearConversationOptions
	if err := a.decodeAgentV1(r, &opts); err != nil || opts == nil {
		a.errorResp(w, http.StatusBadRequest, "expected clear options as one JSON object")
		return
	}
	result, err := a.Services.ClearSession(r.Context(), r.PathValue("id"), *opts)
	if err != nil {
		var confirmation *service.ClearConfirmationRequired
		if errors.As(err, &confirmation) {
			a.jsonResp(w, http.StatusConflict, map[string]any{"code": "clear_confirmation_required", "error": confirmation.Error(), "active_turn_ids": confirmation.TurnIDs})
		} else if errors.Is(err, sql.ErrNoRows) {
			a.errorResp(w, http.StatusNotFound, "session not found")
		} else {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	a.jsonResp(w, http.StatusOK, result)
}
