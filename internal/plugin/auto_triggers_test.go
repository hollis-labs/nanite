package plugin

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestAutoTriggerHandler_FiresOnMatchingEvent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	h := NewHost(http.NewServeMux(), NewLogger("test"))
	h.SetStore(s)

	// Create a custom action with on_new_session trigger.
	action := &store.CustomAction{
		Name:         "greet",
		Command:      "Welcome!",
		AutoTriggers: `["on_new_session"]`,
		Enabled:      true,
	}
	if err := s.CreateCustomAction(action); err != nil {
		t.Fatalf("create action: %v", err)
	}

	// Register a hook to capture the action.triggered event.
	hook := &TestEventHook{eventTypes: []string{EventActionTriggered}}
	h.RegisterEventHook([]string{EventActionTriggered}, hook)

	// Register auto-trigger handler.
	RegisterAutoTriggerHandler(h)

	// Emit a session.start event.
	h.EmitSessionStart("test-session", "agent-1", "chat")

	// Give async hooks time to process.
	time.Sleep(100 * time.Millisecond)

	if hook.callCount == 0 {
		t.Error("expected action.triggered event to fire")
	}
	if hook.callCount > 0 {
		data := hook.lastEvent.Data
		actionData, ok := data["action_data"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected action_data map, got %T", data["action_data"])
		}
		if actionData["action_name"] != "greet" {
			t.Errorf("expected action_name=greet, got %v", actionData["action_name"])
		}
		if actionData["command"] != "Welcome!" {
			t.Errorf("expected command=Welcome!, got %v", actionData["command"])
		}
		if actionData["trigger"] != "on_new_session" {
			t.Errorf("expected trigger=on_new_session, got %v", actionData["trigger"])
		}
	}
}

func TestAutoTriggerHandler_IgnoresDisabledActions(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	h := NewHost(http.NewServeMux(), NewLogger("test"))
	h.SetStore(s)

	// Create a disabled action.
	action := &store.CustomAction{
		Name:         "disabled",
		Command:      "Should not fire",
		AutoTriggers: `["on_new_session"]`,
		Enabled:      false,
	}
	if err := s.CreateCustomAction(action); err != nil {
		t.Fatalf("create action: %v", err)
	}

	hook := &TestEventHook{eventTypes: []string{EventActionTriggered}}
	h.RegisterEventHook([]string{EventActionTriggered}, hook)
	RegisterAutoTriggerHandler(h)

	h.EmitSessionStart("test-session", "agent-1", "chat")
	time.Sleep(100 * time.Millisecond)

	if hook.callCount != 0 {
		t.Error("disabled action should not fire")
	}
}

func TestAutoTriggerHandler_IgnoresUnrelatedEvents(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	h := NewHost(http.NewServeMux(), NewLogger("test"))
	h.SetStore(s)

	// Action triggers on on_agent_switch only.
	action := &store.CustomAction{
		Name:         "agent-only",
		Command:      "Agent action",
		AutoTriggers: `["on_agent_switch"]`,
		Enabled:      true,
	}
	if err := s.CreateCustomAction(action); err != nil {
		t.Fatalf("create action: %v", err)
	}

	hook := &TestEventHook{eventTypes: []string{EventActionTriggered}}
	h.RegisterEventHook([]string{EventActionTriggered}, hook)
	RegisterAutoTriggerHandler(h)

	// Emit session.start — should NOT match on_agent_switch.
	h.EmitSessionStart("test-session", "agent-1", "chat")
	time.Sleep(100 * time.Millisecond)

	if hook.callCount != 0 {
		t.Error("action should not fire for unrelated event")
	}
}
