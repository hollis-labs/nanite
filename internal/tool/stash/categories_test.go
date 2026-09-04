package stash

import "testing"

func TestBuiltinCategorizer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
	}{
		{name: "dev_read", want: categoryCoreIO},
		{name: "dev_grep", want: categorySearch},
		{name: "agent_create", want: categoryAgent},
		{name: "tesseract_recall", want: categoryContext},
		{name: "mcp_github_search", want: categoryMCP},
		{name: "nanite_find_record", want: categorySearch},
		{name: "nanite_show_card", want: categoryCoreIO},
		{name: "nanite_skill_install", want: categoryAgent},
		{name: "unknown_tool", want: ""},
	}

	categorizer := BuiltinCategorizer()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := categorizer.Categorize(tt.name); got != tt.want {
				t.Fatalf("Categorize(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}
