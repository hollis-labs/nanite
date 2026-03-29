package plugin

import (
	"context"
	"errors"
	"time"

	"github.com/hollis-labs/fragments-engine/plugin"
)

// Conduit Event Catalog
// These are the standard events that Conduit emits for plugins to listen to.
const (
	// Session Events
	EventSessionStart = "session.start"
	EventSessionEnd   = "session.end"

	// Agent Events
	EventAgentSwitched = "agent.switched"
	EventAgentLoaded   = "agent.loaded"

	// Message Events
	EventMessageSent     = "message.sent"
	EventMessageReceived = "message.received"
	EventMessageDeleted  = "message.deleted"

	// Mode Events
	EventModeChanged = "mode.changed"
	EventScopeChanged = "scope.changed"

	// Tool Events
	EventToolCalled   = "tool.called"
	EventToolFailed   = "tool.failed"
	EventToolComplete = "tool.complete"

	// UI Events
	EventEnvelopeRendered = "envelope.rendered"
	EventWidgetLoaded     = "widget.loaded"
	EventActionTriggered  = "action.triggered"

	// Workflow Events
	EventWorkflowStarted  = "workflow.started"
	EventWorkflowComplete = "workflow.complete"
	EventWorkflowFailed   = "workflow.failed"

	// Config Events
	EventConfigChanged = "config.changed"

	// Plugin Lifecycle Events
	EventPluginInstalled   = "plugin.installed"
	EventPluginUninstalled = "plugin.uninstalled"

	// Session Lifecycle Events
	EventSessionArchived = "session.archived"

	// Provider Events
	EventProviderError    = "provider.error"
	EventProviderFallback = "provider.fallback"

	// Pre-hook Events (can signal cancellation via "cancel" key in event data)
	EventMessageSending = "message.sending" // before message is sent to LLM
	EventToolExecuting  = "tool.executing"  // before tool is executed
)

// EventData provides structured data for common event types
type EventData struct {
	// Common fields
	SessionID string `json:"session_id,omitempty"`
	UserID    string `json:"user_id,omitempty"`

	// Session events
	AgentID string `json:"agent_id,omitempty"`
	Mode    string `json:"mode,omitempty"`

	// Agent events
	PreviousAgentID string `json:"previous_agent_id,omitempty"`
	AgentName       string `json:"agent_name,omitempty"`
	AgentVersion    string `json:"agent_version,omitempty"`

	// Message events
	MessageID      string      `json:"message_id,omitempty"`
	Content        string      `json:"content,omitempty"`
	Role           string      `json:"role,omitempty"`
	TokensUsed     int         `json:"tokens_used,omitempty"`
	ResponseTime   int64       `json:"response_time_ms,omitempty"`
	EnvelopeType   string      `json:"envelope_type,omitempty"`
	EnvelopeData   interface{} `json:"envelope_data,omitempty"`

	// Mode events
	PreviousMode  string `json:"previous_mode,omitempty"`
	PreviousScope string `json:"previous_scope,omitempty"`
	NewMode       string `json:"new_mode,omitempty"`
	NewScope      string `json:"new_scope,omitempty"`

	// Tool events
	ToolName   string      `json:"tool_name,omitempty"`
	ToolArgs   interface{} `json:"tool_args,omitempty"`
	ToolResult interface{} `json:"tool_result,omitempty"`
	Error      string      `json:"error,omitempty"`

	// UI events
	ComponentID   string      `json:"component_id,omitempty"`
	ComponentType string      `json:"component_type,omitempty"`
	WidgetSlot    string      `json:"widget_slot,omitempty"`
	ActionID      string      `json:"action_id,omitempty"`
	ActionData    interface{} `json:"action_data,omitempty"`

	// Workflow events
	WorkflowName string      `json:"workflow_name,omitempty"`
	WorkflowData interface{} `json:"workflow_data,omitempty"`
}

