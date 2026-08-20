package mcpserver

import (
	"encoding/json"
	"strings"

	"github.com/hollis-labs/nanite/internal/chat"
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
//
// The delimiter scan itself delegates to chat.ReplaceEnvelopeMarkers, the
// shared marker-extraction machinery three independent hand-scans (this
// one, internal/service/chat_generate.go's captureEnvelopeData,
// internal/api/tools_call.go's extractEnvelopeMarker) collapsed onto
// (TASKS/harness-reactive-self-tools/06-collapse-envelope-marker-consumers.md).
// This function's own remaining job — replacing every marker occurrence
// with a fenced code block, rather than just reading the first payload out
// — is call-site-specific, not duplicated scan logic; it's why this caller
// uses ReplaceEnvelopeMarkers instead of the single-marker
// ExtractEnvelopeMarker the other two use.
func convertEnvelopeMarkers(text string) string {
	return chat.ReplaceEnvelopeMarkers(text, func(payload string) string {
		return "\n\n```nanite-envelope\n" + payload + "\n```"
	})
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
