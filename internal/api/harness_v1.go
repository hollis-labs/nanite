package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/effort"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/version"
)

const harnessV1RoutePrefix = "/api/harness/v1"

type harnessV1AppInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type harnessV1PermissionSupport struct {
	SupportLevel          string   `json:"support_level"`
	ApprovalResponseRoute string   `json:"approval_response_route,omitempty"`
	Notes                 []string `json:"notes,omitempty"`
}

type harnessV1RouteHints struct {
	Capabilities      string `json:"capabilities"`
	Sessions          string `json:"sessions"`
	SessionEvents     string `json:"session_events"`
	SessionCancel     string `json:"session_cancel"`
	SessionApprovals  string `json:"session_approvals"`
	DurableAgents     string `json:"durable_agents"`
	DurableAgentStart string `json:"durable_agent_start"`
	DurableAgentWake  string `json:"durable_agent_wake"`
}

type harnessV1InitializeResponse struct {
	SchemaVersion       int                        `json:"schema_version"`
	ProtocolVersion     string                     `json:"protocol_version"`
	RoutePrefix         string                     `json:"route_prefix"`
	App                 harnessV1AppInfo           `json:"app"`
	Operations          []string                   `json:"operations"`
	StreamTransports    []string                   `json:"stream_transports"`
	SupportedEventTypes []string                   `json:"supported_event_types"`
	PermissionRequests  harnessV1PermissionSupport `json:"permission_requests"`
	RouteHints          harnessV1RouteHints        `json:"route_hints"`
	Unsupported         []string                   `json:"unsupported"`
}

type harnessV1FieldSupport struct {
	Supported   []string `json:"supported"`
	Unsupported []string `json:"unsupported"`
}

type harnessV1CapabilitiesResponse struct {
	SchemaVersion         int                        `json:"schema_version"`
	ProtocolVersion       string                     `json:"protocol_version"`
	RoutePrefix           string                     `json:"route_prefix"`
	App                   harnessV1AppInfo           `json:"app"`
	Operations            []string                   `json:"operations"`
	StreamTransports      []string                   `json:"stream_transports"`
	RuntimeKinds          []runtimeKindOption        `json:"runtime_kinds"`
	LifecycleClasses      []enumOption               `json:"lifecycle_classes"`
	DurableStatuses       []enumOption               `json:"durable_statuses"`
	AttachmentRelations   []enumOption               `json:"attachment_relations"`
	WakeReasons           []enumOption               `json:"wake_reasons"`
	SessionActivityStates []enumOption               `json:"session_activity_states"`
	SupportedEventTypes   []enumOption               `json:"supported_event_types"`
	SessionCreateFields   harnessV1FieldSupport      `json:"session_create_fields"`
	TurnSendFields        harnessV1FieldSupport      `json:"turn_send_fields"`
	PermissionRequests    harnessV1PermissionSupport `json:"permission_requests"`
	RouteHints            harnessV1RouteHints        `json:"route_hints"`
}