// NewEvent creates a new plugin event with Conduit-specific data
func NewEvent(eventType, source string, data EventData) plugin.Event {
	// Convert EventData to map[string]interface{} for the plugin.Event
	eventMap := make(map[string]interface{})

	if data.SessionID != "" {
		eventMap["session_id"] = data.SessionID
	}
	if data.UserID != "" {
		eventMap["user_id"] = data.UserID
	}
	if data.AgentID != "" {
		eventMap["agent_id"] = data.AgentID
	}
	if data.Mode != "" {
		eventMap["mode"] = data.Mode
	}
	if data.PreviousAgentID != "" {
		eventMap["previous_agent_id"] = data.PreviousAgentID
	}
	if data.AgentName != "" {
		eventMap["agent_name"] = data.AgentName
	}
	if data.AgentVersion != "" {
		eventMap["agent_version"] = data.AgentVersion
	}
	if data.MessageID != "" {
		eventMap["message_id"] = data.MessageID
	}
	if data.Content != "" {
		eventMap["content"] = data.Content
	}
	if data.Role != "" {
		eventMap["role"] = data.Role
	}
	if data.TokensUsed > 0 {
		eventMap["tokens_used"] = data.TokensUsed
	}
	if data.ResponseTime > 0 {
		eventMap["response_time_ms"] = data.ResponseTime
	}
	if data.EnvelopeType != "" {
		eventMap["envelope_type"] = data.EnvelopeType
	}
	if data.EnvelopeData != nil {
		eventMap["envelope_data"] = data.EnvelopeData
	}
	if data.PreviousMode != "" {
		eventMap["previous_mode"] = data.PreviousMode
	}
	if data.PreviousScope != "" {
		eventMap["previous_scope"] = data.PreviousScope
	}
	if data.NewMode != "" {
		eventMap["new_mode"] = data.NewMode
	}
	if data.NewScope != "" {
		eventMap["new_scope"] = data.NewScope
	}
	if data.ToolName != "" {
		eventMap["tool_name"] = data.ToolName
	}
	if data.ToolArgs != nil {
		eventMap["tool_args"] = data.ToolArgs
	}
	if data.ToolResult != nil {
		eventMap["tool_result"] = data.ToolResult
	}
	if data.Error != "" {
		eventMap["error"] = data.Error
	}
	if data.ComponentID != "" {
		eventMap["component_id"] = data.ComponentID
	}
	if data.ComponentType != "" {
		eventMap["component_type"] = data.ComponentType
	}
	if data.WidgetSlot != "" {
		eventMap["widget_slot"] = data.WidgetSlot
	}
	if data.ActionID != "" {
		eventMap["action_id"] = data.ActionID
	}
	if data.ActionData != nil {
		eventMap["action_data"] = data.ActionData
	}
	if data.WorkflowName != "" {
		eventMap["workflow_name"] = data.WorkflowName
	}
	if data.WorkflowData != nil {
		eventMap["workflow_data"] = data.WorkflowData
	}

	return plugin.Event{
		Type:      eventType,
		Source:    source,
		Timestamp: time.Now(),
		Data:      eventMap,
		SessionID: data.SessionID,
	}
}

// EmitSessionStart emits a session.start event
func (h *Host) EmitSessionStart(sessionID, agentID, mode string) {
	event := NewEvent(EventSessionStart, "conduit", EventData{
		SessionID: sessionID,
		AgentID:   agentID,
		Mode:      mode,
	})
	h.EmitEvent(event)
}

// EmitSessionEnd emits a session.end event
func (h *Host) EmitSessionEnd(sessionID string) {
	event := NewEvent(EventSessionEnd, "conduit", EventData{
		SessionID: sessionID,
	})
	h.EmitEvent(event)
}

// EmitAgentSwitched emits an agent.switched event
func (h *Host) EmitAgentSwitched(sessionID, previousAgentID, newAgentID string) {
	event := NewEvent(EventAgentSwitched, "conduit", EventData{
		SessionID:       sessionID,
		AgentID:         newAgentID,
		PreviousAgentID: previousAgentID,
	})
	h.EmitEvent(event)
}

// EmitMessageSent emits a message.sent event
func (h *Host) EmitMessageSent(sessionID, messageID, content, role string, tokensUsed int) {
	event := NewEvent(EventMessageSent, "conduit", EventData{
		SessionID:  sessionID,
		MessageID:  messageID,
		Content:    content,
		Role:       role,
		TokensUsed: tokensUsed,
	})
	h.EmitEvent(event)
}

// EmitMessageReceived emits a message.received event
func (h *Host) EmitMessageReceived(sessionID, messageID, content string, responseTime int64) {
	event := NewEvent(EventMessageReceived, "conduit", EventData{
		SessionID:    sessionID,
		MessageID:    messageID,
		Content:      content,
		Role:         "assistant",
		ResponseTime: responseTime,
	})
	h.EmitEvent(event)
}

// EmitModeChanged emits a mode.changed event
func (h *Host) EmitModeChanged(sessionID, previousMode, newMode string) {
	event := NewEvent(EventModeChanged, "conduit", EventData{
		SessionID:    sessionID,
		PreviousMode: previousMode,
		NewMode:      newMode,
	})
	h.EmitEvent(event)
}

