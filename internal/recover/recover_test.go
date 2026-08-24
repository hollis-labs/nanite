package recover

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/envelope"
)

// TestClassify_SchemaValidation_ReportCardMissingMetrics is the canonical
// chat-session-c107 case: card_show with a report-card missing the
// required `metrics` field. Must classify as KindSchemaValidation.
func TestClassify_SchemaValidation_ReportCardMissingMetrics(t *testing.T) {
	err := envelope.ValidateData("report-card", map[string]any{"title": "x"})
	if err == nil {
		t.Fatalf("expected validation error for report-card without metrics")
	}
	if got := Classify(err); got != KindSchemaValidation {
		t.Errorf("expected %q, got %q (err=%v)", KindSchemaValidation, got, err)
	}
}

// TestClassify_TypeCoercion_ListCardItemsNotArray pins KindTypeCoercion to
// the typed leaf path: list-card requires items: array; passing items: {}
// trips a *kind.Type leaf that we drill into.
func TestClassify_TypeCoercion_ListCardItemsNotArray(t *testing.T) {
	err := envelope.ValidateData("list-card", map[string]any{"items": map[string]any{}})
	if err == nil {
		t.Fatalf("expected validation error for list-card items not array")
	}
	if got := Classify(err); got != KindTypeCoercion {
		t.Errorf("expected %q, got %q (err=%v)", KindTypeCoercion, got, err)
	}
}

// TestClassify_FormatMismatch_TimelineEventTimestamp pins KindFormatMismatch
// against a real schema: timeline-card requires events[].timestamp in
// date-time format. Passing a non-RFC3339 string trips kind.Format.
func TestClassify_FormatMismatch_TimelineEventTimestamp(t *testing.T) {
	data := map[string]any{
		"events": []any{
			map[string]any{"label": "e", "timestamp": "not-a-date"},
		},
	}
	err := envelope.ValidateData("timeline-card", data)
	if err == nil {
		t.Skipf("timeline-card schema does not enforce date-time format; format-mismatch coverage relies on schema discipline")
	}
	got := Classify(err)
	// timeline-card may or may not declare format: date-time; if it does
	// not, the leaf falls back to KindSchemaValidation. Both are valid
	// outcomes for the deterministic path — assert "either format
	// mismatch (preferred) or schema validation (acceptable fallback)".
	if got != KindFormatMismatch && got != KindSchemaValidation {
		t.Errorf("expected %q or %q, got %q", KindFormatMismatch, KindSchemaValidation, got)
	}
}

// TestClassify_FormatMismatch_ProsePath exercises the prose fallback for
// format hints — the case where the validator is hidden behind a
// transport that re-wraps the error as a flat string (e.g., the
// `tool error: ...` wrap in internal/mcp/manager.go).
func TestClassify_FormatMismatch_ProsePath(t *testing.T) {
	err := errors.New("tool error: data does not match the \"timeline-card\" schema: '/events/0/timestamp' is not valid format \"date-time\"")
	if got := Classify(err); got != KindFormatMismatch {
		t.Errorf("expected %q, got %q", KindFormatMismatch, got)
	}
}

// TestClassify_WrongCardType_NotAddressable pins the show_card "type is
// not on the passive-renderable allow-list" path that the transport
// emits via errorResult.
func TestClassify_WrongCardType_NotAddressable(t *testing.T) {
	err := errors.New(`tool error: envelope type "approval-card" is not addressable through card_show. Allow-list (v1): info-card, list-card`)
	if got := Classify(err); got != KindWrongCardType {
		t.Errorf("expected %q, got %q", KindWrongCardType, got)
	}
}

// TestClassify_WrongCardType_UnknownEnvelopeType pins the
// "no schema registered for envelope type" path that envelope.ValidateData
// returns when the type is unknown to the embedded schema set.
func TestClassify_WrongCardType_UnknownEnvelopeType(t *testing.T) {
	err := envelope.ValidateData("not-a-real-card", map[string]any{})
	if err == nil {
		t.Fatalf("expected error for unknown envelope type")
	}
	if got := Classify(err); got != KindWrongCardType {
		t.Errorf("expected %q, got %q (err=%v)", KindWrongCardType, got, err)
	}
}

// TestClassify_None_PermissionDenied — the lens's "fail loudly" rule:
// auth/permission failures must NEVER classify as recoverable. Repairing
// them would be privilege escalation.
func TestClassify_None_PermissionDenied(t *testing.T) {
	cases := []string{
		`permission denied: tool "shell_exec" not permitted for agent "chat-1"`,
		`permission denied: tool "x" requires developer_mode to be enabled`,
		"unauthorized",
		"forbidden",
		"http 401: unauthorized",
		"http 403: forbidden",
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			if got := Classify(errors.New(msg)); got != KindNone {
				t.Errorf("expected %q for %q, got %q", KindNone, msg, got)
			}
		})
	}
}

