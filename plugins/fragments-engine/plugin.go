package fragmentsengine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/mcp"
	hostplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/plugin"
)

func init() {
	hostplugin.RegisterPlugin("fragments-engine", func() plugin.Plugin { return New() })
}

// EnginePlugin provides Fragments Engine integration: sprint planning,
// backlog management, and task operations via Engine MCP tools.
type EnginePlugin struct {
	host       plugin.Host
	mcpManager *mcp.Manager
	status     plugin.PluginStatus
}

func New() *EnginePlugin {
	return &EnginePlugin{}
}

func (p *EnginePlugin) ID() string          { return "fragments-engine" }
func (p *EnginePlugin) Name() string        { return "Fragments Engine" }
func (p *EnginePlugin) Version() string     { return "0.1.0" }
func (p *EnginePlugin) Description() string { return "Fragments Engine integration — sprint planning, backlog, task management" }
func (p *EnginePlugin) Dependencies() []string { return nil }

func (p *EnginePlugin) Load(host plugin.Host) error {
	p.host = host
	logger := host.Logger()

	// Get MCP manager for tool registration and Engine MCP proxy calls.
	if svc, err := host.GetService("mcp"); err == nil {
		if mgr, ok := svc.(*mcp.Manager); ok {
			p.mcpManager = mgr
		}
	}

	// Register envelope types so they pass backend validation.
	chat.RegisterEnvelopeType("sprint-planning-review")
	chat.RegisterEnvelopeType("task-disposition")
	chat.RegisterEnvelopeType("task-complete-notification")

	// Register UI components.
	for _, env := range []plugin.UIComponent{
		{ID: "sprint-planning-review", Type: plugin.UIComponentTypeEnvelope, Name: "Sprint Planning Review", Description: "Interactive sprint planning review card with task assignment"},
		{ID: "task-disposition", Type: plugin.UIComponentTypeEnvelope, Name: "Task Disposition", Description: "Engine task disposition card"},
		{ID: "task-complete-notification", Type: plugin.UIComponentTypeEnvelope, Name: "Task Complete Notification", Description: "Engine task completion notification card"},
	} {
		if err := host.RegisterUIComponent(env); err != nil {
			return fmt.Errorf("failed to register %s envelope: %w", env.ID, err)
		}
	}

	// Register sprint MCP tools (open_sprint_planning + show_sprint_planning_review).
	if p.mcpManager != nil {
		tools := NewSprintToolsTransport(p.mcpManager)
		p.mcpManager.AddServer("engine-sprint-tools", tools)
		logger.Info("engine sprint planning tools registered as MCP server")
	}

	// Register Engine proxy HTTP routes.
	p.registerHTTPRoutes(host)

	// Register sprint planning button in the chat header via slot system.
	if nh, ok := host.(*hostplugin.Host); ok {
		if err := nh.RegisterSlot(hostplugin.UISlotEntry{
			ID:        "sprint-planning",
			Slot:      hostplugin.SlotChatHeaderAction,
			Label:     "Sprint Planning",
			Icon:      "clipboard-list",
			Priority:  10,
			Action:    "modal",
			Component: "sprint-planning",
		}); err != nil {
			logger.Warn("failed to register sprint-planning slot", "error", fmt.Sprintf("%v", err))
		}
	}

	// Register event hook for workflow events related to sprints.
	hook := &engineEventHook{logger: logger}
	if err := host.RegisterEventHook([]string{"workflow.completed"}, hook); err != nil {
		return fmt.Errorf("failed to register event hook: %w", err)
	}

	p.status = plugin.PluginStatus{
		Loaded:   true,
		Enabled:  true,
		LoadedAt: time.Now(),
	}

	logger.Info("fragments-engine plugin loaded", "version", p.Version())
	return nil
}

func (p *EnginePlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	if p.host != nil {
		p.host.Logger().Info("fragments-engine plugin unloaded")
	}
	return nil
}

func (p *EnginePlugin) Status() plugin.PluginStatus {
	return p.status
}

