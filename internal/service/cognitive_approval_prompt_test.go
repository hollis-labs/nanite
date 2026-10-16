package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/ui-go/chatstream/conformance"
	"github.com/hollis-labs/substrate/agent/approval"
	permissionlib "github.com/hollis-labs/substrate/harness/interception/permission"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

// Permission approval cannot substitute for the absent verified actor issuer.
// The shared approval registry's bound once-only and competing-facade behavior
// is exercised independently by the real HTTP approval facade tests.
func TestCognitiveUnissuedToolsRefuseBeforeApprovalOrDiscovery(t *testing.T) {
	for _, name := range []string{"lookup", "write", "request_tools"} {
		t.Run(name, func(t *testing.T) {
			tool := llmtypes.ToolUseBlock{ID: "unissued-call", Name: name, Input: map[string]any{"value": "example"}}
			f := newHandleMessageFixture(t, []characterizationProviderStep{{events: toolTurnEvents(tool)}, {events: doneEvents("refusal explained")}})
			bindTestDefinedConfiguration(t, f)
			f.tools.definitions = []llmtypes.ToolDefinition{{Name: name, Description: "private unissued fixture"}}
			f.svc.permissions = permissionlib.NewEngine(permissionlib.ModeDefault, &permissionlib.RuleSet{Rules: []permissionlib.Rule{{Tool: name, Behavior: permissionlib.DecisionAsk}}})
			f.svc.cognitiveApprovals = approval.New(f.svc.permissions)
			id, err := f.svc.SubmitCognitiveTurn(t.Context(), f.session, "request an unissued operation")
			if err != nil {
				t.Fatal(err)
			}
			stream, ok := f.svc.streams.GetStream(id)
			if !ok {
				t.Fatal("missing canonical producer")
			}
			events := drainStream(stream)
			denied := false
			for _, ev := range events {
				if ev.Type == "approval_request" {
					t.Fatal("permission prompt invented missing tool authority")
				}
				if ev.Type == "tool_result" && ev.ToolID == tool.ID {
					denied = ev.IsError && strings.Contains(ev.Summary, "verified actor tool grants are unavailable")
				}
			}
			if !denied || len(f.tools.calls()) != 0 || f.provider.callCount() != 2 {
				t.Fatalf("unissued call outcome: denied=%v calls=%v provider=%d", denied, f.tools.calls(), f.provider.callCount())
			}
			sub, err := f.svc.streams.CognitiveTurns().Subscribe(t.Context(), f.session, id, 0)
			if err != nil {
				t.Fatal(err)
			}
			canonical := readCognitiveEvents(t, sub)
			if violations := conformance.Validate(canonical); len(violations) > 0 {
				t.Fatal(violations)
			}
			boundCall := false
			for _, ev := range canonical {
				var callID string
				_ = json.Unmarshal(ev.Meta["call_id"], &callID)
				if callID == tool.ID {
					boundCall = true
				}
				if ev.Verb == "approval.request" {
					t.Fatal("canonical stream invented approval authority")
				}
			}
			outcome, err := f.svc.streams.CognitiveTurns().Get(f.session, id)
			if err != nil || outcome.State != "completed" || !boundCall {
				t.Fatalf("canonical denied-call settlement: %+v %v bound=%v", outcome, err, boundCall)
			}
		})
	}
}
