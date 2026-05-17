package service

import "encoding/json"

// EnvelopeDisplayClass classifies how a standalone plugin-envelope should be
// rendered by the frontend transcript.
type EnvelopeDisplayClass string

const (
	EnvelopeDisplayClassContent        EnvelopeDisplayClass = "content"
	EnvelopeDisplayClassAlert          EnvelopeDisplayClass = "alert"
	EnvelopeDisplayClassActionRequired EnvelopeDisplayClass = "action-required"
)

// stampEnvelopeDisplayClass injects display_class into an already-marshalled
// envelope object when absent. Legacy emit paths stream whole envelopes rather
// than the plugin-envelope wrapper, so they need a post-marshal seam.
func stampEnvelopeDisplayClass(raw string, class EnvelopeDisplayClass) string {
	if raw == "" || class == "" {
		return raw
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return raw
	}
	if _, ok := env["display_class"]; ok {
		return raw
	}
	env["display_class"] = string(class)
	out, err := json.Marshal(env)
	if err != nil {
		return raw
	}
	return string(out)
}
