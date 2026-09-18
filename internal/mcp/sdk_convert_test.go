package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestConvertSDKCallResultKeepsNonTextContent(t *testing.T) {
	res := &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: "before"},
			&sdkmcp.ImageContent{Data: []byte("pretend-png"), MIMEType: "image/png"},
		},
	}
	got := convertSDKCallResult(res)
	if len(got.Content) != 2 {
		t.Fatalf("content = %+v, want both blocks — a dropped block reaches the agent as a shorter answer, not as a gap", got.Content)
	}
	if got.Content[0].Text != "before" {
		t.Errorf("first block = %+v", got.Content[0])
	}
	if !strings.Contains(got.Content[1].Text, "image/png") {
		t.Errorf("second block = %+v, want the image block's JSON", got.Content[1])
	}
}

// A structured-only result has its payload nowhere else; the agent surface is
// text, so it has to be rendered rather than returned as an empty success.
func TestConvertSDKCallResultRendersStructuredOnlyResults(t *testing.T) {
	res := &sdkmcp.CallToolResult{StructuredContent: map[string]any{"employee_id": "42"}}
	got := convertSDKCallResult(res)
	if len(got.Content) != 1 {
		t.Fatalf("content = %+v, want one rendered block", got.Content)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(got.Content[0].Text), &decoded); err != nil {
		t.Fatalf("rendered block is not JSON: %q", got.Content[0].Text)
	}
	if decoded["employee_id"] != "42" {
		t.Errorf("rendered block = %q", got.Content[0].Text)
	}
}
