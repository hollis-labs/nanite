package api

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/hollis-labs/nanite/internal/apiauth"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// Agent management over HTTP (CW-20261022-0001): make an agent launchable in
// one call, and read back what exists. Everything here is operator-class: the
// route table classes the prefixes as admin and each handler re-checks that
// the caller is not an agent credential, because setup records tool authority.
// Changing an agent afterwards uses the existing routes: PUT/DELETE
// /api/native-authority/{authority_id}/tools/{tool} for grants, POST
// /api/native-authority/{id}/revoke to withdraw authority, PUT
// /api/agent-host-settings/{id} (enabled=false) to retire, and POST
// /api/agent/v1/sessions to start one.
func (a *API) registerAgentManagementRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/agent-setup", a.handleAgentSetup)
	mux.HandleFunc("POST /api/agent-reload", a.handleAgentReload)
	mux.HandleFunc("GET /api/native-agents", a.handleNativeAgentList)
	mux.HandleFunc("GET /api/native-agents/{id}", a.handleNativeAgentGet)
}

// agentManagementActor returns the granting credential, or has already answered.
func (a *API) agentManagementActor(w http.ResponseWriter, r *http.Request) (apiauth.Principal, bool) {
	p, ok := apiauth.FromContext(r.Context())
	if !ok {
		a.errorResp(w, http.StatusUnauthorized, "authentication is required")
		return p, false
	}
	if !agentManager(p) {
		a.errorResp(w, http.StatusForbidden, "the credential lacks the required scope")
		return p, false
	}
	return p, true
}

// agentManager is the test every agent-management route applies: a non-agent
// credential with agents:manage (the operator holds every scope).
func agentManager(p apiauth.Principal) bool {
	return p.Kind != apiauth.KindAgent && p.Has(apiauth.ScopeAgentsManage) && strings.TrimSpace(p.CredentialID) != ""
}

// canGrantTools reports whether the principal may grant or withdraw native
// tool authority. That stays admin: granting is the privilege-escalation path.
func canGrantTools(p apiauth.Principal) bool {
	return p.Kind != apiauth.KindAgent && p.Has(apiauth.ScopeAdmin) && strings.TrimSpace(p.CredentialID) != ""
}

type agentSetupBody struct {
	Artifact  string `json:"artifact,omitempty"`
	Archetype string `json:"archetype,omitempty"`
	Resources []struct {
		URI     string `json:"uri"`
		Content string `json:"content"`
	} `json:"resources,omitempty"`
	Slug        string   `json:"slug,omitempty"`
	Title       string   `json:"title,omitempty"`
	Provider    string   `json:"provider"`
	Model       string   `json:"model,omitempty"`
	Runtime     string   `json:"runtime,omitempty"`
	Directories []string `json:"directories,omitempty"`
	Tools       []string `json:"tools"`
	Reason      string   `json:"reason"`
}

func (a *API) handleAgentSetup(w http.ResponseWriter, r *http.Request) {
	p, ok := a.agentManagementActor(w, r)
	if !ok {
		return
	}
	if a.Services == nil || a.Services.AgentSetup == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "agent setup unavailable")
		return
	}
	var body agentSetupBody
	if err := definitionRequest(w, r, &body); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid agent setup request")
		return
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if body.Reason == "" || len(body.Reason) > 500 {
		a.errorResp(w, http.StatusBadRequest, "a reason of at most 500 characters is required")
		return
	}
	// Setup grants the tools it names, so naming any needs admin. An
	// agents:manage caller creates the agent with no grants (tools: []) and an
	// admin grants tools afterwards.
	grantsAllowed := canGrantTools(p)
	if !grantsAllowed && len(body.Tools) > 0 {
		a.errorResp(w, http.StatusForbidden, "granting tools requires the admin scope; send tools as an empty list")
		return
	}
	req := service.AgentSetupRequest{AllowNoTools: !grantsAllowed, Definition: []byte(body.Artifact), Archetype: body.Archetype, Slug: body.Slug, Title: body.Title,
		Provider: body.Provider, Model: body.Model, Runtime: body.Runtime, Directories: body.Directories, Tools: body.Tools, Reason: body.Reason}
	for _, res := range body.Resources {
		req.Resources = append(req.Resources, store.DefinitionResource{URI: res.URI, Content: []byte(res.Content)})
	}
	res, err := a.Services.AgentSetup.Setup(r.Context(), req, p.CredentialID)
	if err != nil {
		a.agentSetupError(w, err)
		return
	}
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	a.jsonResp(w, status, res)
}

