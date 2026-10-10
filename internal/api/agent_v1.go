package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	agentservice "github.com/hollis-labs/substrate/agent/service"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/clientcontext"
	"github.com/hollis-labs/nanite/internal/effort"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/version"
)

const agentV1RoutePrefix = "/api/agent/v1"

type agentV1AppInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type agentV1PermissionSupport struct {
	SupportLevel          string   `json:"support_level"`
	ApprovalResponseRoute string   `json:"approval_response_route,omitempty"`
	Notes                 []string `json:"notes,omitempty"`
}

type agentV1RouteHints struct {
	DefinitionCatalog string `json:"definition_catalog"`
	DefinitionAuthor  string `json:"definition_author"`
	HostSettings      string `json:"host_settings"`
	Capabilities      string `json:"capabilities"`
	Sessions          string `json:"sessions"`
	TurnStatus        string `json:"turn_status"`
	TurnEvents        string `json:"turn_events"`
	TurnCancel        string `json:"turn_cancel"`
	SessionApprovals  string `json:"session_approvals"`
}

type agentV1InitializeResponse struct {
	DefaultDefinitionRef   service.DefinitionRef    `json:"default_definition_ref"`
	StreamEncodings        []string                 `json:"stream_encodings"`
	SessionStreamSupported bool                     `json:"session_stream_supported"`
	SchemaVersion          int                      `json:"schema_version"`
	ProtocolVersion        string                   `json:"protocol_version"`
	RoutePrefix            string                   `json:"route_prefix"`
	App                    agentV1AppInfo           `json:"app"`
	Operations             []string                 `json:"operations"`
	StreamTransports       []string                 `json:"stream_transports"`
	SupportedEventTypes    []string                 `json:"supported_event_types"`
	PermissionRequests     agentV1PermissionSupport `json:"permission_requests"`
	RouteHints             agentV1RouteHints        `json:"route_hints"`
	Unsupported            []string                 `json:"unsupported"`
}

// agentV1TurnDelivery states what a turn sent while another is running on
// the session does, and how to interrupt instead (CW-20261001-0072).
type agentV1TurnDelivery struct {
	MidRun    string   `json:"mid_run"`
	Interrupt string   `json:"interrupt"`
	Notes     []string `json:"notes,omitempty"`
}

type agentV1FieldSupport struct {
	Supported   []string `json:"supported"`
	Unsupported []string `json:"unsupported"`
}

type agentV1CapabilitiesResponse struct {
	DefaultDefinitionRef        service.DefinitionRef    `json:"default_definition_ref"`
	StreamEncodings             []string                 `json:"stream_encodings"`
	SessionStreamSupported      bool                     `json:"session_stream_supported"`
	EventRetention              int                      `json:"event_retention"`
	EventGraceSeconds           int                      `json:"event_grace_seconds"`
	QueueLimit                  int                      `json:"queue_limit"`
	SupportedPermissionProfiles []string                 `json:"supported_permission_profiles"`
	SchemaVersion               int                      `json:"schema_version"`
	ProtocolVersion             string                   `json:"protocol_version"`
	RoutePrefix                 string                   `json:"route_prefix"`
	App                         agentV1AppInfo           `json:"app"`
	Operations                  []string                 `json:"operations"`
	StreamTransports            []string                 `json:"stream_transports"`
	RuntimeKinds                []runtimeKindOption      `json:"runtime_kinds"`
	TurnStates                  []enumOption             `json:"turn_states"`
	SupportedEventTypes         []enumOption             `json:"supported_event_types"`
	SessionCreateFields         agentV1FieldSupport      `json:"session_create_fields"`
	TurnSendFields              agentV1FieldSupport      `json:"turn_send_fields"`
	TurnDelivery                agentV1TurnDelivery      `json:"turn_delivery"`
	PermissionRequests          agentV1PermissionSupport `json:"permission_requests"`
	RouteHints                  agentV1RouteHints        `json:"route_hints"`
}

