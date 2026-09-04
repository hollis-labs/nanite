// Package recover implements the recoverable-error taxonomy described by
// the self-healing-tool-surface lens
// (decisions.nanite.architecture.self_healing_tool_surface_lens — layer 3).
//
// This is the deterministic foundation for auto-repair (CW-20260429-0008 /
// C2). C1 (this package) does NOT call any LLM and does NOT retry — it
// classifies tool errors so the harness can:
//
//  1. Tag recoverable errors with a stable kind.
//  2. Pass a structured error envelope back to the agent (kind, reason,
//     suggestion, schema URI / path) so even without auto-repair the agent
//     gets actionable feedback.
//  3. Let C2 anchor LLM-based repair on the kind without burning budget on
//     auth / network / service-unavailable failures that no payload-shape
//     fix can rescue.
//
// Errors outside the taxonomy classify as Kind == KindNone (unrecoverable);
// they flow through unchanged.
//
// Note on the package name. `recover` is a Go builtin but the identifier is
// only shadowed inside this package itself — callers see `recover.Classify`
// and never need the builtin. The lens's prose names this layer "Repair"
// but the ticket fixes the import path as `internal/recover/`.
package recover

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/go-envelopes"
	"github.com/hollis-labs/nanite/internal/envelope"
)

// Kind names a recoverable error class. Values are stable lower-snake-case
// strings so they can be passed through logs, JSON envelopes, and the
// future C2 repair selector without further mapping.
type Kind string

const (
	// KindNone marks an error as unrecoverable: auth / permission /
	// service-unavailable / network / not-found / not-implemented and
	// anything the classifier could not match against a recoverable
	// pattern. The lens's "fail loudly" rule applies here.
	KindNone Kind = "none"

	// KindSchemaValidation covers JSON-Schema-style violations the agent
	// can plausibly fix on the next call: missing required fields,
	// additional/unknown properties, enum-value mismatches, and the
	// generic "data does not match schema" prose form. This is the
	// originating-incident kind (chat session c107 — `card_show`).
	KindSchemaValidation Kind = "schema_validation"

	// KindTypeCoercion covers cases where the value's JSON type is wrong
	// but the *content* is salvageable by a deterministic coercion the
	// harness (or, in C2, a small repair pass) can perform: number passed
	// where string is expected, single object passed where array is
	// expected, boolean passed where string is expected. The structured
	// suggestion already produced by envelope.FlattenSchemaError is the
	// repair seed.
	KindTypeCoercion Kind = "type_coercion"

	// KindWrongCardType covers the `card_show` (and adjacent)
	// envelope-emit surface specifically: the agent picked an envelope
	// type that isn't passive-renderable, or named a type that isn't
	// registered. C2's repair pass can remap the type when the data
	// shape obviously fits a different known type.
	KindWrongCardType Kind = "wrong_card_type"

	// KindMissingOptionalField is reserved for cases where the tool
	// surface signals "this field is recommended/optional but its absence
	// reduced the result" — distinct from a hard required-field miss
	// (which classifies as KindSchemaValidation). The classifier is
	// conservative: pure "required" misses do NOT classify here.
	KindMissingOptionalField Kind = "missing_optional_field"

	// KindFormatMismatch covers JSON Schema `format` failures (date-time,
	// email, uri, uuid, etc.). The value's type is right but the format
	// is wrong — usually a reformat away from valid.
	KindFormatMismatch Kind = "format_mismatch"
)

// IsRecoverable reports whether k describes a recoverable error class.
// KindNone is the only non-recoverable value.
func (k Kind) IsRecoverable() bool { return k != KindNone && k != "" }

// String returns the kind as its canonical lower-snake-case string. This
// is the form that lands in logs, JSON envelopes, and Tesseract keys.
func (k Kind) String() string {
	if k == "" {
		return string(KindNone)
	}
	return string(k)
}

// RecoverableError wraps an underlying tool error with the taxonomy kind
// and the context the agent (and C2) need to act on it. RecoverableError
// is only constructed when Classify returned a kind != KindNone — see
// Wrap.
//
// The struct is intentionally agent-facing: the JSON tags drive the error
// envelope shape returned at the agent boundary. SentArgs is included so
// a downstream repair pass can diff (sent_args ↔ proposed_args) without a
// second round trip.
type RecoverableError struct {
	Kind          Kind           `json:"kind"`
	ToolName      string         `json:"tool"`
	SentArgs      map[string]any `json:"sent_args,omitempty"`
	SchemaURI     string         `json:"schema_uri,omitempty"`
	ErrorPath     string         `json:"path,omitempty"`
	ErrorReason   string         `json:"reason,omitempty"`
	Suggestion    string         `json:"suggestion,omitempty"`
	OriginalError error          `json:"-"`
}

