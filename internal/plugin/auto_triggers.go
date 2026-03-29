package plugin

import (
	"context"
	"encoding/json"

	pluginsdk "github.com/hollis-labs/fragments-engine/plugin"
)

// AutoTriggerHandler maps plugin events to custom action auto-triggers.
// When a matching event fires, it looks up custom actions with that trigger
// and emits an "action.triggered" event with the command to execute.
type AutoTriggerHandler struct {
	host   *Host
	logger pluginsdk.Logger
}

// eventToTrigger maps event types to auto-trigger names.
var eventToTrigger = map[string]string{
	EventSessionStart:  "on_new_session",
	EventAgentSwitched: "on_agent_switch",
	EventModeChanged:   "on_mode_change",
}

// RegisterAutoTriggerHandler creates and registers an event hook that
// dispatches custom action auto-triggers.
func RegisterAutoTriggerHandler(host *Host) {
	handler := &AutoTriggerHandler{
		host:   host,
		logger: host.logger.With("component", "auto-trigger"),
	}

	// Listen to all events that can fire auto-triggers.
	eventTypes := make([]string, 0, len(eventToTrigger))
	for eventType := range eventToTrigger {
		eventTypes = append(eventTypes, eventType)
	}

	host.RegisterEventHook(eventTypes, handler)
	host.logger.Info("registered auto-trigger handler", "eventTypes", eventTypes)
}

// Handle processes an event and dispatches matching custom action auto-triggers.
func (h *AutoTriggerHandler) Handle(ctx context.Context, event pluginsdk.Event) error {
	triggerName, ok := eventToTrigger[event.Type]
	if !ok {
		return nil
	}

	if h.host.store == nil {
		return nil
	}

	actions, err := h.host.store.ListCustomActionsByTrigger(triggerName)
	if err != nil {
		h.logger.Error("failed to load auto-trigger actions",
			"trigger", triggerName, "error", err)
		return nil
	}

	sessionID := event.SessionID
	for _, action := range actions {
		h.logger.Info("auto-trigger firing",
			"action", action.Name, "trigger", triggerName, "session", sessionID)

		// Parse the auto_triggers field to verify it's valid JSON.
		var triggers []string
		if err := json.Unmarshal([]byte(action.AutoTriggers), &triggers); err != nil {
			h.logger.Warn("invalid auto_triggers JSON", "action", action.Name, "error", err)
			continue
		}

		// Emit an action.triggered event so the chat engine or frontend can
		// pick it up and inject the command into the session.
		h.host.EmitActionTriggered(sessionID, action.ID, map[string]interface{}{
			"action_name": action.Name,
			"command":     action.Command,
			"trigger":     triggerName,
			"auto":        true,
		})
	}

	return nil
}

// EventTypes returns the event types this hook handles.
func (h *AutoTriggerHandler) EventTypes() []string {
	types := make([]string, 0, len(eventToTrigger))
	for t := range eventToTrigger {
		types = append(types, t)
	}
	return types
}