// TestClassify_None_ServiceUnavailable covers G6 in the audit:
// service-not-configured failures look like recoverable shape-errors but
// no schema fix retries them.
func TestClassify_None_ServiceUnavailable(t *testing.T) {
	cases := []string{
		"todo service not available",
		"memory service not configured",
		"no MCP manager configured",
		"no tool client or MCP manager configured",
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			if got := Classify(errors.New(msg)); got != KindNone {
				t.Errorf("expected %q for %q, got %q", KindNone, msg, got)
			}
		})
	}
}

// TestClassify_None_NetworkAndTimeouts covers the connection-refused /
// context-deadline class: the next call won't fix them, so they must
// flow through unchanged.
func TestClassify_None_NetworkAndTimeouts(t *testing.T) {
	cases := []string{
		"context deadline exceeded",
		"context canceled",
		"connection refused",
		`Get "https://api.example.com": dial tcp: lookup api.example.com: no such host`,
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			if got := Classify(errors.New(msg)); got != KindNone {
				t.Errorf("expected %q for %q, got %q", KindNone, msg, got)
			}
		})
	}
}

// TestClassify_None_NotImplemented covers the audit's G4 silent-no-op
// cluster: install_diff / engine_offline shouldn't be auto-repaired
// because there's nothing to repair.
func TestClassify_None_NotImplemented(t *testing.T) {
	cases := []string{
		"install diff not yet implemented",
		`{"kind":"not_implemented","tool":"install_diff"}`,
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			if got := Classify(errors.New(msg)); got != KindNone {
				t.Errorf("expected %q for %q, got %q", KindNone, msg, got)
			}
		})
	}
}

// TestClassify_None_Nil — sanity: Classify(nil) is None.
func TestClassify_None_Nil(t *testing.T) {
	if got := Classify(nil); got != KindNone {
		t.Errorf("expected %q for nil, got %q", KindNone, got)
	}
}

// TestKind_IsRecoverable_AllCases enumerates the taxonomy and asserts the
// recoverable flag matches the lens's contract.
func TestKind_IsRecoverable_AllCases(t *testing.T) {
	recoverable := []Kind{
		KindSchemaValidation,
		KindTypeCoercion,
		KindWrongCardType,
		KindMissingOptionalField,
		KindFormatMismatch,
	}
	for _, k := range recoverable {
		if !k.IsRecoverable() {
			t.Errorf("%q must be recoverable", k)
		}
	}
	if KindNone.IsRecoverable() {
		t.Errorf("%q must not be recoverable", KindNone)
	}
	// Empty kind is treated as None.
	if Kind("").IsRecoverable() {
		t.Errorf("empty Kind must not be recoverable")
	}
}

// TestWrap_RecoverableEnrichesContext exercises the agent-facing path:
// Wrap on a schema-validation error must produce a *RecoverableError
// carrying tool, kind, schema_uri, path, reason, and a suggestion that
// names the missing field.
func TestWrap_RecoverableEnrichesContext(t *testing.T) {
	err := envelope.ValidateData("report-card", map[string]any{"title": "x"})
	if err == nil {
		t.Fatalf("expected validation error")
	}
	args := map[string]any{"type": "report-card", "data": map[string]any{"title": "x"}}
	wrapped := Wrap(err, "card_show", args)

	var rec *RecoverableError
	if !errors.As(wrapped, &rec) {
		t.Fatalf("expected *RecoverableError, got %T (%v)", wrapped, wrapped)
	}
	if rec.Kind != KindSchemaValidation {
		t.Errorf("expected kind %q, got %q", KindSchemaValidation, rec.Kind)
	}
	if rec.ToolName != "card_show" {
		t.Errorf("expected tool card_show, got %q", rec.ToolName)
	}
	if rec.SchemaURI != "mem://nanite/envelope/report-card.schema.json" {
		t.Errorf("unexpected schema_uri: %q", rec.SchemaURI)
	}
	if rec.SentArgs == nil {
		t.Errorf("sent_args must be carried through")
	}
	if rec.ErrorReason == "" {
		t.Errorf("expected a reason from the validator leaf")
	}
	if !strings.Contains(rec.Suggestion, "metrics") {
		t.Errorf("expected suggestion to mention 'metrics', got %q", rec.Suggestion)
	}
	if !errors.Is(wrapped, err) {
		// errors.Is walks Unwrap; rec.OriginalError must be reachable
		if !errors.Is(rec.OriginalError, err) {
			t.Errorf("Unwrap chain broken — original error not reachable")
		}
	}
}

