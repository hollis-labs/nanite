package envelope

import (
	"strings"
	"testing"
)

// TestValidateData_ReportCard_ErrorReferencesStableURI is the regression for
// the bug surfaced in chat session c107: ValidateData returned a schema
// location like 'file:///Users/.../nanite/report-card.schema.json' (or a
// $HOME-redacted 'file://~/...') that leaked the binary's working directory
// into the user-facing error and falsely implied the schema was loaded from
// disk. Schemas are //go:embed-ed; the error location should be a stable,
// in-memory URI that does not change between dev / deploy hosts.
func TestValidateData_ReportCard_ErrorReferencesStableURI(t *testing.T) {
	// report-card requires `metrics`; omit it so validation fails and we can
	// inspect the error message.
	bad := map[string]any{"title": "X"}

	err := ValidateData("report-card", bad)
	if err == nil {
		t.Fatalf("expected validation error for report-card without metrics, got nil")
	}
	msg := err.Error()

	// The error must NOT carry a file:// URL — that would mean the schema
	// resource URI is filesystem-rooted and leaks cwd into the message.
	if strings.Contains(msg, "file://") {
		t.Errorf("error message must not reference a file:// URL (schemas are embedded), got: %s", msg)
	}

	// The error must still identify which envelope type failed.
	if !strings.Contains(msg, "report-card") {
		t.Errorf("error message should identify the envelope type, got: %s", msg)
	}
}

// TestValidateData_AllPassiveRenderables_NoFileURLInErrors covers the same
// guarantee for every type the agent can emit through nanite_show_card. Any
// of these surfacing a filesystem URL would re-introduce the same UX bug.
func TestValidateData_AllPassiveRenderables_NoFileURLInErrors(t *testing.T) {
	for _, envType := range PassiveRenderableTypes {
		envType := envType
		t.Run(envType, func(t *testing.T) {
			// Empty object will fail validation for every type that has
			// required fields (which is all of them in the v1 allow-list).
			err := ValidateData(envType, map[string]any{})
			if err == nil {
				// Some schemas may accept {} — that's a separate concern.
				t.Skipf("type %q accepts empty object; cannot exercise error path", envType)
			}
			if strings.Contains(err.Error(), "file://") {
				t.Errorf("type %q error leaks file:// URL: %s", envType, err.Error())
			}
		})
	}
}
