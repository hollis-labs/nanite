package redaction

import (
	"strings"
	"testing"

	"github.com/hollis-labs/libs/security/redact"
)

func TestTelemetryTextFailsClosed(t *testing.T) {
	for _, input := range []string{
		string([]byte{0xff, 0xfe}),
		strings.Repeat("a", redact.DefaultMaxBytes+1),
		strings.Repeat("\"", redact.DefaultMaxBytes), // escaping exceeds the scalar limit
	} {
		if got := TelemetryText(input); got != redact.Marker {
			t.Fatal("invalid or oversized telemetry was not replaced with a fixed marker")
		}
	}
}

func TestResolvedCredentialScopeProtectsTelemetryAndDiagnostics(t *testing.T) {
	const secret = "arbitrary-provider-credential-value"
	Sink{}.Add(secret)
	for _, scrub := range []func(string) string{TelemetryText, Credentials().Text} {
		if got := scrub("diagnostic " + secret); strings.Contains(got, secret) || !strings.Contains(got, "diagnostic") {
			t.Fatal("known credential not scrubbed while preserving diagnostic text")
		}
	}
}
