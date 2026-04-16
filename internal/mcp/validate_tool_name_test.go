package mcp

import (
	"strings"
	"testing"
)

func TestValidateToolName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple", "dev_read", false},
		{"valid with dash", "web-fetch", false},
		{"valid with numbers", "tool123", false},
		{"valid underscore", "mcp_tool_name", false},
		{"empty", "", true},
		{"space", "tool name", true},
		{"dot", "tool.name", true},
		{"slash", "path/tool", true},
		{"unicode", "tööl", true},
		{"at sign", "tool@v2", true},
		{"max length", strings.Repeat("a", 128), false},
		{"over max length", strings.Repeat("a", 129), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason := validateToolName(tt.input)
			if tt.wantErr && reason == "" {
				t.Errorf("expected error for %q, got none", tt.input)
			}
			if !tt.wantErr && reason != "" {
				t.Errorf("expected no error for %q, got: %s", tt.input, reason)
			}
		})
	}
}