type agentV1CreateSessionRequest struct {
	DefinitionRef  service.DefinitionRef    `json:"definition_ref"`
	ModelSelection *service.ModelSelection  `json:"model_selection,omitempty"`
	HostSettings   *service.HostSettingsRef `json:"host_settings,omitempty"`
	ProjectID      string                   `json:"project_id,omitempty"`
	Title          string                   `json:"title,omitempty"`
	Metadata       map[string]any           `json:"metadata,omitempty"`
}

type agentV1SessionResponse struct {
	SessionViewID       string                 `json:"session_view_id"`
	DefinitionRef       *service.DefinitionRef `json:"definition_ref"`
	CurrentTurnID       *string                `json:"current_turn_id"`
	ParentSessionViewID *string                `json:"parent_session_view_id"`
	ForkMessageID       *string                `json:"fork_message_id"`
	HostSubject         any                    `json:"host_subject"`
	Session             *SessionView           `json:"session"`
	StreamTransport     string                 `json:"stream_transport"`
	RouteHints          agentV1SessionRoutes   `json:"route_hints"`
}
type agentV1SessionRoutes struct {
	Self      string `json:"self"`
	History   string `json:"history"`
	Turns     string `json:"turns"`
	Approvals string `json:"approvals"`
}

type agentV1InputPart struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type agentV1TurnRequest struct {
	Content  []agentV1InputPart `json:"content"`
	Delivery string             `json:"delivery"`
	Effort   string             `json:"effort,omitempty"`
	// DeltaMode is "phased" (default) or "live"; see SendMessageRequest.DeltaMode.
	DeltaMode     string          `json:"delta_mode,omitempty"`
	ClientContext json.RawMessage `json:"client_context,omitempty"`
}

type agentV1TurnResponse struct {
	SessionID            string           `json:"session_view_id"`
	MessageID            string           `json:"output_message_id"`
	StreamURL            string           `json:"stream_url"`
	EventTransport       string           `json:"event_transport"`
	InitialActivityState string           `json:"state"`
	TurnID               string           `json:"turn_id"`
	RunID                string           `json:"run_id"`
	Links                agentV1TurnLinks `json:"links"`
	DeltaMode            chat.DeltaMode   `json:"delta_mode"`
	Effort               string           `json:"effort"`
}

func (a *API) handleAgentV1Initialize(w http.ResponseWriter, r *http.Request) {
	a.jsonResp(w, http.StatusOK, agentV1InitializeResponse{
		DefaultDefinitionRef: a.Services.CognitiveViews.DefaultDefinitionRef, StreamEncodings: []string{"chatstream/v1"},
		SchemaVersion:       1,
		ProtocolVersion:     "v1",
		RoutePrefix:         agentV1RoutePrefix,
		App:                 agentV1App(),
		Operations:          agentV1Operations(),
		StreamTransports:    []string{"sse"},
		SupportedEventTypes: agentV1EventTypeValues(),
		PermissionRequests:  agentV1PermissionSupportInfo(),
		RouteHints:          agentV1Routes(),
		Unsupported: []string{
			"raw_pty_tui_sessions",
			"arbitrary_callback_execution",
			"runtime_kind_override_on_session_create",
		},
	})
}

