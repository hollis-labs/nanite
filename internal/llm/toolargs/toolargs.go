// Package toolargs normalizes streamed tool-call argument payloads emitted by
// provider adapters.
package toolargs

import "encoding/json"

// ParseObject parses a streamed tool-call argument JSON object. Malformed
// provider JSON degrades to a raw payload so one bad tool call does not abort
// the rest of the turn.
func ParseObject(raw string) map[string]any {
	if raw == "" {
		return map[string]any{}
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return map[string]any{"_raw": raw}
	}
	return input
}
