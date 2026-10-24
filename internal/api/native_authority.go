package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/hollis-labs/nanite/internal/apiauth"
	"github.com/hollis-labs/nanite/internal/store"
)

// Operator-only management of local authority for native views
// (CW-20261011-0037). These are the only handlers that reach the writer. The
// route table classifies the whole prefix as admin; each handler re-checks the
// principal because the decision depends on it: an agent credential is never an
// operator, and the credential id recorded in the receipt and audit row comes
// from the authenticated principal, never from the request body.
func (a *API) registerNativeAuthorityRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/native-authority", a.handleNativeAuthorityList)
	mux.HandleFunc("POST /api/native-authority", a.handleNativeAuthorityCreate)
	mux.HandleFunc("GET /api/native-authority/tools", a.handleNativeAuthorityTools)
	mux.HandleFunc("GET /api/native-authority/{id}", a.handleNativeAuthorityGet)
	mux.HandleFunc("POST /api/native-authority/{id}/revoke", a.handleNativeAuthorityRevoke)
	mux.HandleFunc("PUT /api/native-authority/{id}/tools/{tool}", a.handleNativeAuthorityGrant)
	mux.HandleFunc("DELETE /api/native-authority/{id}/tools/{tool}", a.handleNativeAuthorityUngrant)
}

type nativeAuthorityReason struct {
	Reason string `json:"reason"`
}

// nativeAuthorityOperator returns the writer and the granting credential id, or
// has already answered the request.
func (a *API) nativeAuthorityOperator(w http.ResponseWriter, r *http.Request) (*store.LocalAuthority, string, bool) {
	p, ok := apiauth.FromContext(r.Context())
	if !ok {
		a.errorResp(w, http.StatusUnauthorized, "authentication is required")
		return nil, "", false
	}
	// Reads need agents:manage; every write is admin (grants are the
	// privilege-escalation path).
	allowed := canGrantTools(p)
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		allowed = agentManager(p)
	}
	if !allowed {
		a.errorResp(w, http.StatusForbidden, "the credential lacks the required scope")
		return nil, "", false
	}
	if a.Services == nil || a.Services.NativeAuthority == nil || a.Services.NativeAuthority.Writer == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "native authority unavailable")
		return nil, "", false
	}
	return a.Services.NativeAuthority.Writer, p.CredentialID, true
}

func (a *API) nativeAuthorityReason(w http.ResponseWriter, r *http.Request, required bool) (string, bool) {
	var req nativeAuthorityReason
	if r.ContentLength != 0 {
		if err := definitionRequest(w, r, &req); err != nil {
			a.errorResp(w, http.StatusBadRequest, "invalid request")
			return "", false
		}
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if (required && req.Reason == "") || len(req.Reason) > 500 {
		a.errorResp(w, http.StatusBadRequest, "a reason of at most 500 characters is required")
		return "", false
	}
	return req.Reason, true
}

func (a *API) nativeAuthorityError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, store.ErrNativeAuthorityNotFound):
		status = http.StatusNotFound
	case errors.Is(err, store.ErrNativeAuthorityExists), errors.Is(err, store.ErrNativeAuthorityStale), errors.Is(err, store.ErrNativeAuthorityRevoked):
		status = http.StatusConflict
	case errors.Is(err, store.ErrNativeToolNotGrantable):
		status = http.StatusUnprocessableEntity
	}
	if status == http.StatusInternalServerError {
		a.errorResp(w, status, "native authority operation failed")
		return
	}
	a.errorResp(w, status, err.Error())
}

type nativeAuthorityView struct {
	store.NativeAuthority
	Tools []store.NativeAuthorityTool `json:"tools"`
}

