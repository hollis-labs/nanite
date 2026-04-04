package plugin

import "testing"

func TestNormalizeEventType(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// Claude Code aliases resolve to Nanite names.
		{"PreToolUse", EventToolExecuting},
		{"PostToolUse", EventToolComplete},
		{"Notification", EventMessageReceived},
		{"SessionStart", EventSessionStart},
		{"SessionEnd", EventSessionEnd},
		{"Stop", EventSessionEnd},

		// Nanite names pass through unchanged.
		{EventToolExecuting, EventToolExecuting},
		{EventToolComplete, EventToolComplete},
		{EventSessionStart, EventSessionStart},
		{"custom.event", "custom.event"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeEventType(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeEventType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