// EmitToolCalled emits a tool.called event
func (h *Host) EmitToolCalled(sessionID, toolName string, args, result interface{}) {
	event := NewEvent(EventToolCalled, "conduit", EventData{
		SessionID:  sessionID,
		ToolName:   toolName,
		ToolArgs:   args,
		ToolResult: result,
	})
	h.EmitEvent(event)
}

// EmitToolFailed emits a tool.failed event
func (h *Host) EmitToolFailed(sessionID, toolName string, args interface{}, err string) {
	event := NewEvent(EventToolFailed, "conduit", EventData{
		SessionID: sessionID,
		ToolName:  toolName,
		ToolArgs:  args,
		Error:     err,
	})
	h.EmitEvent(event)
}

// EmitEnvelopeRendered emits an envelope.rendered event
func (h *Host) EmitEnvelopeRendered(sessionID, envelopeType string, data interface{}) {
	event := NewEvent(EventEnvelopeRendered, "conduit", EventData{
		SessionID:    sessionID,
		EnvelopeType: envelopeType,
		EnvelopeData: data,
	})
	h.EmitEvent(event)
}

// EmitActionTriggered emits an action.triggered event
func (h *Host) EmitActionTriggered(sessionID, actionID string, actionData interface{}) {
	event := NewEvent(EventActionTriggered, "conduit", EventData{
		SessionID:  sessionID,
		ActionID:   actionID,
		ActionData: actionData,
	})
	h.EmitEvent(event)
}

// EmitConfigChanged emits a config.changed event when plugin or user settings change.
func (h *Host) EmitConfigChanged(pluginID, key, value string) {
	event := NewEvent(EventConfigChanged, "conduit", EventData{})
	event.Data["plugin_id"] = pluginID
	event.Data["key"] = key
	event.Data["value"] = value
	h.EmitEvent(event)
}

// EmitPluginInstalled emits a plugin.installed event.
func (h *Host) EmitPluginInstalled(pluginID, pluginName, version string) {
	event := NewEvent(EventPluginInstalled, "conduit", EventData{})
	event.Data["plugin_id"] = pluginID
	event.Data["plugin_name"] = pluginName
	event.Data["version"] = version
	h.EmitEvent(event)
}

// EmitPluginUninstalled emits a plugin.uninstalled event.
func (h *Host) EmitPluginUninstalled(pluginID string) {
	event := NewEvent(EventPluginUninstalled, "conduit", EventData{})
	event.Data["plugin_id"] = pluginID
	h.EmitEvent(event)
}

// EmitSessionArchived emits a session.archived event.
func (h *Host) EmitSessionArchived(sessionID string) {
	event := NewEvent(EventSessionArchived, "conduit", EventData{
		SessionID: sessionID,
	})
	h.EmitEvent(event)
}

// EmitProviderError emits a provider.error event when an LLM call fails.
func (h *Host) EmitProviderError(sessionID, providerName, model, errMsg string) {
	event := NewEvent(EventProviderError, "conduit", EventData{
		SessionID: sessionID,
		Error:     errMsg,
	})
	event.Data["provider"] = providerName
	event.Data["model"] = model
	h.EmitEvent(event)
}

// EmitProviderFallback emits a provider.fallback event when the engine falls through the chain.
func (h *Host) EmitProviderFallback(sessionID, fromProvider, toProvider string) {
	event := NewEvent(EventProviderFallback, "conduit", EventData{
		SessionID: sessionID,
	})
	event.Data["from_provider"] = fromProvider
	event.Data["to_provider"] = toProvider
	h.EmitEvent(event)
}

// EmitPreHook emits a pre-hook event and returns true if any hook signaled cancellation.
// Pre-hooks can set event.Data["cancel"] = true to prevent the action from proceeding.
func (h *Host) EmitPreHook(eventType, sessionID string, data map[string]interface{}) bool {
	if data == nil {
		data = make(map[string]interface{})
	}
	data["session_id"] = sessionID

	event := plugin.Event{
		Type:      eventType,
		Source:    "conduit",
		Timestamp: time.Now(),
		Data:      data,
		SessionID: sessionID,
	}

	h.mu.RLock()
	hooks, exists := h.eventHooks[eventType]
	h.mu.RUnlock()

	if !exists {
		return false
	}

	for _, hook := range hooks {
		ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
		err := hook.Handle(ctx, event)
		cancel()
		if err != nil {
			if errors.Is(err, plugin.ErrCancelled) {
				h.logger.Info("pre-hook cancelled action", "eventType", eventType)
				return true
			}
			h.logger.Error("pre-hook failed", "eventType", eventType, "error", err)
		}
	}

	// Legacy: check map-based cancel flag for backward compatibility.
	if cancelled, ok := event.Data["cancel"].(bool); ok && cancelled {
		return true
	}
	return false
}