// Error renders a compact human-readable summary that still carries the
// kind tag so log aggregations don't lose discrimination. The agent
// envelope uses the JSON shape, not this string.
func (e *RecoverableError) Error() string {
	if e == nil {
		return ""
	}
	parts := []string{fmt.Sprintf("recoverable[%s]", e.Kind)}
	if e.ToolName != "" {
		parts = append(parts, "tool="+e.ToolName)
	}
	if e.ErrorPath != "" {
		parts = append(parts, "path="+e.ErrorPath)
	}
	if e.ErrorReason != "" {
		parts = append(parts, e.ErrorReason)
	} else if e.OriginalError != nil {
		parts = append(parts, e.OriginalError.Error())
	}
	return strings.Join(parts, " ")
}

// Unwrap exposes the wrapped error so errors.Is / errors.As keep working
// on the underlying type (e.g., *envelope.ValidationError).
func (e *RecoverableError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.OriginalError
}

// Classify pattern-matches err against the taxonomy and returns the
// matched Kind, or KindNone when no pattern fires. Classify is a pure
// function: no logging, no side effects.
//
// Resolution order, highest specificity first:
//  1. Direct *envelope.ValidationError (typed) → inspect module-owned
//     structured failures for
//     KindFormatMismatch / KindTypeCoercion, default KindSchemaValidation.
//  2. A validation error recognized by go-envelopes → same classification.
//  3. *RecoverableError already wrapped → return its Kind (idempotent).
//  4. Unrecoverable prose patterns (auth, permission, service unavailable,
//     not implemented, network) → KindNone explicitly.
//  5. Recoverable prose patterns (envelope.ValidateData formatted message,
//     card_show "not addressable" + "no schema registered" etc.) →
//     the matching Kind.
//  6. Fallback → KindNone.
func Classify(err error) Kind {
	if err == nil {
		return KindNone
	}

	// (3) Already classified — idempotent.
	var rec *RecoverableError
	if errors.As(err, &rec) && rec != nil {
		return rec.Kind
	}

	// (1) Direct typed envelope.ValidationError — strongest signal. Generic
	// validator-tree traversal remains owned by go-envelopes.
	var ve *envelope.ValidationError
	if errors.As(err, &ve) && ve != nil {
		return classifyValidationFailures(ve.Details())
	}

	// (2) Preserve classification for validation errors that have not been
	// wrapped by an envelope registry, while leaving their traversal and
	// formatting to the public go-envelopes API.
	if failures := envelopes.FlattenValidationFailures(err, nil); len(failures) > 0 {
		return classifyValidationFailures(failures)
	}

	msg := strings.ToLower(err.Error())

	// (4) Unrecoverable prose patterns — short-circuit before recoverable
	// matching so an auth message that mentions "schema" by accident
	// doesn't classify as schema_validation.
	if isUnrecoverableProse(msg) {
		return KindNone
	}

	// (5) Recoverable prose patterns.
	switch {
	case strings.Contains(msg, "not addressable through card_show"),
		strings.Contains(msg, "is not on the passive-renderable allow-list"),
		strings.Contains(msg, "no schema registered for envelope type"),
		strings.Contains(msg, "unknown envelope type"):
		return KindWrongCardType

	case strings.Contains(msg, "data does not match the") && strings.Contains(msg, "schema"):
		// envelope.ValidateData's formatted prose form (and the show_card
		// transport's `errorResult(fmt.Sprintf("data does not match the
		// %q schema: %v", ...))` re-wrap). Without the typed leaf in
		// hand we can't distinguish a format-mismatch leaf from a
		// type-mismatch leaf reliably; default to schema_validation.
		return classifyByContainedHints(msg)

	case strings.Contains(msg, "envelope") && strings.Contains(msg, "failed schema validation"):
		return classifyByContainedHints(msg)

	case strings.Contains(msg, "schema validation") || strings.Contains(msg, "missing properties"),
		strings.Contains(msg, "additional properties"),
		strings.Contains(msg, "expected"):
		return classifyByContainedHints(msg)
	}

	return KindNone
}

