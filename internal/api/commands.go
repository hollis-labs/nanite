package api

import (
	"net/http"

	"github.com/hollis-labs/mentat-chat/internal/chat"
)

func (a *API) handleListCommands(w http.ResponseWriter, r *http.Request) {
	a.jsonResp(w, http.StatusOK, chat.ListCommands())
}
