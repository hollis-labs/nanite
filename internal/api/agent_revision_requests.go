package api

import (
	"net/http"
	"strconv"
)

func (a *API) handleListAgentRevisions(w http.ResponseWriter, r *http.Request) {
	limit, offset := 50, 0
	for name, target := range map[string]*int{"limit": &limit, "offset": &offset} {
		if raw := r.URL.Query().Get(name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				a.errorResp(w, http.StatusBadRequest, "invalid revision page")
				return
			}
			*target = value
		}
	}
	if limit < 1 || limit > 100 || offset < 0 {
		a.errorResp(w, http.StatusBadRequest, "limit must be 1..100 and offset non-negative")
		return
	}
	rows, err := a.Services.AgentConfig.ListRevisions(r.Context(), r.PathValue("id"), limit, offset)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{"revisions": rows, "limit": limit, "offset": offset, "restore_scope": "partial_profile_and_assignments"})
}
