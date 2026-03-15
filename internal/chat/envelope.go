package chat

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Envelope represents a structured envelope block embedded in assistant messages.
type Envelope struct {
	Kind      string     `json:"kind"`
	Version   int        `json:"version"`
	Type      string     `json:"type"`                // standard, conduit, custom
	Proposals []Proposal `json:"proposals,omitempty"`
	Questions []Question `json:"questions,omitempty"`
	Status    *Status    `json:"status,omitempty"`
}

// Proposal represents a proposed action within an envelope.
type Proposal struct {
	Type    string         `json:"type"`             // create_task, update_sprint, etc.
	Payload map[string]any `json:"payload"`
	Schema  map[string]any `json:"schema,omitempty"`
}

// Question represents an interactive question within an envelope.
type Question struct {
	Prompt   string   `json:"prompt"`
	Type     string   `json:"type"`              // text, textarea, select, radio, checkbox
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required"`
	Default  string   `json:"default,omitempty"`
}

// Status represents a progress status within an envelope.
type Status struct {
	Phase    string  `json:"phase"`
	Progress float64 `json:"progress"`
}

// envelopePattern matches fenced code blocks with volon-envelope or conduit-envelope language tags.
var envelopePattern = regexp.MustCompile("(?s)```(?:volon-envelope|conduit-envelope)\\s*\n(.*?)```")

// ParseEnvelopes extracts envelope blocks from assistant message content.
// Returns the parsed envelopes and the remaining text with envelope blocks removed.
func ParseEnvelopes(content string) ([]Envelope, string) {
	matches := envelopePattern.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return nil, content
	}

	var envelopes []Envelope
	var cleaned strings.Builder
	lastEnd := 0

	for _, match := range matches {
		// match[0]:match[1] is the full match, match[2]:match[3] is the captured JSON group.
		cleaned.WriteString(content[lastEnd:match[0]])
		lastEnd = match[1]

		jsonContent := strings.TrimSpace(content[match[2]:match[3]])
		var env Envelope
		if err := json.Unmarshal([]byte(jsonContent), &env); err == nil {
			envelopes = append(envelopes, env)
		}
	}
	cleaned.WriteString(content[lastEnd:])

	// Trim any extra whitespace left by removal.
	cleanedStr := strings.TrimSpace(cleaned.String())

	return envelopes, cleanedStr
}
