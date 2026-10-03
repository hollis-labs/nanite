package pluginapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/pkg/pluginapi"
	"github.com/hollis-labs/plugin-sdk/manifest"
	sdkprocess "github.com/hollis-labs/plugin-sdk/subprocess"
)

func alwaysShipFixture(t *testing.T) (pluginapi.Block, sdkprocess.CapabilityRequest, []manifest.Tool) {
	t.Helper()
	block := pluginapi.Block{Registers: pluginapi.Registrations{AlwaysShipSources: []pluginapi.AlwaysShipSource{{ID: "pins", Title: "Pinned Context", ListTool: "pins_list"}}}}
	raw, err := json.Marshal(pluginapi.AlwaysShipScope{SourceIDs: []string{"pins"}, SessionIDs: []string{"session-one"}, MaxBytes: 6000})
	if err != nil {
		t.Fatal(err)
	}
	return block, sdkprocess.CapabilityRequest{Name: pluginapi.CapabilityContextAlwaysShip, Metadata: raw}, []manifest.Tool{{Name: "pins_list", Description: "List visible pin inventory in the calling session", Effect: pluginapi.ToolEffectRead, InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`)}}
}

func TestAlwaysShipSharedManifestAndSessionAuthority(t *testing.T) {
	block, capability, tools := alwaysShipFixture(t)
	raw, err := pluginapi.EncodeBlock(block)
	if err != nil {
		t.Fatal(err)
	}
	m := manifest.Manifest{SchemaVersion: manifest.SchemaVersion, ID: "nanite.pins", Name: "Pins", Version: "0.2.0", Protocol: sdkprocess.ProtocolVersion, Runtime: manifest.Runtime,
		Entrypoint: manifest.Entrypoint{Command: "bin/pins"}, Hosts: map[string]manifest.HostRange{"nanite": {Min: pluginapi.Version}}, Nanite: raw, Capabilities: []sdkprocess.CapabilityRequest{capability}, Tools: tools}
	var encoded strings.Builder
	if err = manifest.Encode(&encoded, m); err != nil {
		t.Fatal(err)
	}
	decoded, err := manifest.Decode(strings.NewReader(encoded.String()))
	if err != nil {
		t.Fatal(err)
	}
	gotBlock, err := pluginapi.DecodeBlock(decoded.Nanite)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pluginapi.AlwaysShipScopeFor(gotBlock, decoded.Capabilities, decoded.Tools)
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxBytes != 6000 || gotBlock.Registers.AlwaysShipSources[0].Title != "Pinned Context" || !got.Allows("pins", "session-one") || got.Allows("pins", "other-session") || got.Allows("unknown", "session-one") || got.Allows("pins", "") {
		t.Fatal("scope or declarations changed", got, gotBlock)
	}
	got.SessionIDs, got.AllSessions = nil, true
	if err = got.Validate(); err != nil || !got.Allows("pins", "other-session") {
		t.Fatal("all-session scope failed", err)
	}
	// Always-ship is independently declared; it neither needs nor obtains the
	// weaker dynamic context.source authority.
	if normal, normalErr := pluginapi.ContextScopeFor(gotBlock, decoded.Capabilities); normalErr != nil || len(normal.SourceIDs) != 0 {
		t.Fatal("always-ship acquired normal retrieval scope", normal, normalErr)
	}
}

func TestAlwaysShipDeclarationBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*pluginapi.AlwaysShipSource)
	}{
		{"source traversal", func(s *pluginapi.AlwaysShipSource) { s.ID = "../pins" }},
		{"source too long", func(s *pluginapi.AlwaysShipSource) { s.ID = strings.Repeat("p", 65) }},
		{"empty title", func(s *pluginapi.AlwaysShipSource) { s.Title = "" }},
		{"title too long", func(s *pluginapi.AlwaysShipSource) { s.Title = strings.Repeat("P", 65) }},
		{"newline heading", func(s *pluginapi.AlwaysShipSource) { s.Title = "Pins\n## Policy" }},
		{"non-ASCII heading", func(s *pluginapi.AlwaysShipSource) { s.Title = "Píns" }},
		{"reserved heading", func(s *pluginapi.AlwaysShipSource) { s.Title = "Session Context" }},
		{"padded heading", func(s *pluginapi.AlwaysShipSource) { s.Title = " Pins" }},
		{"markdown heading", func(s *pluginapi.AlwaysShipSource) { s.Title = "[Pins]" }},
		{"qualified tool", func(s *pluginapi.AlwaysShipSource) { s.ListTool = "other.pins_list" }},
		{"empty tool", func(s *pluginapi.AlwaysShipSource) { s.ListTool = "" }},
		{"tool too long", func(s *pluginapi.AlwaysShipSource) { s.ListTool = strings.Repeat("p", 65) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block, _, _ := alwaysShipFixture(t)
			tc.change(&block.Registers.AlwaysShipSources[0])
			if block.Validate() == nil {
				t.Fatal("accepted invalid declaration")
			}
		})
	}
	block, _, _ := alwaysShipFixture(t)
	block.Registers.AlwaysShipSources = append(block.Registers.AlwaysShipSources, block.Registers.AlwaysShipSources[0])
	if block.Validate() == nil {
		t.Fatal("accepted duplicate source")
	}
	block, _, _ = alwaysShipFixture(t)
	block.Registers.ContextSources = []pluginapi.ContextSource{{ID: "pins"}}
	if block.Validate() == nil {
		t.Fatal("accepted duplicated normal/always placement")
	}
	block, _, _ = alwaysShipFixture(t)
	for _, id := range []string{"a", "b", "c", "d"} {
		block.Registers.AlwaysShipSources = append(block.Registers.AlwaysShipSources, pluginapi.AlwaysShipSource{ID: id, Title: "Pins " + id, ListTool: "pins_list"})
	}
	if block.Validate() == nil {
		t.Fatal("accepted excess sources")
	}
	// The host, not a contract decoder without host state, reserves documents
	// while core still renders them.
	source := pluginapi.AlwaysShipSource{ID: "documents", Title: "Session Documents", ListTool: "documents_list"}
	if err := source.Validate(); err != nil {
		t.Fatal("documents contract cannot be declared before host adoption", err)
	}
}

func TestAlwaysShipRequiresOwnReviewedScopeAndReadTool(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*pluginapi.Block, *[]sdkprocess.CapabilityRequest, *[]manifest.Tool)
	}{
		{"missing grant", func(_ *pluginapi.Block, c *[]sdkprocess.CapabilityRequest, _ *[]manifest.Tool) { *c = nil }},
		{"weaker grant", func(_ *pluginapi.Block, c *[]sdkprocess.CapabilityRequest, _ *[]manifest.Tool) {
			(*c)[0].Name = pluginapi.CapabilityContextSource
		}},
		{"optional grant", func(_ *pluginapi.Block, c *[]sdkprocess.CapabilityRequest, _ *[]manifest.Tool) {
			(*c)[0].Optional = true
		}},
		{"duplicate grant", func(_ *pluginapi.Block, c *[]sdkprocess.CapabilityRequest, _ *[]manifest.Tool) {
			*c = append(*c, (*c)[0])
		}},
		{"no declarations", func(b *pluginapi.Block, _ *[]sdkprocess.CapabilityRequest, _ *[]manifest.Tool) {
			b.Registers.AlwaysShipSources = nil
		}},
		{"wrong source", func(b *pluginapi.Block, _ *[]sdkprocess.CapabilityRequest, _ *[]manifest.Tool) {
			b.Registers.AlwaysShipSources[0].ID = "other"
		}},
		{"undeclared tool", func(_ *pluginapi.Block, _ *[]sdkprocess.CapabilityRequest, tools *[]manifest.Tool) { *tools = nil }},
		{"other owner tool", func(_ *pluginapi.Block, _ *[]sdkprocess.CapabilityRequest, tools *[]manifest.Tool) {
			(*tools)[0].Name = "other_list"
		}},
		{"write tool", func(_ *pluginapi.Block, _ *[]sdkprocess.CapabilityRequest, tools *[]manifest.Tool) {
			(*tools)[0].Effect = pluginapi.ToolEffectWrite
		}},
		{"destructive tool", func(_ *pluginapi.Block, _ *[]sdkprocess.CapabilityRequest, tools *[]manifest.Tool) {
			(*tools)[0].Effect = pluginapi.ToolEffectDestructive
		}},
		{"duplicate tool", func(_ *pluginapi.Block, _ *[]sdkprocess.CapabilityRequest, tools *[]manifest.Tool) {
			*tools = append(*tools, (*tools)[0])
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block, capability, tools := alwaysShipFixture(t)
			caps := []sdkprocess.CapabilityRequest{capability}
			tc.change(&block, &caps, &tools)
			if _, err := pluginapi.AlwaysShipScopeFor(block, caps, tools); err == nil {
				t.Fatal("accepted undeclared authority")
			}
		})
	}
	if scope, err := pluginapi.AlwaysShipScopeFor(pluginapi.Block{}, nil, nil); err != nil || len(scope.SourceIDs) != 0 {
		t.Fatal("ordinary manifest acquired always-ship scope", scope, err)
	}
	block, capability, tools := alwaysShipFixture(t)
	block.Registers.AlwaysShipSources = append(block.Registers.AlwaysShipSources, pluginapi.AlwaysShipSource{ID: "other", Title: "Other", ListTool: "pins_list"})
	if _, err := pluginapi.AlwaysShipScopeFor(block, []sdkprocess.CapabilityRequest{capability}, tools); err == nil {
		t.Fatal("approved subset of declarations")
	}
}

func TestAlwaysShipScopeStrictness(t *testing.T) {
	valid := `{"source_ids":["pins"],"all_sessions":true,"max_bytes":6000}`
	if _, err := pluginapi.DecodeAlwaysShipScope(json.RawMessage(valid)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`null`, `{}`, `{"source_ids":["pins"],"max_bytes":6000}`,
		`{"source_ids":["pins"],"all_sessions":true,"session_ids":["s"],"max_bytes":6000}`,
		`{"source_ids":["pins","pins"],"all_sessions":true,"max_bytes":6000}`,
		`{"source_ids":["a","b","c","d","e"],"all_sessions":true,"max_bytes":6000}`,
		`{"source_ids":["../pins"],"all_sessions":true,"max_bytes":6000}`,
		`{"source_ids":["pins"],"session_ids":["s","s"],"max_bytes":6000}`,
		`{"source_ids":["pins"],"session_ids":["../s"],"max_bytes":6000}`,
		`{"source_ids":["pins"],"all_sessions":true,"max_bytes":0}`,
		`{"source_ids":["pins"],"all_sessions":true,"max_bytes":-1}`,
		`{"source_ids":["pins"],"all_sessions":true,"max_bytes":6001}`,
		`{"source_ids":["pins"],"all_sessions":true,"max_bytes":6000,"include_query":true}`,
		`{"source_ids":["pins"],"all_sessions":true,"max_bytes":6000,"MaxBytes":1}`,
		`{"source_ids":["pins"],"all_sessions":true,"max_bytes":6000,"max_bytes":1}`,
		valid + `{}`, strings.Repeat(" ", 1<<20) + valid,
	} {
		if _, err := pluginapi.DecodeAlwaysShipScope(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid scope %.200s", raw)
		}
	}
}

func TestAlwaysShipPrivateRequest(t *testing.T) {
	body := `{"protocol":1,"source_id":"pins","session_id":"session-one","agent_id":"agent-one","intent":"review_session","max_bytes":10}`
	request := func(raw string) *sdkprocess.HTTPRequest {
		return &sdkprocess.HTTPRequest{Method: "POST", Path: pluginapi.AlwaysShipFetchPath, SessionID: "session-one", Body: []byte(raw)}
	}
	got, err := pluginapi.DecodeAlwaysShipRequest(request(body))
	if err != nil || got.MaxBytes != 10 || got.AgentID != "agent-one" || got.Intent != "review_session" {
		t.Fatal("request changed", got, err)
	}
	for _, change := range []func(*sdkprocess.HTTPRequest){
		func(r *sdkprocess.HTTPRequest) { r.Method = "GET" },
		func(r *sdkprocess.HTTPRequest) { r.Path = pluginapi.ContextFetchPath },
		func(r *sdkprocess.HTTPRequest) { r.SessionID = "other" },
		func(r *sdkprocess.HTTPRequest) { r.SessionID = "" },
	} {
		r := request(body)
		change(r)
		if _, err := pluginapi.DecodeAlwaysShipRequest(r); err == nil {
			t.Fatal("accepted wrong transport or session")
		}
	}
	if _, err := pluginapi.DecodeAlwaysShipRequest(nil); err == nil {
		t.Fatal("accepted nil request")
	}
	for _, raw := range []string{
		strings.Replace(body, `"protocol":1`, `"protocol":2`, 1),
		strings.Replace(body, `"max_bytes":10`, `"max_bytes":0`, 1),
		strings.Replace(body, `"max_bytes":10`, `"max_bytes":6001`, 1),
		strings.Replace(body, `"source_id":"pins"`, `"source_id":"../pins"`, 1),
		strings.Replace(body, `"intent":"review_session"`, `"query_text":"secret"`, 1),
		strings.Replace(body, `"max_bytes":10`, `"max_bytes":10,"max_bytes":20`, 1),
		strings.Replace(body, `"agent_id":"agent-one"`, `"agent_id":"`+strings.Repeat("a", 129)+`"`, 1),
		strings.Replace(body, `"intent":"review_session"`, `"intent":"`+strings.Repeat("x", 65)+`"`, 1),
		strings.Replace(body, `"intent":"review_session"`, `"intent":"\u0000"`, 1),
		strings.Replace(body, "agent-one", "\xff", 1),
		body + `{}`, strings.Repeat(" ", pluginapi.MaxAlwaysShipWireBytes) + body,
	} {
		if _, err := pluginapi.DecodeAlwaysShipRequest(request(raw)); err == nil {
			t.Fatalf("accepted invalid request %.200s", raw)
		}
	}
}

func TestAlwaysShipResponseIsExplicitAndBounded(t *testing.T) {
	for _, body := range []string{"", "[pinned] exact bytes\n[pinned:project] more", "☃"} {
		raw, err := json.Marshal(pluginapi.AlwaysShipResponse{Protocol: pluginapi.AlwaysShipProtocol, Body: body})
		if err != nil {
			t.Fatal(err)
		}
		got, err := pluginapi.DecodeAlwaysShipResponse(raw, len(body))
		if err != nil || got.Body != body {
			t.Fatal("body changed", got, err)
		}
		if body != "" {
			if _, err := pluginapi.DecodeAlwaysShipResponse(raw, len(body)-1); err == nil {
				t.Fatal("exceeded assigned BYTE allowance")
			}
		}
	}
	for _, raw := range []string{
		`null`, `{}`, `{"protocol":1}`, `{"protocol":1,"body":null}`, `{"protocol":2,"body":""}`,
		`{"protocol":1,"body":"a","body":"b"}`, `{"protocol":1,"Body":"x"}`,
		`{"protocol":1,"body":"x","items":[]}`, `{"protocol":1,"body":"\u0000"}`,
		`{"protocol":1,"body":""}{}`, "{\"protocol\":1,\"body\":\"\xff\"}",
		`{"protocol":1,"body":"` + strings.Repeat("x", 6001) + `"}`,
		strings.Repeat(" ", pluginapi.MaxAlwaysShipWireBytes) + `{"protocol":1,"body":""}`,
	} {
		if _, err := pluginapi.DecodeAlwaysShipResponse([]byte(raw), 6000); err == nil {
			t.Fatalf("accepted invalid response %.200s", raw)
		}
	}
	for _, budget := range []int{-1, 6001} {
		if _, err := pluginapi.DecodeAlwaysShipResponse([]byte(`{"protocol":1,"body":""}`), budget); err == nil {
			t.Fatal("accepted invalid allowance")
		}
	}
	// Worst-case JSON escaping still fits the wire bound at the body cap.
	body := strings.Repeat("\x01", pluginapi.MaxAlwaysShipBodyBytes)
	raw, _ := json.Marshal(pluginapi.AlwaysShipResponse{Protocol: 1, Body: body})
	if got, err := pluginapi.DecodeAlwaysShipResponse(raw, 6000); err != nil || got.Body != body {
		t.Fatal("valid escaped maximum body failed", err)
	}
}

func TestAlwaysShipLiteralWireContract(t *testing.T) {
	if pluginapi.CapabilityContextAlwaysShip != "context.always_ship" || pluginapi.AlwaysShipFetchPath != "/__nanite/context/always-ship/fetch" || pluginapi.AlwaysShipProtocol != 1 || pluginapi.MaxAlwaysShipWireBytes != 65536 || pluginapi.MaxAlwaysShipBodyBytes != 6000 || pluginapi.MaxAlwaysShipSources != 4 {
		t.Fatal("public always-ship constants changed")
	}
	blockJSON := `{"registers":{"always_ship_sources":[{"id":"pins","title":"Pinned Context","list_tool":"pins_list"}]}}`
	block, err := pluginapi.DecodeBlock(json.RawMessage(blockJSON))
	if err != nil || len(block.Registers.AlwaysShipSources) != 1 || block.Registers.AlwaysShipSources[0] != (pluginapi.AlwaysShipSource{ID: "pins", Title: "Pinned Context", ListTool: "pins_list"}) {
		t.Fatal("literal block changed", block, err)
	}
	encoded, err := pluginapi.EncodeBlock(block)
	if err != nil || string(encoded) != blockJSON {
		t.Fatal("block wire changed", string(encoded), err)
	}
	encoded, err = pluginapi.EncodeBlock(pluginapi.Block{Registers: pluginapi.Registrations{ContextSources: []pluginapi.ContextSource{{ID: "ordinary"}}}})
	if err != nil || string(encoded) != `{"registers":{"context_sources":[{"id":"ordinary"}]}}` {
		t.Fatal("ordinary block gained always_ship_sources", string(encoded), err)
	}
	for _, scopeJSON := range []string{
		`{"source_ids":["pins"],"session_ids":["session-one"],"max_bytes":6000}`,
		`{"source_ids":["pins"],"all_sessions":true,"max_bytes":6000}`,
	} {
		scope, scopeErr := pluginapi.DecodeAlwaysShipScope(json.RawMessage(scopeJSON))
		if scopeErr != nil || len(scope.SourceIDs) != 1 || scope.SourceIDs[0] != "pins" || scope.MaxBytes != 6000 || !scope.Allows("pins", "session-one") || scope.AllSessions != strings.Contains(scopeJSON, "all_sessions") || (!scope.AllSessions && (len(scope.SessionIDs) != 1 || scope.SessionIDs[0] != "session-one")) {
			t.Fatal("literal scope changed", scope, scopeErr)
		}
		raw, marshalErr := json.Marshal(scope)
		if marshalErr != nil || string(raw) != scopeJSON {
			t.Fatal("scope wire changed", string(raw), marshalErr)
		}
	}
	capJSON := `{"name":"context.always_ship","metadata":{"source_ids":["pins"],"session_ids":["session-one"],"max_bytes":6000}}`
	var capRequest sdkprocess.CapabilityRequest
	if err = json.Unmarshal([]byte(capJSON), &capRequest); err != nil {
		t.Fatal(err)
	}
	_, _, tools := alwaysShipFixture(t)
	if _, err = pluginapi.AlwaysShipScopeFor(block, []sdkprocess.CapabilityRequest{capRequest}, tools); err != nil {
		t.Fatal("literal capability rejected", err)
	}
	requestJSON := `{"protocol":1,"source_id":"pins","session_id":"session-one","agent_id":"agent-one","intent":"review_session","max_bytes":10}`
	request, err := pluginapi.DecodeAlwaysShipRequest(&sdkprocess.HTTPRequest{Method: "POST", Path: "/__nanite/context/always-ship/fetch", SessionID: "session-one", Body: []byte(requestJSON)})
	wantRequest := pluginapi.AlwaysShipRequest{Protocol: 1, SourceID: "pins", SessionID: "session-one", AgentID: "agent-one", Intent: "review_session", MaxBytes: 10}
	if err != nil || request != wantRequest {
		t.Fatal("literal request changed", request, err)
	}
	raw, err := json.Marshal(request)
	if err != nil || string(raw) != requestJSON {
		t.Fatal("request wire changed", string(raw), err)
	}
	request.AgentID = ""
	raw, err = json.Marshal(request)
	if err != nil || string(raw) != `{"protocol":1,"source_id":"pins","session_id":"session-one","intent":"review_session","max_bytes":10}` {
		t.Fatal("optional agent_id wire changed", string(raw), err)
	}
	responseJSON := `{"protocol":1,"body":"[pinned] X"}`
	response, err := pluginapi.DecodeAlwaysShipResponse([]byte(responseJSON), 10)
	if err != nil || response != (pluginapi.AlwaysShipResponse{Protocol: 1, Body: "[pinned] X"}) {
		t.Fatal("literal response changed", response, err)
	}
	raw, err = json.Marshal(response)
	if err != nil || string(raw) != responseJSON {
		t.Fatal("response wire changed", string(raw), err)
	}
}

func TestAlwaysShipTitleRules(t *testing.T) {
	for _, title := range []string{" P", "P ", ".P", "-P", "P\nP", "P#P", "P[P", "P]P", "P`P", "P\tP", "P\x00P", "P\x01P", strings.Repeat("P", 65), "session context", "SESSION CONTEXT", "Session  Context", "Session.Context", "Session_Context", "Session-Context", "Session._- Context"} {
		t.Run(title, func(t *testing.T) {
			if err := (pluginapi.AlwaysShipSource{ID: "pins", Title: title, ListTool: "pins_list"}).Validate(); err == nil {
				t.Fatal("accepted invalid or reserved title")
			}
		})
	}
	for _, title := range []string{"P", strings.Repeat("P", 64), "Session Documents", "session_documents"} {
		if err := (pluginapi.AlwaysShipSource{ID: "pins", Title: title, ListTool: "pins_list"}).Validate(); err != nil {
			t.Fatal("rejected allowed title", title, err)
		}
	}
	for _, title := range []string{"Pinned Context", "pinned context", "PINNED CONTEXT", "Pinned  Context", "Pinned.Context", "Pinned_Context", "Pinned-Context", "Pinned._- Context"} {
		block, _, _ := alwaysShipFixture(t)
		block.Registers.AlwaysShipSources = append(block.Registers.AlwaysShipSources, pluginapi.AlwaysShipSource{ID: "other", Title: title, ListTool: "pins_list"})
		if err := block.Validate(); err == nil {
			t.Fatal("accepted ambiguous titles", title)
		}
	}
	// Similar but distinct headings remain legal, and the source count reaches
	// its independent boundary without duplicate titles masking the result.
	block, _, _ := alwaysShipFixture(t)
	for _, id := range []string{"a", "b", "c"} {
		block.Registers.AlwaysShipSources = append(block.Registers.AlwaysShipSources, pluginapi.AlwaysShipSource{ID: id, Title: "Pins " + id, ListTool: "pins_list"})
	}
	if err := block.Validate(); err != nil {
		t.Fatal("four distinct sources rejected", err)
	}
	block.Registers.AlwaysShipSources = append(block.Registers.AlwaysShipSources, pluginapi.AlwaysShipSource{ID: "d", Title: "Pins d", ListTool: "pins_list"})
	if err := block.Validate(); err == nil {
		t.Fatal("fifth source accepted")
	}
}

func TestAlwaysShipScopeRuleBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*pluginapi.Block, *sdkprocess.CapabilityRequest, *[]manifest.Tool)
	}{
		{"invalid block", func(b *pluginapi.Block, _ *sdkprocess.CapabilityRequest, _ *[]manifest.Tool) {
			b.Registers.AlwaysShipSources[0].Title = "#Bad"
		}},
		{"invalid unrelated tool", func(_ *pluginapi.Block, _ *sdkprocess.CapabilityRequest, tools *[]manifest.Tool) {
			*tools = append(*tools, manifest.Tool{Name: "unrelated", Effect: "unknown", InputSchema: json.RawMessage(`{"type":"object"}`)})
		}},
		{"more scope IDs than declarations", func(_ *pluginapi.Block, capRequest *sdkprocess.CapabilityRequest, _ *[]manifest.Tool) {
			capRequest.Metadata = json.RawMessage(`{"source_ids":["pins","extra"],"all_sessions":true,"max_bytes":6000}`)
		}},
		{"required list argument", func(_ *pluginapi.Block, _ *sdkprocess.CapabilityRequest, tools *[]manifest.Tool) {
			(*tools)[0].InputSchema = json.RawMessage(`{"type":"object","properties":{"page":{"type":"integer"}},"required":["page"]}`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block, capRequest, tools := alwaysShipFixture(t)
			tc.change(&block, &capRequest, &tools)
			if _, err := pluginapi.AlwaysShipScopeFor(block, []sdkprocess.CapabilityRequest{capRequest}, tools); err == nil {
				t.Fatal("accepted invalid scope or list tool")
			}
		})
	}
	block, capRequest, tools := alwaysShipFixture(t)
	tools[0].InputSchema = json.RawMessage(`{"type":"object","properties":{"page":{"type":"integer"}},"required":[]}`)
	if _, err := pluginapi.AlwaysShipScopeFor(block, []sdkprocess.CapabilityRequest{capRequest}, tools); err != nil {
		t.Fatal("optional pagination rejected", err)
	}
	valid := `{"source_ids":["pins"],"all_sessions":true,"max_bytes":6000}`
	atLimit := strings.Repeat(" ", 16384-len(valid)) + valid
	if _, err := pluginapi.DecodeAlwaysShipScope(json.RawMessage(atLimit)); err != nil {
		t.Fatal("16 KiB scope rejected", err)
	}
	if scope, err := pluginapi.DecodeAlwaysShipScope(json.RawMessage(" " + atLimit)); err == nil || len(scope.SourceIDs) != 0 || scope.MaxBytes != 0 || scope.AllSessions {
		t.Fatal("16 KiB+1 scope accepted or nonzero on failure", scope, err)
	}
	for _, invalid := range []string{`{"source_ids":["pins"],"all_sessions":true,"max_bytes":0}`, `{"source_ids":["pins"],"max_bytes":6000}`, `{"source_ids":["../pins"],"all_sessions":true,"max_bytes":6000}`, `{"source_ids":["pins"],"session_ids":["../s"],"max_bytes":6000}`} {
		scope, err := pluginapi.DecodeAlwaysShipScope(json.RawMessage(invalid))
		if err == nil || len(scope.SourceIDs) != 0 || len(scope.SessionIDs) != 0 || scope.AllSessions || scope.MaxBytes != 0 || !strings.Contains(err.Error(), "always-ship scope") || strings.Contains(err.Error(), "context scope") {
			t.Fatal("invalid scope returned authority or wrong diagnostic", scope, err)
		}
	}
	scope, err := pluginapi.DecodeAlwaysShipScope(json.RawMessage(valid))
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{"../s", "s/s", "s s", "s\n", "s\x00", "☃", "", strings.Repeat("s", 129)} {
		if scope.Allows("pins", session) {
			t.Fatal("invalid session authorized", session)
		}
		raw, marshalErr := json.Marshal(pluginapi.AlwaysShipRequest{Protocol: 1, SourceID: "pins", SessionID: session, Intent: "review_session", MaxBytes: 10})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if _, err := pluginapi.DecodeAlwaysShipRequest(&sdkprocess.HTTPRequest{Method: "POST", Path: "/__nanite/context/always-ship/fetch", SessionID: session, Body: raw}); err == nil {
			t.Fatal("invalid request session accepted", session)
		}
	}
}