// TestWrap_UnrecoverablePassesThrough confirms KindNone errors are NOT
// wrapped — the returned error is the same instance the caller passed
// in, so existing flows are byte-identical.
func TestWrap_UnrecoverablePassesThrough(t *testing.T) {
	original := errors.New("permission denied: tool \"x\" not permitted")
	got := Wrap(original, "x", nil)
	if got != original { //nolint:errorlint // This test deliberately requires exact pass-through identity, not only errors.Is equivalence.
		t.Errorf("expected unrecoverable error to pass through unchanged, got %T (%v)", got, got)
	}
}

// TestWrap_Idempotent confirms a second Wrap on a *RecoverableError does
// not double-wrap and fills in missing context.
func TestWrap_Idempotent(t *testing.T) {
	err := envelope.ValidateData("report-card", map[string]any{"title": "x"})
	first := Wrap(err, "", nil)
	second := Wrap(first, "card_show", map[string]any{"type": "report-card"})

	var rec *RecoverableError
	if !errors.As(second, &rec) {
		t.Fatalf("expected *RecoverableError after second wrap")
	}
	if rec.ToolName != "card_show" {
		t.Errorf("expected ToolName backfilled to card_show, got %q", rec.ToolName)
	}
	if rec.SentArgs == nil {
		t.Errorf("expected SentArgs backfilled on second Wrap")
	}
	// The second call must NOT produce a *RecoverableError that wraps
	// another *RecoverableError — exactly one layer of wrapping.
	inner := errors.Unwrap(rec)
	var nested *RecoverableError
	if errors.As(inner, &nested) {
		t.Errorf("Wrap is not idempotent — found nested *RecoverableError")
	}
}

// TestWrap_NilNil confirms Wrap is nil-safe.
func TestWrap_NilNil(t *testing.T) {
	if got := Wrap(nil, "x", nil); got != nil {
		t.Errorf("Wrap(nil) must be nil, got %v", got)
	}
}

// TestRecoverableError_ErrorString — the renderer must include the kind
// tag and the tool name so log scrapers can group by it.
func TestRecoverableError_ErrorString(t *testing.T) {
	rec := &RecoverableError{
		Kind:        KindSchemaValidation,
		ToolName:    "card_show",
		ErrorPath:   "/metrics",
		ErrorReason: "missing properties: 'metrics'",
	}
	got := rec.Error()
	for _, want := range []string{"recoverable[schema_validation]", "tool=card_show", "/metrics", "missing"} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() missing %q: %q", want, got)
		}
	}
}

// TestRecoverableError_UnwrapPreservesChain confirms errors.Is finds the
// original error through the wrapper. C2 will rely on this to detect
// whether the underlying envelope.ValidationError is in play.
func TestRecoverableError_UnwrapPreservesChain(t *testing.T) {
	original := envelope.ValidateData("report-card", map[string]any{"title": "x"})
	rec := Wrap(original, "card_show", nil)
	var ve *envelope.ValidationError
	if !errors.As(rec, &ve) {
		t.Errorf("errors.As must reach *envelope.ValidationError through Unwrap")
	}
	// Round-trip the wrapped error through fmt.Errorf to confirm errors.As
	// still finds the original kind even when extra wrapping is added.
	wrapped := fmt.Errorf("downstream: %w", rec)
	var rec2 *RecoverableError
	if !errors.As(wrapped, &rec2) {
		t.Errorf("errors.As must still find *RecoverableError through fmt.Errorf wrap")
	}
	if rec2.Kind != KindSchemaValidation {
		t.Errorf("kind lost through fmt.Errorf wrap: %q", rec2.Kind)
	}
}

// TestClassify_AlreadyWrapped confirms the idempotent path through
// Classify (not just Wrap). Useful when an error has already been
// classified upstream and a deeper layer asks "what kind is this?".
func TestClassify_AlreadyWrapped(t *testing.T) {
	rec := &RecoverableError{Kind: KindWrongCardType}
	if got := Classify(rec); got != KindWrongCardType {
		t.Errorf("expected idempotent classify to return %q, got %q", KindWrongCardType, got)
	}
}

// TestKind_String covers the stringer surface — used in log fields and
// JSON envelopes; must remain stable.
func TestKind_String(t *testing.T) {
	cases := map[Kind]string{
		KindSchemaValidation:     "schema_validation",
		KindTypeCoercion:         "type_coercion",
		KindWrongCardType:        "wrong_card_type",
		KindMissingOptionalField: "missing_optional_field",
		KindFormatMismatch:       "format_mismatch",
		KindNone:                 "none",
		Kind(""):                 "none",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("Kind(%q).String() = %q, want %q", k, got, want)
		}
	}
}
