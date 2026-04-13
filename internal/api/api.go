package api

import (
	"encoding/json"
	"net/http"

	"github.com/hollis-labs/nanite/internal/service"
)

// API holds dependencies for HTTP handlers.
type API struct {
	Services *service.Container
}

// New creates a new API instance from a service container.
func New(svc *service.Container) *API {
	return &API{Services: svc}
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
	mux.HandleFunc("PUT /api/workspaces/{wid}/projects/{pid}", a.handleUpdateProject)
	mux.HandleFunc("DELETE /api/workspaces/{wid}/projects/{pid}", a.handleDeleteProject)
	mux.HandleFunc("GET /api/projects/{id}/agents", a.handleListProjectAgents)

	// Sessions
	mux.HandleFunc("GET /api/sessions", a.handleListSessions)
	mux.HandleFunc("POST /api/sessions", a.handleCreateSession)
	mux.HandleFunc("GET /api/sessions/{id}", a.handleGetSession)
	mux.HandleFunc("PUT /api/sessions/{id}", a.handleUpdateSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", a.handleDeleteSession)
	mux.HandleFunc("POST /api/sessions/{id}/fork", a.handleForkSession)
	mux.HandleFunc("GET /api/sessions/{id}/messages", a.handleListSessionMessages)

	// Messages
	mux.HandleFunc("POST /api/messages", a.handleSendMessage)

	// SSE stream
	mux.HandleFunc("GET /api/stream/{messageID}", a.handleStream)

	// Presence SSE stream (one per browser tab)
	mux.HandleFunc("GET /api/presence", a.handlePresenceStream)

	// Retry (circuit breaker reset + re-generate)
	mux.HandleFunc("POST /api/sessions/{id}/retry", a.handleRetryStream)

	// Session mode switching
	mux.HandleFunc("POST /api/sessions/{id}/mode", a.handleSwitchSessionMode)

	// Agents
	mux.HandleFunc("GET /api/agents", a.handleListAgents)
	mux.HandleFunc("POST /api/agents", a.handleCreateAgent)
	mux.HandleFunc("GET /api/agents/{id}", a.handleGetAgent)
	mux.HandleFunc("PUT /api/agents/{id}", a.handleUpdateAgent)
	mux.HandleFunc("GET /api/agents/{id}/modes", a.handleListAgentModes)
	mux.HandleFunc("POST /api/agents/{id}/modes", a.handleCreateAgentMode)
	mux.HandleFunc("GET /api/agents/{id}/projects", a.handleListAgentProjects)
	mux.HandleFunc("POST /api/agents/{id}/projects", a.handleAddAgentProject)
	mux.HandleFunc("DELETE /api/agents/{id}/projects/{projectId}", a.handleRemoveAgentProject)

	// Session compaction
	mux.HandleFunc("POST /api/sessions/{id}/compact", a.handleCompactSession)

	// Bookmarks
	mux.HandleFunc("GET /api/sessions/{id}/bookmarks", a.handleListBookmarks)
	mux.HandleFunc("POST /api/bookmarks", a.handleCreateBookmark)
	mux.HandleFunc("DELETE /api/bookmarks/{id}", a.handleDeleteBookmark)
	mux.HandleFunc("POST /api/messages/{id}/bookmark", a.handleToggleBookmark)
	mux.HandleFunc("POST /api/bookmarks/{id}/autotitle", a.handleAutotitleBookmark)

	// Artifacts
	mux.HandleFunc("GET /api/sessions/{id}/artifacts", a.handleListArtifactsByOrigin) // supports ?origin= filter
	mux.HandleFunc("GET /api/artifacts/{id}/download", a.handleDownloadArtifact)
	mux.HandleFunc("POST /api/artifacts/upload", a.handleUploadArtifact)
	mux.HandleFunc("POST /api/artifacts/place", a.handlePlaceArtifact)

	// Slash commands
	mux.HandleFunc("GET /api/commands", a.handleListCommands)
	mux.HandleFunc("POST /api/commands/execute", a.handleExecuteCommand)

	// Autocomplete
	mux.HandleFunc("GET /api/autocomplete/files", a.handleAutocompleteFiles)

	// Providers & Models
	mux.HandleFunc("GET /api/providers", a.handleListProviders)
	mux.HandleFunc("GET /api/providers/status", a.handleGetAllProviderStatuses)
	mux.HandleFunc("GET /api/providers/detect-cli", a.handleDetectCLI)
	mux.HandleFunc("PUT /api/providers/{id}", a.handleUpdateProvider)
	mux.HandleFunc("POST /api/providers/{id}/api-key", a.handleSetProviderAPIKey)
	mux.HandleFunc("GET /api/providers/{id}/status", a.handleGetProviderStatus)
	mux.HandleFunc("GET /api/models", a.handleListModels)

	// Agent-to-agent messaging
	mux.HandleFunc("POST /api/sessions/{id}/agent-message", a.handleAgentMessage)

	// Delegation
	mux.HandleFunc("POST /api/sessions/{id}/delegate", a.handleDelegateTask)
	mux.HandleFunc("POST /api/sessions/{id}/delegate-aggregate", a.handleDelegateAndAggregate)

	// Multi-agent group sessions
	mux.HandleFunc("GET /api/sessions/{id}/agents", a.handleListSessionAgents)
	mux.HandleFunc("POST /api/sessions/{id}/agents", a.handleAddSessionAgent)
	mux.HandleFunc("DELETE /api/sessions/{id}/agents/{agentId}", a.handleRemoveSessionAgent)

	// Token usage
	mux.HandleFunc("GET /api/sessions/{id}/usage", a.handleGetSessionUsage)
	mux.HandleFunc("GET /api/usage/summary", a.handleGetUsageSummary)
	mux.HandleFunc("GET /api/sessions/{id}/context-breakdown", a.handleGetContextBreakdown)

	// Tools (Tool Broker)
	mux.HandleFunc("GET /api/tools", a.handleListTools)
	mux.HandleFunc("GET /api/tools/servers", a.handleListToolServers)
	mux.HandleFunc("GET /api/tools/all", a.handleListToolsWithLoadType)
	mux.HandleFunc("GET /api/tools/load-preferences", a.handleGetToolLoadPreferences)
	mux.HandleFunc("PUT /api/tools/load-preferences", a.handleUpdateToolLoadPreferences)
	mux.HandleFunc("POST /api/tools/select", a.handleSelectTools)
	mux.HandleFunc("POST /api/tools/refresh", a.handleRefreshTools)
	mux.HandleFunc("GET /api/broker/decisions", a.handleListBrokerDecisions)
	mux.HandleFunc("GET /api/agents/{id}/tools", a.handleListAgentTools)

	// Permissions & Approvals
	mux.HandleFunc("GET /api/permissions/mode", a.handleGetPermissionMode)
	mux.HandleFunc("PUT /api/permissions/mode", a.handleSetPermissionMode)
	mux.HandleFunc("POST /api/sessions/{id}/approvals/{requestId}", a.handleRespondApproval)

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

	// Modes (first-class reusable modes)
	mux.HandleFunc("GET /api/modes", a.handleListModes)
	mux.HandleFunc("POST /api/modes", a.handleCreateMode)
	mux.HandleFunc("GET /api/modes/{id}", a.handleGetMode)
	mux.HandleFunc("PUT /api/modes/{id}", a.handleUpdateMode)
	mux.HandleFunc("DELETE /api/modes/{id}", a.handleDeleteMode)

	// Agent ↔ Mode assignments (many-to-many)
	mux.HandleFunc("GET /api/agents/{id}/assigned-modes", a.handleListAgentAssignedModes)
	mux.HandleFunc("POST /api/agents/{id}/assigned-modes", a.handleAssignModeToAgent)
	mux.HandleFunc("DELETE /api/agents/{id}/assigned-modes/{modeId}", a.handleUnassignModeFromAgent)

	// MCP Servers (user-managed)
	mux.HandleFunc("GET /api/mcp-servers", a.handleListMCPServers)
	mux.HandleFunc("POST /api/mcp-servers", a.handleCreateMCPServer)
	mux.HandleFunc("PUT /api/mcp-servers/{name}", a.handleUpdateMCPServer)
	mux.HandleFunc("DELETE /api/mcp-servers/{name}", a.handleDeleteMCPServer)
	mux.HandleFunc("POST /api/mcp-servers/import", a.handleImportMCPServers)
	mux.HandleFunc("GET /api/mcp-servers/export", a.handleExportMCPServers)

	// A2A Messaging
	mux.HandleFunc("GET /api/a2a/inbox", a.handleA2AInbox)
	mux.HandleFunc("GET /api/a2a/threads/{threadId}", a.handleA2AThread)
	mux.HandleFunc("POST /api/a2a/messages", a.handleA2ASendMessage)
	mux.HandleFunc("PUT /api/a2a/messages/{id}/ack", a.handleA2AAck)
	mux.HandleFunc("PUT /api/a2a/messages/{id}/resolve", a.handleA2AResolve)
	mux.HandleFunc("GET /api/a2a/unread", a.handleA2AUnreadCount)

	// A2A handoff
	mux.HandleFunc("POST /api/a2a/handoffs", a.handleA2AHandoffRequest)
	mux.HandleFunc("POST /api/a2a/handoffs/{id}/approve", a.handleA2AHandoffApprove)
	mux.HandleFunc("POST /api/a2a/handoffs/{id}/reject", a.handleA2AHandoffReject)

	// A2A recent messages (for handoff catch-up / debugging)
	mux.HandleFunc("GET /api/a2a/recent", a.handleA2ARecent)

	// Output Templates
	mux.HandleFunc("GET /api/templates", a.handleListTemplates)
	mux.HandleFunc("POST /api/templates", a.handleCreateTemplate)
	mux.HandleFunc("GET /api/templates/{name}", a.handleGetTemplate)
	mux.HandleFunc("PUT /api/templates/{name}", a.handleUpdateTemplate)
	mux.HandleFunc("DELETE /api/templates/{name}", a.handleDeleteTemplate)
	mux.HandleFunc("POST /api/templates/{name}/apply", a.handleApplyTemplate)

	// Search
	mux.HandleFunc("GET /api/search", a.handleSearchMessages)

	// User Settings
	mux.HandleFunc("GET /api/settings", a.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", a.handleUpdateSettings)

	// Plugin Config (prefixed to avoid collision with plugin CRUD routes)
	mux.HandleFunc("GET /api/plugin-config/{id}", a.handleGetPluginConfig)
	mux.HandleFunc("PUT /api/plugin-config/{id}", a.handleUpdatePluginConfig)
	mux.HandleFunc("GET /api/plugin-config", a.handleListPluginSettings)

	// Process Health
	mux.HandleFunc("GET /api/processes/health", a.handleProcessHealth)
	mux.HandleFunc("POST /api/processes/kill-stale", a.handleKillStaleProcesses)

	// Connector Triggers
	mux.HandleFunc("GET /api/plugins/triggers", a.handleListTriggerRules)
	mux.HandleFunc("POST /api/plugins/triggers", a.handleCreateTriggerRule)
	mux.HandleFunc("GET /api/plugins/triggers/{id}", a.handleGetTriggerRule)
	mux.HandleFunc("PUT /api/plugins/triggers/{id}", a.handleUpdateTriggerRule)
	mux.HandleFunc("DELETE /api/plugins/triggers/{id}", a.handleDeleteTriggerRule)

	// Connectors (health & status) — under /api/connectors to avoid conflict
	// with the /api/plugins/{name}/ui/{file...} wildcard route.
	mux.HandleFunc("GET /api/connectors", a.handleListConnectors)
	mux.HandleFunc("GET /api/connectors/{name}/health", a.handleCheckConnectorHealth)

	// Keybindings (plugin-registered keyboard shortcuts)
	mux.HandleFunc("GET /api/plugins/keybindings", a.handleListKeybindings)

	// Custom Actions
	mux.HandleFunc("GET /api/actions", a.handleListActions)
	mux.HandleFunc("POST /api/actions", a.handleCreateAction)
	mux.HandleFunc("GET /api/actions/{id}", a.handleGetAction)
	mux.HandleFunc("PUT /api/actions/{id}", a.handleUpdateAction)
	mux.HandleFunc("DELETE /api/actions/{id}", a.handleDeleteAction)
	mux.HandleFunc("POST /api/actions/{id}/execute", a.handleExecuteAction)

	// Workflow runs + SSE event stream
	mux.HandleFunc("GET /api/workflows/runs", a.handleListWorkflowRuns)
	mux.HandleFunc("POST /api/workflows/runs", a.handleRunWorkflow)
	mux.HandleFunc("GET /api/workflows/runs/{runId}", a.handleGetWorkflowRun)
	mux.HandleFunc("POST /api/workflows/runs/{runId}/cancel", a.handleCancelWorkflowRun)
	mux.HandleFunc("GET /api/workflows/events", a.handleWorkflowEvents)

	// Workers (multi-agent orchestration)
	mux.HandleFunc("GET /api/workers", a.handleListWorkers)
	mux.HandleFunc("POST /api/workers/{id}/cancel", a.handleCancelWorker)

	// Tasks (multi-agent orchestration)
	mux.HandleFunc("GET /api/tasks", a.handleListTasks)
	mux.HandleFunc("POST /api/tasks", a.handleCreateTask)
	mux.HandleFunc("GET /api/tasks/{id}", a.handleGetTask)
	mux.HandleFunc("PUT /api/tasks/{id}", a.handleUpdateTask)
	mux.HandleFunc("POST /api/tasks/{id}/transition", a.handleTransitionTask)
	mux.HandleFunc("POST /api/tasks/{id}/assign", a.handleAssignTask)
	mux.HandleFunc("GET /api/sessions/{id}/tasks", a.handleListSessionTasks)

	// Shell Execution
	mux.HandleFunc("GET /api/sessions/{id}/shell-mode", a.handleGetShellMode)
	mux.HandleFunc("PUT /api/sessions/{id}/shell-mode", a.handleSetShellMode)
	mux.HandleFunc("POST /api/sessions/{id}/shell-exec", a.handleShellExec)
	mux.HandleFunc("GET /api/sessions/{id}/shell-check", a.handleShellCheck)
	mux.HandleFunc("GET /api/sessions/{id}/shell-info", a.handleShellInfo)

	// Todos (internal todo system)
	mux.HandleFunc("GET /api/todos", a.handleListTodos)
	mux.HandleFunc("POST /api/todos", a.handleCreateTodo)
	mux.HandleFunc("GET /api/todos/{id}", a.handleGetTodo)
	mux.HandleFunc("PUT /api/todos/{id}", a.handleUpdateTodo)
	mux.HandleFunc("DELETE /api/todos/{id}", a.handleDeleteTodo)
	mux.HandleFunc("GET /api/todos/{id}/children", a.handleListTodoChildren)

	// Plans (internal plan system)
	mux.HandleFunc("GET /api/plans", a.handleListPlans)
	mux.HandleFunc("POST /api/plans", a.handleCreatePlan)
	mux.HandleFunc("GET /api/plans/{id}", a.handleGetPlan)
	mux.HandleFunc("PUT /api/plans/{id}", a.handleUpdatePlan)
	mux.HandleFunc("PUT /api/plans/{id}/steps/{stepID}", a.handleUpdatePlanStep)
	mux.HandleFunc("DELETE /api/plans/{id}", a.handleDeletePlan)
	mux.HandleFunc("POST /api/plans/{id}/approve", a.handleApprovePlan)

	// Work sync (batched todo/plan changes from UI)
	mux.HandleFunc("POST /api/work/sync", a.handleSyncWork)

	// Memories
	mux.HandleFunc("GET /api/memories", a.handleListMemories)
	mux.HandleFunc("POST /api/memories", a.handleCreateMemory)
	mux.HandleFunc("PUT /api/memories/{key}", a.handleUpdateMemory)
	mux.HandleFunc("DELETE /api/memories/{key}", a.handleDeleteMemory)
	mux.HandleFunc("PUT /api/memories/{key}/status", a.handleUpdateMemoryStatus)

	// Debug
	mux.HandleFunc("GET /api/debug/slots", a.handleDebugSlots)

	// Execution Metrics
	mux.HandleFunc("GET /api/sessions/{id}/metrics", a.handleGetSessionExecutionMetrics)
	mux.HandleFunc("GET /api/metrics/executions", a.handleGetRecentExecutionMetrics)
	mux.HandleFunc("GET /api/metrics/utility", a.handleGetUtilityCallSummary)
	mux.HandleFunc("GET /api/metrics/utility/log", a.handleGetUtilityCallLog)
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
