package permission

import (
	"log/slog"
	"testing"

	"github.com/hollis-labs/libs/security/policy"
)

func TestServerRulesOverrideMCPReadOnlyHint(t *testing.T) {
	for _, want := range []policy.Effect{policy.Ask, policy.Deny} {
		src := []byte("permissions:\n  rules:\n    - tool: mcp__fixture__*\n      behavior: " + string(want) + "\n")
		local, err := Rules("default", src)
		if err != nil {
			t.Fatal(err)
		}
		req := PermissionRequest{ID: "mcp", ConversationID: "test", Tool: "mcp__fixture__inspect", ReadOnly: true}
		got, err := Evaluate(t.Context(), slog.Default(), nil, local, "urn:nanite:agent:fixture", "chat", req, nil)
		if err != nil || got.Effect != want {
			t.Fatalf("decision = %+v, %v; want %s", got, err, want)
		}
	}
}
