package pluginapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/pkg/pluginapi"
	sdkprocess "github.com/hollis-labs/plugin-sdk/subprocess"
)

func contextScope() pluginapi.ContextScope {
	return pluginapi.ContextScope{SourceIDs: []string{"reminders"}, SessionIDs: []string{"session-one"}}
}
func contextBlock() pluginapi.Block {
	return pluginapi.Block{Registers: pluginapi.Registrations{ContextSources: []pluginapi.ContextSource{{ID: "reminders"}}}}
}
func contextCapability(scope pluginapi.ContextScope) sdkprocess.CapabilityRequest {
	raw, _ := json.Marshal(scope)
	return sdkprocess.CapabilityRequest{Name: pluginapi.CapabilityContextSource, Metadata: raw}
}

func TestContextScopeAuthority(t *testing.T) {
	scope := contextScope()
	got, err := pluginapi.ContextScopeFor(contextBlock(), []sdkprocess.CapabilityRequest{contextCapability(scope)})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Allows("reminders", "session-one") || got.Allows("reminders", "other") || got.Allows("other", "session-one") || got.Allows("reminders", "") {
		t.Fatal("scope widened")
	}
	scope.AllSessions = true
	scope.SessionIDs = nil
	if err := scope.Validate(); err != nil || !scope.Allows("reminders", "other") {
		t.Fatal("all-workspace sessions failed")
	}
	for _, raw := range []string{`{}`, `null`, `{"source_ids":["reminders"],"all_sessions":true,"unknown":true}`, `{"source_ids":["reminders"],"all_sessions":true,"all_sessions":false}`, `{"source_ids":["reminders"],"all_sessions":true,"IncludeQuery":true}`} {
		if _, err := pluginapi.DecodeContextScope(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestContextDeclarationsRequireMatchingApproval(t *testing.T) {
	scope := contextScope()
	capability := contextCapability(scope)
	for _, tc := range []struct {
		name  string
		block pluginapi.Block
		caps  []sdkprocess.CapabilityRequest
	}{
		{"missing", contextBlock(), nil},
		{"undeclared", pluginapi.Block{}, []sdkprocess.CapabilityRequest{capability}},
		{"duplicate capability", contextBlock(), []sdkprocess.CapabilityRequest{capability, capability}},
		{"unknown source", pluginapi.Block{Registers: pluginapi.Registrations{ContextSources: []pluginapi.ContextSource{{ID: "documents"}}}}, []sdkprocess.CapabilityRequest{capability}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pluginapi.ContextScopeFor(tc.block, tc.caps); err == nil {
				t.Fatal("accepted mismatched authority")
			}
		})
	}
	capability.Optional = true
	if _, err := pluginapi.ContextScopeFor(contextBlock(), []sdkprocess.CapabilityRequest{capability}); err == nil {
		t.Fatal("accepted optional authority for mandatory contribution")
	}
	for _, change := range []func(*pluginapi.ContextScope){
		func(s *pluginapi.ContextScope) { s.AllSessions = true },
		func(s *pluginapi.ContextScope) { s.SessionIDs = nil },
		func(s *pluginapi.ContextScope) { s.SessionIDs = []string{"../session"} },
		func(s *pluginapi.ContextScope) { s.SourceIDs = []string{"reminders", "reminders"} },
		func(s *pluginapi.ContextScope) { s.SourceIDs = []string{"../source"} },
	} {
		bad := contextScope()
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("accepted invalid scope")
		}
	}
	block := contextBlock()
	block.Registers.ContextSources = append(block.Registers.ContextSources, block.Registers.ContextSources[0])
	if block.Validate() == nil {
		t.Fatal("accepted duplicate declarations")
	}
}

func TestContextWire(t *testing.T) {
	body := []byte(`{"protocol":1,"source_id":"reminders","session_id":"session-one","intent":"write_code","token_budget":100}`)
	req := &sdkprocess.HTTPRequest{Method: "POST", Path: pluginapi.ContextFetchPath, SessionID: "session-one", Body: body}
	got, err := pluginapi.DecodeContextRequest(req)
	if err != nil || got.QueryText != "" || len(got.Keywords) != 0 {
		t.Fatalf("request: %+v %v", got, err)
	}
	req.SessionID = "other"
	if _, err := pluginapi.DecodeContextRequest(req); err == nil {
		t.Fatal("accepted mismatched session")
	}
	valid := `{"protocol":1,"items":[{"key":"r1","content":"Remember","relevance":0.8}]}`
	if _, err := pluginapi.DecodeContextResponse([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"protocol":2,"items":[]}`, `{"protocol":1}`, `{"protocol":1,"items":null}`, `{"protocol":1,"items":[{"key":"r","content":"x","relevance":2}]}`, `{"protocol":1,"items":[{"key":"r","content":"x","relevance":1,"token_estimate":0}]}`, `{"protocol":1,"items":[{"key":"r","content":"x","relevance":1},{"key":"r","content":"x","relevance":1}]}`, strings.Repeat("x", pluginapi.MaxContextBytes+1)} {
		if _, err := pluginapi.DecodeContextResponse([]byte(raw)); err == nil {
			t.Fatal("accepted invalid response")
		}
	}
}
