package api

import (
	"net/http"
	"strconv"

	"github.com/hollis-labs/nanite/internal/store"
)

func (a *API) handleSearchMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	query := q.Get("q")
	if query == "" {
		a.errorResp(w, http.StatusBadRequest, "q query parameter is required")
		return
	}

	// CW-20260815-0010: resolve to the sole real workspace when the param
	// is missing or names a workspace that no longer exists, instead of
	// erroring — see Store.ResolveWorkspaceID.
	workspaceID, err := a.Services.Store.ResolveWorkspaceID(q.Get("workspace_id"))
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if workspaceID == "" {
		a.errorResp(w, http.StatusBadRequest, "workspace_id query parameter is required (multiple workspaces exist)")
		return
	}

	projectID := q.Get("project_id") // optional

	limit := 20
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	results, err := a.Services.Store.SearchMessages(query, workspaceID, projectID, limit)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if results == nil {
		results = []store.SearchResult{}
	}
	a.jsonResp(w, http.StatusOK, results)
}
