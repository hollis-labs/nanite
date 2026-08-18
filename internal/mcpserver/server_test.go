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

func TestMustMarshalSchema(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	}
	data := mustMarshalSchema(schema)
	if len(data) == 0 {
		t.Error("expected non-empty schema bytes")
	}
}

func TestMustMarshalSchema_Nil(t *testing.T) {
	data := mustMarshalSchema(nil)
	if string(data) != `{"type":"object","properties":{}}` {
		t.Errorf("expected fallback schema, got %s", string(data))
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