func (a *API) agentSetupError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrAgentSetupInvalid), errors.Is(err, store.ErrAgentSetupRequest):
		a.errorResp(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrAgentSetupConflict), errors.Is(err, store.ErrDefinitionRevisionConflict), errors.Is(err, store.ErrAgentHostSlugConflict):
		a.errorResp(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrNativeToolNotGrantable):
		a.errorResp(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, store.ErrProfileIngestionRetired):
		a.errorResp(w, http.StatusForbidden, err.Error())
	default:
		// Storage and filesystem failures carry paths and SQL; keep them in the log.
		slog.Error("agent setup failed", "error", err)
		a.errorResp(w, http.StatusInternalServerError, "agent setup failed")
	}
}

func (a *API) handleNativeAgentList(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.agentManagementActor(w, r); !ok {
		return
	}
	if a.Services == nil || a.Services.AgentSetup == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "agent setup unavailable")
		return
	}
	list, err := a.Services.AgentSetup.List(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "listing agents failed")
		return
	}
	page, err := pageOf(r, list)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, page)
}

func (a *API) handleNativeAgentGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.agentManagementActor(w, r); !ok {
		return
	}
	if a.Services == nil || a.Services.AgentSetup == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "agent setup unavailable")
		return
	}
	v, err := a.Services.AgentSetup.Get(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		a.errorResp(w, http.StatusNotFound, "agent not found")
	case err != nil:
		a.errorResp(w, http.StatusInternalServerError, "reading agent failed")
	default:
		a.jsonResp(w, http.StatusOK, v)
	}
}

// pageOf applies the optional ?limit=&offset= query to a list before it is
// rendered. Without limit the list is returned whole, as before.
func pageOf[T any](r *http.Request, list []T) ([]T, error) {
	q := r.URL.Query()
	if q.Get("limit") == "" && q.Get("offset") == "" {
		return list, nil
	}
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit < 1 || limit > 500 {
		return nil, errors.New("limit must be between 1 and 500")
	}
	offset := 0
	if raw := q.Get("offset"); raw != "" {
		if offset, err = strconv.Atoi(raw); err != nil || offset < 0 {
			return nil, errors.New("offset must be a non-negative integer")
		}
	}
	if offset >= len(list) {
		return list[:0], nil
	}
	return list[offset:min(offset+limit, len(list))], nil
}

type agentReloadBody struct {
	// Host is the host settings (id or slug) of a loaded agent revision.
	Host string `json:"host"`
	// Force publishes a new revision of a pinned agent.
	Force bool `json:"force,omitempty"`
	// DryRun reports what would change and writes nothing.
	DryRun bool `json:"dry_run,omitempty"`
	// GrantSameAs re-grants the tools of this host to the new revision; it
	// needs an admin credential and a reason.
	GrantSameAs string `json:"grant_same_as,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// handleAgentReload re-reads the agent directory a host row was loaded from and
// publishes its current state as a new revision (docs/architecture/agent-alignment.md,
// "Live and pinned"). It is an operator call: the directory is the one recorded
// on the host row, never a path from the request, and the new row carries no
// authority unless an admin names a host to copy tools from.
func (a *API) handleAgentReload(w http.ResponseWriter, r *http.Request) {
	p, ok := apiauth.FromContext(r.Context())
	if !ok {
		a.errorResp(w, http.StatusUnauthorized, "authentication is required")
		return
	}
	if p.Kind == apiauth.KindAgent || !p.Has(apiauth.ScopeMaintain) || strings.TrimSpace(p.CredentialID) == "" {
		a.errorResp(w, http.StatusForbidden, "the credential lacks the required scope")
		return
	}
	if a.Services == nil || a.Services.AgentLoad == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "agent reload unavailable")
		return
	}
	var body agentReloadBody
	if err := definitionRequest(w, r, &body); err != nil || strings.TrimSpace(body.Host) == "" {
		a.errorResp(w, http.StatusBadRequest, "invalid agent reload request: name the host settings to reload")
		return
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if len(body.Reason) > 500 {
		a.errorResp(w, http.StatusBadRequest, "reason is at most 500 characters")
		return
	}
	if body.GrantSameAs != "" && (!p.Has(apiauth.ScopeAdmin) || body.Reason == "") {
		a.errorResp(w, http.StatusForbidden, "granting tools on a reload needs an admin credential and a reason")
		return
	}
	res, err := a.Services.AgentLoad.Reload(r.Context(), strings.TrimSpace(body.Host), service.AgentLoadOptions{
		Force: body.Force, DryRun: body.DryRun, GrantFrom: body.GrantSameAs, Reason: body.Reason,
		AppDefaultMode: service.AppDefaultModeFromEnv(), Labels: service.DeployLabelsFromEnv(),
	}, p.CredentialID)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			a.errorResp(w, http.StatusNotFound, "host settings not found")
		case errors.Is(err, store.ErrAgentLoadPinned):
			a.errorResp(w, http.StatusConflict, err.Error())
		default:
			a.agentSetupError(w, err)
		}
		return
	}
	status := http.StatusOK
	if res.Changed && !res.DryRun {
		status = http.StatusCreated
	}
	a.jsonResp(w, status, res)
}
