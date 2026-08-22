package skill

import "testing"

// TASKS/skills/02: rewritten against the redesigned, index-only
// store.Skill shape — ToStoreSkill no longer produces ToolBindings,
// IsBuiltin, Settings, or Prompt (see convert.go's doc comment for why).
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
	if sk.SourceTier != "project" {
		t.Errorf("SourceTier = %q, want %q (from Definition.Source)", sk.SourceTier, "project")
	}
	if !sk.Enabled {
		t.Error("Enabled should be true for file-based skills")
	}
	if sk.InputSchema != "{}" {
		t.Errorf("InputSchema = %q, want %q", sk.InputSchema, "{}")
	}
	if sk.DeclaredDependencies != "[]" {
		t.Errorf("DeclaredDependencies = %q, want %q", sk.DeclaredDependencies, "[]")
	}
	if sk.Version != 1 {
		t.Errorf("Version = %d, want 1", sk.Version)
	}
}

func TestToStoreSkill_DefaultsSourceTierToUser(t *testing.T) {
	def := &Definition{
		Name: "Minimal",
		Slug: "minimal",
	}

	sk := def.ToStoreSkill()
	if sk.SourceTier != "user" {
		t.Errorf("SourceTier = %q, want %q (default when Definition.Source is unset)", sk.SourceTier, "user")
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