func (a *API) handleAgentV1Capabilities(w http.ResponseWriter, r *http.Request) {
	a.jsonResp(w, http.StatusOK, agentV1CapabilitiesResponse{
		DefaultDefinitionRef: a.Services.CognitiveViews.DefaultDefinitionRef, StreamEncodings: []string{"chatstream/v1"},
		EventRetention: agentservice.EventRetention, EventGraceSeconds: int(agentservice.EventGrace.Seconds()), QueueLimit: service.CognitiveQueuedTurnLimit, SupportedPermissionProfiles: []string{"default", "read-only"},
		SchemaVersion:       1,
		ProtocolVersion:     "v1",
		RoutePrefix:         agentV1RoutePrefix,
		App:                 agentV1App(),
		Operations:          agentV1Operations(),
		StreamTransports:    []string{"sse"},
		RuntimeKinds:        []runtimeKindOption{{Value: "api", Label: "Native cognition", ProductSupported: true}},
		TurnStates:          agentV1SessionActivityOptions(),
		SupportedEventTypes: agentV1EventTypes(),
		SessionCreateFields: agentV1FieldSupport{Supported: []string{"definition_ref", "host_settings", "model_selection", "project_id", "title", "metadata"}, Unsupported: []string{"agent_id", "provider", "model", "runtime_kind", "work_root", "durable_agent_id", "boot_profile_id", "subagent_runtime"}},
		TurnSendFields: agentV1FieldSupport{
			Supported:   []string{"content", "delivery", "effort", "delta_mode", "client_context"},
			Unsupported: []string{},
		},
		TurnDelivery:       agentV1TurnDelivery{MidRun: "at_idle", Interrupt: agentV1RoutePrefix + "/sessions/{id}/turns/{turnId}/cancel", Notes: []string{"One executing turn and up to sixteen queued turns per view; overflow is refused before transcript mutation.", "Cancellation targets one turn. Observers resume independently; a disconnected observer does not cancel execution."}},
		PermissionRequests: agentV1PermissionSupportInfo(),
		RouteHints:         agentV1Routes(),
	})
}

func (a *API) handleAgentV1CreateSession(w http.ResponseWriter, r *http.Request) {
	var req agentV1CreateSessionRequest
	if err := a.decodeAgentV1(r, &req); err != nil {
		a.agentV1Error(w, 400, "invalid JSON: "+err.Error())
		return
	}
	if err := req.DefinitionRef.Validate(); err != nil {
		a.agentV1Error(w, 400, err.Error())
		return
	}
	if req.ProjectID != "" {
		if _, err := a.Services.Projects.WorkRoot(r.Context(), req.ProjectID); err != nil && !errors.Is(err, service.ErrProjectNoRepoPath) {
			status := 422
			if errors.Is(err, service.ErrProjectNotFound) {
				status = 404
			}
			a.agentV1Error(w, status, "project work root unavailable")
			return
		}
	}
	blob, err := json.Marshal(req.Metadata)
	if err != nil {
		a.agentV1Error(w, 400, err.Error())
		return
	}
	if req.Metadata == nil {
		blob = []byte("{}")
	}
	sess, err := a.Services.CognitiveViews.Create(r.Context(), service.CreateDefinedView{DefinitionRef: req.DefinitionRef, HostSettings: req.HostSettings, ModelSelection: req.ModelSelection, ProjectID: req.ProjectID, Title: req.Title, Metadata: string(blob)})
	if err != nil {
		status := 500
		switch {
		case errors.Is(err, service.ErrDefinitionDigestMismatch), errors.Is(err, store.ErrAgentHostRevisionConflict):
			status = 409
		case errors.Is(err, service.ErrDefinitionNotFound), errors.Is(err, sql.ErrNoRows):
			status = 404
		case errors.Is(err, service.ErrUnsupportedDefinition), errors.Is(err, service.ErrUnsupportedModel):
			status = 422
		}
		a.agentV1Error(w, status, err.Error())
		return
	}
	w.Header().Set("Location", agentV1RoutePrefix+"/sessions/"+url.PathEscape(sess.ID))
	// No post-commit lookup: the committed view is the create response.
	view := sessionToView(sess)
	ref := req.DefinitionRef
	a.jsonResp(w, 201, agentV1SessionResponse{SessionViewID: sess.ID, DefinitionRef: &ref, Session: &view, StreamTransport: "sse", RouteHints: agentV1SessionRoutesForSession(sess.ID)})
}
func (a *API) handleAgentV1GetSession(w http.ResponseWriter, r *http.Request) {
	sess, err := a.Services.Sessions.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		a.agentV1LookupError(w, err)
		return
	}
	view := sessionToView(sess)
	response := agentV1SessionResponse{SessionViewID: sess.ID, ParentSessionViewID: sess.ParentSessionID, Session: &view, StreamTransport: "sse", RouteHints: agentV1SessionRoutesForSession(sess.ID)}
	ref, _, err := a.Services.CognitiveViews.Get(r.Context(), sess.ID)
	if err == nil {
		response.DefinitionRef = &ref
	} else if !errors.Is(err, sql.ErrNoRows) {
		a.agentV1Error(w, 500, "view definition unavailable")
		return
	}
	// Retained profile views have no imported definition pin. Null is truthful;
	// they remain local resources and are not silently converted to agentdef v2.
	if id, err := a.Services.CognitiveViews.LatestTurn(r.Context(), sess.ID); err == nil {
		response.CurrentTurnID = &id
	} else if !errors.Is(err, sql.ErrNoRows) {
		a.agentV1Error(w, 500, "view turn unavailable")
		return
	}
	a.jsonResp(w, 200, response)
}

