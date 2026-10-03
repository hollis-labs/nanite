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
		block.Registers.AlwaysShipSources = append(block.Registers.AlwaysShipSources, pluginapi.AlwaysShipSource{ID: id, Title: "Pins", ListTool: "pins_list"})
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
