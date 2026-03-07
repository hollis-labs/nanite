package api

import (
	"encoding/json"
	"net/http"

	"github.com/hollis-labs/mentat-chat/internal/chat"
	"github.com/hollis-labs/mentat-chat/internal/store"
)

// API holds dependencies for HTTP handlers.
type API struct {
	Store  *store.Store
	Engine *chat.Engine
}

// New creates a new API instance.
func New(s *store.Store, engine *chat.Engine) *API {
	return &API{Store: s, Engine: engine}
}

// RegisterRoutes wires all API routes onto the given ServeMux.
func (a *API) RegisterRoutes(mux *http.ServeMux) {
	// Workspaces
	mux.HandleFunc("GET /api/workspaces", a.handleListWorkspaces)
	mux.HandleFunc("POST /api/workspaces", a.handleCreateWorkspace)
	mux.HandleFunc("GET /api/workspaces/{id}", a.handleGetWorkspace)
	mux.HandleFunc("PUT /api/workspaces/{id}", a.handleUpdateWorkspace)
	mux.HandleFunc("DELETE /api/workspaces/{id}", a.handleDeleteWorkspace)
	mux.HandleFunc("GET /api/workspaces/{wid}/projects", a.handleListProjects)
	mux.HandleFunc("POST /api/workspaces/{wid}/projects", a.handleCreateProject)

	// Sessions
	mux.HandleFunc("GET /api/sessions", a.handleListSessions)
	mux.HandleFunc("POST /api/sessions", a.handleCreateSession)
	mux.HandleFunc("GET /api/sessions/{id}", a.handleGetSession)
	mux.HandleFunc("PUT /api/sessions/{id}", a.handleUpdateSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", a.handleDeleteSession)
	mux.HandleFunc("GET /api/sessions/{id}/messages", a.handleListSessionMessages)

	// Messages
	mux.HandleFunc("POST /api/messages", a.handleSendMessage)

	// SSE stream
	mux.HandleFunc("GET /api/stream/{messageID}", a.handleStream)

	// Session mode switching
	mux.HandleFunc("POST /api/sessions/{id}/mode", a.handleSwitchSessionMode)

	// Agents
	mux.HandleFunc("GET /api/agents", a.handleListAgents)
	mux.HandleFunc("POST /api/agents", a.handleCreateAgent)
	mux.HandleFunc("GET /api/agents/{id}", a.handleGetAgent)
	mux.HandleFunc("PUT /api/agents/{id}", a.handleUpdateAgent)
	mux.HandleFunc("GET /api/agents/{id}/modes", a.handleListAgentModes)
	mux.HandleFunc("POST /api/agents/{id}/modes", a.handleCreateAgentMode)

	// Providers & Models
	mux.HandleFunc("GET /api/providers", a.handleListProviders)
	mux.HandleFunc("GET /api/models", a.handleListModels)
}

// jsonResp writes a JSON response with the given status code.
func (a *API) jsonResp(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// errorResp writes a JSON error response.
func (a *API) errorResp(w http.ResponseWriter, status int, msg string) {
	a.jsonResp(w, status, map[string]string{"error": msg})
}

// decode decodes a JSON request body into v.
func (a *API) decode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