func (a *API) handleAgentV1SendTurn(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if _, err := a.Services.Sessions.Get(r.Context(), sessionID); err != nil {
		a.agentV1LookupError(w, err)
		return
	}

	var req agentV1TurnRequest
	if err := a.decodeAgentV1(r, &req); err != nil {
		a.agentV1Error(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	var text strings.Builder
	for _, part := range req.Content {
		if part.Kind != "text" {
			a.agentV1Error(w, http.StatusUnprocessableEntity, "only text content parts are supported")
			return
		}
		text.WriteString(part.Text)
	}
	if strings.TrimSpace(text.String()) == "" {
		a.agentV1Error(w, http.StatusBadRequest, "content is required")
		return
	}

	if req.Delivery != "at_idle" {
		a.agentV1Error(w, http.StatusUnprocessableEntity, "delivery must be at_idle")
		return
	}
	turnEffort := effort.Parse(req.Effort)
	if !turnEffort.IsValid() {
		a.agentV1Error(w, http.StatusBadRequest, "unsupported effort")
		return
	}
	ctx := effort.WithContext(r.Context(), turnEffort)
	deltaMode, err := chat.ParseDeltaMode(req.DeltaMode)
	if err != nil {
		a.agentV1Error(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx = chat.WithDeltaMode(ctx, deltaMode)
	if len(req.ClientContext) > 0 {
		snapshot, validationErr := clientcontext.Decode(req.ClientContext)
		if validationErr != nil {
			a.agentV1Error(w, http.StatusBadRequest, validationErr.Error())
			return
		}
		ctx = service.WithClientContextSnapshot(ctx, snapshot)
	}
	admission, ok := a.Services.Chat.(service.CognitiveTurnAdmission)
	if !ok {
		a.agentV1Error(w, http.StatusServiceUnavailable, "bounded native turn admission unavailable")
		return
	}
	msgID, err := admission.SubmitCognitiveTurn(ctx, sessionID, text.String())
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, service.ErrUnsupportedModel) {
			status = 422
		}
		if errors.Is(err, service.ErrCognitiveQueueFull) {
			status = http.StatusTooManyRequests
		}
		if errors.Is(err, service.ErrCognitiveAdmissionClosed) {
			status = http.StatusServiceUnavailable
		}
		a.agentV1Error(w, status, err.Error())
		return
	}
	links := agentV1TurnRoutes(sessionID, msgID)
	a.jsonResp(w, http.StatusAccepted, agentV1TurnResponse{
		SessionID: sessionID,
		MessageID: msgID,
		StreamURL: links.Events,
		TurnID:    msgID, RunID: msgID, Links: links, DeltaMode: deltaMode, Effort: turnEffort.String(),
		EventTransport:       "sse",
		InitialActivityState: "submitted",
	})
}

func agentV1App() agentV1AppInfo {
	return agentV1AppInfo{
		ID:      brand.ID,
		Name:    brand.Name,
		Version: version.Full(),
	}
}

func agentV1Operations() []string {
	return []string{
		"initialize",
		"capabilities",
		"sessions.create",
		"sessions.get",
		"sessions.list",
		"history.read",
		"sessions.turns.create",
		"sessions.turns.cancel",
		"sessions.turns.get",
		"sessions.turns.snapshot",
		"sessions.turns.events.stream",
		"sessions.approvals.respond",
	}
}

func agentV1Routes() agentV1RouteHints {
	return agentV1RouteHints{DefinitionCatalog: "/api/agent-definitions", DefinitionAuthor: "/api/agent-definitions/author", HostSettings: "/api/agent-host-settings", Capabilities: agentV1RoutePrefix + "/capabilities", Sessions: agentV1RoutePrefix + "/sessions", TurnStatus: agentV1RoutePrefix + "/sessions/{id}/turns/{turnId}", TurnEvents: agentV1RoutePrefix + "/sessions/{id}/turns/{turnId}/events", TurnCancel: agentV1RoutePrefix + "/sessions/{id}/turns/{turnId}/cancel", SessionApprovals: agentV1RoutePrefix + "/sessions/{id}/approvals/{requestId}/responses"}
}
func agentV1PermissionSupportInfo() agentV1PermissionSupport {
	return agentV1PermissionSupport{SupportLevel: "inband_once", ApprovalResponseRoute: agentV1RoutePrefix + "/sessions/{id}/approvals/{requestId}/responses", Notes: []string{"approval.request binds approval_id to run_id/call_id, includes its expiry and supports only once scope.", "Identical recorded responses return the same outcome; conflicts, expired and unanswered closed prompts return 409."}}
}
func agentV1SessionActivityOptions() []enumOption {
	return []enumOption{{Value: "submitted", Label: "Submitted"}, {Value: "working", Label: "Working"}, {Value: "input_required", Label: "Input required"}, {Value: "completed", Label: "Completed"}, {Value: "failed", Label: "Failed"}, {Value: "canceled", Label: "Canceled"}}
}
func agentV1EventTypes() []enumOption {
	var out []enumOption
	for _, verb := range []string{"run.start", "run.finish", "run.error", "run.abort", "message.start", "message.end", "part.start", "part.delta", "part.end", "approval.request", "activity", "gap"} {
		out = append(out, enumOption{Value: verb, Label: verb})
	}
	return out
}
func agentV1EventTypeValues() []string {
	var out []string
	for _, option := range agentV1EventTypes() {
		out = append(out, option.Value)
	}
	return out
}
func agentV1SessionRoutesForSession(id string) agentV1SessionRoutes {
	base := agentV1RoutePrefix + "/sessions/" + url.PathEscape(id)
	return agentV1SessionRoutes{Self: base, History: base + "/messages", Turns: base + "/turns", Approvals: base + "/approvals/{requestId}/responses"}
}

// decodeAgentV1 rejects removed fields and trailing values rather than
// silently falling back to a different operation.
func (a *API) decodeAgentV1(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected one JSON value")
	}
	return nil
}

func (a *API) agentV1LookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		a.agentV1Error(w, http.StatusNotFound, "view not found")
		return
	}
	a.agentV1Error(w, http.StatusInternalServerError, "view lookup failed")
}
func (a *API) agentV1Error(w http.ResponseWriter, status int, message string) {
	code := "invalid_request"
	switch status {
	case http.StatusUnauthorized:
		code = "unauthenticated"
	case http.StatusForbidden:
		code = "denied"
	case http.StatusNotFound:
		code = "not_found"
	case http.StatusConflict:
		code = "invalid_state"
	case http.StatusUnprocessableEntity:
		code = "unsupported_selection"
	case http.StatusTooManyRequests:
		code = "queue_full"
	case http.StatusServiceUnavailable:
		code = "unavailable"
	case http.StatusInternalServerError:
		code = "internal_error"
	}
	if status == http.StatusInternalServerError {
		message = "internal operation failed"
	}
	w.Header().Set("Cache-Control", "private, no-store")
	a.jsonResp(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": status == http.StatusServiceUnavailable || status == http.StatusTooManyRequests, "request_id": uuid.NewString()}})
}

func (a *API) agentV1Handler(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		handler(w, r)
	}
}
