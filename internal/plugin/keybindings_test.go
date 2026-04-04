package plugin

import (
	"net/http"
	"testing"

	pluginsdk "github.com/hollis-labs/plugin"
)

func TestRegisterKeybinding(t *testing.T) {
	h := NewHost(http.NewServeMux(), NewLogger("test"))

	kb := pluginsdk.KeybindingDef{
		ID:          "git.commit",
		Key:         "mod+shift+g",
		Action:      "command",
		ActionValue: "git-commit",
		Label:       "Commit Changes",
		Description: "Run git commit",
	}

	if err := h.RegisterKeybinding(kb); err != nil {
		t.Fatalf("RegisterKeybinding: %v", err)
	}

	keybindings := h.GetKeybindings()
	if len(keybindings) != 1 {
		t.Fatalf("expected 1 keybinding, got %d", len(keybindings))
	}
	if keybindings[0].ID != "git.commit" {
		t.Errorf("expected ID git.commit, got %s", keybindings[0].ID)
	}
	if keybindings[0].Key != "mod+shift+g" {
		t.Errorf("expected key mod+shift+g, got %s", keybindings[0].Key)
	}
}

func TestRegisterKeybinding_CoreCollision(t *testing.T) {
	h := NewHost(http.NewServeMux(), NewLogger("test"))

	kb := pluginsdk.KeybindingDef{
		ID:    "plugin.sidebar",
		Key:   "mod+b", // core binding
		Label: "Toggle Something",
	}

	err := h.RegisterKeybinding(kb)
	if err == nil {
		t.Error("expected error for core binding collision")
	}
}

func TestRegisterKeybinding_PluginCollision(t *testing.T) {
	h := NewHost(http.NewServeMux(), NewLogger("test"))

	kb1 := pluginsdk.KeybindingDef{
		ID:    "plugin-a.action",
		Key:   "mod+shift+x",
		Label: "Plugin A Action",
	}
	if err := h.RegisterKeybinding(kb1); err != nil {
		t.Fatalf("first registration: %v", err)
	}

	// Different ID, same key should fail.
	kb2 := pluginsdk.KeybindingDef{
		ID:    "plugin-b.action",
		Key:   "mod+shift+x",
		Label: "Plugin B Action",
	}
	err := h.RegisterKeybinding(kb2)
	if err == nil {
		t.Error("expected error for plugin key collision")
	}
}

func TestRegisterKeybinding_SameIDReplaces(t *testing.T) {
	h := NewHost(http.NewServeMux(), NewLogger("test"))

	kb := pluginsdk.KeybindingDef{
		ID:    "my.binding",
		Key:   "mod+shift+m",
		Label: "Version 1",
	}
	h.RegisterKeybinding(kb)

	kb.Label = "Version 2"
	if err := h.RegisterKeybinding(kb); err != nil {
		t.Fatalf("re-registration with same ID should succeed: %v", err)
	}

	keybindings := h.GetKeybindings()
	if len(keybindings) != 1 {
		t.Fatalf("expected 1 keybinding after re-registration, got %d", len(keybindings))
	}
	if keybindings[0].Label != "Version 2" {
		t.Errorf("expected label 'Version 2', got %q", keybindings[0].Label)
	}
}

func TestRegisterKeybinding_ValidationErrors(t *testing.T) {
	h := NewHost(http.NewServeMux(), NewLogger("test"))

	tests := []struct {
		name string
		kb   pluginsdk.KeybindingDef
	}{
		{"empty ID", pluginsdk.KeybindingDef{Key: "mod+x", Label: "X"}},
		{"empty key", pluginsdk.KeybindingDef{ID: "test", Label: "X"}},
		{"empty label", pluginsdk.KeybindingDef{ID: "test", Key: "mod+x"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := h.RegisterKeybinding(tt.kb)
			if err == nil {
				t.Error("expected validation error")
			}
		})
	}
}