// classifyValidationFailures surfaces the most-specific recoverable kind from
// module-owned validation details. Format failures beat type failures beat the
// generic schema-validation default.
func classifyValidationFailures(failures []envelopes.ValidationFailure) Kind {
	hasTypeMismatch := false
	for _, failure := range failures {
		switch failure.Keyword {
		case "format":
			return KindFormatMismatch
		case "type":
			hasTypeMismatch = true
		}
	}
	if hasTypeMismatch {
		return KindTypeCoercion
	}
	return KindSchemaValidation
}

// classifyByContainedHints is a fallback for prose-only error strings:
// pick format/type/wrong-card by keyword heuristics, default to
// schema_validation. Kept narrow on purpose — broad pattern matching here
// would steal classifications from the typed paths above.
func classifyByContainedHints(msg string) Kind {
	switch {
	case strings.Contains(msg, "format ") || strings.Contains(msg, "is not valid date") ||
		strings.Contains(msg, "is not valid email") || strings.Contains(msg, "is not valid uri"):
		return KindFormatMismatch
	case strings.Contains(msg, "got string, want") || strings.Contains(msg, "got number, want") ||
		strings.Contains(msg, "got integer, want") || strings.Contains(msg, "got boolean, want") ||
		strings.Contains(msg, "got object, want array") || strings.Contains(msg, "got array, want object"):
		return KindTypeCoercion
	default:
		return KindSchemaValidation
	}
}

// isUnrecoverableProse short-circuits classification for messages that
// describe failures no payload-shape repair can fix. This list is
// deliberately narrow: false positives here mean recoverable errors
// silently flow through unrepaired, so we anchor on phrases the
// codebase actually emits (see internal/mcp/self_tools_transport.go,
// internal/toolclient/broker.go, internal/mcp/memory_tools.go).
func isUnrecoverableProse(msg string) bool {
	switch {
	case strings.Contains(msg, "permission denied"),
		strings.Contains(msg, "forbidden"),
		strings.Contains(msg, "unauthorized"),
		strings.Contains(msg, "401"),
		strings.Contains(msg, "403"):
		return true
	case strings.Contains(msg, "service not configured"),
		strings.Contains(msg, "service not available"),
		strings.Contains(msg, "service unavailable"),
		strings.Contains(msg, "no mcp manager configured"),
		strings.Contains(msg, "no tool client"):
		return true
	case strings.Contains(msg, "not yet implemented"),
		strings.Contains(msg, "not_implemented"):
		return true
	case strings.Contains(msg, "context deadline exceeded"),
		strings.Contains(msg, "context canceled"),
		strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "no such host"):
		return true
	}
	return false
}

// Wrap classifies err, and when the resulting kind is recoverable returns
// a *RecoverableError carrying the supplied context. When the kind is
// KindNone the original err is returned unchanged so callers can use
// `err = recover.Wrap(err, name, args)` as a transparent classifier.
//
// Wrap is idempotent: calling it on an already-wrapped error returns the
// existing *RecoverableError (with toolName / sentArgs filled in if they
// were missing).
func Wrap(err error, toolName string, sentArgs map[string]any) error {
	if err == nil {
		return nil
	}
	// Idempotent path.
	var existing *RecoverableError
	if errors.As(err, &existing) && existing != nil {
		if existing.ToolName == "" {
			existing.ToolName = toolName
		}
		if existing.SentArgs == nil {
			existing.SentArgs = sentArgs
		}
		return existing
	}

	kind := Classify(err)
	if !kind.IsRecoverable() {
		return err
	}

	rec := &RecoverableError{
		Kind:          kind,
		ToolName:      toolName,
		SentArgs:      sentArgs,
		OriginalError: err,
	}
	enrich(rec, err)
	return rec
}

// enrich populates Path / Reason / Suggestion / SchemaURI from the
// underlying error when it carries the structured envelope shape.
func enrich(rec *RecoverableError, err error) {
	var ve *envelope.ValidationError
	if errors.As(err, &ve) && ve != nil {
		details := ve.Details()
		if len(details) > 0 {
			rec.SchemaURI = details[0].SchemaURI
		}
		// Surface the first leaf as the "primary" reason. The full leaf
		// list is available via envelope.FlattenSchemaError in callers
		// that want it.
		leaves := envelope.FlattenSchemaError(ve, nil)
		if len(leaves) > 0 {
			rec.ErrorPath = leaves[0].Path
			rec.ErrorReason = leaves[0].Reason
			rec.Suggestion = leaves[0].Suggestion
		}
		return
	}
	// Fallback: use the error's text as the reason. Path / suggestion
	// stay empty — callers can still see the wrapped err via Unwrap.
	rec.ErrorReason = err.Error()
}
