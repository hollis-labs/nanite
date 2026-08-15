package service

import (
	"reflect"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/mcp"
)

// TestNormalizeToolInputSchemas_SourceMapInvariance verifies that
// normalizeToolInputSchemas does NOT mutate the InputSchema map (or any of its
// nested maps) handed in by the caller. This is the core fix for
// CW-20260429-0017: BuiltinToolRegistry holds these maps by reference, so any
// in-place mutation would corrupt the canonical schema for subsequent harness
// arg-validation passes.
func TestNormalizeToolInputSchemas_SourceMapInvariance(t *testing.T) {
	dataNode := map[string]any{
		"type": "object",
	}
	props := map[string]any{
		"data": dataNode,
	}
	root := map[string]any{
		"type":       "object",
		"properties": props,
	}

	tools := []llmtypes.ToolDefinition{
		{Name: "fake_tool", InputSchema: root},
	}
	normalizeToolInputSchemas(tools)

	// Original `data` sub-map must remain loose: no additionalProperties key.
	if _, exists := dataNode["additionalProperties"]; exists {
		t.Fatalf("source dataNode was mutated: got additionalProperties=%v, expected key absent",
			dataNode["additionalProperties"])
	}
	// Original root must also remain loose.
	if _, exists := root["additionalProperties"]; exists {
		t.Fatalf("source root was mutated: got additionalProperties=%v, expected key absent",
			root["additionalProperties"])
	}
	// Properties map identity should still match the original — the same
	// underlying map header, not a freshly-allocated copy substituted in.
	gotProps, ok := root["properties"].(map[string]any)
	if !ok {
		t.Fatal("source root properties unexpectedly missing or wrong type after normalize")
	}
	if reflect.ValueOf(gotProps).Pointer() != reflect.ValueOf(props).Pointer() {
		t.Fatal("source root properties map was replaced (expected original map identity)")
	}
}

// TestNormalizeToolInputSchemas_ProviderFacingNormalization verifies that the
// clone landed in the tools slice DOES have additionalProperties:false
// injected at every object node THAT ENUMERATES ITS OWN PROPERTIES. This
// confirms the closing behavior is preserved for genuinely-fixed-shape
// objects while the source map stays loose (clone, not in-place mutation).
//
// Prior to CW-20260815-0016 this test's `data` fixture had NO `properties`
// key and still asserted additionalProperties:false — that was itself the
// bug this ticket fixed (see
// TestNormalizeToolInputSchemas_PropertyLessNodeNotClosed below): closing a
// property-less node makes it accept only {}, not "closed against
// surprises" as intended.
func TestNormalizeToolInputSchemas_ProviderFacingNormalization(t *testing.T) {
	root := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"data": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{"type": "string"},
				},
			},
		},
	}
	tools := []llmtypes.ToolDefinition{
		{Name: "fake_tool", InputSchema: root},
	}
	normalizeToolInputSchemas(tools)

	got := tools[0].InputSchema
	if got == nil {
		t.Fatal("tools[0].InputSchema is nil after normalize")
	}
	// Clone identity: the slice's InputSchema must point at a fresh map,
	// not the same underlying map header the caller passed in. Map values
	// in Go can't be compared with ==, so probe identity via the runtime
	// pointer.
	if reflect.ValueOf(got).Pointer() == reflect.ValueOf(root).Pointer() {
		t.Fatal("tools[0].InputSchema was not cloned (same map identity as source)")
	}
	if v, ok := got["additionalProperties"]; !ok || v != false {
		t.Fatalf("normalized root: expected additionalProperties=false, got %v (ok=%v)", v, ok)
	}
	props, ok := got["properties"].(map[string]any)
	if !ok {
		t.Fatalf("normalized root: properties map missing, got %T", got["properties"])
	}
	dataChild, ok := props["data"].(map[string]any)
	if !ok {
		t.Fatalf("normalized properties.data missing, got %T", props["data"])
	}
	if v, ok := dataChild["additionalProperties"]; !ok || v != false {
		t.Fatalf("normalized properties.data (has its own properties): expected additionalProperties=false, got %v (ok=%v)", v, ok)
	}
}