func (a *API) handleNativeAuthorityList(w http.ResponseWriter, r *http.Request) {
	writer, _, ok := a.nativeAuthorityOperator(w, r)
	if !ok {
		return
	}
	host := strings.TrimSpace(r.URL.Query().Get("host_settings_id"))
	if host == "" {
		a.errorResp(w, http.StatusBadRequest, "host_settings_id is required")
		return
	}
	list, err := writer.ListAuthorities(r.Context(), host)
	if err != nil {
		a.nativeAuthorityError(w, err)
		return
	}
	if list, err = pageOf(r, list); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	out := make([]nativeAuthorityView, 0, len(list))
	for _, item := range list {
		tools, terr := writer.ListTools(r.Context(), item.ID)
		if terr != nil {
			a.nativeAuthorityError(w, terr)
			return
		}
		out = append(out, nativeAuthorityView{NativeAuthority: item, Tools: tools})
	}
	a.jsonResp(w, http.StatusOK, out)
}

// handleNativeAuthorityTools lists the tool names a grant can name.
func (a *API) handleNativeAuthorityTools(w http.ResponseWriter, r *http.Request) {
	writer, _, ok := a.nativeAuthorityOperator(w, r)
	if !ok {
		return
	}
	tools, err := writer.GrantableTools(r.Context())
	if err != nil {
		a.nativeAuthorityError(w, err)
		return
	}
	page, err := pageOf(r, tools)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, page)
}

func (a *API) handleNativeAuthorityGet(w http.ResponseWriter, r *http.Request) {
	writer, _, ok := a.nativeAuthorityOperator(w, r)
	if !ok {
		return
	}
	item, err := writer.GetAuthority(r.Context(), r.PathValue("id"))
	if err != nil {
		a.nativeAuthorityError(w, err)
		return
	}
	tools, err := writer.ListTools(r.Context(), item.ID)
	if err != nil {
		a.nativeAuthorityError(w, err)
		return
	}
	a.jsonResp(w, http.StatusOK, nativeAuthorityView{NativeAuthority: item, Tools: tools})
}

func (a *API) handleNativeAuthorityCreate(w http.ResponseWriter, r *http.Request) {
	writer, by, ok := a.nativeAuthorityOperator(w, r)
	if !ok {
		return
	}
	var req struct {
		HostSettingsID string `json:"host_settings_id"`
		Reason         string `json:"reason"`
	}
	if err := definitionRequest(w, r, &req); err != nil || strings.TrimSpace(req.HostSettingsID) == "" || strings.TrimSpace(req.Reason) == "" || len(req.Reason) > 500 {
		a.errorResp(w, http.StatusBadRequest, "host_settings_id and a reason of at most 500 characters are required")
		return
	}
	item, err := writer.CreateAuthority(r.Context(), by, strings.TrimSpace(req.HostSettingsID), strings.TrimSpace(req.Reason))
	if err != nil {
		a.nativeAuthorityError(w, err)
		return
	}
	a.jsonResp(w, http.StatusCreated, nativeAuthorityView{NativeAuthority: item, Tools: []store.NativeAuthorityTool{}})
}

func (a *API) handleNativeAuthorityRevoke(w http.ResponseWriter, r *http.Request) {
	writer, by, ok := a.nativeAuthorityOperator(w, r)
	if !ok {
		return
	}
	reason, ok := a.nativeAuthorityReason(w, r, true)
	if !ok {
		return
	}
	if err := writer.RevokeAuthority(r.Context(), by, r.PathValue("id"), reason); err != nil {
		a.nativeAuthorityError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleNativeAuthorityGrant(w http.ResponseWriter, r *http.Request) {
	writer, by, ok := a.nativeAuthorityOperator(w, r)
	if !ok {
		return
	}
	reason, ok := a.nativeAuthorityReason(w, r, true)
	if !ok {
		return
	}
	if err := writer.GrantTool(r.Context(), by, r.PathValue("id"), r.PathValue("tool"), reason); err != nil {
		a.nativeAuthorityError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleNativeAuthorityUngrant(w http.ResponseWriter, r *http.Request) {
	writer, by, ok := a.nativeAuthorityOperator(w, r)
	if !ok {
		return
	}
	reason, ok := a.nativeAuthorityReason(w, r, true)
	if !ok {
		return
	}
	if err := writer.RevokeTool(r.Context(), by, r.PathValue("id"), r.PathValue("tool"), reason); err != nil {
		a.nativeAuthorityError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
