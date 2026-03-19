package chat

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Envelope represents a structured envelope block embedded in assistant messages.
type Envelope struct {
	Kind      string         `json:"kind"`
	Version   int            `json:"version"`
	Type      string         `json:"type"`                // standard, conduit, custom
	Proposals []Proposal     `json:"proposals,omitempty"`
	Questions []Question     `json:"questions,omitempty"`
	Status    *Status        `json:"status,omitempty"`
	Data      map[string]any `json:"data,omitempty"`      // custom payload for plugin envelopes
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

// buildKBEnvelope transforms a search_kb JSON result into a conduit-envelope JSON string.
// The search result is {"results":[...],"query":"...","total_results":N}.
// The envelope wraps it as {"kind":"envelope","version":1,"type":"kb-result","data":{...}}.
func buildKBEnvelope(searchResult string) string {
	// The search result may have a trailing [SYSTEM: ...] instruction — strip it.
	jsonEnd := strings.LastIndex(searchResult, "}")
	if jsonEnd < 0 {
		return ""
	}
	jsonPart := searchResult[:jsonEnd+1]

	// Parse the search result to sanitize body fields.
	// Body content may contain triple backticks (markdown code fences)
	// which break the conduit-envelope fence delimiter.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(jsonPart), &parsed); err != nil {
		return ""
	}

	// Sanitize body fields — replace ``` with ~~~ in article bodies
	// so they don't break the envelope fence.
	if results, ok := parsed["results"].([]any); ok {
		for _, r := range results {
			if article, ok := r.(map[string]any); ok {
				if body, ok := article["body"].(string); ok {
					article["body"] = strings.ReplaceAll(body, "```", "~~~")
				}
			}
		}
	}

	env := map[string]any{
		"kind":    "envelope",
		"version": 1,
		"type":    "kb-result",
		"data":    parsed,
	}
	data, err := json.Marshal(env)
	if err != nil {
		return ""
	}
	return string(data)
}

// buildTicketConfirmationEnvelope wraps ticket JSON as a ticket-confirmation envelope.
func buildTicketConfirmationEnvelope(ticketJSON string) string {
	var ticket map[string]any
	if err := json.Unmarshal([]byte(ticketJSON), &ticket); err != nil {
		return ""
	}
	env := map[string]any{
		"kind":    "envelope",
		"version": 1,
		"type":    "ticket-confirmation",
		"data":    map[string]any{"ticket": ticket},
	}
	data, err := json.Marshal(env)
	if err != nil {
		return ""
	}
	return string(data)
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
