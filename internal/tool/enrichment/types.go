// Package enrichment provides per-tool hint metadata and system-prompt override
// composition for the agent harness (CW-20260420-0006, P1 ToolSurface).
package enrichment

import (
	"encoding/json"
	"fmt"
)

// Hints carries tool-specific guidance surfaced to the LLM via the broker's
// override block. Stored externally to the tool.Tool interface so Hints can be
// updated via data / probe-agent enrichment (CW-20260419-0024) without touching
// tool implementations. Each field is optional.
type Hints struct {
	// Preconditions are assertions the agent should verify before invoking
	// the tool ("Call list/search first to get a real ID").
	Preconditions []string `json:"preconditions,omitempty"`

	// AntiPatterns are ways the agent commonly misuses the tool that the
	// broker wants to prevent ("IDs are ULIDs, not file paths").
	AntiPatterns []string `json:"anti_patterns,omitempty"`

	// ChainsWith lists other tools this one is commonly paired with
	// ("dev_glob before dev_read").
	ChainsWith []string `json:"chains_with,omitempty"`

	// OutputShape is a one-line description of what the tool returns,
	// so the agent can plan the next call without re-reading the full result.
	OutputShape string `json:"output_shape,omitempty"`
}

// IsEmpty returns true if none of the hint fields carry content.
func (h Hints) IsEmpty() bool {
	return len(h.Preconditions) == 0 &&
		len(h.AntiPatterns) == 0 &&
		len(h.ChainsWith) == 0 &&
		h.OutputShape == ""
}

// MarshalHints encodes Hints to the JSON string stored in tool_enrichments.hints_json.
func MarshalHints(h Hints) (string, error) {
	b, err := json.Marshal(h)
	if err != nil {
		return "", fmt.Errorf("marshal hints: %w", err)
	}
	return string(b), nil
}

// UnmarshalHints decodes the stored JSON into a Hints struct. Empty object ("{}")
// yields a zero-value Hints with no error.
func UnmarshalHints(s string) (Hints, error) {
	if s == "" {
		return Hints{}, nil
	}
	var h Hints
	if err := json.Unmarshal([]byte(s), &h); err != nil {
		return Hints{}, fmt.Errorf("unmarshal hints: %w", err)
	}
	return h, nil
}