func TestAlwaysShipHintsRejectControlsButBodyPreservesText(t *testing.T) {
	for control := rune(0); control <= 0x9f; control++ {
		if control > 0x1f && control < 0x7f {
			continue
		}
		for _, field := range []string{"agent_id", "intent"} {
			request := pluginapi.AlwaysShipRequest{Protocol: 1, SourceID: "pins", SessionID: "s", AgentID: "agent", Intent: "review_session", MaxBytes: 10}
			if field == "agent_id" {
				request.AgentID = "a" + string(control) + "b"
			} else {
				request.Intent = "a" + string(control) + "b"
			}
			raw, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pluginapi.DecodeAlwaysShipRequest(&sdkprocess.HTTPRequest{Method: "POST", Path: "/__nanite/context/always-ship/fetch", SessionID: "s", Body: raw}); err == nil {
				t.Fatalf("accepted %s control U+%04X", field, control)
			}
		}
	}
	for _, body := range []string{"\x1b[31mred\x1b[0m", "one\r\ntwo", "\n## X"} {
		raw, err := json.Marshal(pluginapi.AlwaysShipResponse{Protocol: 1, Body: body})
		if err != nil {
			t.Fatal(err)
		}
		if got, err := pluginapi.DecodeAlwaysShipResponse(raw, len(body)); err != nil || got.Body != body {
			t.Fatal("body was sanitized", got, err)
		}
	}
}