// registerHTTPRoutes wires Engine proxy endpoints onto the plugin host's router.
func (p *EnginePlugin) registerHTTPRoutes(host plugin.Host) {
	h, ok := host.(*hostplugin.Host)
	if !ok {
		host.Logger().Warn("fragments-engine: cannot register HTTP routes — host type assertion failed")
		return
	}

	h.RegisterHTTPHandler("POST /api/plugins/engine/backlog", http.HandlerFunc(p.handleCreateBacklog))
	h.RegisterHTTPHandler("GET /api/plugins/engine/backlog", http.HandlerFunc(p.handleListBacklog))
	h.RegisterHTTPHandler("POST /api/plugins/engine/backlog/{id}/promote", http.HandlerFunc(p.handlePromoteBacklog))
	h.RegisterHTTPHandler("GET /api/plugins/engine/sprints", http.HandlerFunc(p.handleListSprints))
	h.RegisterHTTPHandler("GET /api/plugins/engine/tasks", http.HandlerFunc(p.handleListTasks))
	h.RegisterHTTPHandler("POST /api/plugins/engine/tasks/{id}/transition", http.HandlerFunc(p.handleTransitionTask))
	h.RegisterHTTPHandler("DELETE /api/plugins/engine/tasks/{id}", http.HandlerFunc(p.handleDeleteTask))
}

// --- Engine MCP proxy handlers ---

