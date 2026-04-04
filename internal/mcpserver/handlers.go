package mcpserver

import (
	"encoding/json"
	"strings"

	condmcp "github.com/hollis-labs/nanite/internal/mcp"
)

// extractText concatenates all text content blocks from a ToolResult.
func extractText(result *condmcp.ToolResult) string {
	if result == nil {
		return ""
	}
	var sb strings.Builder
	for _, c := range result.Content {
		if c.Type == "text" && c.Text != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(c.Text)
		}
	}
	return sb.String()
}

// convertEnvelopeMarkers replaces <!--ENVELOPE_DATA:{...}:ENVELOPE_DATA-->
// markers with ```nanite-envelope fenced blocks so the engine's
// ParseEnvelopes() can extract them from streamed PTY output.
func convertEnvelopeMarkers(text string) string {
	const startTag = "<!--ENVELOPE_DATA:"
	const endTag = ":ENVELOPE_DATA-->"

	for {
		start := strings.Index(text, startTag)
		if start < 0 {
			break
		}
		tail := text[start+len(startTag):]
		end := strings.Index(tail, endTag)
		if end < 0 {
			break
		}
		payload := tail[:end]
		replacement := "\n\n```nanite-envelope\n" + payload + "\n```"
		text = text[:start] + replacement + tail[end+len(endTag):]
	}
	return text
}

// mustMarshalSchema marshals a tool input schema to JSON bytes.
// Falls back to an empty object on error.
func mustMarshalSchema(schema map[string]any) []byte {
	if schema == nil {
		return []byte(`{"type":"object","properties":{}}`)
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return []byte(`{"type":"object","properties":{}}`)
	}
	return data
}
