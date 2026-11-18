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