type harnessV1CreateSessionRequest struct {
	WorkspaceID    string         `json:"workspace_id"`
	ProjectID      string         `json:"project_id,omitempty"`
	Provider       string         `json:"provider,omitempty"`
	Model          string         `json:"model,omitempty"`
	AgentID        string         `json:"agent_id,omitempty"`
	Title          string         `json:"title,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	ModeID         string         `json:"mode_id,omitempty"`
	RuntimeKind    string         `json:"runtime_kind,omitempty"`
	WorkRoot       string         `json:"work_root,omitempty"`
	BootProfileID  string         `json:"boot_profile_id,omitempty"`
	DurableAgentID string         `json:"durable_agent_id,omitempty"`
}

type harnessV1SessionResponse struct {
	Session         *store.Session         `json:"session"`
	Details         sessionDetailsResponse `json:"details"`
	StreamTransport string                 `json:"stream_transport"`
	RouteHints      harnessV1SessionRoutes `json:"route_hints"`
}

type harnessV1SessionRoutes struct {
	Self      string `json:"self"`
	Events    string `json:"events"`
	Cancel    string `json:"cancel"`
	Approvals string `json:"approvals"`
}

type harnessV1TurnRequest struct {
	Content   string `json:"content"`
	CycleKind string `json:"cycle_kind,omitempty"`
	Effort    string `json:"effort,omitempty"`
}

type harnessV1TurnResponse struct {
	SessionID            string `json:"session_id"`
	MessageID            string `json:"message_id"`
	StreamURL            string `json:"stream_url"`
	RawStreamURL         string `json:"raw_stream_url"`
	EventTransport       string `json:"event_transport"`
	InitialActivityState string `json:"initial_activity_state"`
}

type harnessV1CancelResponse struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}

func (a *API) handleHarnessV1Initialize(w http.ResponseWriter, r *http.Request) {
	a.jsonResp(w, http.StatusOK, harnessV1InitializeResponse{
		SchemaVersion:       1,
		ProtocolVersion:     "v1",
		RoutePrefix:         harnessV1RoutePrefix,
		App:                 harnessV1App(),
		Operations:          harnessV1Operations(),
		StreamTransports:    []string{"sse"},
		SupportedEventTypes: harnessV1EventTypeValues(),
		PermissionRequests:  harnessV1PermissionSupportInfo(),
		RouteHints:          harnessV1Routes(),
		Unsupported: []string{
			"raw_pty_tui_sessions",
			"arbitrary_callback_execution",
			"runtime_kind_override_on_session_create",
		},
	})
}

func (a *API) handleHarnessV1Capabilities(w http.ResponseWriter, r *http.Request) {
	a.jsonResp(w, http.StatusOK, harnessV1CapabilitiesResponse{
		SchemaVersion:         1,
		ProtocolVersion:       "v1",
		RoutePrefix:           harnessV1RoutePrefix,
		App:                   harnessV1App(),
		Operations:            harnessV1Operations(),
		StreamTransports:      []string{"sse"},
		RuntimeKinds:          runtimeKindOptions(),
		LifecycleClasses:      lifecycleClassOptions(),
		DurableStatuses:       durableStatusOptions(),
		AttachmentRelations:   attachmentRelationOptions(),
		WakeReasons:           wakeReasonOptions(),
		SessionActivityStates: harnessV1SessionActivityOptions(),
		SupportedEventTypes:   harnessV1EventTypes(),
		SessionCreateFields: harnessV1FieldSupport{
			Supported: []string{
				"workspace_id",
				"project_id",
				"provider",
				"model",
				"agent_id",
				"title",
				"metadata",
				"mode_id",
				"boot_profile_id",
			},
			Unsupported: []string{
				"runtime_kind",
				"work_root",
				"durable_agent_id",
			},
		},
		TurnSendFields: harnessV1FieldSupport{
			Supported:   []string{"content", "cycle_kind", "effort"},
			Unsupported: []string{},
		},
		PermissionRequests: harnessV1PermissionSupportInfo(),
		RouteHints:         harnessV1Routes(),
	})
}

func (a *API) handleHarnessV1CreateSession(w http.ResponseWriter, r *http.Request) {
	var req harnessV1CreateSessionRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.WorkspaceID == "" {
		a.errorResp(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	if req.RuntimeKind != "" {
		a.errorResp(w, http.StatusUnprocessableEntity, "runtime_kind override is unsupported on harness v1 session create; runtime is inferred from provider or boot profile")
		return
	}
	if req.WorkRoot != "" {
		a.errorResp(w, http.StatusUnprocessableEntity, "work_root is unsupported on harness v1 session create; use durable-agent start/resume/wake when work-root policy matters")
		return
	}
	if req.DurableAgentID != "" {
		a.errorResp(w, http.StatusUnprocessableEntity, "durable_agent_id is unsupported on harness v1 session create; use /api/harness/v1/durable-agents/{id}/start, /resume, or /wake")
		return
	}

	providerID := strings.TrimSpace(req.Provider)
	if req.BootProfileID != "" {
		bootProvider := "bootprofile:" + strings.TrimSpace(req.BootProfileID)
		if providerID != "" && providerID != bootProvider {
			a.errorResp(w, http.StatusBadRequest, "provider and boot_profile_id conflict")
			return
		}
		providerID = bootProvider
	}

	sess := &store.Session{
		WorkspaceID: req.WorkspaceID,
		ProjectID:   req.ProjectID,
		Provider:    providerID,
		Model:       req.Model,
	}
	if err := a.Services.Store.CreateSession(sess); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.AgentID != "" {
		if err := a.Services.Store.EnsureSessionAgent(sess.ID, req.AgentID, "default", true); err != nil {
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.Title != "" {
		sess.Title = req.Title
		if err := a.Services.Store.UpdateSession(sess); err != nil {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if len(req.Metadata) > 0 {
		blob, err := json.Marshal(req.Metadata)
		if err != nil {
			a.errorResp(w, http.StatusBadRequest, "metadata must be serializable JSON")
			return
		}
		if err := a.Services.Store.UpdateSessionMetadata(sess.ID, string(blob)); err != nil {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if req.ModeID != "" {
		if err := a.Services.Store.SetSessionMode(sess.ID, req.ModeID); err != nil {
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	details, err := a.sessionDetails(sess.ID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, harnessV1SessionResponse{
		Session:         details.Session,
		Details:         details,
		StreamTransport: "sse",
		RouteHints:      harnessV1SessionRoutesForSession(sess.ID),
	})
}

func (a *API) handleHarnessV1GetSession(w http.ResponseWriter, r *http.Request) {
	details, err := a.sessionDetails(r.PathValue("id"))
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "session not found")
		return
	}
	a.jsonResp(w, http.StatusOK, harnessV1SessionResponse{
		Session:         details.Session,
		Details:         details,
		StreamTransport: "sse",
		RouteHints:      harnessV1SessionRoutesForSession(details.Session.ID),
	})
}

func (a *API) handleHarnessV1SendTurn(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if _, err := a.Services.Store.GetSession(sessionID); err != nil {
		a.errorResp(w, http.StatusNotFound, "session not found")
		return
	}

	var req harnessV1TurnRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		a.errorResp(w, http.StatusBadRequest, "content is required")
		return
	}

	turnEffort := effort.Parse(req.Effort)
	if !turnEffort.IsValid() {
		turnEffort = effort.Default
	}
	ctx := effort.WithContext(r.Context(), turnEffort)
	if req.CycleKind != "" {
		ctx = service.WithAgentCycleKindForAPI(ctx, req.CycleKind)
	}
	msgID, err := a.Services.Chat.HandleMessage(ctx, sessionID, req.Content)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	streamURL := fmt.Sprintf("%s/sessions/%s/events?message_id=%s", harnessV1RoutePrefix, sessionID, msgID)
	a.jsonResp(w, http.StatusAccepted, harnessV1TurnResponse{
		SessionID:            sessionID,
		MessageID:            msgID,
		StreamURL:            streamURL,
		RawStreamURL:         fmt.Sprintf("/api/stream/%s", msgID),
		EventTransport:       "sse",
		InitialActivityState: "working",
	})
}

func (a *API) handleHarnessV1CancelTurn(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if _, err := a.Services.Store.GetSession(sessionID); err != nil {
		a.errorResp(w, http.StatusNotFound, "session not found")
		return
	}
	status := "idle"
	if a.Services.Chat.CancelActiveGeneration(sessionID) {
		status = "cancelled"
	}
	a.jsonResp(w, http.StatusOK, harnessV1CancelResponse{
		SessionID: sessionID,
		Status:    status,
	})
}

func (a *API) handleHarnessV1SessionEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	messageID := strings.TrimSpace(r.URL.Query().Get("message_id"))
	if messageID == "" {
		a.errorResp(w, http.StatusBadRequest, "message_id query parameter is required")
		return
	}
	a.streamMessageEvents(w, r, messageID, sessionID)
}

func (a *API) handleHarnessV1ListDurableAgents(w http.ResponseWriter, r *http.Request) {
	includeArchived := r.URL.Query().Get("include_archived") == "true"
	instances, err := a.Services.DurableAgents.List(r.Context(), includeArchived)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, instances)
}

func (a *API) handleHarnessV1GetDurableAgent(w http.ResponseWriter, r *http.Request) {
	inst, err := a.Services.DurableAgents.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "durable agent not found")
		return
	}
	a.jsonResp(w, http.StatusOK, inst)
}

func (a *API) handleHarnessV1DurableStart(w http.ResponseWriter, r *http.Request) {
	var req DurableAgentStartRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	result, err := a.Services.DurableAgents.Start(r.Context(), r.PathValue("id"), service.DurableAgentStartRequest{
		WorkspaceID: req.WorkspaceID,
		ProjectID:   req.ProjectID,
		WakePayload: durableAgentWakePayloadFromRequest(req.WakePayload),
	})
	a.writeDurableAgentLaunchResult(w, result, err)
}

func (a *API) handleHarnessV1DurableResume(w http.ResponseWriter, r *http.Request) {
	var req DurableAgentStartRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	result, err := a.Services.DurableAgents.Resume(r.Context(), r.PathValue("id"), service.DurableAgentStartRequest{
		WorkspaceID: req.WorkspaceID,
		ProjectID:   req.ProjectID,
		WakePayload: durableAgentWakePayloadFromRequest(req.WakePayload),
	})
	a.writeDurableAgentLaunchResult(w, result, err)
}

func (a *API) handleHarnessV1DurableWake(w http.ResponseWriter, r *http.Request) {
	var req DurableAgentStartRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	result, err := a.Services.DurableWake.Wake(r.Context(), r.PathValue("id"), service.DurableAgentWakeRequest{
		WorkspaceID: req.WorkspaceID,
		ProjectID:   req.ProjectID,
		WakePayload: durableAgentWakePayloadFromRequest(req.WakePayload),
	})
	if err != nil {
		a.errorResp(w, http.StatusConflict, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, result)
}

func harnessV1App() harnessV1AppInfo {
	return harnessV1AppInfo{
		ID:      brand.ID,
		Name:    brand.Name,
		Version: version.Full(),
	}
}

func harnessV1Operations() []string {
	return []string{
		"initialize",
		"capabilities",
		"sessions.create",
		"sessions.get",
		"sessions.turns.create",
		"sessions.turns.cancel",
		"sessions.events.stream",
		"sessions.approvals.respond",
		"durable_agents.list",
		"durable_agents.get",
		"durable_agents.start",
		"durable_agents.resume",
		"durable_agents.wake",
	}
}

func harnessV1Routes() harnessV1RouteHints {
	return harnessV1RouteHints{
		Capabilities:      harnessV1RoutePrefix + "/capabilities",
		Sessions:          harnessV1RoutePrefix + "/sessions",
		SessionEvents:     harnessV1RoutePrefix + "/sessions/{id}/events?message_id={message_id}",
		SessionCancel:     harnessV1RoutePrefix + "/sessions/{id}/cancel",
		SessionApprovals:  harnessV1RoutePrefix + "/sessions/{id}/approvals/{requestId}",
		DurableAgents:     harnessV1RoutePrefix + "/durable-agents",
		DurableAgentStart: harnessV1RoutePrefix + "/durable-agents/{id}/start",
		DurableAgentWake:  harnessV1RoutePrefix + "/durable-agents/{id}/wake",
	}
}

func harnessV1PermissionSupportInfo() harnessV1PermissionSupport {
	return harnessV1PermissionSupport{
		SupportLevel:          "approval_request_stream_event",
		ApprovalResponseRoute: harnessV1RoutePrefix + "/sessions/{id}/approvals/{requestId}",
		Notes: []string{
			"Tool permission prompts arrive as SSE events of type `approval_request` with request_id, tool, input, and reason in the JSON data payload.",
			"Interactive envelope-style prompts are not normalized by this API beyond the existing plugin_envelope stream event.",
		},
	}
}

func harnessV1SessionActivityOptions() []enumOption {
	return []enumOption{
		{Value: "idle", Label: "Idle", Description: "No active runtime work is visible."},
		{Value: "online", Label: "Online", Description: "Attached or running without active streaming work."},
		{Value: "working", Label: "Working", Description: "A turn or runtime stream is actively producing work."},
		{Value: "pending_action", Label: "Pending action", Description: "Waiting on approval, input, or another external action."},
		{Value: "failed", Label: "Failed", Description: "Latest runtime or lifecycle state is failed."},
		{Value: "halted", Label: "Halted", Description: "Session is explicitly halted and requires operator action."},
		{Value: "stopped", Label: "Stopped", Description: "Runtime or durable session is stopped but not archived."},
		{Value: "archived", Label: "Archived", Description: "Session is archived and no longer active."},
	}
}

func harnessV1EventTypes() []enumOption {
	return []enumOption{
		{Value: "stream_start", Label: "Stream start"},
		{Value: "delta", Label: "Assistant delta"},
		{Value: "replace_content", Label: "Replace content"},
		{Value: "tool_call", Label: "Tool call"},
		{Value: "tool_result", Label: "Tool result"},
		{Value: "approval_request", Label: "Approval request", Description: "Permission gate emitted before a tool runs."},
		{Value: "tool_warning", Label: "Tool warning"},
		{Value: "notify_pause", Label: "Notify pause"},
		{Value: "plugin_envelope", Label: "Plugin envelope"},
		{Value: "message_received", Label: "Message received"},
		{Value: "subagent_run_status_changed", Label: "Subagent status"},
		{Value: "mode_suggestion", Label: "Mode suggestion"},
		{Value: "status", Label: "Status"},
		{Value: "error", Label: "Error"},
		{Value: "stream_end", Label: "Completion"},
		{Value: "session_takeover", Label: "Session takeover"},
	}
}

func harnessV1EventTypeValues() []string {
	options := harnessV1EventTypes()
	values := make([]string, 0, len(options))
	for _, option := range options {
		values = append(values, option.Value)
	}
	return values
}

func harnessV1SessionRoutesForSession(sessionID string) harnessV1SessionRoutes {
	return harnessV1SessionRoutes{
		Self:      fmt.Sprintf("%s/sessions/%s", harnessV1RoutePrefix, sessionID),
		Events:    fmt.Sprintf("%s/sessions/%s/events?message_id={message_id}", harnessV1RoutePrefix, sessionID),
		Cancel:    fmt.Sprintf("%s/sessions/%s/cancel", harnessV1RoutePrefix, sessionID),
		Approvals: fmt.Sprintf("%s/sessions/%s/approvals/{requestId}", harnessV1RoutePrefix, sessionID),
	}
}
