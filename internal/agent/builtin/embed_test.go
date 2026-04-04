package builtin

import "testing"

func TestDefaultAgent(t *testing.T) {
	def, err := DefaultAgent()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if def.Slug != "default" {
		t.Errorf("Slug = %q, want %q", def.Slug, "default")
	}
	if def.Name != "Default" {
		t.Errorf("Name = %q, want %q", def.Name, "Default")
	}
	if def.Source != "builtin" {
		t.Errorf("Source = %q, want %q", def.Source, "builtin")
	}
	if def.SourceRef != "embedded:default.md" {
		t.Errorf("SourceRef = %q, want %q", def.SourceRef, "embedded:default.md")
	}
	if def.SystemPrompt == "" {
		t.Error("SystemPrompt is empty")
	}
	if def.Icon != "chat" {
		t.Errorf("Icon = %q, want %q", def.Icon, "chat")
	}
}
