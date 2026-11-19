package permission

import (
	"log/slog"
	"testing"

	"github.com/hollis-labs/libs/security/policy"
)

func TestBashPermissionModes(t *testing.T) {
	for _, tc := range []struct {
		mode, rules string
		want        policy.Effect
	}{
		{"default", "", policy.Ask},
		{"accept-edits", "", policy.Ask},
		{"plan", "", policy.Deny},
		{"yolo", "", policy.Allow},
		{"default", "permissions:\n  rules:\n    - tool: bash\n      pattern: printf\n      behavior: allow\n", policy.Allow},
	} {
		local, err := Rules(tc.mode, []byte(tc.rules))
		if err != nil {
			t.Fatal(err)
		}
		req := PermissionRequest{ID: "one", ConversationID: "test", Tool: "bash", Destructive: true}
		d, err := Evaluate(t.Context(), slog.Default(), nil, local, "urn:nanite:agent:test", "chat", req, map[string]any{"command": "printf test"})
		if err != nil || d.Effect != tc.want {
			t.Fatalf("mode %s decision = %+v, %v", tc.mode, d, err)
		}
	}
}

func TestToolPermissionModeMatrix(t *testing.T) {
	for _, mode := range []struct {
		name               string
		read, edit, danger policy.Effect
	}{
		{"default", policy.Allow, policy.Ask, policy.Ask},
		{"accept-edits", policy.Allow, policy.Allow, policy.Ask},
		{"plan", policy.Allow, policy.Deny, policy.Deny},
		{"yolo", policy.Allow, policy.Allow, policy.Allow},
	} {
		for _, tool := range []struct {
			name                  string
			readonly, destructive bool
			expected              policy.Effect
		}{
			{"read_file", true, false, mode.read},
			{"write_file", false, false, mode.edit},
			{"edit_file", false, false, mode.edit},
			{"bash", false, true, mode.danger},
		} {
			t.Run(mode.name+"/"+tool.name, func(t *testing.T) {
				local, err := Rules(mode.name, nil)
				if err != nil {
					t.Fatal(err)
				}
				req := PermissionRequest{ID: "one", ConversationID: "test", Tool: tool.name, ReadOnly: tool.readonly, Destructive: tool.destructive}
				got, err := Evaluate(t.Context(), slog.Default(), nil, local, "urn:nanite:agent:test", "chat", req, nil)
				if err != nil || got.Effect != tool.expected {
					t.Fatalf("decision = %+v, %v; want %s", got, err, tool.expected)
				}
			})
		}
	}
}
