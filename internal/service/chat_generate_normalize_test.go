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
// clone landed in the tools slice DOES have additionalProperties:false injected
// at every object node. This confirms the Anthropic strict-mode contract is
// preserved while the source map stays loose.
func TestNormalizeToolInputSchemas_ProviderFacingNormalization(t *testing.T) {
	root := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"data": map[string]any{
				"type": "object",
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
		t.Fatalf("normalized properties.data: expected additionalProperties=false, got %v (ok=%v)", v, ok)
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
