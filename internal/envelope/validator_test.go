package envelope

import (
	"strings"
	"testing"
)

// TestValidateEnvelopeData_ReportCardHappyPath confirms the structured
// validator returns nil errors for a well-formed report-card payload —
// the same shape the ticket's "valid: true" acceptance check exercises.
func TestValidateEnvelopeData_ReportCardHappyPath(t *testing.T) {
	data := map[string]any{
		"title":   "X",
		"metrics": []any{map[string]any{"label": "Y", "value": "Z"}},
	}
	errs, err := ValidateEnvelopeData("report-card", data)
	if err != nil {
		t.Fatalf("unexpected schema-load error: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("expected no errors for valid report-card, got: %+v", errs)
	}
}

// TestValidateEnvelopeData_ReportCardAdditionalPropsRejected pins the
// ticket's exact failure case: a `data: {sections: [...]}` payload should
// fail with an additionalProperties error, the suggestion enumerating
// the schema-allowed keys.
func TestValidateEnvelopeData_ReportCardAdditionalPropsRejected(t *testing.T) {
	data := map[string]any{"sections": []any{}}
	errs, err := ValidateEnvelopeData("report-card", data)
	if err != nil {
		t.Fatalf("unexpected schema-load error: %v", err)
	}
	if len(errs) == 0 {
		t.Fatalf("expected at least one error for unknown key 'sections'")
	}
	var got *StructuredError
	for i, e := range errs {
		if strings.Contains(e.Reason, "additional propert") {
			got = &errs[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("expected an additionalProperties error among %+v", errs)
	}
	if got.Suggestion == "" {
		t.Errorf("expected a suggestion enumerating allowed keys, got empty")
	}
	for _, key := range []string{"title", "metrics"} {
		if !strings.Contains(got.Suggestion, key) {
			t.Errorf("suggestion %q missing allowed key %q", got.Suggestion, key)
		}
	}
}

// TestValidateEnvelopeData_MissingRequired hints at the missing field by
// name. report-card requires `metrics`; an empty {title} payload should
// trip exactly that suggestion.
func TestValidateEnvelopeData_MissingRequired(t *testing.T) {
	data := map[string]any{"title": "X"}
	errs, err := ValidateEnvelopeData("report-card", data)
	if err != nil {
		t.Fatalf("schema load: %v", err)
	}
	if len(errs) == 0 {
		t.Fatalf("expected at least one error for missing 'metrics'")
	}
	var got *StructuredError
	for i, e := range errs {
		if strings.Contains(e.Reason, "missing") && strings.Contains(e.Reason, "metrics") {
			got = &errs[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("expected a missing-required error mentioning 'metrics', got: %+v", errs)
	}
	if !strings.Contains(got.Suggestion, "metrics") {
		t.Errorf("suggestion should hint at 'metrics', got: %q", got.Suggestion)
	}
}

// TestValidateEnvelopeData_TypeMismatchSuggestsArrayWrap exercises the
// array-vs-object hint. list-card requires items: array; passing
// items: {} should suggest wrapping in [].
func TestValidateEnvelopeData_TypeMismatchSuggestsArrayWrap(t *testing.T) {
	data := map[string]any{"items": map[string]any{}}
	errs, err := ValidateEnvelopeData("list-card", data)
	if err != nil {
		t.Fatalf("schema load: %v", err)
	}
	if len(errs) == 0 {
		t.Fatalf("expected at least one error for wrong items type")
	}
	var got *StructuredError
	for i, e := range errs {
		if strings.Contains(e.Reason, "got object") && strings.Contains(e.Reason, "array") {
			got = &errs[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("expected a type-mismatch error (got object, want array), got: %+v", errs)
	}
	if !strings.Contains(got.Suggestion, "[") {
		t.Errorf("suggestion should mention `[...]`, got: %q", got.Suggestion)
	}
}

// TestValidateEnvelopeData_AllPassiveRenderables_HappyPaths is the v1
// acceptance check: every type on the passive-renderable allow-list
// validates a minimal payload without errors. Any drift between the
// allow-list and the schema set will surface here.
func TestValidateEnvelopeData_AllPassiveRenderables_HappyPaths(t *testing.T) {
	cases := map[string]map[string]any{
		"giphy-modal":     {"gif_url": "https://example.com/x.gif", "title": "x", "source": "GIPHY", "query": "x"},
		"document-viewer": {"title": "doc", "content": "hello"},
		"report-card":     {"title": "x", "metrics": []any{map[string]any{"label": "y", "value": "z"}}},
		"info-card":       {"title": "x", "body": "y"},
		"list-card":       {"items": []any{map[string]any{"label": "x"}}},
		"metric-card":     {"label": "x", "value": "y"},
		"progress-card":   {"title": "x", "progress": 50},
		"table-card":      {"columns": []any{map[string]any{"key": "k", "label": "L"}}, "rows": []any{map[string]any{"k": "v"}}},
		"timeline-card":   {"events": []any{map[string]any{"label": "e", "timestamp": "2026-04-29T00:00:00Z"}}},
		"diff-card":       {"before": map[string]any{"label": "a", "content": "x"}, "after": map[string]any{"label": "b", "content": "y"}},
		"artifact-mini":   {"artifact_id": "a", "name": "n", "mime_type": "text/plain"},
	}
	for _, envType := range PassiveRenderableTypes {
		envType := envType
		t.Run(envType, func(t *testing.T) {
			data, ok := cases[envType]
			if !ok {
				t.Fatalf("test case missing for passive renderable %q — update the cases map", envType)
			}
			errs, err := ValidateEnvelopeData(envType, data)
			if err != nil {
				t.Fatalf("schema load for %q: %v", envType, err)
			}
			if len(errs) > 0 {
				t.Errorf("expected zero validation errors for %q, got: %+v", envType, errs)
			}
		})
	}
}

// TestValidateAgainstSchema_NonEnvelope covers the non-envelope path: a
// generic JSON Schema (the input shape of an arbitrary tool) flowing
// through the same flatten + structured-error pipeline.
func TestValidateAgainstSchema_NonEnvelope(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"name"},
		"properties": map[string]any{
			"name":  map[string]any{"type": "string"},
			"count": map[string]any{"type": "integer"},
		},
		"additionalProperties": false,
	}
	// happy path
	if errs, err := ValidateAgainstSchema(schema, map[string]any{"name": "x"}); err != nil || len(errs) != 0 {
		t.Errorf("expected valid input to pass, errs=%+v err=%v", errs, err)
	}
	// missing required
	errs, err := ValidateAgainstSchema(schema, map[string]any{"count": 1})
	if err != nil {
		t.Fatalf("schema validate: %v", err)
	}
	if len(errs) == 0 {
		t.Fatalf("expected a missing-required error, got none")
	}
	hit := false
	for _, e := range errs {
		if strings.Contains(e.Reason, "missing") && strings.Contains(e.Reason, "name") {
			hit = true
			break
		}
	}
	if !hit {
		t.Errorf("expected a missing 'name' error, got: %+v", errs)
	}
	// additional property
	errs, err = ValidateAgainstSchema(schema, map[string]any{"name": "x", "extra": 1})
	if err != nil {
		t.Fatalf("schema validate: %v", err)
	}
	hit = false
	for _, e := range errs {
		if strings.Contains(e.Reason, "additional propert") {
			hit = true
			if !strings.Contains(e.Suggestion, "name") || !strings.Contains(e.Suggestion, "count") {
				t.Errorf("expected suggestion to enumerate allowed keys, got: %q", e.Suggestion)
			}
		}
	}
	if !hit {
		t.Errorf("expected an additionalProperties error, got: %+v", errs)
	}
}

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
// guarantee for every type the agent can emit through card_show. Any
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
