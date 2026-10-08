package service

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/agent/approval"

	"github.com/hollis-labs/go-chatstream/conformance"
	llmtypes "github.com/hollis-labs/go-llm-types"
	permissionlib "github.com/hollis-labs/go-permission"
	"github.com/hollis-labs/nanite/internal/chat"
)

func TestCognitiveApprovalPromptPublishesBindingBeforeResponse(t *testing.T) {
	tool := llmtypes.ToolUseBlock{ID: "call-native", Name: "lookup", Input: map[string]any{"value": "example"}}
	f := newHandleMessageFixture(t, []characterizationProviderStep{{events: toolTurnEvents(tool)}, {events: doneEvents("approved answer")}})
	f.tools.definitions = []llmtypes.ToolDefinition{{Name: tool.Name, Description: "fixture lookup"}}
	f.svc.permissions = permissionlib.NewEngine(permissionlib.ModeDefault, &permissionlib.RuleSet{Rules: []permissionlib.Rule{{Tool: tool.Name, Behavior: permissionlib.DecisionAsk}}})
	f.svc.cognitiveApprovals = approval.New(f.svc.permissions)
	runID, err := f.svc.SubmitCognitiveTurn(t.Context(), f.session, "look up the example")
	if err != nil {
		t.Fatal(err)
	}
	consumer, ok := f.svc.streams.GetStream(runID)
	if !ok {
		t.Fatal("missing native stream")
	}
	var prompt chat.ApprovalRequestPayload
	timeout := time.After(5 * time.Second)
waitPrompt:
	for {
		select {
		case event, open := <-consumer:
			if !open {
				t.Fatal("native turn ended before its approval")
			}
			if event.Type == "approval_request" {
				if err := json.Unmarshal([]byte(event.Data), &prompt); err != nil {
					t.Fatal(err)
				}
				break waitPrompt
			}
		case <-timeout:
			t.Fatal("native approval was not published")
		}
	}
	if prompt.RunID != runID || prompt.CallID != tool.ID || len(prompt.SupportedScopes) != 1 || prompt.SupportedScopes[0] != "once" {
		t.Fatalf("unbound native prompt published: %+v", prompt)
	}
	if calls := f.tools.calls(); len(calls) != 0 {
		t.Fatalf("tool executed before permission: %v", calls)
	}
	if err := f.svc.cognitiveApprovals.RespondRetained(t.Context(), f.session, prompt.RequestID, permissionlib.DecisionAllow, permissionlib.ScopeSession); !errors.Is(err, approval.ErrScope) {
		t.Fatalf("retained facade widened native prompt: %v", err)
	}
	if _, err := f.svc.cognitiveApprovals.Respond(t.Context(), f.session, prompt.RequestID, permissionlib.DecisionAllow, permissionlib.ScopeOnce); err != nil {
		t.Fatal(err)
	}
	if events := drainStream(consumer); findEvent(events, "stream_end") == nil {
		t.Fatalf("native tool turn did not complete: %v", eventTypes(events))
	}
	if calls := f.tools.calls(); len(calls) != 1 || calls[0] != tool.Name {
		t.Fatalf("approved call execution = %v", calls)
	}
	if err := f.svc.cognitiveApprovals.RespondRetained(t.Context(), f.session, prompt.RequestID, permissionlib.DecisionAllow, permissionlib.ScopeOnce); err != nil {
		t.Fatalf("retained repeat after native tool returned: %v", err)
	}
	if result := f.svc.permissions.Check(t.Context(), f.session, tool.Name, tool.Input, permissionlib.ToolMeta{}); result.Decision != permissionlib.DecisionAsk {
		t.Fatalf("native decision granted a subsequent call: %+v", result)
	}
}

func TestCognitiveBoundApprovalCanonicalCallAndExactExpiry(t *testing.T) {
	tool := llmtypes.ToolUseBlock{ID: "native-call", Name: "write", Input: map[string]any{"value": "x"}}
	f := newHandleMessageFixture(t, []characterizationProviderStep{{events: toolTurnEvents(tool)}, {events: doneEvents("done")}})
	f.tools.definitions = []llmtypes.ToolDefinition{{Name: tool.Name, Description: "fixture"}}
	f.svc.permissions = permissionlib.NewEngine(permissionlib.ModeDefault, &permissionlib.RuleSet{Rules: []permissionlib.Rule{{Tool: tool.Name, Behavior: permissionlib.DecisionAsk}}})
	f.svc.cognitiveApprovals = approval.New(f.svc.permissions)
	id, err := f.svc.SubmitCognitiveTurn(t.Context(), f.session, "do it")
	if err != nil {
		t.Fatal(err)
	}
	stream, _ := f.svc.streams.GetStream(id)
	var prompt chat.ApprovalRequestPayload
	for event := range stream {
		if event.Type == "approval_request" {
			if err = json.Unmarshal([]byte(event.Data), &prompt); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if prompt.ExpiresAt.IsZero() {
		t.Fatal("expiry absent")
	}
	if _, err = f.svc.cognitiveApprovals.Respond(t.Context(), f.session, prompt.RequestID, permissionlib.DecisionAllow, permissionlib.ScopeOnce); err != nil {
		t.Fatal(err)
	}
	drainStream(stream)
	sub, err := f.svc.streams.CognitiveTurns().Subscribe(t.Context(), f.session, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	events := readCognitiveEvents(t, sub)
	if violations := conformance.Validate(events); len(violations) > 0 {
		t.Fatal(violations)
	}
	for _, event := range events {
		if event.Verb == "approval.request" && (event.CallID != tool.ID || event.ExpiresAt == nil || !event.ExpiresAt.Equal(prompt.ExpiresAt)) {
			t.Fatal("canonical prompt binding/expiry differs", event, prompt)
		}
	}
}
