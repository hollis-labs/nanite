package mcpserver

import (
	"testing"

	condmcp "github.com/hollis-labs/nanite/internal/mcp"
)

func TestExtractText(t *testing.T) {
	result := &condmcp.ToolResult{
		Content: []condmcp.ToolContent{
			{Type: "text", Text: "Hello"},
			{Type: "text", Text: "World"},
		},
	}
	got := extractText(result)
	if got != "Hello\nWorld" {
		t.Errorf("expected 'Hello\\nWorld', got %q", got)
	}
}

func TestExtractText_Nil(t *testing.T) {
	got := extractText(nil)
	if got != "" {
		t.Errorf("expected empty string for nil, got %q", got)
	}
}

func TestExtractText_NoTextBlocks(t *testing.T) {
	result := &condmcp.ToolResult{
		Content: []condmcp.ToolContent{
			{Type: "image", Text: ""},
		},
	}
	got := extractText(result)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestConvertEnvelopeMarkers(t *testing.T) {
	input := `Here you go!
<!--ENVELOPE_DATA:{"kind":"envelope","version":1,"type":"info-card","data":{"title":"cat"}}:ENVELOPE_DATA-->`

	got := convertEnvelopeMarkers(input)

	expected := "Here you go!\n\n\n```nanite-envelope\n{\"kind\":\"envelope\",\"version\":1,\"type\":\"info-card\",\"data\":{\"title\":\"cat\"}}\n```"
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestConvertEnvelopeMarkers_NoMarkers(t *testing.T) {
	input := "Just plain text with no markers."
	got := convertEnvelopeMarkers(input)
	if got != input {
		t.Errorf("expected unchanged text, got %q", got)
	}
}

func TestConvertEnvelopeMarkers_Multiple(t *testing.T) {
	input := `First <!--ENVELOPE_DATA:{"a":1}:ENVELOPE_DATA--> and <!--ENVELOPE_DATA:{"b":2}:ENVELOPE_DATA-->`
	got := convertEnvelopeMarkers(input)

	if got == input {
		t.Error("expected markers to be converted")
	}
	// Both should be converted.
	if count := countOccurrences(got, "```nanite-envelope"); count != 2 {
		t.Errorf("expected 2 nanite-envelope blocks, got %d", count)
	}
}

// TestConvertEnvelopeMarkers_MalformedMarkerHaltsLoop is
// TASKS/harness-reactive-self-tools/06-collapse-envelope-marker-consumers.md's
// regression proof for this call site: convertEnvelopeMarkers no longer
// hand-scans for the marker itself — it delegates to
// chat.ReplaceEnvelopeMarkers (internal/chat/envelope_marker.go), the same
// shared machinery internal/service/chat_generate.go's captureEnvelopeData
// and internal/api/tools_call.go's extractEnvelopeMarker delegate to (via
// chat.ExtractEnvelopeMarker). This exercises convertEnvelopeMarkers' own
// real call-site behavior — a malformed marker (an opening delimiter with
// no closing delimiter anywhere later in the text) halts the replace loop
// entirely, leaving the text unconverted — matching this function's
// pre-collapse behavior exactly.
func TestConvertEnvelopeMarkers_MalformedMarkerHaltsLoop(t *testing.T) {
	input := `<!--ENVELOPE_DATA:{"truncated":true} no closing delimiter anywhere in this text`
	got := convertEnvelopeMarkers(input)
	if got != input {
		t.Errorf("got:\n%s\nwant unchanged (malformed leading marker halts the loop):\n%s", got, input)
	}
}

func countOccurrences(s, substr string) int {
	count := 0
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			count++
		}
	}
	return count
}