func (p *EnginePlugin) handleCreateBacklog(w http.ResponseWriter, r *http.Request) {
	if p.mcpManager == nil {
		errorResp(w, http.StatusServiceUnavailable, "Engine not connected")
		return
	}

	var req struct {
		Title     string   `json:"title"`
		Body      string   `json:"body"`
		Priority  string   `json:"priority"`
		Tags      []string `json:"tags"`
		ProjectID string   `json:"project_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}
	defer r.Body.Close()

	if req.Title == "" {
		errorResp(w, http.StatusBadRequest, "title is required")
		return
	}
	if req.Priority == "" {
		req.Priority = "B"
	}

	args := map[string]any{
		"title":    req.Title,
		"body":     req.Body,
		"priority": req.Priority,
	}
	if req.ProjectID != "" {
		args["project_id"] = req.ProjectID
	}
	if len(req.Tags) > 0 {
		tagsJSON, _ := json.Marshal(req.Tags)
		args["tags"] = string(tagsJSON)
	}

	result, err := p.mcpManager.ExecuteTool(r.Context(), "mcp__engine__engine_backlog_capture", args)
	if err != nil {
		errorResp(w, http.StatusBadGateway, fmt.Sprintf("engine backlog capture failed: %v", err))
		return
	}

	writeEngineJSON(w, http.StatusCreated, result)
}

func (p *EnginePlugin) handleListBacklog(w http.ResponseWriter, r *http.Request) {
	if p.mcpManager == nil {
		jsonResp(w, http.StatusOK, []any{})
		return
	}

	args := map[string]any{}
	if pid := r.URL.Query().Get("project_id"); pid != "" {
		args["project_id"] = pid
	}

	result, err := p.mcpManager.ExecuteTool(r.Context(), "mcp__engine__engine_backlog_list", args)
	if err != nil {
		errorResp(w, http.StatusBadGateway, fmt.Sprintf("engine backlog_list: %v", err))
		return
	}

	writeEngineJSON(w, http.StatusOK, result)
}

func (p *EnginePlugin) handlePromoteBacklog(w http.ResponseWriter, r *http.Request) {
	if p.mcpManager == nil {
		errorResp(w, http.StatusServiceUnavailable, "Engine not connected")
		return
	}

	backlogID := r.PathValue("id")
	if backlogID == "" {
		errorResp(w, http.StatusBadRequest, "missing backlog item id")
		return
	}

	var req struct {
		SprintID string `json:"sprint_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	defer r.Body.Close()

	if req.SprintID == "" {
		errorResp(w, http.StatusBadRequest, "sprint_id is required")
		return
	}

	args := map[string]any{
		"id":        backlogID,
		"sprint_id": req.SprintID,
	}

	result, err := p.mcpManager.ExecuteTool(r.Context(), "mcp__engine__engine_backlog_promote", args)
	if err != nil {
		errorResp(w, http.StatusBadGateway, fmt.Sprintf("engine backlog_promote: %v", err))
		return
	}

	writeEngineJSON(w, http.StatusOK, result)
}

func (p *EnginePlugin) handleListSprints(w http.ResponseWriter, r *http.Request) {
	if p.mcpManager == nil {
		jsonResp(w, http.StatusOK, []any{})
		return
	}

	args := map[string]any{}
	if pid := r.URL.Query().Get("project_id"); pid != "" {
		args["project_id"] = pid
	}

	result, err := p.mcpManager.ExecuteTool(r.Context(), "mcp__engine__engine_sprints_list", args)
	if err != nil {
		errorResp(w, http.StatusBadGateway, fmt.Sprintf("engine sprints_list: %v", err))
		return
	}

	writeEngineJSON(w, http.StatusOK, result)
}

func (p *EnginePlugin) handleListTasks(w http.ResponseWriter, r *http.Request) {
	if p.mcpManager == nil {
		jsonResp(w, http.StatusOK, []any{})
		return
	}

	args := map[string]any{}
	if sid := r.URL.Query().Get("sprint_id"); sid != "" {
		args["sprint_id"] = sid
	}
	if status := r.URL.Query().Get("status"); status != "" {
		args["status"] = status
	}
	if pid := r.URL.Query().Get("project_id"); pid != "" {
		args["project_id"] = pid
	}

	result, err := p.mcpManager.ExecuteTool(r.Context(), "mcp__engine__engine_tasks_list", args)
	if err != nil {
		errorResp(w, http.StatusBadGateway, fmt.Sprintf("engine tasks_list: %v", err))
		return
	}

	writeEngineJSON(w, http.StatusOK, result)
}

func (p *EnginePlugin) handleTransitionTask(w http.ResponseWriter, r *http.Request) {
	if p.mcpManager == nil {
		errorResp(w, http.StatusServiceUnavailable, "Engine not connected")
		return
	}

	taskID := r.PathValue("id")
	if taskID == "" {
		errorResp(w, http.StatusBadRequest, "missing task id")
		return
	}

	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	defer r.Body.Close()

	if req.Status == "" {
		errorResp(w, http.StatusBadRequest, "status is required")
		return
	}

	args := map[string]any{
		"id":     taskID,
		"status": req.Status,
	}

	result, err := p.mcpManager.ExecuteTool(r.Context(), "mcp__engine__engine_task_transition", args)
	if err != nil {
		errorResp(w, http.StatusBadGateway, fmt.Sprintf("engine task_transition: %v", err))
		return
	}

	writeEngineJSON(w, http.StatusOK, result)
}

func (p *EnginePlugin) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	if p.mcpManager == nil {
		errorResp(w, http.StatusServiceUnavailable, "Engine not connected")
		return
	}

	taskID := r.PathValue("id")
	if taskID == "" {
		errorResp(w, http.StatusBadRequest, "missing task id")
		return
	}

	args := map[string]any{
		"id": taskID,
	}

	result, err := p.mcpManager.ExecuteTool(r.Context(), "mcp__engine__engine_task_delete", args)
	if err != nil {
		errorResp(w, http.StatusBadGateway, fmt.Sprintf("engine task_delete: %v", err))
		return
	}

	writeEngineJSON(w, http.StatusOK, result)
}

// --- HTTP response helpers ---

func jsonResp(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func errorResp(w http.ResponseWriter, status int, msg string) {
	jsonResp(w, status, map[string]string{"error": msg})
}

func writeEngineJSON(w http.ResponseWriter, status int, raw string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if json.Valid([]byte(raw)) {
		w.Write([]byte(raw))
		return
	}

	envelope := map[string]string{"result": raw}
	json.NewEncoder(w).Encode(envelope)
}

// --- Event hook ---

type engineEventHook struct {
	logger plugin.Logger
}

func (h *engineEventHook) Handle(ctx context.Context, event plugin.Event) error {
	h.logger.Debug("fragments-engine: received event", "type", event.Type, "session", event.SessionID)
	return nil
}

func (h *engineEventHook) EventTypes() []string {
	return []string{"workflow.completed"}
}
