package plugin

import (
	"context"
	"net/http"
	"testing"

	"github.com/hollis-labs/plugin-sdk"
)

// cancelingHook returns plugin.ErrCancelled to signal cancellation.
type cancelingHook struct {
	eventTypes []string
	called     bool
}

func (h *cancelingHook) Handle(ctx context.Context, event plugin.Event) error {
	h.called = true
	return plugin.ErrCancelled
}

func (h *cancelingHook) EventTypes() []string { return h.eventTypes }
func (h *cancelingHook) PluginID() string     { return "test-plugin" }

// passingHook records the call but does not cancel.
type passingHook struct {
	eventTypes []string
	called     bool
}

func (h *passingHook) Handle(ctx context.Context, event plugin.Event) error {
	h.called = true
	return nil
}

func (h *passingHook) EventTypes() []string { return h.eventTypes }
func (h *passingHook) PluginID() string     { return "test-plugin" }

func TestEmitPreHook_NoCancel(t *testing.T) {
	host := NewHost(http.NewServeMux(), NewLogger("test"))

	hook := &passingHook{eventTypes: []string{EventMessageSending}}
	if err := host.RegisterEventHook([]string{EventMessageSending}, hook); err != nil {
		t.Fatalf("RegisterEventHook: %v", err)
	}

	canceled := host.EmitPreHook(EventMessageSending, "s1", map[string]any{"agent_id": "a1"})
	if canceled {
		t.Error("expected EmitPreHook to return false when no hook canceled")
	}
	if !hook.called {
		t.Error("expected hook to have been called")
	}
}

func TestEmitPreHook_CancelViaError(t *testing.T) {
	host := NewHost(http.NewServeMux(), NewLogger("test"))

	hook := &cancelingHook{eventTypes: []string{EventToolExecuting}}
	if err := host.RegisterEventHook([]string{EventToolExecuting}, hook); err != nil {
		t.Fatalf("RegisterEventHook: %v", err)
	}

	canceled := host.EmitPreHook(EventToolExecuting, "s1", map[string]any{"tool_name": "dev_bash"})
	if !canceled {
		t.Error("expected EmitPreHook to return true when hook returned plugin.ErrCancelled")
	}
	if !hook.called {
		t.Error("expected hook to have been called")
	}
}

func TestEmitPreHook_NoHooksRegistered(t *testing.T) {
	host := NewHost(http.NewServeMux(), NewLogger("test"))

	canceled := host.EmitPreHook(EventMessageSending, "s1", nil)
	if canceled {
		t.Error("expected EmitPreHook to return false with no hooks registered")
	}
}

func TestEmitPreHook_CancelViaMapFlag(t *testing.T) {
	// Legacy path: hooks that mutate event.Data["cancel"] = true.
	// This requires a hook that can mutate the shared event data map.
	host := NewHost(http.NewServeMux(), NewLogger("test"))

	hook := &mutatingHook{eventTypes: []string{EventMessageSending}}
	if err := host.RegisterEventHook([]string{EventMessageSending}, hook); err != nil {
		t.Fatalf("RegisterEventHook: %v", err)
	}

	data := map[string]any{"agent_id": "a1"}
	canceled := host.EmitPreHook(EventMessageSending, "s1", data)
	if !canceled {
		t.Error("expected EmitPreHook to return true when hook set cancel flag")
	}
}

// mutatingHook sets the legacy "cancel" key on event.Data.
type mutatingHook struct {
	eventTypes []string
}

func (h *mutatingHook) Handle(ctx context.Context, event plugin.Event) error {
	event.Data["cancel"] = true
	return nil
}

func (h *mutatingHook) EventTypes() []string { return h.eventTypes }
func (h *mutatingHook) PluginID() string     { return "test-plugin" }

func TestNewEventConstantsDefined(t *testing.T) {
	// Ensure new event constants added for Task 1 are non-empty.
	newEvents := map[string]string{
		"EventShellExec":        EventShellExec,
		"EventShellError":       EventShellError,
		"EventShellBlocked":     EventShellBlocked,
		"EventContextCompacted": EventContextCompacted,
		"EventContextAssembled": EventContextAssembled,
		"EventArtifactCreated":  EventArtifactCreated,
		"EventArtifactDeleted":  EventArtifactDeleted,
	}
	for name, value := range newEvents {
		if value == "" {
			t.Errorf("%s constant is empty", name)
		}
	}
}

func TestNewEmitters_FireEventHook(t *testing.T) {
	// Smoke test: each new emitter method dispatches to the registered hook
	// with the correct event type.
	host := NewHost(http.NewServeMux(), NewLogger("test"))

	type emitCase struct {
		name      string
		eventType string
		emit      func()
	}
	cases := []emitCase{
		{"AgentLoaded", EventAgentLoaded, func() { host.EmitAgentLoaded("s", "a", "AgentName", "1") }},
		{"MessageDeleted", EventMessageDeleted, func() { host.EmitMessageDeleted("s", "m") }},
		{"ScopeChanged", EventScopeChanged, func() { host.EmitScopeChanged("s", "old", "new") }},
		{"WidgetLoaded", EventWidgetLoaded, func() { host.EmitWidgetLoaded("id", "widget", "slot") }},
		{"WorkflowStarted", EventWorkflowStarted, func() { host.EmitWorkflowStarted("s", "wf", nil) }},
		{"WorkflowComplete", EventWorkflowComplete, func() { host.EmitWorkflowComplete("s", "wf", nil) }},
		{"WorkflowFailed", EventWorkflowFailed, func() { host.EmitWorkflowFailed("s", "wf", "err") }},
		{"ContextAssembled", EventContextAssembled, func() { host.EmitContextAssembled("s", 100, 5, 3) }},
		{"ContextCompacted", EventContextCompacted, func() { host.EmitContextCompacted("s", 500, []string{"stage1"}) }},
		{"ArtifactCreated", EventArtifactCreated, func() { host.EmitArtifactCreated("s", "a1", "text/plain", "auto") }},
		{"ArtifactDeleted", EventArtifactDeleted, func() { host.EmitArtifactDeleted("s", "a1") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hook := &TestEventHook{eventTypes: []string{tc.eventType}}
			if err := host.RegisterEventHook([]string{tc.eventType}, hook); err != nil {
				t.Fatalf("RegisterEventHook: %v", err)
			}
			tc.emit()
			if hook.callCount != 1 {
				t.Errorf("expected hook for %s to fire once, got %d", tc.eventType, hook.callCount)
			}
			if hook.lastEvent.Type != tc.eventType {
				t.Errorf("expected event type %s, got %s", tc.eventType, hook.lastEvent.Type)
			}
		})
	}
}
