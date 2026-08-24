// Package structuredmessage contains shared helpers for persisted
// StructuredMessage-shaped content without depending on internal/chat.
package structuredmessage

import (
	"encoding/json"
	"strings"
)

type persisted struct {
	Version int    `json:"v"`
	Text    string `json:"text"`
}

// UnwrapText returns the text from a StructuredMessage-shaped JSON payload.
func UnwrapText(content string) (string, bool) {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "{") {
		return "", false
	}
	var sm persisted
	if err := json.Unmarshal([]byte(trimmed), &sm); err != nil || sm.Version <= 0 {
		return "", false
	}
	return sm.Text, true
}
