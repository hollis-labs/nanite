package store

import (
	"testing"
)

func TestCreateTriggerRule(t *testing.T) {
	s := newTestStore(t)

	rule := &TriggerRule{
		PluginID:        "test-plugin",
		EventType:       "session.end",
		ConnectorName:   "slack-webhook",
		PayloadTemplate: `{"text": "Session ended: {{.SessionID}}"}`,
		FilterExpr:      "",
		Enabled:         true,
		Description:     "Notify Slack when a session ends",
	}

	if err := s.CreateTriggerRule(rule); err != nil {
		t.Fatalf("CreateTriggerRule: %v", err)
	}

	if rule.ID == "" {
		t.Error("expected ID to be generated")
	}
	if rule.CreatedAt == "" {
		t.Error("expected CreatedAt to be set")
	}
}

func TestGetTriggerRule(t *testing.T) {
	s := newTestStore(t)

	rule := &TriggerRule{
		PluginID:        "test-plugin",
		EventType:       "tool.called",
		ConnectorName:   "webhook",
		PayloadTemplate: "{}",
		Enabled:         true,
		Description:     "test rule",
	}
	if err := s.CreateTriggerRule(rule); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.GetTriggerRule(rule.ID)
	if err != nil {
		t.Fatalf("GetTriggerRule: %v", err)
	}
	if got.EventType != "tool.called" {
		t.Errorf("expected event_type tool.called, got %s", got.EventType)
	}
	if got.ConnectorName != "webhook" {
		t.Errorf("expected connector_name webhook, got %s", got.ConnectorName)
	}
	if !got.Enabled {
		t.Error("expected enabled to be true")
	}
}

func TestGetTriggerRuleNotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.GetTriggerRule("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent rule")
	}
}

func TestUpdateTriggerRule(t *testing.T) {
	s := newTestStore(t)

	rule := &TriggerRule{
		PluginID:      "p1",
		EventType:     "session.start",
		ConnectorName: "email",
		Enabled:       true,
	}
	if err := s.CreateTriggerRule(rule); err != nil {
		t.Fatalf("create: %v", err)
	}

	rule.EventType = "session.end"
	rule.Enabled = false
	rule.Description = "updated description"
	if err := s.UpdateTriggerRule(rule); err != nil {
		t.Fatalf("UpdateTriggerRule: %v", err)
	}

	got, _ := s.GetTriggerRule(rule.ID)
	if got.EventType != "session.end" {
		t.Errorf("expected event_type session.end, got %s", got.EventType)
	}
	if got.Enabled {
		t.Error("expected enabled to be false after update")
	}
	if got.Description != "updated description" {
		t.Errorf("expected updated description, got %q", got.Description)
	}
}

func TestUpdateTriggerRuleNotFound(t *testing.T) {
	s := newTestStore(t)

	rule := &TriggerRule{ID: "nonexistent", EventType: "x", ConnectorName: "y"}
	err := s.UpdateTriggerRule(rule)
	if err == nil {
		t.Error("expected error for nonexistent rule")
	}
}

func TestDeleteTriggerRule(t *testing.T) {
	s := newTestStore(t)

	rule := &TriggerRule{
		PluginID:      "p1",
		EventType:     "session.start",
		ConnectorName: "webhook",
		Enabled:       true,
	}
	if err := s.CreateTriggerRule(rule); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := s.DeleteTriggerRule(rule.ID); err != nil {
		t.Fatalf("DeleteTriggerRule: %v", err)
	}

	_, err := s.GetTriggerRule(rule.ID)
	if err == nil {
		t.Error("expected error after deletion")
	}
}

func TestDeleteTriggerRuleNotFound(t *testing.T) {
	s := newTestStore(t)

	err := s.DeleteTriggerRule("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent rule")
	}
}

func TestListTriggerRules(t *testing.T) {
	s := newTestStore(t)

	// Create rules for two plugins.
	for _, pluginID := range []string{"plugin-a", "plugin-b"} {
		rule := &TriggerRule{
			PluginID:      pluginID,
			EventType:     "session.end",
			ConnectorName: "webhook",
			Enabled:       true,
		}
		if err := s.CreateTriggerRule(rule); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	// List all.
	all, err := s.ListTriggerRules("")
	if err != nil {
		t.Fatalf("ListTriggerRules: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 rules, got %d", len(all))
	}

	// Filter by plugin.
	filtered, err := s.ListTriggerRules("plugin-a")
	if err != nil {
		t.Fatalf("ListTriggerRules filtered: %v", err)
	}
	if len(filtered) != 1 {
		t.Errorf("expected 1 rule for plugin-a, got %d", len(filtered))
	}
}

func TestListTriggerRulesByEvent(t *testing.T) {
	s := newTestStore(t)

	// One enabled, one disabled for the same event.
	enabled := &TriggerRule{
		PluginID:      "p1",
		EventType:     "tool.called",
		ConnectorName: "webhook",
		Enabled:       true,
	}
	disabled := &TriggerRule{
		PluginID:      "p1",
		EventType:     "tool.called",
		ConnectorName: "email",
		Enabled:       false,
	}
	other := &TriggerRule{
		PluginID:      "p1",
		EventType:     "session.start",
		ConnectorName: "webhook",
		Enabled:       true,
	}
	for _, r := range []*TriggerRule{enabled, disabled, other} {
		if err := s.CreateTriggerRule(r); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	rules, err := s.ListTriggerRulesByEvent("tool.called")
	if err != nil {
		t.Fatalf("ListTriggerRulesByEvent: %v", err)
	}
	if len(rules) != 1 {
		t.Errorf("expected 1 enabled rule for tool.called, got %d", len(rules))
	}
	if rules[0].ConnectorName != "webhook" {
		t.Errorf("expected connector webhook, got %s", rules[0].ConnectorName)
	}
}

func TestDeleteTriggerRulesByPlugin(t *testing.T) {
	s := newTestStore(t)

	for i := 0; i < 3; i++ {
		r := &TriggerRule{
			PluginID:      "doomed-plugin",
			EventType:     "session.start",
			ConnectorName: "webhook",
			Enabled:       true,
		}
		if err := s.CreateTriggerRule(r); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	// One rule for a different plugin should survive.
	survivor := &TriggerRule{
		PluginID:      "safe-plugin",
		EventType:     "session.start",
		ConnectorName: "webhook",
		Enabled:       true,
	}
	if err := s.CreateTriggerRule(survivor); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := s.DeleteTriggerRulesByPlugin("doomed-plugin"); err != nil {
		t.Fatalf("DeleteTriggerRulesByPlugin: %v", err)
	}

	all, _ := s.ListTriggerRules("")
	if len(all) != 1 {
		t.Errorf("expected 1 surviving rule, got %d", len(all))
	}
	if all[0].PluginID != "safe-plugin" {
		t.Errorf("wrong plugin survived: %s", all[0].PluginID)
	}
}
