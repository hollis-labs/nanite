package permission

import (
	"context"
	"log/slog"
	"testing"

	"github.com/hollis-labs/libs/security/policy"
)

type pdpFunc func(context.Context, policy.Request) (policy.Decision, error)

func (f pdpFunc) Evaluate(ctx context.Context, req policy.Request) (policy.Decision, error) {
	return f(ctx, req)
}

// A host PDP that allows with an obligation Nanite cannot enforce is denied:
// the call fails closed rather than running without the obligation.
func TestObligationFailsClosed(t *testing.T) {
	obligated := pdpFunc(func(context.Context, policy.Request) (policy.Decision, error) {
		return policy.Decision{Effect: policy.Allow, Obligations: []policy.Obligation{{Kind: policy.Approval, PolicyID: "host", Reference: "ticket"}}}, nil
	})
	local, err := Rules("yolo", nil)
	if err != nil {
		t.Fatal(err)
	}
	req := PermissionRequest{ID: "one", ConversationID: "test", Tool: "read_file", ReadOnly: true}
	for name, pdps := range map[string][2]policy.PDP{"outer": {obligated, local}, "local": {nil, obligated}} {
		got, err := Evaluate(t.Context(), slog.Default(), pdps[0], pdps[1], "urn:nanite:agent:test", "chat", req, nil, nil)
		if err != nil || got.Effect != policy.Deny {
			t.Fatalf("%s obligation: %+v, %v", name, got, err)
		}
	}
}

// Authored allow rules make file edits Allow in default mode, but plan mode
// denies edits whatever the rules say.
func TestAuthoredEditRulesByMode(t *testing.T) {
	const allowEdits = "permissions:\n  rules:\n    - tool: write_file\n      behavior: allow\n    - tool: edit_file\n      behavior: allow\n"
	for _, tc := range []struct {
		mode string
		want policy.Effect
	}{
		{"default", policy.Allow},
		{"accept-edits", policy.Allow},
		{"plan", policy.Deny},
	} {
		for _, tool := range []string{"write_file", "edit_file"} {
			t.Run(tc.mode+"/"+tool, func(t *testing.T) {
				local, err := Rules(tc.mode, []byte(allowEdits))
				if err != nil {
					t.Fatal(err)
				}
				req := PermissionRequest{ID: "one", ConversationID: "test", Tool: tool}
				got, err := Evaluate(t.Context(), slog.Default(), nil, local, "urn:nanite:agent:test", "chat", req, map[string]any{"path": "a.txt"}, nil)
				if err != nil || got.Effect != tc.want {
					t.Fatalf("decision = %+v, %v; want %s", got, err, tc.want)
				}
			})
		}
	}
}

// extra context from a subtask cannot override the request's own facts.
func TestExtraContextCannotOverrideRequest(t *testing.T) {
	var seen policy.Request
	outer := pdpFunc(func(_ context.Context, req policy.Request) (policy.Decision, error) {
		seen = req
		return policy.Decision{Effect: policy.Allow}, nil
	})
	local, err := Rules("default", nil)
	if err != nil {
		t.Fatal(err)
	}
	req := PermissionRequest{ID: "one", ConversationID: "test", Tool: "bash", Destructive: true}
	args := map[string]any{"command": "printf test"}
	extra := map[string]any{"read_only": true, "destructive": false, "input": map[string]any{"command": "true"}, "role": "forged", "conversation": "other", "variant": "worker"}
	got, err := Evaluate(t.Context(), slog.Default(), outer, local, "urn:nanite:agent:test", "chat", req, args, extra)
	if err != nil || got.Effect != policy.Ask {
		t.Fatalf("forged read_only changed the decision: %+v, %v", got, err)
	}
	ctx := seen.Context
	if ctx["read_only"] != false || ctx["destructive"] != true || ctx["role"] != "chat" || ctx["conversation"] != "test" {
		t.Fatalf("request facts overridden: %v", ctx)
	}
	if input, _ := ctx["input"].(map[string]any); input["command"] != "printf test" {
		t.Fatalf("input overridden: %v", ctx["input"])
	}
	if ctx["variant"] != "worker" {
		t.Fatal("non-colliding extra context dropped")
	}
}
