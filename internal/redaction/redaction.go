// Package redaction composes Nanite's shared credential redactor and telemetry
// sanitizer around one process-wide set of resolved secret values.
package redaction

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/hollis-labs/libs/security/redact"
)

var scope = redact.NewScope()

var credentials = func() *redact.Redactor {
	r, err := redact.New(redact.WithScope(scope))
	if err != nil {
		panic(err)
	}
	return r
}()

var telemetry = func() *redact.Sanitizer {
	s, err := redact.NewSanitizer(redact.Policy{Mode: redact.Denylist}, redact.WithScope(scope))
	if err != nil {
		panic(err)
	}
	return s
}()

// Sink adapts the secrets resolver's value notification to the shared scope.
type Sink struct{}

// Add registers known values without exposing their contents in diagnostics.
func (Sink) Add(values ...string) {
	for _, value := range values {
		scope.Add("resolved credential", value)
	}
}

// Credentials scrubs credential patterns and values learned from resolvers.
func Credentials() *redact.Redactor { return credentials }

// TelemetryText sanitizes a JSON scalar so mandatory PII detection also runs.
// Invalid or oversized values produce a fixed marker, never an input-derived
// error. Prompt inclusion remains the exporter's separate key policy.
func TelemetryText(value string) string {
	if len(value) > redact.DefaultMaxBytes || !utf8.ValidString(value) {
		return redact.Marker
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return redact.Marker
	}
	clean, report := telemetry.Sanitize(raw)
	if report.ErrorClass != "" {
		return redact.Marker
	}
	var result string
	if err := json.Unmarshal(clean, &result); err != nil {
		return redact.Marker
	}
	return result
}
