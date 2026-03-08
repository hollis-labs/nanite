package api

import (
	"encoding/json"
	"net/http"

	"github.com/hollis-labs/mentat-chat/internal/chat"
	"github.com/hollis-labs/mentat-chat/internal/mcp"
	"github.com/hollis-labs/mentat-chat/internal/store"
	"github.com/hollis-labs/mentat-chat/internal/toolbroker"
	"github.com/hollis-labs/mentat-chat/internal/workflow"
)

// API holds dependencies for HTTP handlers.
type API struct {
	Store          *store.Store
	Engine         *chat.Engine
	ToolBroker     *toolbroker.ToolBroker
	MCPManager     *mcp.Manager
	WorkflowLoader *workflow.Loader
	WorkflowEngine *workflow.Engine
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

	// Session compaction
	mux.HandleFunc("POST /api/sessions/{id}/compact", a.handleCompactSession)

	// Bookmarks
	mux.HandleFunc("GET /api/sessions/{id}/bookmarks", a.handleListBookmarks)
	mux.HandleFunc("POST /api/bookmarks", a.handleCreateBookmark)
	mux.HandleFunc("DELETE /api/bookmarks/{id}", a.handleDeleteBookmark)
	mux.HandleFunc("POST /api/messages/{id}/bookmark", a.handleToggleBookmark)

	// Artifacts
	mux.HandleFunc("GET /api/sessions/{id}/artifacts", a.handleListArtifacts)
	mux.HandleFunc("GET /api/artifacts/{id}/download", a.handleDownloadArtifact)
	mux.HandleFunc("POST /api/artifacts/upload", a.handleUploadArtifact)

	// Slash commands
	mux.HandleFunc("GET /api/commands", a.handleListCommands)

	// Providers & Models
	mux.HandleFunc("GET /api/providers", a.handleListProviders)
	mux.HandleFunc("GET /api/models", a.handleListModels)

	// Workflows
	mux.HandleFunc("GET /api/workflows", a.handleListWorkflows)
	mux.HandleFunc("GET /api/workflows/{name}", a.handleGetWorkflow)
	mux.HandleFunc("POST /api/workflows/{name}/run", a.handleRunWorkflow)

	// Agent-to-agent messaging
	mux.HandleFunc("POST /api/sessions/{id}/agent-message", a.handleAgentMessage)

	// Multi-agent group sessions
	mux.HandleFunc("GET /api/sessions/{id}/agents", a.handleListSessionAgents)
	mux.HandleFunc("POST /api/sessions/{id}/agents", a.handleAddSessionAgent)

	// Token usage
	mux.HandleFunc("GET /api/sessions/{id}/usage", a.handleGetSessionUsage)
	mux.HandleFunc("GET /api/usage/summary", a.handleGetUsageSummary)

	// Tools (Tool Broker)
	mux.HandleFunc("GET /api/tools", a.handleListTools)
	mux.HandleFunc("GET /api/tools/servers", a.handleListToolServers)
	mux.HandleFunc("POST /api/tools/select", a.handleSelectTools)
	mux.HandleFunc("POST /api/tools/refresh", a.handleRefreshTools)
	mux.HandleFunc("GET /api/agents/{id}/tools", a.handleListAgentTools)

	// Skills
	mux.HandleFunc("GET /api/skills", a.handleListSkills)
	mux.HandleFunc("POST /api/skills", a.handleCreateSkill)
	mux.HandleFunc("GET /api/skills/{id}", a.handleGetSkill)
	mux.HandleFunc("PUT /api/skills/{id}", a.handleUpdateSkill)
	mux.HandleFunc("DELETE /api/skills/{id}", a.handleDeleteSkill)
	mux.HandleFunc("GET /api/agents/{id}/skills", a.handleListAgentSkills)
	mux.HandleFunc("POST /api/agents/{id}/skills", a.handleAssignAgentSkill)
	mux.HandleFunc("DELETE /api/agents/{id}/skills/{skillId}", a.handleRemoveAgentSkill)

	// Prompt Templates
	mux.HandleFunc("GET /api/prompt-templates", a.handleListPromptTemplates)
	mux.HandleFunc("POST /api/prompt-templates", a.handleCreatePromptTemplate)
	mux.HandleFunc("GET /api/prompt-templates/{id}", a.handleGetPromptTemplate)
	mux.HandleFunc("PUT /api/prompt-templates/{id}", a.handleUpdatePromptTemplate)
	mux.HandleFunc("DELETE /api/prompt-templates/{id}", a.handleDeletePromptTemplate)
	mux.HandleFunc("GET /api/agents/{id}/prompt-templates", a.handleListAgentPromptTemplates)
	mux.HandleFunc("POST /api/agents/{id}/prompt-templates", a.handleAssignAgentPromptTemplate)
	mux.HandleFunc("DELETE /api/agents/{id}/prompt-templates/{templateId}", a.handleRemoveAgentPromptTemplate)

	// Output Templates
	mux.HandleFunc("GET /api/templates", a.handleListTemplates)
	mux.HandleFunc("POST /api/templates", a.handleCreateTemplate)
	mux.HandleFunc("GET /api/templates/{name}", a.handleGetTemplate)
	mux.HandleFunc("PUT /api/templates/{name}", a.handleUpdateTemplate)
	mux.HandleFunc("DELETE /api/templates/{name}", a.handleDeleteTemplate)
	mux.HandleFunc("POST /api/templates/{name}/apply", a.handleApplyTemplate)
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
