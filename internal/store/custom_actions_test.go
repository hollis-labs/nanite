package store

import (
	"testing"
)

func TestCreateCustomAction(t *testing.T) {
	s := newTestStore(t)

	action := &CustomAction{
		Name:        "deploy-prod",
		Description: "Deploy to production",
		Keybinding:  "mod+shift+d",
		Command:     "/deploy production",
		Enabled:     true,
	}

	if err := s.CreateCustomAction(action); err != nil {
		t.Fatalf("CreateCustomAction: %v", err)
	}
	if action.ID == "" {
		t.Error("expected ID to be generated")
	}
	if action.CreatedAt == "" {
		t.Error("expected CreatedAt to be set")
	}
}

func TestGetCustomAction(t *testing.T) {
	s := newTestStore(t)

	action := &CustomAction{
		Name:         "test-action",
		Command:      "echo hello",
		SlashCommand: "hello",
		AutoTriggers: `["on_new_session"]`,
		Enabled:      true,
	}
	if err := s.CreateCustomAction(action); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.GetCustomAction(action.ID)
	if err != nil {
		t.Fatalf("GetCustomAction: %v", err)
	}
	if got.Name != "test-action" {
		t.Errorf("expected name test-action, got %s", got.Name)
	}
	if got.SlashCommand != "hello" {
		t.Errorf("expected slash_command hello, got %s", got.SlashCommand)
	}
	if got.AutoTriggers != `["on_new_session"]` {
		t.Errorf("expected auto_triggers [\"on_new_session\"], got %s", got.AutoTriggers)
	}
}

func TestGetCustomActionNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetCustomAction("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent action")
	}
}

func TestUpdateCustomAction(t *testing.T) {
	s := newTestStore(t)

	action := &CustomAction{
		Name:    "updatable",
		Command: "old command",
		Enabled: true,
	}
	if err := s.CreateCustomAction(action); err != nil {
		t.Fatalf("create: %v", err)
	}

	action.Command = "new command"
	action.Enabled = false
	action.Keybinding = "mod+u"
	if err := s.UpdateCustomAction(action); err != nil {
		t.Fatalf("UpdateCustomAction: %v", err)
	}

	got, _ := s.GetCustomAction(action.ID)
	if got.Command != "new command" {
		t.Errorf("expected new command, got %s", got.Command)
	}
	if got.Enabled {
		t.Error("expected disabled after update")
	}
	if got.Keybinding != "mod+u" {
		t.Errorf("expected keybinding mod+u, got %s", got.Keybinding)
	}
}

func TestUpdateCustomActionNotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.UpdateCustomAction(&CustomAction{ID: "nope", Name: "x", Command: "y"})
	if err == nil {
		t.Error("expected error for nonexistent action")
	}
}

func TestDeleteCustomAction(t *testing.T) {
	s := newTestStore(t)

	action := &CustomAction{Name: "doomed", Command: "x", Enabled: true}
	if err := s.CreateCustomAction(action); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := s.DeleteCustomAction(action.ID); err != nil {
		t.Fatalf("DeleteCustomAction: %v", err)
	}

	_, err := s.GetCustomAction(action.ID)
	if err == nil {
		t.Error("expected error after deletion")
	}
}

func TestDeleteCustomActionNotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.DeleteCustomAction("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent action")
	}
}

func TestListCustomActions(t *testing.T) {
	s := newTestStore(t)

	for _, name := range []string{"beta", "alpha", "gamma"} {
		a := &CustomAction{Name: name, Command: "x", Enabled: true}
		if err := s.CreateCustomAction(a); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	actions, err := s.ListCustomActions()
	if err != nil {
		t.Fatalf("ListCustomActions: %v", err)
	}
	if len(actions) != 3 {
		t.Errorf("expected 3 actions, got %d", len(actions))
	}
	// Sorted by name ASC.
	if actions[0].Name != "alpha" {
		t.Errorf("expected first action alpha, got %s", actions[0].Name)
	}
}

func TestListCustomActionsByTrigger(t *testing.T) {
	s := newTestStore(t)

	// Action with on_new_session trigger.
	a1 := &CustomAction{
		Name:         "greet",
		Command:      "Hello!",
		AutoTriggers: `["on_new_session"]`,
		Enabled:      true,
	}
	// Action with on_agent_switch trigger.
	a2 := &CustomAction{
		Name:         "agent-note",
		Command:      "Agent switched",
		AutoTriggers: `["on_agent_switch"]`,
		Enabled:      true,
	}
	// Action with on_new_session trigger but disabled.
	a3 := &CustomAction{
		Name:         "disabled-greet",
		Command:      "Disabled",
		AutoTriggers: `["on_new_session"]`,
		Enabled:      false,
	}
	// Action with multiple triggers.
	a4 := &CustomAction{
		Name:         "multi",
		Command:      "multi trigger",
		AutoTriggers: `["on_new_session","on_mode_change"]`,
		Enabled:      true,
	}

	for _, a := range []*CustomAction{a1, a2, a3, a4} {
		if err := s.CreateCustomAction(a); err != nil {
			t.Fatalf("create %s: %v", a.Name, err)
		}
	}

	// Should find a1 and a4 (not a3 which is disabled).
	results, err := s.ListCustomActionsByTrigger("on_new_session")
	if err != nil {
		t.Fatalf("ListCustomActionsByTrigger: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 actions for on_new_session, got %d", len(results))
	}

	// Should find a2 only.
	results2, _ := s.ListCustomActionsByTrigger("on_agent_switch")
	if len(results2) != 1 {
		t.Errorf("expected 1 action for on_agent_switch, got %d", len(results2))
	}

	// Should find a4 only.
	results3, _ := s.ListCustomActionsByTrigger("on_mode_change")
	if len(results3) != 1 {
		t.Errorf("expected 1 action for on_mode_change, got %d", len(results3))
	}

	// Should find nothing for unknown trigger.
	results4, _ := s.ListCustomActionsByTrigger("on_unknown")
	if len(results4) != 0 {
		t.Errorf("expected 0 actions for unknown trigger, got %d", len(results4))
	}
}

func TestCustomActionUniqueNameConstraint(t *testing.T) {
	s := newTestStore(t)

	a1 := &CustomAction{Name: "unique-name", Command: "x", Enabled: true}
	if err := s.CreateCustomAction(a1); err != nil {
		t.Fatalf("create first: %v", err)
	}

	a2 := &CustomAction{Name: "unique-name", Command: "y", Enabled: true}
	err := s.CreateCustomAction(a2)
	if err == nil {
		t.Error("expected error for duplicate name")
	}
}