// TestNormalizeToolInputSchemas_PropertyLessNodeNotClosed is the
// CW-20260815-0016 regression test. A "type":"object" node with NO
// "properties" key is a free-form/pass-through container by construction
// (mux_call's "arguments", card_show's "data", dispatch_executor's "data",
// several workflow_* tools' "args"/"params" — confirmed via an audit of the
// live self-tool/dev-tool catalog during this fix). Forcing
// additionalProperties:false on such a node whitelists nothing (no
// properties key) and admits nothing (additionalProperties:false) — the
// node becomes satisfiable ONLY by the empty object {}. Before the fix,
// this collapsed exactly the ARG_VALIDATION_FAILED failure mode observed
// on a live Orchestrator session's mux_call attempts
// ("/arguments: got string, want object" — even a well-formed object
// argument had no valid shape left to match).
func TestNormalizeToolInputSchemas_PropertyLessNodeNotClosed(t *testing.T) {
	root := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tool_name": map[string]any{"type": "string"},
			"arguments": map[string]any{
				"type":        "object",
				"description": "Arguments object matching the tool's input schema",
			},
		},
	}
	tools := []llmtypes.ToolDefinition{
		{Name: "mux_call", InputSchema: root},
	}
	normalizeToolInputSchemas(tools)

	got := tools[0].InputSchema
	props, ok := got["properties"].(map[string]any)
	if !ok {
		t.Fatalf("normalized root: properties map missing, got %T", got["properties"])
	}
	argsChild, ok := props["arguments"].(map[string]any)
	if !ok {
		t.Fatalf("normalized properties.arguments missing, got %T", props["arguments"])
	}
	if v, exists := argsChild["additionalProperties"]; exists {
		t.Fatalf("property-less node was closed: additionalProperties=%v (expected key absent — "+
			"this collapses the node to accepting only {})", v)
	}
	if _, hasProps := argsChild["properties"]; hasProps {
		t.Fatal("property-less node unexpectedly gained a properties key from normalization")
	}
	// The enclosing root, which DOES enumerate its properties, must still
	// be closed — this fix is scoped to property-less nodes only.
	if v, ok := got["additionalProperties"]; !ok || v != false {
		t.Fatalf("root (has properties) should still be closed: additionalProperties=%v (ok=%v)", v, ok)
	}
}

// TestNormalizeToolInputSchemas_ShowCardC107Regression is the c107/c109
// regression guard. It runs normalizeToolInputSchemas against the live
// card_show definition pulled from mcp.SelfToolProviderDefinitions, then
// validates a realistic report-card payload against the SHARED schema map (the
// one BuiltinToolRegistry / GetToolSchema reads). Before the fix, the shared
// map's `data` node had been silently closed and the validator rejected every
// inner field. After the fix, the shared map stays loose and validation passes.
func TestNormalizeToolInputSchemas_ShowCardC107Regression(t *testing.T) {
	defs := mcp.SelfToolProviderDefinitions()
	var liveSchema map[string]any
	for _, d := range defs {
		if d.Name == "card_show" {
			liveSchema = d.InputSchema
			break
		}
	}
	if liveSchema == nil {
		t.Fatal("card_show not found in mcp.SelfToolProviderDefinitions()")
	}

	// Hold a reference to the original `data` sub-schema BEFORE normalize runs.
	// This is the canonical loose object the show_card handler relies on.
	rootProps, _ := liveSchema["properties"].(map[string]any)
	if rootProps == nil {
		t.Fatal("live card_show schema has no properties map")
	}
	originalDataNode, _ := rootProps["data"].(map[string]any)
	if originalDataNode == nil {
		t.Fatal("live card_show schema has no data property")
	}

	tools := []llmtypes.ToolDefinition{
		{Name: "card_show", InputSchema: liveSchema},
	}
	// Run normalize twice — the bug surfaced after the FIRST chat call, but
	// running it twice is the strongest invariance assertion.
	normalizeToolInputSchemas(tools)
	normalizeToolInputSchemas(tools)

	if _, exists := originalDataNode["additionalProperties"]; exists {
		t.Fatalf("card_show.data was mutated by normalizeToolInputSchemas: "+
			"additionalProperties=%v (expected key absent — this is the c107/c109 bug)",
			originalDataNode["additionalProperties"])
	}

	// Validate a realistic report-card payload against the SHARED (canonical)
	// schema map. This mirrors what GetToolSchema feeds into argValidator on
	// every harness arg-validation pass.
	v := newArgValidator()
	args := map[string]any{
		"type": "report-card",
		"data": map[string]any{
			"title": "x",
			"metrics": []any{
				map[string]any{"label": "alpha", "value": 1},
			},
		},
	}
	if msg := v.validate("card_show", liveSchema, args); msg != "" {
		t.Fatalf("c107/c109 regression: shared card_show schema rejects valid "+
			"report-card payload after normalize: %s", msg)
	}
}
