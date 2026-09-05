package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hollis-labs/go-agent-wrapper/acp"
	"github.com/hollis-labs/nanite/internal/permission"
)

func TestBestEffortPermissionResponder_NilDependenciesKeepWrapperDefault(t *testing.T) {
	engine := permission.NewEngine(permission.ModeDefault, nil)
	if got := bestEffortPermissionResponder("session", nil, func(*permission.ApprovalRequest) {}); got != nil {
		t.Fatal("nil engine produced a responder; wrapper safe-cancel default must remain active")
	}
	if got := bestEffortPermissionResponder("session", engine, nil); got != nil {
		t.Fatal("nil UI sink produced a responder; an unanswerable request must use wrapper safe-cancel")
	}
}

func TestBestEffortPermissionResponder_UsesNaniteApprovalVocabulary(t *testing.T) {
	cases := []struct {
		name         string
		decision     permission.Decision
		scope        permission.Scope
		options      []acp.PermissionOption
		wantOptionID string
	}{
		{
			name:     "allow once",
			decision: permission.DecisionAllow, scope: permission.ScopeOnce,
			options:      []acp.PermissionOption{{OptionID: "yes-once", Kind: acp.PermissionAllowOnce}},
			wantOptionID: "yes-once",
		},
		{
			name:     "allow session falls back safely to once",
			decision: permission.DecisionAllow, scope: permission.ScopeSession,
			options:      []acp.PermissionOption{{OptionID: "yes-once", Kind: acp.PermissionAllowOnce}},
			wantOptionID: "yes-once",
		},
		{
			name:     "deny session",
			decision: permission.DecisionDeny, scope: permission.ScopeSession,
			options:      []acp.PermissionOption{{OptionID: "no-always", Kind: acp.PermissionRejectAlways}},
			wantOptionID: "no-always",
		},
		{
			name:     "once decision never broadens",
			decision: permission.DecisionAllow, scope: permission.ScopeOnce,
			options:      []acp.PermissionOption{{OptionID: "yes-always", Kind: acp.PermissionAllowAlways}},
			wantOptionID: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine := permission.NewEngine(permission.ModeDefault, nil)
			engine.SetApprovalTimeout(time.Second)
			emitted := make(chan *permission.ApprovalRequest, 1)
			responder := bestEffortPermissionResponder("nanite-session", engine, func(req *permission.ApprovalRequest) {
				emitted <- req
			})

			result := make(chan acp.PermissionSelection, 1)
			go func() {
				selection, _ := responder(context.Background(), acp.PermissionRequest{
					SessionID: "provider-session",
					ToolCall: acp.PermissionToolCall{
						Name: "shell", Title: "Run command", RawInput: json.RawMessage(`{"command":"pwd"}`),
					},
					Options: tc.options,
				})
				result <- selection
			}()

			req := <-emitted
			if req.SessionID != "nanite-session" || req.ToolName != "shell" {
				t.Fatalf("approval request identity = session %q tool %q", req.SessionID, req.ToolName)
			}
			if req.Input["command"] != "pwd" {
				t.Fatalf("approval input = %#v", req.Input)
			}
			if !engine.Respond(req.ID, tc.decision, tc.scope, "nanite-session") {
				t.Fatal("permission engine rejected response")
			}
			if got := <-result; got.OptionID != tc.wantOptionID {
				t.Fatalf("selection = %q, want %q", got.OptionID, tc.wantOptionID)
			}
		})
	}
}

func TestBestEffortPermissionResponder_CanceledTurnReturnsZeroSelection(t *testing.T) {
	engine := permission.NewEngine(permission.ModeDefault, nil)
	emitted := make(chan *permission.ApprovalRequest, 1)
	responder := bestEffortPermissionResponder("nanite-session", engine, func(req *permission.ApprovalRequest) {
		emitted <- req
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan acp.PermissionSelection, 1)
	go func() {
		selection, _ := responder(ctx, acp.PermissionRequest{
			ToolCall: acp.PermissionToolCall{Name: "shell"},
			Options:  []acp.PermissionOption{{OptionID: "reject-once", Kind: acp.PermissionRejectOnce}},
		})
		result <- selection
	}()
	<-emitted
	cancel()
	if got := <-result; got.OptionID != "" {
		t.Fatalf("selection after turn cancellation = %q, want zero/cancel", got.OptionID)
	}
}
