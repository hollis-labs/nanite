package api

import (
	"database/sql"
	"errors"
	"io"
	"net/http"

	"github.com/hollis-labs/nanite/internal/agentauthor"
	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	mesh "github.com/hollis-labs/substrate/mesh"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

func (a *API) registerAgentDefinitionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/agent-definitions", a.handleDefinitionCatalog)
	mux.HandleFunc("POST /api/agent-definitions", a.handleDefinitionInstall)
	mux.HandleFunc("POST /api/agent-definitions/author", a.handleDefinitionAuthor)
	mux.HandleFunc("GET /api/agent-host-settings", a.handleHostSettingsList)
	mux.HandleFunc("POST /api/agent-host-settings", a.handleHostSettingsCreate)
	mux.HandleFunc("PUT /api/agent-host-settings/{id}", a.handleHostSettingsUpdate)
	mux.HandleFunc("DELETE /api/agent-host-settings/{id}", a.handleHostSettingsDelete)
}

func (a *API) handleDefinitionAuthor(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Role     string `json:"role,omitempty"`
		Concrete string `json:"concrete"`
	}
	if err := definitionRequest(w, r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid authoring request")
		return
	}
	concrete, err := agentdef.Parse([]byte(req.Concrete), agentpolicy.Option())
	if err != nil {
		a.definitionError(w, err)
		return
	}
	var role *agentdef.Definition
	if req.Role != "" {
		role, err = agentdef.Parse([]byte(req.Role), agentpolicy.Option())
		if err != nil {
			a.definitionError(w, err)
			return
		}
	}
	flat, err := agentauthor.Flatten(role, concrete)
	if err != nil {
		a.definitionError(w, err)
		return
	}
	data, err := agentauthor.Render(flat)
	if err != nil {
		a.definitionError(w, err)
		return
	}
	a.jsonResp(w, http.StatusOK, struct {
		Artifact string `json:"artifact"`
	}{string(data)})
}
func definitionRequest(w http.ResponseWriter, r *http.Request, v any) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		return err
	}
	return agentpolicy.DecodeJSON(body, v)
}
func (a *API) definitionHost(w http.ResponseWriter) *service.AgentDefinitions {
	if a.Services == nil || a.Services.AgentDefinitions == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "agent definition host unavailable")
		return nil
	}
	return a.Services.AgentDefinitions
}
func (a *API) definitionError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, sql.ErrNoRows):
		status = http.StatusNotFound
	case errors.Is(err, store.ErrDefinitionRevisionConflict), errors.Is(err, store.ErrAgentHostRevisionConflict), errors.Is(err, store.ErrAgentHostSlugConflict):
		status = http.StatusConflict
	case errors.Is(err, store.ErrProfileIngestionRetired):
		status = http.StatusForbidden
	}
	a.errorResp(w, status, err.Error())
}
func (a *API) handleDefinitionCatalog(w http.ResponseWriter, r *http.Request) {
	host := a.definitionHost(w)
	if host == nil {
		return
	}
	list, err := host.List(r.Context())
	if err != nil {
		a.definitionError(w, err)
		return
	}
	type entry struct {
		Ref      mesh.DefinitionRef `json:"definition_ref"`
		Artifact string             `json:"artifact"`
	}
	out := make([]entry, 0, len(list))
	for _, e := range list {
		out = append(out, entry{e.Ref, string(e.Data)})
	}
	a.jsonResp(w, http.StatusOK, out)
}
func (a *API) handleDefinitionInstall(w http.ResponseWriter, r *http.Request) {
	host := a.definitionHost(w)
	if host == nil {
		return
	}
	var req struct {
		Artifact  string `json:"artifact"`
		Resources []struct {
			URI     string `json:"uri"`
			Content string `json:"content"`
		} `json:"resources"`
	}
	if err := definitionRequest(w, r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid authored definition request")
		return
	}
	refs := make([]store.DefinitionResource, 0, len(req.Resources))
	for _, ref := range req.Resources {
		refs = append(refs, store.DefinitionResource{URI: ref.URI, Content: []byte(ref.Content)})
	}
	pin, err := host.Install(r.Context(), []byte(req.Artifact), refs)
	if err != nil {
		a.definitionError(w, err)
		return
	}
	a.jsonResp(w, http.StatusCreated, pin)
}
func (a *API) handleHostSettingsList(w http.ResponseWriter, r *http.Request) {
	host := a.definitionHost(w)
	if host == nil {
		return
	}
	list, err := host.ListHosts(r.Context())
	if err != nil {
		a.definitionError(w, err)
		return
	}
	a.jsonResp(w, http.StatusOK, list)
}

type hostSettingsInput struct {
	Slug          string                   `json:"slug"`
	Title         string                   `json:"title"`
	DefinitionRef mesh.DefinitionRef       `json:"definition_ref"`
	Settings      store.NativeHostSettings `json:"settings"`
	Enabled       bool                     `json:"enabled"`
	Revision      string                   `json:"revision,omitempty"`
}

func (a *API) handleHostSettingsCreate(w http.ResponseWriter, r *http.Request) {
	host := a.definitionHost(w)
	if host == nil {
		return
	}
	var req hostSettingsInput
	if err := definitionRequest(w, r, &req); err != nil || req.Revision != "" {
		a.errorResp(w, http.StatusBadRequest, "invalid host settings request")
		return
	}
	h, err := host.CreateHost(r.Context(), store.AgentHostSettings{Slug: req.Slug, Title: req.Title, DefinitionRef: req.DefinitionRef, Settings: req.Settings, Enabled: req.Enabled})
	if err != nil {
		a.definitionError(w, err)
		return
	}
	a.jsonResp(w, http.StatusCreated, h)
}
func (a *API) handleHostSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	host := a.definitionHost(w)
	if host == nil {
		return
	}
	var req hostSettingsInput
	if err := definitionRequest(w, r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid host settings request")
		return
	}
	h, err := host.GetHost(r.Context(), r.PathValue("id"))
	if err != nil {
		a.definitionError(w, err)
		return
	}
	h.Slug = req.Slug
	h.Title = req.Title
	h.DefinitionRef = req.DefinitionRef
	h.Settings = req.Settings
	h.Enabled = req.Enabled
	saved, err := host.UpdateHost(r.Context(), h, req.Revision)
	if err != nil {
		a.definitionError(w, err)
		return
	}
	a.jsonResp(w, http.StatusOK, saved)
}
func (a *API) handleHostSettingsDelete(w http.ResponseWriter, r *http.Request) {
	host := a.definitionHost(w)
	if host == nil {
		return
	}
	var req struct {
		Revision string `json:"revision"`
	}
	if err := definitionRequest(w, r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid revision request")
		return
	}
	if err := host.DeleteHost(r.Context(), r.PathValue("id"), req.Revision); err != nil {
		a.definitionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
