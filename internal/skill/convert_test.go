package skill

import (
	"encoding/json"
	"testing"
)

func TestToStoreSkill(t *testing.T) {
	def := &Definition{
		Name:         "Go Lint",
		Slug:         "go-lint",
		Description:  "Run Go linting",
		AllowedTools: []string{"shell", "dev_read"},
		Model:        "haiku",
		Effort:       "low",
		Context:      "fork",
		Tags:         []string{"code", "lint"},
		BrokerHints:  []string{"code"},
		Source:       "project",
		SourceRef:    "/path/to/go-lint.md",
	}

	sk := def.ToStoreSkill()

	if sk.ID != "file-go-lint" {
		t.Errorf("ID = %q, want %q", sk.ID, "file-go-lint")
	}
	if sk.Name != "Go Lint" {
		t.Errorf("Name = %q", sk.Name)
	}
	if sk.Slug != "go-lint" {
		t.Errorf("Slug = %q", sk.Slug)
	}
	if sk.Category != "code" {
		t.Errorf("Category = %q, want %q (first tag)", sk.Category, "code")
	}
	if !sk.IsBuiltin {
		t.Error("IsBuiltin should be true for file-based skills")
	}

	// Check ToolBindings JSON.
	var tools []string
	if err := json.Unmarshal([]byte(sk.ToolBindings), &tools); err != nil {
		t.Fatalf("ToolBindings JSON: %v", err)
	}
	if len(tools) != 2 || tools[0] != "shell" {
		t.Errorf("ToolBindings = %v", tools)
	}

	// Check settings contain expected fields.
	var settings map[string]any
	if err := json.Unmarshal([]byte(sk.Settings), &settings); err != nil {
		t.Fatalf("Settings JSON: %v", err)
	}
	if settings["model"] != "haiku" {
		t.Errorf("settings.model = %v", settings["model"])
	}
	if settings["effort"] != "low" {
		t.Errorf("settings.effort = %v", settings["effort"])
	}
	if settings["context"] != "fork" {
		t.Errorf("settings.context = %v", settings["context"])
	}
	if settings["source"] != "project" {
		t.Errorf("settings.source = %v", settings["source"])
	}
}

func TestToStoreSkill_NilTools(t *testing.T) {
	def := &Definition{
		Name: "Minimal",
		Slug: "minimal",
	}

	sk := def.ToStoreSkill()
	if sk.ToolBindings != "[]" {
		t.Errorf("ToolBindings = %q, want %q", sk.ToolBindings, "[]")
	}
}

func TestIsFileBasedID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"file-go-lint", true},
		{"file-", false},
		{"uuid-1234", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsFileBasedID(tt.id); got != tt.want {
			t.Errorf("IsFileBasedID(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestSlugFromFileID(t *testing.T) {
	if got := SlugFromFileID("file-go-lint"); got != "go-lint" {
		t.Errorf("SlugFromFileID = %q, want %q", got, "go-lint")
	}
	if got := SlugFromFileID("not-file"); got != "" {
		t.Errorf("SlugFromFileID = %q, want empty", got)
	}
}
